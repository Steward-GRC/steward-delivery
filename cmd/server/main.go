// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Command server runs the delivery service over gRPC: the rendered HTML of a
// policy version, the version diff, magic links and PDF export requests. It
// calls steward-core for content and diffs and publishes steward-audit's
// AuditEvent. A PDF export creates a PdfRender resource that
// steward-pdf-renderer renders; the renderer fetches the HTML from this
// service's internal HTTP port.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	buildinfo "github.com/Bugs5382/go-buildinfo"
	"github.com/Bugs5382/go-buildinfo/health"
	log "github.com/Bugs5382/go-log"
	objectstore "github.com/Bugs5382/go-objectstore"
	"github.com/Bugs5382/go-objectstore/s3store"
	gootel "github.com/Bugs5382/go-otel"
	postgres "github.com/Bugs5382/go-postgres"
	pgotel "github.com/Bugs5382/go-postgres/otel"
	"github.com/Bugs5382/go-rabbitmq"
	rmqotel "github.com/Bugs5382/go-rabbitmq/otel"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	corev1 "github.com/Steward-GRC/steward-delivery/gen/go/thirdparty/core/v1"
	"github.com/Steward-GRC/steward-delivery/internal/audit"
	"github.com/Steward-GRC/steward-delivery/internal/config"
	"github.com/Steward-GRC/steward-delivery/internal/corepolicy"
	"github.com/Steward-GRC/steward-delivery/internal/grpcsvc"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/pdfrender"
	"github.com/Steward-GRC/steward-delivery/internal/policyhttp"
	"github.com/Steward-GRC/steward-delivery/internal/readiness"
	"github.com/Steward-GRC/steward-delivery/internal/server"
	"github.com/Steward-GRC/steward-delivery/internal/store"
)

const serviceName = "delivery"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	logger := log.NewLogger(serviceName)
	if err := run(ctx, logger); err != nil {
		logger.Fatal(err, "delivery service stopped")
	}
}

