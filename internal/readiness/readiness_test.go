// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package readiness_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Bugs5382/go-buildinfo/health"
	"github.com/stretchr/testify/require"

	"github.com/Steward-GRC/steward-delivery/internal/readiness"
)

type fakeDB struct{ down atomic.Bool }

func (f *fakeDB) Ping(context.Context) error {
	if f.down.Load() {
		return errors.New("connection refused")
	}
	return nil
}

func (*fakeDB) ServerVersion(context.Context) (string, error) { return "16.4", nil }

type fakeBroker struct{ down atomic.Bool }

func (f *fakeBroker) Healthy() bool { return !f.down.Load() }

type toggle struct{ down atomic.Bool }

func (t *toggle) check(context.Context) error {
	if t.down.Load() {
		return errors.New("connection refused")
	}
	return nil
}

type fakeCore struct{ toggle }

func (c *fakeCore) Check(ctx context.Context) error       { return c.check(ctx) }
func (*fakeCore) Version(context.Context) (string, error) { return "v0.1.0", nil }

func checker(t *testing.T, d readiness.Deps) *health.Checker {
	t.Helper()
	c, err := readiness.New(d, health.WithTTL(time.Millisecond), health.WithTimeout(time.Second))
	require.NoError(t, err)
	return c
}

func dep(t *testing.T, r health.Report, name string) health.DependencyReport {
	t.Helper()
	for _, d := range r.Dependencies {
		if d.Name == name {
			return d
		}
	}
	t.Fatalf("no %q in the report", name)
	return health.DependencyReport{}
}

func names(r health.Report) []string {
	var out []string
	for _, d := range r.Dependencies {
		out = append(out, d.Name)
	}
	return out
}

func required() readiness.Deps {
	return readiness.Deps{Postgres: &fakeDB{}, Broker: &fakeBroker{}, Core: &fakeCore{}}
}

func TestRequiredOnlyWhenPDFExportIsOff(t *testing.T) {
	r := checker(t, required()).Report(context.Background())
	require.True(t, r.Ready)
	require.Equal(t, health.StateOK, r.Status)
	require.ElementsMatch(t, []string{readiness.Postgres, readiness.RabbitMQ, readiness.Core}, names(r))
	for _, n := range names(r) {
		require.True(t, dep(t, r, n).Required, n)
	}
	require.Equal(t, "16.4", dep(t, r, readiness.Postgres).Version)
	require.Equal(t, "v0.1.0", dep(t, r, readiness.Core).Version)
}

func TestEachRequiredDependencyDownMakesDeliveryNotReady(t *testing.T) {
	db, b, core := &fakeDB{}, &fakeBroker{}, &fakeCore{}
	c := checker(t, readiness.Deps{Postgres: db, Broker: b, Core: core})
	for name, down := range map[string]*atomic.Bool{readiness.Postgres: &db.down, readiness.RabbitMQ: &b.down, readiness.Core: &core.down} {
		down.Store(true)
		require.Eventually(t, func() bool { return !c.Report(context.Background()).Ready }, 2*time.Second, 5*time.Millisecond, name)
		require.Equal(t, health.StateDown, dep(t, c.Report(context.Background()), name).State)
		down.Store(false)
		require.Eventually(t, func() bool { return c.Report(context.Background()).Ready }, 2*time.Second, 5*time.Millisecond, name+" recovers")
	}
}

func TestPDFDependenciesDegradeButStayReady(t *testing.T) {
	var objects, kube toggle
	d := required()
	d.Objects, d.Kubernetes = objects.check, kube.check
	c := checker(t, d)
	require.Equal(t, health.StateOK, c.Report(context.Background()).Status)
	for name, down := range map[string]*atomic.Bool{readiness.ObjectStore: &objects.down, readiness.Kubernetes: &kube.down} {
		down.Store(true)
		require.Eventually(t, func() bool { return c.Report(context.Background()).Status == health.StateDegraded }, 2*time.Second, 5*time.Millisecond, name)
		r := c.Report(context.Background())
		require.True(t, r.Ready, "only PDF export needs %s", name)
		require.False(t, dep(t, r, name).Required)
		down.Store(false)
		require.Eventually(t, func() bool { return c.Report(context.Background()).Status == health.StateOK }, 2*time.Second, 5*time.Millisecond)
	}
}

func TestJWKSDownMakesDeliveryNotReady(t *testing.T) {
	var jwks toggle
	d := required()
	d.JWKS = jwks.check
	c := checker(t, d)
	r := c.Report(context.Background())
	require.True(t, r.Ready)
	require.True(t, dep(t, r, readiness.JWKS).Required, "callers can't be verified without the issuer's keys")
	jwks.down.Store(true)
	require.Eventually(t, func() bool { return !c.Report(context.Background()).Ready }, 2*time.Second, 5*time.Millisecond)
	require.Equal(t, health.StateDown, dep(t, c.Report(context.Background()), readiness.JWKS).State)
}

func TestWorkloadAuthDisabledDegradesButStaysReady(t *testing.T) {
	d := required()
	d.WorkloadAuthDisabled = true
	r := checker(t, d).Report(context.Background())
	require.True(t, r.Ready)
	require.Equal(t, health.StateDegraded, r.Status)
	w := dep(t, r, readiness.WorkloadAuth)
	require.False(t, w.Required)
	require.Equal(t, health.StateDegraded, w.State)
	require.NotContains(t, names(r), readiness.JWKS)
}

func TestRecheckEveryKeepsASuccessAndRetriesAFailure(t *testing.T) {
	now := time.Unix(1000, 0)
	var calls atomic.Int32
	var fail atomic.Bool
	check := readiness.RecheckEvery(func(context.Context) error {
		calls.Add(1)
		if fail.Load() {
			return errors.New("down")
		}
		return nil
	}, time.Minute, func() time.Time { return now })
	ctx := context.Background()
	require.NoError(t, check(ctx))
	require.NoError(t, check(ctx))
	require.Equal(t, int32(1), calls.Load(), "a success is kept for the interval")
	now = now.Add(time.Minute)
	fail.Store(true)
	require.Error(t, check(ctx))
	require.Error(t, check(ctx))
	require.Equal(t, int32(3), calls.Load(), "a failure is retried on the next check")
	fail.Store(false)
	require.NoError(t, check(ctx))
}
