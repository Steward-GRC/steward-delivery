// Copyright 2026 The Steward Authors
// SPDX-License-Identifier: Apache-2.0

package policyhttp

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Steward-GRC/steward-delivery/internal/store"
)

type stubJobStore struct {
	versionID string
	raw       []byte
	err       error
}

func (s stubJobStore) GetJobHistory(_ context.Context, _ string) (string, []byte, error) {
	return s.versionID, s.raw, s.err
}

func TestJobHistoryRoundTripsThroughJSON(t *testing.T) {
	at := time.Date(2026, 3, 2, 9, 30, 0, 0, time.UTC)
	raw, err := MarshalHistory([]HistoryEntry{{At: at, Version: 3, Label: "Approved", Actor: "Lee Park"}})
	if err != nil {
		t.Fatal(err)
	}
	got, requested, err := NewJobHistory(stubJobStore{versionID: "pv-1", raw: raw}).JobHistory(context.Background(), "j", "pv-1")
	if err != nil || !requested || len(got) != 1 || !got[0].At.Equal(at) || got[0].Version != 3 || got[0].Label != "Approved" || got[0].Actor != "Lee Park" {
		t.Errorf("got %+v requested=%v err=%v", got, requested, err)
	}
}

func TestJobHistoryNilMeansNotRequestedAndEmptyMeansRequested(t *testing.T) {
	_, requested, err := NewJobHistory(stubJobStore{versionID: "pv-1"}).JobHistory(context.Background(), "j", "pv-1")
	if err != nil || requested {
		t.Errorf("nil: requested=%v err=%v", requested, err)
	}
	raw, _ := MarshalHistory(nil)
	got, requested, err := NewJobHistory(stubJobStore{versionID: "pv-1", raw: raw}).JobHistory(context.Background(), "j", "pv-1")
	if err != nil || !requested || len(got) != 0 {
		t.Errorf("empty: %v requested=%v err=%v", got, requested, err)
	}
}

func TestJobHistoryRefusesAJobOfAnotherVersion(t *testing.T) {
	raw, _ := MarshalHistory(nil)
	_, _, err := NewJobHistory(stubJobStore{versionID: "pv-2", raw: raw}).JobHistory(context.Background(), "j", "pv-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}

func TestJobHistoryOfAnUnknownJobIsNotFound(t *testing.T) {
	_, _, err := NewJobHistory(stubJobStore{err: store.ErrPDFJobNotFound}).JobHistory(context.Background(), "j", "pv-1")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("got %v, want ErrNotFound", err)
	}
}