func run(ctx context.Context, logger log.Logger) error {
	bi := buildinfo.Get()
	logger.Info("starting", log.F("version", bi.Version), log.F("commit", bi.Commit), log.F("go_version", bi.GoVersion))
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	otelShutdown, err := gootel.Init(ctx, serviceName, cfg.OTLPEndpoint)
	if err != nil {
		return fmt.Errorf("otel: %w", err)
	}
	defer func() {
		if err := otelShutdown(context.Background()); err != nil {
			logger.Warn("otel shutdown", log.F("error", err.Error()))
		}
	}()

	if err := pgotel.InstrumentMigrate(ctx, serviceName, func() error {
		return postgres.Migrate(cfg.MigrateDSN, cfg.MigrationsDir)
	}); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	db, err := postgres.New(ctx, cfg.DatabaseDSN, pgotel.WithTracing())
	if err != nil {
		return fmt.Errorf("postgres: %w", err)
	}
	defer db.Close()

	// Plain gRPC to core until service mTLS identities are defined.
	coreConn, err := grpc.NewClient(cfg.CoreGRPCAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithStatsHandler(gootel.GRPCClientStatsHandler()))
	if err != nil {
		return fmt.Errorf("core client: %w", err)
	}
	defer func() { _ = coreConn.Close() }()
	policyService := corev1.NewPolicyServiceClient(coreConn)
	policyClient := corepolicy.New(policyService)
	appendixClient := corepolicy.NewAppendixClient(corev1.NewAppendixServiceClient(coreConn))
	logger.Info("core client ready", log.F("addr", cfg.CoreGRPCAddr))

	conn, err := rabbitmq.Connect(ctx, cfg.RabbitURL, append(rmqotel.Instrument(), rabbitmq.WithLogger(rabbitLogger{logger}))...)
	if err != nil {
		return fmt.Errorf("rabbitmq: %w", err)
	}
	defer func() { _ = conn.Close() }()
	auditPub := conn.NewPublisher(audit.Exchange,
		rabbitmq.WithExchangeDeclare(rabbitmq.ExchangeConfig{Name: audit.Exchange, Kind: "topic", Durable: true}),
		rabbitmq.WithDefaultContentType(audit.ContentType))
	auditor := audit.New(publisher{auditPub})

	deps := readiness.Deps{Postgres: readiness.PostgresDB(db), Broker: conn, Core: readiness.CoreConn(coreConn)}

	var signer grpcsvc.Signer
	if cfg.S3Endpoint != "" {
		s3, err := s3store.New(s3store.Config{
			Endpoint: cfg.S3Endpoint, Region: cfg.S3Region, Bucket: cfg.S3Bucket,
			AccessKeyID: cfg.S3AccessKey, SecretAccessKey: cfg.S3SecretKey, PathStyle: cfg.S3ForcePathStyle,
		})
		if err != nil {
			return fmt.Errorf("object store: %w", err)
		}
		signer = grpcsvc.ObjectStoreSigner{Store: s3}
		deps.Objects = func(ctx context.Context) error {
			_, err := s3.List(ctx, objectstore.ListOptions{Limit: 1})
			return err
		}
		logger.Info("PDF downloads on", log.F("bucket", cfg.S3Bucket))
	} else {
		logger.Info("S3_ENDPOINT is not set: PDF downloads are off")
	}

	jobStore := store.NewPDFJobRepo(db)
	var pdfClient grpcsvc.PDFRenderClient
	switch restCfg, err := rest.InClusterConfig(); {
	case !cfg.PDFExportEnabled:
		logger.Info("PDF export is off (PDF_EXPORT_ENABLED=false)")
	case err != nil:
		logger.Warn("no in-cluster Kubernetes config: PDF export is off", log.F("error", err.Error()))
	default:
		dyn, err := dynamic.NewForConfig(restCfg)
		if err != nil {
			return fmt.Errorf("kubernetes client: %w", err)
		}
		client := pdfrender.NewClient(dyn, cfg.PodNamespace)
		pdfClient = client
		deps.Kubernetes = client.Ping
		informer := pdfrender.NewInformer(dyn, cfg.PodNamespace, jobStore, func(err error) {
			logger.Warn("PdfRender status not projected", log.F("error", err.Error()))
		})
		go func() {
			if err := informer.Run(ctx); err != nil {
				logger.Error(err, "PdfRender informer stopped")
			}
		}()
		logger.Info("PDF export on", log.F("namespace", cfg.PodNamespace))
	}

	mlSvc := magiclink.NewService(store.NewMagicLinkRepo(db), grpcsvc.NewAuditEmitter(auditor),
		magiclink.WithTTLs(cfg.MagicLinkNonSensitiveTTL, cfg.MagicLinkSensitiveTTL))
	handler := grpcsvc.NewDeliveryHandlerFull(policyClient, mlSvc, pdfClient,
		grpcsvc.PDFConfig{FetchURLBase: cfg.InternalBaseURL, OutputBucket: cfg.S3Bucket}, jobStore, signer).
		WithSensitivity(corepolicy.NewSensitivity(policyService)).
		WithPDFLinkTTL(cfg.PDFLinkTTL)

	checker, err := readiness.New(deps, health.WithTTL(5*time.Second), health.WithTimeout(2*time.Second), health.WithLogger(logger))
	if err != nil {
		return fmt.Errorf("readiness: %w", err)
	}

	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", ":"+cfg.GRPCPort)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	probeLis, err := lc.Listen(ctx, "tcp", ":"+cfg.ProbePort)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	internalLis, err := lc.Listen(ctx, "tcp", ":"+cfg.InternalHTTPPort)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	logger.Info("serving", log.F("port", cfg.GRPCPort), log.F("probe_port", cfg.ProbePort), log.F("internal_http_port", cfg.InternalHTTPPort))

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	probesDone := make(chan error, 1)
	go func() {
		probesDone <- server.ServeProbes(ctx, probeLis, checker)
		cancel()
	}()
	internalDone := make(chan error, 1)
	go func() {
		internalDone <- serveInternal(ctx, internalLis, policyhttp.NewWithAppendix(policyClient, appendixClient, nil).WithLogger(logger))
		cancel()
	}()
	opts := server.Options{CertFile: cfg.TLSCertFile, KeyFile: cfg.TLSKeyFile, ClientCAFile: cfg.TLSClientCAFile, Checker: checker}
	err = server.Serve(ctx, lis, logger, opts, func(s *grpc.Server) {
		deliveryv1.RegisterDeliveryServiceServer(s, handler)
	})
	cancel()
	return errors.Join(err, <-probesDone, <-internalDone)
}

// serveInternal serves the HTML the renderer fetches until ctx is cancelled.
func serveInternal(ctx context.Context, lis net.Listener, h *policyhttp.Handler) error {
	mux := http.NewServeMux()
	h.Mount(mux)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(lis) }()
	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
		<-errCh
		return nil
	}
}

// publisher narrows a go-rabbitmq publisher to the Publish the emitter uses.
type publisher struct{ p *rabbitmq.Publisher }

func (p publisher) Publish(ctx context.Context, routingKey string, body []byte) error {
	return p.p.Publish(ctx, routingKey, body)
}

type rabbitLogger struct{ l log.Logger }

func (r rabbitLogger) Debugf(f string, a ...any) { r.l.Debug(fmt.Sprintf(f, a...)) }
func (r rabbitLogger) Infof(f string, a ...any)  { r.l.Info(fmt.Sprintf(f, a...)) }
func (r rabbitLogger) Warnf(f string, a ...any)  { r.l.Warn(fmt.Sprintf(f, a...)) }
func (r rabbitLogger) Errorf(f string, a ...any) { r.l.Error(nil, fmt.Sprintf(f, a...)) }
