// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bugs5382/go-buildinfo/health"
	log "github.com/Bugs5382/go-log"
	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/metadata"
	reflectionpb "google.golang.org/grpc/reflection/grpc_reflection_v1"
	"google.golang.org/grpc/status"

	workloadidentity "github.com/Bugs5382/go-workload-identity"
	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/config"
	"github.com/Steward-GRC/steward-delivery/internal/readiness"
)

const testNS = "steward"

// localIssuer is an OIDC issuer on a local TLS test server: discovery and a
// JWKS with one P-256 key generated in the test.
type localIssuer struct {
	url, caFile string
	key         *ecdsa.PrivateKey
	// jwksStatus, when set, is the status the JWKS endpoint answers instead
	// of the key set.
	jwksStatus atomic.Int32
}

func newLocalIssuer(t *testing.T) *localIssuer {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	iss := &localIssuer{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"issuer": iss.url, "jwks_uri": iss.url + "/openid/v1/jwks"})
	})
	mux.HandleFunc("/openid/v1/jwks", func(w http.ResponseWriter, _ *http.Request) {
		if code := iss.jwksStatus.Load(); code != 0 {
			http.Error(w, http.StatusText(int(code)), int(code))
			return
		}
		pub, err := key.PublicKey.ECDH()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		raw := pub.Bytes()
		b64 := base64.RawURLEncoding.EncodeToString
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
			"kty": "EC", "kid": "k1", "use": "sig", "crv": "P-256", "x": b64(raw[1:33]), "y": b64(raw[33:]),
		}}})
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	iss.url = srv.URL
	iss.caFile = filepath.Join(t.TempDir(), "ca.pem")
	require.NoError(t, os.WriteFile(iss.caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}), 0o600))
	return iss
}

func (i *localIssuer) token(t *testing.T, sa, audience string) string {
	t.Helper()
	now := time.Now()
	tok := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{
		"iss": i.url, "aud": []string{audience}, "sub": "system:serviceaccount:" + testNS + ":" + sa,
		"iat": now.Unix(), "nbf": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	tok.Header["kid"] = "k1"
	s, err := tok.SignedString(i.key)
	require.NoError(t, err)
	return s
}

var getDiff = deliveryv1.DeliveryService_GetDiff_FullMethodName

// authServe serves the probe with workload auth on: steward-gateway and
// steward-reporting hold valid identities, but the policy lists only the
// gateway. The probe leaves GetDiff unimplemented, so Unimplemented means the
// call got past authentication.
func authServe(t *testing.T) (*localIssuer, *grpc.ClientConn, func()) {
	t.Helper()
	iss := newLocalIssuer(t)
	v, err := workloadidentity.NewVerifier(config.StewardWorkload(workloadidentity.Config{
		Issuer: iss.url, CAFile: iss.caFile,
		AllowedServiceAccounts: []string{testNS + "/steward-gateway", testNS + "/steward-reporting"},
	}), log.Nop())
	require.NoError(t, err)
	require.NoError(t, v.Refresh(context.Background()))
	conn, stop := serve(t, Options{Auth: &Auth{
		Verifier: v,
		Policy:   workloadidentity.Policy{getDiff: {"gateway": workloadidentity.OnBehalf}},
	}})
	return iss, conn, stop
}

func bearerCtx(tok string) context.Context {
	return metadata.AppendToOutgoingContext(context.Background(), "authorization", "Bearer "+tok)
}

func callDiff(conn *grpc.ClientConn, ctx context.Context) codes.Code {
	_, err := deliveryv1.NewDeliveryServiceClient(conn).GetDiff(ctx, &deliveryv1.GetDiffRequest{})
	return status.Code(err)
}

func TestWorkloadAuthLetsTheGatewayThrough(t *testing.T) {
	iss, conn, stop := authServe(t)
	defer stop()
	require.Equal(t, codes.Unimplemented, callDiff(conn, bearerCtx(iss.token(t, "steward-gateway", "steward"))))
}

func TestWorkloadAuthRefusesAValidTokenFromAnUnlistedCaller(t *testing.T) {
	iss, conn, stop := authServe(t)
	defer stop()
	require.Equal(t, codes.PermissionDenied, callDiff(conn, bearerCtx(iss.token(t, "steward-reporting", "steward"))),
		"a verified identity the method doesn't list")
	require.Equal(t, codes.Unauthenticated, callDiff(conn, bearerCtx(iss.token(t, "steward-pdf-renderer", "steward"))),
		"a valid token from a service account outside WORKLOAD_ALLOWED_SERVICEACCOUNTS")
}

func TestWorkloadAuthRefusesAMissingOrInvalidToken(t *testing.T) {
	iss, conn, stop := authServe(t)
	defer stop()
	require.Equal(t, codes.Unauthenticated, callDiff(conn, context.Background()), "no token")
	require.Equal(t, codes.Unauthenticated, callDiff(conn, bearerCtx("not-a-jwt")), "a malformed token")
	require.Equal(t, codes.Unauthenticated, callDiff(conn, bearerCtx(iss.token(t, "steward-gateway", "other"))), "another audience")
	other := newLocalIssuer(t)
	require.Equal(t, codes.Unauthenticated, callDiff(conn, bearerCtx(other.token(t, "steward-gateway", "steward"))), "another issuer")
}

func TestWorkloadAuthLeavesHealthAndReflectionOpen(t *testing.T) {
	_, conn, stop := authServe(t)
	defer stop()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	hc, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	require.NoError(t, err)
	require.Equal(t, healthpb.HealthCheckResponse_SERVING, hc.GetStatus())
	rs, err := reflectionpb.NewServerReflectionClient(conn).ServerReflectionInfo(ctx)
	require.NoError(t, err)
	require.NoError(t, rs.Send(&reflectionpb.ServerReflectionRequest{MessageRequest: &reflectionpb.ServerReflectionRequest_ListServices{}}))
	res, err := rs.Recv()
	require.NoError(t, err)
	require.NotEmpty(t, res.GetListServicesResponse().GetService())
}

func TestWorkloadAuthDisabledLetsCallsThrough(t *testing.T) {
	conn, stop := serve(t, Options{})
	defer stop()
	require.Equal(t, codes.Unimplemented, callDiff(conn, context.Background()), "WORKLOAD_AUTH=disabled serves a call with no token")
}

// tokenSink is a gRPC server that records the authorization metadata of each
// call it gets.
type tokenSink struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	mu   sync.Mutex
	seen []string
}

