package store_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestChangeLogRetentionPrunesByAge(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	now := time.Now().UTC().Truncate(time.Second)
	old := now.Add(-8 * 24 * time.Hour)
	rec, err := s.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: old},
		CreatedAt: old,
		UpdatedAt: old,
	})
	if err != nil {
		t.Fatal(err)
	}
	rec.Activity.State = domain.ActivityIdle
	rec.UpdatedAt = old.Add(time.Second)
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}
	rec.Activity.State = domain.ActivityActive
	rec.UpdatedAt = now
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatal(err)
	}

	removed, err := s.PruneChangeLogBefore(ctx, now.Add(-7*24*time.Hour), 100)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("removed = %d, want 2", removed)
	}
	events, err := s.EventsAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Seq != 3 {
		t.Fatalf("remaining events = %#v, want seq 3", events)
	}
}

func TestChangeLogRetentionPrunesToMaxRowsInBatches(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")

	now := time.Now().UTC().Truncate(time.Second)
	rec, err := s.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer",
		Kind:      domain.KindWorker,
		Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now},
		CreatedAt: now,
		UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if rec.Activity.State == domain.ActivityActive {
			rec.Activity.State = domain.ActivityIdle
		} else {
			rec.Activity.State = domain.ActivityActive
		}
		rec.UpdatedAt = now.Add(time.Duration(i+1) * time.Second)
		if err := s.UpdateSession(ctx, rec); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := s.PruneChangeLogToMaxRows(ctx, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("first batch removed = %d, want 2", removed)
	}
	events, err := s.EventsAfter(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Seq != 3 {
		t.Fatalf("remaining events = %#v, want seq 3-5", events)
	}
}

func TestMaintenanceWritesWaitForTheWriter(t *testing.T) {
	s := newTestStore(t)
	hold := s.HoldWriterForTest()
	var released sync.Once
	release := func() { released.Do(hold) }
	defer release()
	cases := map[string]func(context.Context) error{
		"change log age":  func(ctx context.Context) error { _, err := s.PruneChangeLogBefore(ctx, time.Now(), 10); return err },
		"change log size": func(ctx context.Context) error { _, err := s.PruneChangeLogToMaxRows(ctx, 1, 10); return err },
		"telemetry prune": func(ctx context.Context) error {
			_, err := s.PruneTelemetryEventsBefore(ctx, time.Now(), 10)
			return err
		},
		"incremental vacuum": func(ctx context.Context) error { return s.IncrementalVacuum(ctx, 10) },
	}
	for name, run := range cases {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		err := run(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("%s while writer held: err = %v, want deadline exceeded", name, err)
		}
	}
	release()
	for name, run := range cases {
		for i := 0; i < 2; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := run(ctx)
			cancel()
			if err != nil {
				t.Fatalf("%s run %d after writer released: %v", name, i+1, err)
			}
		}
	}
}
