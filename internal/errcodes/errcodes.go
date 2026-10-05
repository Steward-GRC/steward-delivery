// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package errcodes holds the delivery service's coded errors (8000 to 8099)
// and turns them into gRPC statuses through go-apperr.
package errcodes

import (
	"context"
	"errors"
	"sync"

	apperr "github.com/Bugs5382/go-apperr"
	"github.com/Bugs5382/go-apperr/apperrgrpc"
	log "github.com/Bugs5382/go-log"
)

// Domain is the ErrorInfo domain every delivery error carries.
const Domain = "delivery"

// The delivery service's codes.
const (
	CodeInternal              = 8000
	CodeStoreUnavailable      = 8001
	CodeCoreUnavailable       = 8002
	CodePolicyVersionNotFound = 8003
	CodeMagicLinkNotFound     = 8004
	CodeMagicLinkExpired      = 8005
	CodeMagicLinkRevoked      = 8006
	CodeViewerEmailRequired   = 8007
	CodePDFExportDisabled     = 8008
	CodePDFExportNotFound     = 8009
	CodePDFExportNotReady     = 8010
	CodePDFExportFailed       = 8011
	CodeInvalidRequest        = 8012
)

const askForNewLink = " Ask the person who shared it for a new one."

// Entries returns the registry entries.
func Entries() []apperr.Entry {
	return []apperr.Entry{
		{Code: CodeInternal, Symbol: "INTERNAL", Category: apperr.CategoryInternal,
			Title: "delivery", Cause: "an uncoded failure inside the delivery service"},
		{Code: CodeStoreUnavailable, Symbol: "STORE_UNAVAILABLE", Category: apperr.CategoryInternal,
			Title: "delivery store", Cause: "a delivery Postgres read or write failed; the op metadata names it, the cause is only logged"},
		{Code: CodeCoreUnavailable, Symbol: "CORE_UNAVAILABLE", Category: apperr.CategoryUnavailable,
			Title: "core", Cause: "a call to steward-core failed; the op metadata names it, the cause is only logged"},
		{Code: CodePolicyVersionNotFound, Symbol: "POLICY_VERSION_NOT_FOUND", Category: apperr.CategoryNotFound,
			Title: "policy version", Cause: "core has no policy version with that id",
			UserSafe: true, Message: "That policy version doesn't exist."},
		{Code: CodeMagicLinkNotFound, Symbol: "MAGIC_LINK_NOT_FOUND", Category: apperr.CategoryNotFound,
			Title: "magic link", Cause: "no magic link has that token",
			UserSafe: true, Message: "This link isn't valid."},
		{Code: CodeMagicLinkExpired, Symbol: "MAGIC_LINK_EXPIRED", Category: apperr.CategoryFailedPrecondition,
			Title: "magic link", Cause: "the magic link is past its expiry",
			UserSafe: true, Message: "This link has expired." + askForNewLink},
		{Code: CodeMagicLinkRevoked, Symbol: "MAGIC_LINK_REVOKED", Category: apperr.CategoryFailedPrecondition,
			Title: "magic link", Cause: "the magic link was revoked",
			UserSafe: true, Message: "This link has been revoked." + askForNewLink},
		{Code: CodeViewerEmailRequired, Symbol: "VIEWER_EMAIL_REQUIRED", Category: apperr.CategoryInvalid,
			Title: "magic link", Cause: "a sensitive magic link was opened without the viewer's email address",
			UserSafe: true, Message: "Enter your email address to open this sensitive policy."},
		{Code: CodePDFExportDisabled, Symbol: "PDF_EXPORT_DISABLED", Category: apperr.CategoryUnavailable,
			Title: "PDF export", Cause: "PDF export is off, or the service has no Kubernetes API to create the render on",
			UserSafe: true, Message: "PDF export isn't available right now."},
		{Code: CodePDFExportNotFound, Symbol: "PDF_EXPORT_NOT_FOUND", Category: apperr.CategoryNotFound,
			Title: "PDF export", Cause: "no PDF export job has that id",
			UserSafe: true, Message: "That PDF export doesn't exist."},
		{Code: CodePDFExportNotReady, Symbol: "PDF_EXPORT_NOT_READY", Category: apperr.CategoryFailedPrecondition,
			Title: "PDF export", Cause: "the PDF is still rendering",
			UserSafe: true, Message: "The PDF is still being rendered."},
		{Code: CodePDFExportFailed, Symbol: "PDF_EXPORT_FAILED", Category: apperr.CategoryFailedPrecondition,
			Title: "PDF export", Cause: "the renderer reported a failure; the job row holds its message",
			UserSafe: true, Message: "The PDF couldn't be rendered. Try the export again."},
		{Code: CodeInvalidRequest, Symbol: "INVALID_REQUEST", Category: apperr.CategoryInvalid,
			Title: "request", Cause: "a required request field is empty",
			UserSafe: true, Message: "{field} is required."},
	}
}

