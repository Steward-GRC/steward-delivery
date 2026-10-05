// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bugs5382/go-buildinfo/health"
	log "github.com/Bugs5382/go-log"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
)

type probe struct {
	deliveryv1.UnimplementedDeliveryServiceServer
}

func (probe) GetRenderedContent(context.Context, *deliveryv1.GetRenderedContentRequest) (*deliveryv1.GetRenderedContentResponse, error) {
	panic("boom: secret detail")
}

func serve(t *testing.T, opts Options) (*grpc.ClientConn, func()) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Serve(ctx, lis, log.Nop(), opts, func(s *grpc.Server) { deliveryv1.RegisterDeliveryServiceServer(s, probe{}) })
	}()
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	return conn, func() {
		_ = conn.Close()
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err, "Serve returns nil after shutdown")
		case <-time.After(15 * time.Second):
			t.Fatal("Serve did not stop")
		}
	}
}

func TestServeServesHealthRecoversPanicsAndStops(t *testing.T) {
	conn, stop := serve(t, Options{})
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	hc, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, hc.GetStatus())

	_, err = deliveryv1.NewDeliveryServiceClient(conn).GetRenderedContent(ctx, &deliveryv1.GetRenderedContentRequest{})
	st := status.Convert(err)
	require.Equal(t, codes.Internal, st.Code())
	require.Equal(t, "internal error", st.Message(), "a panic surfaces as a bare Internal")
}

func downChecker(t *testing.T, up *atomic.Bool) *health.Checker {
	t.Helper()
	c := health.New(health.WithTTL(time.Millisecond))
	require.NoError(t, c.Register(health.Dependency{Name: "postgres", Required: true, Check: func(context.Context) error {
		if up.Load() {
			return nil
		}
		return errors.New("postgres down")
	}}))
	return c
}

func TestReadinessFollowsTheCheckerAndLivenessDoesNot(t *testing.T) {
	var up atomic.Bool
	conn, stop := serve(t, Options{Checker: downChecker(t, &up), CheckInterval: 20 * time.Millisecond})
	defer stop()
	hc := healthpb.NewHealthClient(conn)
	status := func(svc string) healthpb.HealthCheckResponse_ServingStatus {
		r, err := hc.Check(context.Background(), &healthpb.HealthCheckRequest{Service: svc})
		require.NoError(t, err)
		return r.GetStatus()
	}
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, status(""))
	require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, status(ReadinessService))
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, status(LivenessService), "liveness never follows a dependency")
	up.Store(true)
	require.Eventually(t, func() bool { return status(ReadinessService) == healthpb.HealthCheckResponse_SERVING }, 2*time.Second, 10*time.Millisecond)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, status(""))
}

func TestHealthCheckCarriesTheBuildAndDependencyHeaders(t *testing.T) {
	var up atomic.Bool
	conn, stop := serve(t, Options{Checker: downChecker(t, &up)})
	defer stop()
	var md metadata.MD
	_, err := healthpb.NewHealthClient(conn).Check(context.Background(), &healthpb.HealthCheckRequest{}, grpc.Header(&md))
	require.NoError(t, err)
	require.Equal(t, []string{"dev"}, md.Get("steward-version"), "an unstamped build")
	require.NotEmpty(t, md.Get("steward-commit"))
	require.Equal(t, []string{"down"}, md.Get("steward-depstate-postgres"))
}

func TestProbesServeLivezAndReadyzOnly(t *testing.T) {
	var up atomic.Bool
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeProbes(ctx, lis, downChecker(t, &up)) }()
	defer func() {
		cancel()
		require.NoError(t, <-done, "ServeProbes returns nil after shutdown")
	}()
	get := func(path string) (int, http.Header) {
		res, err := http.Get("http://" + lis.Addr().String() + path)
		require.NoError(t, err)
		_ = res.Body.Close()
		return res.StatusCode, res.Header
	}
	code, _ := get("/livez")
	require.Equal(t, http.StatusOK, code, "liveness never follows a dependency")
	code, h := get("/readyz")
	require.Equal(t, http.StatusServiceUnavailable, code)
	require.Equal(t, "dev", h.Get("Steward-Version"))
	up.Store(true)
	require.Eventually(t, func() bool { c, _ := get("/readyz"); return c == http.StatusOK }, 2*time.Second, 10*time.Millisecond)
	code, _ = get("/health")
	require.Equal(t, http.StatusNotFound, code, "there is no plain /health")
}
