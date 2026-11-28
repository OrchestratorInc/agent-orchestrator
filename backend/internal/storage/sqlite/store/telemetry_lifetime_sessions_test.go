package store_test

import (
	"context"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestLifetimeSessionsKeepsDeletedSessionsAndSkipsPreparations(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "mer")
	ctx := context.Background()
	first, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	count, err := s.LifetimeSessions(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count after creation = %d, %v", count, err)
	}
	if _, err := s.DeleteSession(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	count, err = s.LifetimeSessions(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count after deletion = %d, %v", count, err)
	}
	preparation := sampleRecord("mer")
	preparation.IsTaskPreparation = true
	if _, err := s.CreateSession(ctx, preparation); err != nil {
		t.Fatal(err)
	}
	count, err = s.LifetimeSessions(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count after preparation = %d, %v", count, err)
	}
}

func TestLifetimeWorkSessionsCountsWorkersOnly(t *testing.T) {
	s := newTestStore(t)
	seedProject(t, s, "mer")
	ctx := context.Background()
	orchestrator := sampleRecord("mer")
	orchestrator.Kind = domain.KindOrchestrator
	if _, err := s.CreateSession(ctx, orchestrator); err != nil {
		t.Fatal(err)
	}
	preparation := sampleRecord("mer")
	preparation.IsTaskPreparation = true
	if _, err := s.CreateSession(ctx, preparation); err != nil {
		t.Fatal(err)
	}
	count, err := s.LifetimeWorkSessions(ctx)
	if err != nil || count != 0 {
		t.Fatalf("count with only an orchestrator and a preparation = %d, %v", count, err)
	}
	worker, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.DeleteSession(ctx, worker.ID); err != nil {
		t.Fatal(err)
	}
	count, err = s.LifetimeWorkSessions(ctx)
	if err != nil || count != 1 {
		t.Fatalf("count after a deleted worker = %d, %v", count, err)
	}
}
