// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package grpcsvc implements steward.delivery.v1.DeliveryService.
package grpcsvc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	objectstore "github.com/Bugs5382/go-objectstore"
	"google.golang.org/protobuf/types/known/timestamppb"

	deliveryv1 "github.com/Steward-GRC/steward-delivery/gen/go/steward/delivery/v1"
	"github.com/Steward-GRC/steward-delivery/internal/errcodes"
	"github.com/Steward-GRC/steward-delivery/internal/magiclink"
	"github.com/Steward-GRC/steward-delivery/internal/pdfrender"
	"github.com/Steward-GRC/steward-delivery/internal/policyhttp"
	"github.com/Steward-GRC/steward-delivery/internal/render"
	"github.com/Steward-GRC/steward-delivery/internal/store"
)

// DefaultPDFLinkTTL is how long a PDF download link lasts unless
// WithPDFLinkTTL says otherwise.
const DefaultPDFLinkTTL = 15 * time.Minute

// PolicyClient reads from steward-core; corepolicy.Client implements it.
type PolicyClient interface {
	// GetContent returns a version's Lexical JSON.
	GetContent(ctx context.Context, policyVersionID string) ([]byte, error)
	// GetDiff returns the section diff between two versions.
	GetDiff(ctx context.Context, fromID, toID string) ([]*deliveryv1.SectionDiff, error)
}

// SensitivityLookup reports whether a version's policy is classified
// sensitive; corepolicy.Sensitivity implements it.
type SensitivityLookup interface {
	Sensitive(ctx context.Context, policyVersionID string) (bool, error)
}

// PDFJobStore persists export jobs; store.PDFJobRepo implements it.
type PDFJobStore interface {
	// Create inserts a pending job, before the PdfRender resource exists,
	// so GetPDFDownloadLink always finds a row.
	Create(ctx context.Context, jobID, policyVersionID, requesterUserID string) error
	// GetArtifactKey returns a done job's object key.
	GetArtifactKey(ctx context.Context, jobID string) (string, error)
}

// PDFRenderClient creates PdfRender resources; pdfrender.Client implements it.
type PDFRenderClient interface {
	Create(ctx context.Context, name string, spec pdfrender.Spec) error
}

// Signer mints download URLs for stored objects.
type Signer interface {
	SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error)
}

// ObjectStoreSigner signs with go-objectstore's PresignGet.
type ObjectStoreSigner struct{ Store objectstore.Store }

// SignedURL presigns a GET of key for ttl.
func (s ObjectStoreSigner) SignedURL(ctx context.Context, key string, ttl time.Duration) (string, error) {
	p, err := s.Store.PresignGet(ctx, key, ttl)
	if err != nil {
		return "", err
	}
	return p.URL.String(), nil
}

// PDFConfig is what a PdfRender needs from the environment.
type PDFConfig struct {
	// FetchURLBase is the in-cluster base URL of delivery's internal HTTP
	// endpoint; a render fetches FetchURLBase + "/internal/policies/{id}/html".
	FetchURLBase string
	// OutputBucket is the bucket the renderer uploads into.
	OutputBucket string
}

// DeliveryHandler implements steward.delivery.v1.DeliveryService.
type DeliveryHandler struct {
	deliveryv1.UnimplementedDeliveryServiceServer
	policy      PolicyClient
	sensitivity SensitivityLookup
	mlSvc       *magiclink.Service
	renderer    *render.Renderer
	pdfClient   PDFRenderClient // nil while PDF export is off
	pdfCfg      PDFConfig
	jobStore    PDFJobStore
	signer      Signer
	pdfLinkTTL  time.Duration
}

// NewDeliveryHandler returns a handler without PDF export.
func NewDeliveryHandler(policy PolicyClient, mlSvc *magiclink.Service) *DeliveryHandler {
	return &DeliveryHandler{policy: policy, mlSvc: mlSvc, renderer: render.New(), pdfLinkTTL: DefaultPDFLinkTTL}
}

// NewDeliveryHandlerWithPDF returns a handler that can request exports but
// has no job store or signer.
func NewDeliveryHandlerWithPDF(policy PolicyClient, mlSvc *magiclink.Service, pdfClient PDFRenderClient, cfg PDFConfig) *DeliveryHandler {
	h := NewDeliveryHandler(policy, mlSvc)
	h.pdfClient, h.pdfCfg = pdfClient, cfg
	return h
}

// NewDeliveryHandlerFull returns the production handler. A nil pdfClient
// means PDF export is off.
func NewDeliveryHandlerFull(policy PolicyClient, mlSvc *magiclink.Service, pdfClient PDFRenderClient, cfg PDFConfig, jobStore PDFJobStore, signer Signer) *DeliveryHandler {
	h := NewDeliveryHandlerWithPDF(policy, mlSvc, pdfClient, cfg)
	h.jobStore, h.signer = jobStore, signer
	return h
}

// WithSensitivity also reads the policy's classification from core when
// deciding the export watermark.
func (h *DeliveryHandler) WithSensitivity(s SensitivityLookup) *DeliveryHandler {
	h.sensitivity = s
	return h
}