var (
	regOnce sync.Once
	reg     *apperr.Registry
)

// Registry returns the service registry. Coded errors are logged through
// go-log with the trace of the request they failed.
func Registry() *apperr.Registry {
	regOnce.Do(func() {
		r, err := apperr.NewRegistry(Entries(), apperr.WithService(80), apperr.WithCodeDigits(4),
			apperr.WithLogger(logSink{log.NewLogger("delivery")}))
		if err != nil {
			panic(err)
		}
		reg = r
	})
	return reg
}

// Error turns err into the gRPC error a handler returns.
func Error(ctx context.Context, err error) error {
	return apperrgrpc.Error(ctx, Registry(), err, CodeInternal, Domain)
}

// Doc is the Markdown body of docs/error-codes.md.
func Doc() string {
	return "# Error codes\n\nEvery coded gRPC error from the delivery service carries an `ErrorInfo` with the symbol as\n" +
		"its reason, the domain `" + Domain + "` and the code in `codeNum`. Only user-safe messages reach\n" +
		"the caller; every other code is sent as `Code N: Internal Error`. Delivery owns 8000 to 8099.\n\n" +
		Registry().Markdown()
}

func withOp(code int, op string, cause error) error {
	return apperr.WithMeta(apperr.Coded(code, cause), apperr.Meta("op", op))
}

// StoreUnavailable codes a failed store call; op names it.
func StoreUnavailable(op string, cause error) error { return withOp(CodeStoreUnavailable, op, cause) }

// CoreUnavailable codes a failed call to steward-core; op names it.
func CoreUnavailable(op string, cause error) error { return withOp(CodeCoreUnavailable, op, cause) }

// PolicyVersionNotFound codes a version core doesn't have.
func PolicyVersionNotFound(id string) error {
	return apperr.WithMeta(apperr.Coded(CodePolicyVersionNotFound, errors.New("delivery: policy version not found")),
		apperr.Meta("policy_version_id", id))
}

// MagicLinkNotFound codes an unknown token.
func MagicLinkNotFound() error {
	return apperr.Coded(CodeMagicLinkNotFound, errors.New("delivery: magic link not found"))
}

// MagicLinkExpired codes a token past its expiry.
func MagicLinkExpired() error {
	return apperr.Coded(CodeMagicLinkExpired, errors.New("delivery: magic link expired"))
}

// MagicLinkRevoked codes a revoked token.
func MagicLinkRevoked() error {
	return apperr.Coded(CodeMagicLinkRevoked, errors.New("delivery: magic link revoked"))
}

// ViewerEmailRequired codes a sensitive link opened without an email.
func ViewerEmailRequired() error {
	return apperr.Coded(CodeViewerEmailRequired, errors.New("delivery: viewer email required"))
}

// PDFExportDisabled codes an export request while export is off.
func PDFExportDisabled() error {
	return apperr.Coded(CodePDFExportDisabled, errors.New("delivery: PDF export is disabled"))
}

func job(code int, msg, jobID string) error {
	return apperr.WithMeta(apperr.Coded(code, errors.New(msg)), apperr.Meta("job_id", jobID))
}

// PDFExportNotFound codes an unknown job id.
func PDFExportNotFound(jobID string) error {
	return job(CodePDFExportNotFound, "delivery: PDF export job not found", jobID)
}

// PDFExportNotReady codes a job that is still pending or rendering.
func PDFExportNotReady(jobID string) error {
	return job(CodePDFExportNotReady, "delivery: PDF export not ready", jobID)
}

// PDFExportFailed codes a job the renderer failed.
func PDFExportFailed(jobID string) error {
	return job(CodePDFExportFailed, "delivery: PDF export failed", jobID)
}

// Required codes an empty required request field.
func Required(field string) error {
	return apperr.WithMeta(apperr.Coded(CodeInvalidRequest, errors.New("delivery: "+field+" is required")),
		apperr.Meta("field", field))
}

type logSink struct{ l log.Logger }

func (s logSink) LogCoded(ctx context.Context, code int, err error) {
	s.l.Ctx(ctx).Debug("coded error", log.F("code", code), log.F("error", err.Error()))
}
