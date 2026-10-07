// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package config reads the delivery service's settings from the environment.
package config

import (
	"errors"
	"os"
	"strconv"
	"time"

	"github.com/Steward-GRC/steward-delivery/internal/workloadauth"
)

// Config is every setting the service runs with.
type Config struct {
	DatabaseDSN   string
	MigrateDSN    string // a direct connection for migrations; defaults to DatabaseDSN
	MigrationsDir string
	RabbitURL     string
	GRPCPort      string
	// ProbePort serves /livez and /readyz over plain HTTP.
	ProbePort    string
	OTLPEndpoint string

	// TLS for the gRPC server: the certificate and the CA client
	// certificates must chain to. Empty serves plain gRPC.
	TLSCertFile     string
	TLSKeyFile      string
	TLSClientCAFile string

	// Object storage for the rendered PDFs. The renderer writes into the
	// same bucket; delivery presigns the downloads.
	S3Endpoint       string
	S3Bucket         string
	S3Region         string
	S3AccessKey      string
	S3SecretKey      string
	S3ForcePathStyle bool

	MagicLinkNonSensitiveTTL time.Duration
	MagicLinkSensitiveTTL    time.Duration
	// PDFLinkTTL is how long a PDF download link stays valid.
	PDFLinkTTL time.Duration

	// InternalHTTPPort serves the policy HTML the renderer fetches.
	InternalHTTPPort string
	// InternalBaseURL is the in-cluster URL of that endpoint, the prefix of
	// every render's fetch URL.
	InternalBaseURL string
	// PodNamespace is where PdfRender resources are created.
	PodNamespace string

	// CoreGRPCAddr is steward-core, the source of every version's content
	// and diff.
	CoreGRPCAddr string

	// PDFExportEnabled turns off the PdfRender client and informer when the
	// renderer isn't deployed.
	PDFExportEnabled bool

	// WorkloadAuth verifies the callers' workload tokens. It is set when
	// WorkloadAuthEnabled; WORKLOAD_AUTH=disabled is the only way to turn it
	// off.
	WorkloadAuth        workloadauth.Config
	WorkloadAuthEnabled bool
	// WorkloadTokenFile is delivery's own projected token, sent on every call
	// to steward-core. Required unless WORKLOAD_AUTH=disabled.
	WorkloadTokenFile string
}

// Load reads the settings from the environment.
func Load() (Config, error) {
	c := Config{
		DatabaseDSN:   os.Getenv("DATABASE_DSN"),
		MigrationsDir: getOr("MIGRATIONS_DIR", "migrations"),
		RabbitURL:     os.Getenv("RABBITMQ_URL"),
		GRPCPort:      getOr("GRPC_PORT", "9090"),
		ProbePort:     getOr("PROBE_PORT", "8080"),
		OTLPEndpoint:  getOr("OTEL_EXPORTER_OTLP_ENDPOINT", "localhost:4317"),

		TLSCertFile:     os.Getenv("GRPC_TLS_CERT_FILE"),
		TLSKeyFile:      os.Getenv("GRPC_TLS_KEY_FILE"),
		TLSClientCAFile: os.Getenv("GRPC_TLS_CLIENT_CA_FILE"),

		S3Endpoint:       os.Getenv("S3_ENDPOINT"),
		S3Bucket:         os.Getenv("S3_BUCKET"),
		S3Region:         getOr("S3_REGION", "us-east-1"),
		S3AccessKey:      os.Getenv("S3_ACCESS_KEY"),
		S3SecretKey:      os.Getenv("S3_SECRET_KEY"),
		S3ForcePathStyle: boolOr("S3_FORCE_PATH_STYLE", true),

		MagicLinkNonSensitiveTTL: durationOr("MAGIC_LINK_NONSENSITIVE_TTL", 30*24*time.Hour),
		MagicLinkSensitiveTTL:    durationOr("MAGIC_LINK_SENSITIVE_TTL", 48*time.Hour),
		PDFLinkTTL:               durationOr("PDF_LINK_TTL", 15*time.Minute),

		InternalHTTPPort: getOr("INTERNAL_HTTP_PORT", "8081"),
		InternalBaseURL:  os.Getenv("INTERNAL_BASE_URL"),
		PodNamespace:     getOr("POD_NAMESPACE", "default"),

		CoreGRPCAddr: os.Getenv("CORE_GRPC_ADDR"),

		PDFExportEnabled: boolOr("PDF_EXPORT_ENABLED", true),

		WorkloadTokenFile: os.Getenv(workloadauth.EnvTokenFile),
	}
	c.MigrateDSN = getOr("MIGRATE_DSN", c.DatabaseDSN)

	var errs []error
	var err error
	if c.WorkloadAuth, c.WorkloadAuthEnabled, err = workloadauth.ServerConfigFromEnv(os.Getenv); err != nil {
		errs = append(errs, err)
	}
	if c.WorkloadTokenFile == "" && os.Getenv(workloadauth.EnvAuthMode) != workloadauth.AuthDisabled {
		errs = append(errs, errors.New(workloadauth.EnvTokenFile+" is required to call steward-core; set "+workloadauth.EnvAuthMode+"="+workloadauth.AuthDisabled+" for local development only"))
	}
	if c.DatabaseDSN == "" {
		errs = append(errs, errors.New("DATABASE_DSN is required"))
	}
	if c.CoreGRPCAddr == "" {
		errs = append(errs, errors.New("CORE_GRPC_ADDR is required"))
	}
	if c.MagicLinkNonSensitiveTTL <= 0 {
		errs = append(errs, errors.New("MAGIC_LINK_NONSENSITIVE_TTL must be > 0"))
	}
	if c.MagicLinkSensitiveTTL <= 0 {
		errs = append(errs, errors.New("MAGIC_LINK_SENSITIVE_TTL must be > 0"))
	}
	if c.PDFLinkTTL <= 0 {
		errs = append(errs, errors.New("PDF_LINK_TTL must be > 0"))
	}
	if (c.S3Endpoint == "") != (c.S3Bucket == "") {
		errs = append(errs, errors.New("S3_ENDPOINT and S3_BUCKET are set together or not at all"))
	}
	tlsSet := c.TLSCertFile != "" || c.TLSKeyFile != "" || c.TLSClientCAFile != ""
	if tlsSet && (c.TLSCertFile == "" || c.TLSKeyFile == "" || c.TLSClientCAFile == "") {
		errs = append(errs, errors.New("GRPC_TLS_CERT_FILE, GRPC_TLS_KEY_FILE and GRPC_TLS_CLIENT_CA_FILE are set together"))
	}
	return c, errors.Join(errs...)
}

// PDFExportMissing lists the settings PDF export needs that aren't set:
// without object storage a render has no bucket to write to and nothing can
// be downloaded, and without INTERNAL_BASE_URL the renderer has no URL to
// fetch the policy from.
func (c Config) PDFExportMissing() []string {
	var missing []string
	if c.S3Endpoint == "" || c.S3Bucket == "" {
		missing = append(missing, "S3_ENDPOINT and S3_BUCKET")
	}
	if c.InternalBaseURL == "" {
		missing = append(missing, "INTERNAL_BASE_URL")
	}
	return missing
}

func getOr(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func boolOr(k string, d bool) bool {
	if v := os.Getenv(k); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return d
}

func durationOr(k string, d time.Duration) time.Duration {
	if v := os.Getenv(k); v != "" {
		if dur, err := time.ParseDuration(v); err == nil {
			return dur
		}
	}
	return d
}
