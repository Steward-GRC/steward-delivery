// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	log "github.com/Bugs5382/go-log"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"

	"github.com/Steward-GRC/steward-delivery/internal/workloadauth"
)

type denials struct {
	mu  sync.Mutex
	got []workloadauth.Denial
}

func (d *denials) record(_ context.Context, x workloadauth.Denial) {
	d.mu.Lock()
	d.got = append(d.got, x)
	d.mu.Unlock()
}

// httpAuthServe puts the middleware in front of a handler that answers 200
// with the verified caller's name. steward-pdf-renderer and steward-gateway
// hold valid identities; only the renderer is allowed on this port.
func httpAuthServe(t *testing.T) (*localIssuer, *httptest.Server, *denials) {
	t.Helper()
	iss := newLocalIssuer(t)
	v, err := workloadauth.NewVerifier(workloadauth.Config{
		Issuer: iss.url, CAFile: iss.caFile, Audience: "steward",
		AllowedServiceAccounts: []string{testNS + "/steward-pdf-renderer", testNS + "/steward-gateway"},
	}, log.Nop())
	require.NoError(t, err)
	require.NoError(t, v.Refresh(context.Background()))
	d := &denials{}
	h := HTTPAuth(v, []string{"pdf-renderer"}, log.Nop(), d.record)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g, ok := workloadauth.GrantFromContext(r.Context())
		require.True(t, ok)
		_, _ = w.Write([]byte(g.Caller.Name))
	}))
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return iss, srv, d
}

func getHTML(t *testing.T, srv *httptest.Server, authorization string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL+"/internal/policies/v1/html", nil)
	require.NoError(t, err)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}
	res, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = res.Body.Close() }()
	b := make([]byte, 64)
	n, _ := res.Body.Read(b)
	return res.StatusCode, string(b[:n])
}

func TestHTTPAuthLetsTheRendererThrough(t *testing.T) {
	iss, srv, d := httpAuthServe(t)
	code, body := getHTML(t, srv, "Bearer "+iss.token(t, "steward-pdf-renderer", "steward"))
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, "pdf-renderer", body)
	require.Empty(t, d.got)
}

func TestHTTPAuthRefusesAMissingToken(t *testing.T) {
	_, srv, d := httpAuthServe(t)
	code, _ := getHTML(t, srv, "")
	require.Equal(t, http.StatusUnauthorized, code)
	require.Len(t, d.got, 1)
	require.Equal(t, codes.Unauthenticated, d.got[0].Code)
	require.Equal(t, workloadauth.ReasonNoToken, d.got[0].Reason)
	require.Equal(t, "GET /internal/policies/v1/html", d.got[0].Method)
}

func TestHTTPAuthRefusesABadToken(t *testing.T) {
	iss, srv, _ := httpAuthServe(t)
	for name, authz := range map[string]string{
		"malformed":                            "Bearer not-a-jwt",
		"another audience":                     "Bearer " + iss.token(t, "steward-pdf-renderer", "other"),
		"another issuer":                       "Bearer " + newLocalIssuer(t).token(t, "steward-pdf-renderer", "steward"),
		"not bearer":                           "Basic " + iss.token(t, "steward-pdf-renderer", "steward"),
		"outside the allowed service accounts": "Bearer " + iss.token(t, "steward-reporting", "steward"),
	} {
		code, _ := getHTML(t, srv, authz)
		require.Equal(t, http.StatusUnauthorized, code, name)
	}
}

func TestHTTPAuthRefusesAVerifiedCallerNotOnTheList(t *testing.T) {
	iss, srv, d := httpAuthServe(t)
	code, _ := getHTML(t, srv, "Bearer "+iss.token(t, "steward-gateway", "steward"))
	require.Equal(t, http.StatusForbidden, code, "the gateway is verified but may not fetch the HTML")
	require.Len(t, d.got, 1)
	require.Equal(t, codes.PermissionDenied, d.got[0].Code)
	require.Equal(t, "gateway", d.got[0].Caller.Name)
}

type unavailableVerifier struct{}

func (unavailableVerifier) Verify(string) (workloadauth.Caller, error) {
	return workloadauth.Caller{}, workloadauth.ErrUnavailable
}

func TestHTTPAuthFailsClosedWhileTheVerifierIsUnavailable(t *testing.T) {
	h := HTTPAuth(unavailableVerifier{}, []string{"pdf-renderer"}, log.Nop(), nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("the handler must not run")
	}))
	srv := httptest.NewServer(h)
	defer srv.Close()
	code, _ := getHTML(t, srv, "Bearer anything")
	require.Equal(t, http.StatusServiceUnavailable, code)
}
