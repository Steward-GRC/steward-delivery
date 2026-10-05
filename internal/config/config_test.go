// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package config

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Steward-GRC/steward-delivery/internal/workloadauth"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("WORKLOAD_AUTH", "disabled")
	t.Setenv("DATABASE_DSN", "postgres://delivery")
	t.Setenv("CORE_GRPC_ADDR", "policy-core:9090")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.DatabaseDSN != "postgres://delivery" {
		t.Errorf("DatabaseDSN: %q", c.DatabaseDSN)
	}
	if c.GRPCPort != "9090" {
		t.Errorf("GRPCPort default: %q", c.GRPCPort)
	}
	if c.S3Region != "us-east-1" {
		t.Errorf("S3Region default: %q", c.S3Region)
	}
	if !c.S3ForcePathStyle {
		t.Errorf("S3ForcePathStyle should default to true")
	}
	if c.MagicLinkNonSensitiveTTL != 30*24*time.Hour {
		t.Errorf("MagicLinkNonSensitiveTTL default: %v", c.MagicLinkNonSensitiveTTL)
	}
	if c.MagicLinkSensitiveTTL != 48*time.Hour {
		t.Errorf("MagicLinkSensitiveTTL default: %v", c.MagicLinkSensitiveTTL)
	}
	if c.InternalHTTPPort != "8081" {
		t.Errorf("InternalHTTPPort default: %q", c.InternalHTTPPort)
	}
	if c.PodNamespace != "default" {
		t.Errorf("PodNamespace default: %q", c.PodNamespace)
	}
	if c.CoreGRPCAddr != "policy-core:9090" {
		t.Errorf("CoreGRPCAddr: %q", c.CoreGRPCAddr)
	}
	if !c.PDFExportEnabled {
		t.Error("PDFExportEnabled should default to true")
	}
}

func TestLoadPDFExportEnabledOverride(t *testing.T) {
	t.Setenv("WORKLOAD_AUTH", "disabled")
	t.Setenv("DATABASE_DSN", "postgres://delivery")
	t.Setenv("CORE_GRPC_ADDR", "policy-core:9090")
	t.Setenv("PDF_EXPORT_ENABLED", "false")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.PDFExportEnabled {
		t.Error("PDFExportEnabled should be false when PDF_EXPORT_ENABLED=false")
	}
}

func TestLoadOverrides(t *testing.T) {
	t.Setenv("WORKLOAD_AUTH", "disabled")
	t.Setenv("DATABASE_DSN", "postgres://delivery")
	t.Setenv("CORE_GRPC_ADDR", "policy-core:9090")
	t.Setenv("S3_ENDPOINT", "https://objects.example.org")
	t.Setenv("S3_BUCKET", "policy-pdfs")
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")
	t.Setenv("MAGIC_LINK_SENSITIVE_TTL", "12h")
	t.Setenv("INTERNAL_HTTP_PORT", "9999")
	t.Setenv("INTERNAL_BASE_URL", "http://delivery.example.org:9999")
	t.Setenv("POD_NAMESPACE", "policy-prod")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.S3Endpoint != "https://objects.example.org" {
		t.Errorf("S3Endpoint: %q", c.S3Endpoint)
	}
	if c.S3Bucket != "policy-pdfs" {
		t.Errorf("S3Bucket: %q", c.S3Bucket)
	}
	if c.MagicLinkSensitiveTTL != 12*time.Hour {
		t.Errorf("MagicLinkSensitiveTTL: %v", c.MagicLinkSensitiveTTL)
	}
	if c.InternalHTTPPort != "9999" {
		t.Errorf("InternalHTTPPort: %q", c.InternalHTTPPort)
	}
	if c.InternalBaseURL != "http://delivery.example.org:9999" {
		t.Errorf("InternalBaseURL: %q", c.InternalBaseURL)
	}
	if c.PodNamespace != "policy-prod" {
		t.Errorf("PodNamespace: %q", c.PodNamespace)
	}
}

func TestLoadMissingDSN(t *testing.T) {
	if err := os.Unsetenv("DATABASE_DSN"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing DATABASE_DSN")
	}
}