// WithPDFLinkTTL sets the lifetime of a PDF download link.
func (h *DeliveryHandler) WithPDFLinkTTL(d time.Duration) *DeliveryHandler {
	h.pdfLinkTTL = d
	return h
}

func (h *DeliveryHandler) content(ctx context.Context, versionID string) ([]byte, error) {
	if versionID == "" {
		return nil, errcodes.Required("policy_version_id")
	}
	raw, err := h.policy.GetContent(ctx, versionID)
	if errors.Is(err, policyhttp.ErrNotFound) {
		return nil, errcodes.PolicyVersionNotFound(versionID)
	}
	if err != nil {
		return nil, errcodes.CoreUnavailable("get_policy_version", err)
	}
	return raw, nil
}

// GetRenderedContent renders a version's content with the server renderer.
func (h *DeliveryHandler) GetRenderedContent(ctx context.Context, req *deliveryv1.GetRenderedContentRequest) (*deliveryv1.GetRenderedContentResponse, error) {
	raw, err := h.content(ctx, req.GetPolicyVersionId())
	if err != nil {
		return nil, errcodes.Error(ctx, err)
	}
	renderedHTML, err := h.renderer.RenderHTML(raw)
	if err != nil {
		return nil, errcodes.Error(ctx, fmt.Errorf("render html: %w", err))
	}
	return &deliveryv1.GetRenderedContentResponse{Html: renderedHTML}, nil
}

// GetDiff passes core's section diff through; the diff itself lives in core.
func (h *DeliveryHandler) GetDiff(ctx context.Context, req *deliveryv1.GetDiffRequest) (*deliveryv1.GetDiffResponse, error) {
	if req.GetPolicyVersionIdFrom() == "" {
		return nil, errcodes.Error(ctx, errcodes.Required("policy_version_id_from"))
	}
	if req.GetPolicyVersionIdTo() == "" {
		return nil, errcodes.Error(ctx, errcodes.Required("policy_version_id_to"))
	}
	sections, err := h.policy.GetDiff(ctx, req.GetPolicyVersionIdFrom(), req.GetPolicyVersionIdTo())
	if err != nil {
		return nil, errcodes.Error(ctx, errcodes.CoreUnavailable("diff_versions", err))
	}
	return &deliveryv1.GetDiffResponse{Sections: sections}, nil
}

// RequestPDFExport records a pending job and creates its PdfRender. The
// watermark follows the policy, never the request: the version is exported
// as sensitive if its content says so or its policy is classified sensitive.
func (h *DeliveryHandler) RequestPDFExport(ctx context.Context, req *deliveryv1.RequestPDFExportRequest) (*deliveryv1.RequestPDFExportResponse, error) {
	if h.pdfClient == nil {
		return nil, errcodes.Error(ctx, errcodes.PDFExportDisabled())
	}
	versionID := req.GetPolicyVersionId()
	raw, err := h.content(ctx, versionID)
	if err != nil {
		return nil, errcodes.Error(ctx, err)
	}
	var meta struct {
		Sensitivity bool `json:"sensitivity"`
	}
	// Malformed content leaves the flag false; the renderer then fails on it.
	_ = json.Unmarshal(raw, &meta)
	sensitive := meta.Sensitivity
	if !sensitive && h.sensitivity != nil {
		if sensitive, err = h.sensitivity.Sensitive(ctx, versionID); err != nil {
			return nil, errcodes.Error(ctx, errcodes.CoreUnavailable("get_policy", err))
		}
	}
	sensitivity := "standard"
	if sensitive {
		sensitivity = "sensitive"
	}

	// The job id is also the resource name, so it must stay a valid
	// Kubernetes name: version ids are lower-case UUIDs.
	jobID := fmt.Sprintf("pdf-%s-%d", versionID, time.Now().UnixNano())
	outputKey := fmt.Sprintf("artifacts/%s/%s.pdf", versionID, jobID)

	if h.jobStore != nil {
		if err := h.jobStore.Create(ctx, jobID, versionID, req.GetRequesterUserId()); err != nil {
			return nil, errcodes.Error(ctx, errcodes.StoreUnavailable("create_pdf_job", err))
		}
	}
	spec := pdfrender.Spec{
		PolicyVersionID: versionID,
		FetchURL:        h.pdfCfg.FetchURLBase + "/internal/policies/" + versionID + "/html",
		OutputBucket:    h.pdfCfg.OutputBucket,
		OutputKey:       outputKey,
		Sensitivity:     sensitivity,
		RequestedBy:     req.GetRequesterUserId(),
	}
	if err := h.pdfClient.Create(ctx, jobID, spec); err != nil {
		return nil, errcodes.Error(ctx, fmt.Errorf("create PdfRender: %w", err))
	}
	return &deliveryv1.RequestPDFExportResponse{JobId: jobID}, nil
}

