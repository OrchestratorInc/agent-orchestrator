package store_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestSessionTelemetryFactsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := sqlite.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}

	// A session created before facts were recorded reads as unknown.
	if facts, err := s.GetSessionTelemetry(ctx, sess.ID); err != nil || facts != (domain.SessionTelemetryFacts{}) {
		t.Fatalf("facts = %+v, %v; want zero", facts, err)
	}
	// The counter may arrive before created_by is written; neither overwrites the other.
	if err := s.IncrementSessionWaitingInputCount(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordSessionCreatedBy(ctx, sess.ID, domain.SessionCreatedByScheduled); err != nil {
		t.Fatal(err)
	}
	if err := s.IncrementSessionWaitingInputCount(ctx, sess.ID); err != nil {
		t.Fatal(err)
	}
	facts, err := s.GetSessionTelemetry(ctx, sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	if facts.CreatedBy != domain.SessionCreatedByScheduled || facts.WaitingInputCount != 2 {
		t.Fatalf("facts = %+v, want scheduled with 2 waits", facts)
	}
}

func TestSessionEndTelemetryClaimRequiresTerminationAndSurvivesReopen(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedProject(t, s, "ao")
	sess, err := s.CreateSession(ctx, sampleRecord("ao"))
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimSessionEndTelemetry(ctx, sess.ID); err != nil || claimed {
		t.Fatalf("live session claim = %v, %v", claimed, err)
	}
	sess.IsTerminated = true
	if err := s.UpdateSession(ctx, sess); err != nil {
		t.Fatal(err)
	}
	var claimedCount atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			claimed, err := s.ClaimSessionEndTelemetry(ctx, sess.ID)
			if err != nil {
				t.Errorf("claim: %v", err)
			} else if claimed {
				claimedCount.Add(1)
			}
		}()
	}
	wg.Wait()
	if got := claimedCount.Load(); got != 1 {
		t.Fatalf("claims = %d, want 1", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if claimed, err := s.ClaimSessionEndTelemetry(ctx, sess.ID); err != nil || claimed {
		t.Fatalf("claim after reopen = %v, %v", claimed, err)
	}
}