func TestLoadMissingCoreAddr(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://delivery")
	if err := os.Unsetenv("CORE_GRPC_ADDR"); err != nil {
		t.Fatalf("unsetenv: %v", err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("expected error for missing CORE_GRPC_ADDR")
	}
}

func TestLoadS3EndpointNeedsABucket(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://delivery")
	t.Setenv("CORE_GRPC_ADDR", "core:9090")
	t.Setenv("S3_ENDPOINT", "http://objects.example.org:9000")
	if _, err := Load(); err == nil {
		t.Fatal("expected an error for S3_ENDPOINT without S3_BUCKET")
	}
}

func TestLoadProbeAndLinkDefaults(t *testing.T) {
	t.Setenv("WORKLOAD_AUTH", "disabled")
	t.Setenv("DATABASE_DSN", "postgres://delivery")
	t.Setenv("CORE_GRPC_ADDR", "core:9090")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.ProbePort != "8080" {
		t.Errorf("ProbePort default: %q", c.ProbePort)
	}
	if c.PDFLinkTTL != 15*time.Minute {
		t.Errorf("PDFLinkTTL default: %v", c.PDFLinkTTL)
	}
	if c.MigrateDSN != "postgres://delivery" {
		t.Errorf("MigrateDSN should default to DATABASE_DSN: %q", c.MigrateDSN)
	}
}

func setWorkloadAuth(t *testing.T) {
	t.Helper()
	t.Setenv("DATABASE_DSN", "postgres://delivery")
	t.Setenv("CORE_GRPC_ADDR", "core:9090")
	t.Setenv("WORKLOAD_OIDC_ISSUER", "https://issuer.example.org")
	t.Setenv("WORKLOAD_ALLOWED_SERVICEACCOUNTS", "steward/steward-gateway")
	t.Setenv("WORKLOAD_TOKEN_FILE", "/var/run/secrets/steward/token")
}

func TestLoadWorkloadAuth(t *testing.T) {
	setWorkloadAuth(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !c.WorkloadAuthEnabled || c.WorkloadAuth.Audience != "steward" || len(c.WorkloadAuth.AllowedServiceAccounts) != 1 {
		t.Errorf("WorkloadAuth: enabled=%v %+v", c.WorkloadAuthEnabled, c.WorkloadAuth)
	}
	if c.WorkloadTokenFile != "/var/run/secrets/steward/token" {
		t.Errorf("WorkloadTokenFile: %q", c.WorkloadTokenFile)
	}
}

func TestLoadFailsClosedWithoutAnIssuer(t *testing.T) {
	setWorkloadAuth(t)
	t.Setenv("WORKLOAD_OIDC_ISSUER", "")
	if _, err := Load(); !errors.Is(err, workloadauth.ErrNotConfigured) {
		t.Fatalf("want ErrNotConfigured, got %v", err)
	}
}

func TestLoadNeedsATokenFileToCallCore(t *testing.T) {
	setWorkloadAuth(t)
	t.Setenv("WORKLOAD_TOKEN_FILE", "")
	_, err := Load()
	if err == nil || !strings.Contains(err.Error(), "WORKLOAD_TOKEN_FILE") {
		t.Fatalf("want an error naming WORKLOAD_TOKEN_FILE, got %v", err)
	}
}

func TestLoadDisabledNeedsNoIssuerOrTokenFile(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://delivery")
	t.Setenv("CORE_GRPC_ADDR", "core:9090")
	t.Setenv("WORKLOAD_AUTH", "disabled")
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.WorkloadAuthEnabled || c.WorkloadTokenFile != "" {
		t.Errorf("disabled: enabled=%v token file %q", c.WorkloadAuthEnabled, c.WorkloadTokenFile)
	}
}

func TestLoadRejectsAnUnknownAuthMode(t *testing.T) {
	setWorkloadAuth(t)
	t.Setenv("WORKLOAD_AUTH", "off")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "WORKLOAD_AUTH") {
		t.Fatalf("want an error naming WORKLOAD_AUTH, got %v", err)
	}
}