// GetPDFDownloadLink presigns a done job's PDF for a short download.
func (h *DeliveryHandler) GetPDFDownloadLink(ctx context.Context, req *deliveryv1.GetPDFDownloadLinkRequest) (*deliveryv1.GetPDFDownloadLinkResponse, error) {
	if h.jobStore == nil || h.signer == nil {
		return nil, errcodes.Error(ctx, errcodes.PDFExportDisabled())
	}
	if req.GetJobId() == "" {
		return nil, errcodes.Error(ctx, errcodes.Required("job_id"))
	}
	key, err := h.jobStore.GetArtifactKey(ctx, req.GetJobId())
	switch {
	case errors.Is(err, store.ErrPDFJobFailed):
		return nil, errcodes.Error(ctx, errcodes.PDFExportFailed(req.GetJobId()))
	case errors.Is(err, store.ErrPDFJobNotReady):
		return nil, errcodes.Error(ctx, errcodes.PDFExportNotReady(req.GetJobId()))
	case errors.Is(err, store.ErrPDFJobNotFound):
		return nil, errcodes.Error(ctx, errcodes.PDFExportNotFound(req.GetJobId()))
	case err != nil:
		return nil, errcodes.Error(ctx, fmt.Errorf("artifact key: %w", err))
	}
	expiresAt := time.Now().Add(h.pdfLinkTTL).UTC()
	url, err := h.signer.SignedURL(ctx, key, h.pdfLinkTTL)
	if err != nil {
		return nil, errcodes.Error(ctx, fmt.Errorf("sign url: %w", err))
	}
	return &deliveryv1.GetPDFDownloadLinkResponse{SignedUrl: url, ExpiresAt: timestamppb.New(expiresAt)}, nil
}

// CreateMagicLink issues a link to one version.
func (h *DeliveryHandler) CreateMagicLink(ctx context.Context, req *deliveryv1.CreateMagicLinkRequest) (*deliveryv1.CreateMagicLinkResponse, error) {
	if req.GetPolicyVersionId() == "" {
		return nil, errcodes.Error(ctx, errcodes.Required("policy_version_id"))
	}
	resp, err := h.mlSvc.Create(ctx, magiclink.CreateRequest{
		PolicyVersionID: req.GetPolicyVersionId(),
		CreatedByUserID: req.GetCreatedByUserId(),
		Sensitive:       req.GetSensitive(),
	})
	if err != nil {
		return nil, errcodes.Error(ctx, errcodes.StoreUnavailable("create_magic_link", err))
	}
	return &deliveryv1.CreateMagicLinkResponse{Token: resp.Token, ExpiresAt: timestamppb.New(resp.ExpiresAt)}, nil
}

// RevokeMagicLink ends a link; revoking twice succeeds.
func (h *DeliveryHandler) RevokeMagicLink(ctx context.Context, req *deliveryv1.RevokeMagicLinkRequest) (*deliveryv1.RevokeMagicLinkResponse, error) {
	if req.GetToken() == "" {
		return nil, errcodes.Error(ctx, errcodes.Required("token"))
	}
	if err := h.mlSvc.Revoke(ctx, magiclink.RevokeRequest{Token: req.GetToken(), RevokedByUserID: req.GetRevokedByUserId()}); err != nil {
		return nil, errcodes.Error(ctx, errcodes.StoreUnavailable("revoke_magic_link", err))
	}
	return &deliveryv1.RevokeMagicLinkResponse{Revoked: true}, nil
}

// ResolveMagicLink returns the version a live link opens. A sensitive link
// needs the viewer's email; the access is recorded before that check, so the
// attempt itself is audited.
func (h *DeliveryHandler) ResolveMagicLink(ctx context.Context, req *deliveryv1.ResolveMagicLinkRequest) (*deliveryv1.ResolveMagicLinkResponse, error) {
	if req.GetToken() == "" {
		return nil, errcodes.Error(ctx, errcodes.Required("token"))
	}
	resp, err := h.mlSvc.Resolve(ctx, magiclink.ResolveRequest{Token: req.GetToken(), ViewerEmail: req.GetViewerEmail()})
	switch {
	case errors.Is(err, store.ErrTokenNotFound):
		return nil, errcodes.Error(ctx, errcodes.MagicLinkNotFound())
	case errors.Is(err, store.ErrTokenExpired):
		return nil, errcodes.Error(ctx, errcodes.MagicLinkExpired())
	case errors.Is(err, store.ErrTokenRevoked):
		return nil, errcodes.Error(ctx, errcodes.MagicLinkRevoked())
	case err != nil:
		return nil, errcodes.Error(ctx, errcodes.StoreUnavailable("resolve_magic_link", err))
	}
	if resp.Sensitive && req.GetViewerEmail() == "" {
		return nil, errcodes.Error(ctx, errcodes.ViewerEmailRequired())
	}
	return &deliveryv1.ResolveMagicLinkResponse{
		PolicyVersionId:   resp.PolicyVersionID,
		Sensitive:         resp.Sensitive,
		WatermarkRequired: resp.WatermarkRequired,
	}, nil
}