func (s *tokenSink) GetDiff(ctx context.Context, _ *deliveryv1.GetDiffRequest) (*deliveryv1.GetDiffResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	s.mu.Lock()
	s.seen = append(s.seen, md.Get("authorization")...)
	s.mu.Unlock()
	return &deliveryv1.GetDiffResponse{}, nil
}

func dialSink(t *testing.T, opts []grpc.DialOption) (*tokenSink, *grpc.ClientConn) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	sink := &tokenSink{}
	s := grpc.NewServer()
	deliveryv1.RegisterDeliveryServiceServer(s, sink)
	go func() { _ = s.Serve(lis) }()
	t.Cleanup(s.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, opts...)...)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })
	return sink, conn
}

func TestClientAuthSendsTheTokenReReadOnEveryCall(t *testing.T) {
	file := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(file, []byte("first\n"), 0o600))
	opts, err := ClientAuth(file)
	require.NoError(t, err)
	sink, conn := dialSink(t, opts)
	client := deliveryv1.NewDeliveryServiceClient(conn)
	_, err = client.GetDiff(context.Background(), &deliveryv1.GetDiffRequest{})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, []byte("rotated"), 0o600))
	_, err = client.GetDiff(context.Background(), &deliveryv1.GetDiffRequest{})
	require.NoError(t, err)
	require.Equal(t, []string{"Bearer first", "Bearer rotated"}, sink.seen)
}

func TestClientAuthFailsClosedOnAMissingTokenFile(t *testing.T) {
	_, err := ClientAuth(filepath.Join(t.TempDir(), "absent"))
	require.ErrorContains(t, err, workloadidentity.EnvTokenFile, "a missing mount stops the boot")

	file := filepath.Join(t.TempDir(), "token")
	require.NoError(t, os.WriteFile(file, []byte("tok"), 0o600))
	opts, err := ClientAuth(file)
	require.NoError(t, err)
	sink, conn := dialSink(t, opts)
	require.NoError(t, os.Remove(file))
	_, err = deliveryv1.NewDeliveryServiceClient(conn).GetDiff(context.Background(), &deliveryv1.GetDiffRequest{})
	require.Error(t, err, "a token that disappears fails the call instead of sending none")
	require.ErrorContains(t, err, workloadidentity.EnvTokenFile)
	require.Empty(t, sink.seen)
}

