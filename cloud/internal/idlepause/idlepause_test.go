package idlepause

import (
	"context"
	"testing"
	"time"
)

type recordingStore struct {
	refs   []SandboxRef
	checks map[string]time.Duration
}

func (s *recordingStore) RunningSandboxSessions(context.Context) ([]SandboxRef, error) {
	return s.refs, nil
}

func (s *recordingStore) PauseIfIdle(_ context.Context, _, sessionID string, threshold time.Duration) (bool, error) {
	s.checks[sessionID] = threshold
	return false, nil
}

func TestProviderThresholdsOverrideTheDefaultAndZeroKeepsWarm(t *testing.T) {
	store := &recordingStore{checks: map[string]time.Duration{}, refs: []SandboxRef{
		{SessionID: "fs", Provider: "freestyle"},
		{SessionID: "coder", Provider: "coder"},
	}}
	scanner := New(store, Options{ProviderIdleThresholds: map[string]time.Duration{"freestyle": 15 * time.Minute}})
	if err := scanner.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.checks["fs"] != 15*time.Minute {
		t.Fatalf("freestyle threshold = %v, want 15m", store.checks["fs"])
	}
	if _, checked := store.checks["coder"]; checked {
		t.Fatal("a provider with a zero threshold must never be checked for idle pause")
	}

	store.checks = map[string]time.Duration{}
	scanner = New(store, Options{IdleThreshold: time.Hour, ProviderIdleThresholds: map[string]time.Duration{"freestyle": 0}})
	if err := scanner.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, checked := store.checks["fs"]; checked || store.checks["coder"] != time.Hour {
		t.Fatalf("checks = %v; want freestyle skipped and coder on the 1h default", store.checks)
	}
}

func TestBilledSandboxesPauseOnTheBilledThresholdAndUnbilledCoderStaysWarm(t *testing.T) {
	store := &recordingStore{checks: map[string]time.Duration{}, refs: []SandboxRef{
		{SessionID: "fs", Provider: "freestyle", Billed: true},
		{SessionID: "billed-coder", Provider: "coder", Billed: true},
		// Coder for an organization with the coder capability is unbilled.
		{SessionID: "free-coder", Provider: "coder"},
	}}
	scanner := New(store, Options{
		ProviderIdleThresholds: map[string]time.Duration{"freestyle": 15 * time.Minute},
		BilledIdleThreshold:    15 * time.Minute,
	})
	if err := scanner.ScanOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.checks["fs"] != 15*time.Minute || store.checks["billed-coder"] != 15*time.Minute {
		t.Fatalf("checks = %v; want freestyle and billed coder on 15m", store.checks)
	}
	if _, checked := store.checks["free-coder"]; checked {
		t.Fatal("an unbilled coder sandbox must keep coder's zero threshold and never be paused")
	}
}
