package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestSessionForkRoundTrip(t *testing.T) {
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	at := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	want := domain.SessionFork{
		ForkSessionID: "p-2", SourceSessionID: "p-1", RootSessionID: "p-1", SourceTurnID: "turn-4",
		SourceTitle: "Retry failed webhook deliveries", ContextMode: domain.ForkContextNativeCurrent, ForkNumber: 1, CreatedAt: at,
	}
	if err := s.RecordSessionFork(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := s.GetSessionFork(ctx, "p-2")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if got != want {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if _, found, err := s.GetSessionFork(ctx, "p-1"); err != nil || found {
		t.Fatalf("a session that isn't a fork: found=%v err=%v", found, err)
	}
}

func TestSessionForkListAndNumbering(t *testing.T) {
	s := sqlitetest.MustOpen(t)
	ctx := context.Background()
	if n, err := s.NextSessionForkNumber(ctx, "p-1"); err != nil || n != 1 {
		t.Fatalf("first number=%d err=%v", n, err)
	}
	for i, id := range []domain.SessionID{"p-2", "p-3"} {
		if err := s.RecordSessionFork(ctx, domain.SessionFork{ForkSessionID: id, SourceSessionID: "p-1", RootSessionID: "p-1", ForkNumber: i + 1, CreatedAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	// A fork of a fork counts against the same root.
	if err := s.RecordSessionFork(ctx, domain.SessionFork{ForkSessionID: "p-4", SourceSessionID: "p-2", RootSessionID: "p-1", ForkNumber: 3, CreatedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if n, err := s.NextSessionForkNumber(ctx, "p-1"); err != nil || n != 4 {
		t.Fatalf("next number=%d err=%v", n, err)
	}
	forks, err := s.ListSessionForks(ctx, "p-1")
	if err != nil || len(forks) != 2 || forks[0].ForkSessionID != "p-2" || forks[1].ForkSessionID != "p-3" {
		t.Fatalf("direct forks=%+v err=%v", forks, err)
	}
}