func TestClientAuthWithNoTokenFileSendsNone(t *testing.T) {
	opts, err := ClientAuth("")
	require.NoError(t, err)
	require.Empty(t, opts, "only with WORKLOAD_AUTH=disabled, which config enforces")
}

type upDB struct{}

func (upDB) Ping(context.Context) error                    { return nil }
func (upDB) ServerVersion(context.Context) (string, error) { return "16.4", nil }

type upBroker struct{}

func (upBroker) Healthy() bool { return true }

type upCore struct{}

func (upCore) Check(context.Context) error             { return nil }
func (upCore) Version(context.Context) (string, error) { return "dev", nil }

// The issuer refusing the JWKS fetch (as an API server does for a bearer with
// the wrong audience) must never leave the verifier inert: a valid-looking
// token is refused on the gRPC port and the internal render port, and
// readiness drains the pod on both probes.
func TestWorkloadAuthFailsClosedWhileTheJWKSIsRefused(t *testing.T) {
	iss := newLocalIssuer(t)
	iss.jwksStatus.Store(http.StatusUnauthorized)
	v, err := workloadidentity.NewVerifier(config.StewardWorkload(workloadidentity.Config{
		Issuer: iss.url, CAFile: iss.caFile,
		AllowedServiceAccounts: []string{testNS + "/steward-gateway", testNS + "/steward-pdf-renderer"},
	}), log.Nop())
	require.NoError(t, err)
	require.Error(t, v.Refresh(context.Background()), "a 401 from the JWKS is a failed refresh")

	checker, err := readiness.New(readiness.Deps{Postgres: upDB{}, Broker: upBroker{}, Core: upCore{},
		JWKS: readiness.RecheckEvery(v.Refresh, time.Minute, time.Now)}, health.WithTTL(time.Millisecond))
	require.NoError(t, err)
	conn, stop := serve(t, Options{Checker: checker, CheckInterval: 20 * time.Millisecond, Auth: &Auth{
		Verifier: v,
		Policy:   workloadidentity.Policy{getDiff: {"gateway": workloadidentity.OnBehalf}},
	}})
	defer stop()

	require.Equal(t, codes.Unavailable, callDiff(conn, bearerCtx(iss.token(t, "steward-gateway", "steward"))),
		"refused before the handler, which would answer Unimplemented")

	internal := httptest.NewServer(HTTPAuth(v, []string{"pdf-renderer"}, log.Nop(), nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("the render handler ran without a verified caller")
	})))
	defer internal.Close()
	code, _ := getHTML(t, internal, "Bearer "+iss.token(t, "steward-pdf-renderer", "steward"))
	require.Equal(t, http.StatusServiceUnavailable, code)

	hc := healthpb.NewHealthClient(conn)
	for _, svc := range []string{"", ReadinessService} {
		r, err := hc.Check(context.Background(), &healthpb.HealthCheckRequest{Service: svc})
		require.NoError(t, err)
		require.Equal(t, healthpb.HealthCheckResponse_NOT_SERVING, r.GetStatus(), "service %q", svc)
	}
	rep := checker.Report(context.Background())
	require.False(t, rep.Ready)
	var jwks *health.DependencyReport
	for i := range rep.Dependencies {
		if rep.Dependencies[i].Name == readiness.JWKS {
			jwks = &rep.Dependencies[i]
		}
	}
	require.NotNil(t, jwks, "the key set is listed in the readiness report")
	require.True(t, jwks.Required)
	require.Equal(t, health.StateDown, jwks.State)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ServeProbes(ctx, lis, checker) }()
	defer func() { cancel(); require.NoError(t, <-done) }()
	res, err := http.Get("http://" + lis.Addr().String() + "/readyz")
	require.NoError(t, err)
	_ = res.Body.Close()
	require.Equal(t, http.StatusServiceUnavailable, res.StatusCode)
}
