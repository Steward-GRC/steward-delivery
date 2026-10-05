// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package errcodes_test

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/Bugs5382/go-apperr/apperrgrpc"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/Steward-GRC/steward-delivery/internal/errcodes"
)

func roundTrip(t *testing.T, err error) (*status.Status, apperrgrpc.Info) {
	t.Helper()
	st := status.Convert(errcodes.Error(context.Background(), err))
	info, ok := apperrgrpc.FromStatus(st)
	require.True(t, ok, "status must carry ErrorInfo")
	require.Equal(t, errcodes.Domain, info.Domain)
	return st, info
}

func TestStoreUnavailableNeverShowsTheCause(t *testing.T) {
	st, info := roundTrip(t, errcodes.StoreUnavailable("create_magic_link",
		errors.New("dial tcp 192.0.2.10:5432: connect: connection refused")))
	require.Equal(t, codes.Internal, st.Code())
	require.Equal(t, "Code 8001: Internal Error", st.Message())
	require.Equal(t, "STORE_UNAVAILABLE", info.Symbol)
	require.Equal(t, "create_magic_link", info.Metadata["op"])
}

func TestCoreUnavailableIsRetryable(t *testing.T) {
	st, info := roundTrip(t, errcodes.CoreUnavailable("get_policy_version", errors.New("connection refused")))
	require.Equal(t, codes.Unavailable, st.Code())
	require.Equal(t, "CORE_UNAVAILABLE", info.Symbol)
	require.Equal(t, "get_policy_version", info.Metadata["op"])
}

func TestPolicyVersionNotFound(t *testing.T) {
	st, info := roundTrip(t, errcodes.PolicyVersionNotFound("pv-1"))
	require.Equal(t, codes.NotFound, st.Code())
	require.Equal(t, "POLICY_VERSION_NOT_FOUND", info.Symbol)
	require.Equal(t, "That policy version doesn't exist.", st.Message())
}

func TestMagicLinkStates(t *testing.T) {
	cases := []struct {
		err    error
		code   codes.Code
		symbol string
		msg    string
	}{
		{errcodes.MagicLinkNotFound(), codes.NotFound, "MAGIC_LINK_NOT_FOUND", "This link isn't valid."},
		{errcodes.MagicLinkExpired(), codes.FailedPrecondition, "MAGIC_LINK_EXPIRED", "This link has expired. Ask the person who shared it for a new one."},
		{errcodes.MagicLinkRevoked(), codes.FailedPrecondition, "MAGIC_LINK_REVOKED", "This link has been revoked. Ask the person who shared it for a new one."},
		{errcodes.ViewerEmailRequired(), codes.InvalidArgument, "VIEWER_EMAIL_REQUIRED", "Enter your email address to open this sensitive policy."},
	}
	for _, c := range cases {
		st, info := roundTrip(t, c.err)
		require.Equal(t, c.code, st.Code(), c.symbol)
		require.Equal(t, c.symbol, info.Symbol)
		require.Equal(t, c.msg, st.Message())
	}
}

func TestPDFExportStates(t *testing.T) {
	cases := []struct {
		err    error
		code   codes.Code
		symbol string
	}{
		{errcodes.PDFExportDisabled(), codes.Unavailable, "PDF_EXPORT_DISABLED"},
		{errcodes.PDFExportNotFound("job-1"), codes.NotFound, "PDF_EXPORT_NOT_FOUND"},
		{errcodes.PDFExportNotReady("job-1"), codes.FailedPrecondition, "PDF_EXPORT_NOT_READY"},
		{errcodes.PDFExportFailed("job-1"), codes.FailedPrecondition, "PDF_EXPORT_FAILED"},
	}
	for _, c := range cases {
		st, info := roundTrip(t, c.err)
		require.Equal(t, c.code, st.Code(), c.symbol)
		require.Equal(t, c.symbol, info.Symbol)
	}
}

func TestRequiredFieldNamesTheField(t *testing.T) {
	st, info := roundTrip(t, errcodes.Required("policy_version_id"))
	require.Equal(t, codes.InvalidArgument, st.Code())
	require.Equal(t, "INVALID_REQUEST", info.Symbol)
	require.Equal(t, "policy_version_id is required.", st.Message())
}

func TestUncodedErrorsFallBackToInternal(t *testing.T) {
	_, info := roundTrip(t, errors.New("boom"))
	require.Equal(t, errcodes.CodeInternal, info.Code)
	require.Equal(t, "INTERNAL", info.Symbol)
}

func TestRegistryRange(t *testing.T) {
	require.NotEmpty(t, errcodes.Entries())
	for _, e := range errcodes.Entries() {
		require.Equal(t, 80, e.Code/100, "code %d must be in 8000 to 8099", e.Code)
		_, ok := errcodes.Registry().Describe(e.Code)
		require.True(t, ok)
	}
}

// docs/error-codes.md is generated from the registry; refresh it with
// UPDATE_DOCS=1 go test ./internal/errcodes.
func TestErrorCodesDocIsCurrent(t *testing.T) {
	const path = "../../docs/error-codes.md"
	want := errcodes.Doc()
	if os.Getenv("UPDATE_DOCS") == "1" {
		require.NoError(t, os.WriteFile(path, []byte(want), 0o600))
	}
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, want, string(got), "docs/error-codes.md is stale; run UPDATE_DOCS=1 go test ./internal/errcodes")
}
