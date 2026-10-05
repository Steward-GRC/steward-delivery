// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

// Package readiness registers delivery's dependencies with go-buildinfo's
// health checker. Postgres, RabbitMQ and steward-core are required: without
// them delivery can't store a link, audit it, or read the content it renders.
// Object storage and the Kubernetes API serve only PDF export, so an outage
// there degrades delivery instead of draining it.
package readiness

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Bugs5382/go-buildinfo/grpcbuildinfo"
	"github.com/Bugs5382/go-buildinfo/health"
	postgres "github.com/Bugs5382/go-postgres"
	"google.golang.org/grpc"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// Dependency names, as they appear in the report and the
// steward-depstate-<name> headers.
const (
	Postgres    = "postgres"
	RabbitMQ    = "rabbitmq"
	Core        = "core"
	ObjectStore = "objectstore"
	Kubernetes  = "kubernetes"
)

// Database is the Postgres the service runs on.
type Database interface {
	Ping(ctx context.Context) error
	ServerVersion(ctx context.Context) (string, error)
}

// Broker is the RabbitMQ connection; go-rabbitmq's Conn reports it.
type Broker interface{ Healthy() bool }

// Upstream is a service delivery calls.
type Upstream interface {
	Check(ctx context.Context) error
	Version(ctx context.Context) (string, error)
}

// Deps are the dependencies to report. A nil Objects or Kubernetes is a
// feature that is off, and is not reported.
type Deps struct {
	Postgres   Database
	Broker     Broker
	Core       Upstream
	Objects    func(ctx context.Context) error
	Kubernetes func(ctx context.Context) error
}

var errBrokerDown = errors.New("rabbitmq connection is down")

// New returns a checker with deps registered.
func New(d Deps, opts ...health.Option) (*health.Checker, error) {
	deps := []health.Dependency{
		{Name: Postgres, Required: true, Check: d.Postgres.Ping, Version: d.Postgres.ServerVersion},
		{Name: RabbitMQ, Required: true, Check: func(context.Context) error {
			if !d.Broker.Healthy() {
				return errBrokerDown
			}
			return nil
		}},
		{Name: Core, Required: true, Check: d.Core.Check, Version: d.Core.Version},
	}
	if d.Objects != nil {
		deps = append(deps, health.Dependency{Name: ObjectStore, Check: d.Objects})
	}
	if d.Kubernetes != nil {
		deps = append(deps, health.Dependency{Name: Kubernetes, Check: d.Kubernetes})
	}
	c := health.New(opts...)
	return c, c.Register(deps...)
}

// PostgresDB adapts go-postgres's DB.
func PostgresDB(db *postgres.DB) Database { return pgDB{db} }

type pgDB struct{ db *postgres.DB }

func (p pgDB) Ping(ctx context.Context) error { return p.db.Ping(ctx) }

// ServerVersion drops the build suffix ("16.4 (Debian 16.4-1)"), which the
// header would redact.
func (p pgDB) ServerVersion(ctx context.Context) (string, error) {
	var v string
	if err := p.db.Pool().QueryRow(ctx, "SHOW server_version").Scan(&v); err != nil {
		return "", err
	}
	if f := strings.Fields(v); len(f) > 0 {
		return f[0], nil
	}
	return v, nil
}

// CoreConn checks steward-core through its grpc.health.v1 answer, which also
// carries its steward-version header.
func CoreConn(conn grpc.ClientConnInterface) Upstream { return coreConn{conn} }

type coreConn struct{ conn grpc.ClientConnInterface }

func (c coreConn) Check(ctx context.Context) error {
	r, err := grpcbuildinfo.Read(ctx, c.conn, "steward")
	if err != nil {
		return err
	}
	if r.Status != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("core reports %s", r.Status)
	}
	return nil
}

func (c coreConn) Version(ctx context.Context) (string, error) {
	r, err := grpcbuildinfo.Read(ctx, c.conn, "steward")
	if err != nil {
		return "", err
	}
	return r.Version, nil
}
