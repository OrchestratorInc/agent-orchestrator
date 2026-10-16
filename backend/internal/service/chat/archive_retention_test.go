package chat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

type fakeArchiveRetentionStore struct {
	results []int64
	err     error
	cutoffs []time.Time
	limits  []int64
	called  chan struct{}
}

func (s *fakeArchiveRetentionStore) PruneSettledProviderDeltas(_ context.Context, settledBefore time.Time, limit int64) (int64, error) {
	s.cutoffs = append(s.cutoffs, settledBefore)
	s.limits = append(s.limits, limit)
	if s.called != nil {
		select {
		case s.called <- struct{}{}:
		default:
		}
	}
	if s.err != nil {
		return 0, s.err
	}
	if len(s.results) == 0 {
		return 0, nil
	}
	n := s.results[0]
	s.results = s.results[1:]
	return n, nil
}

var archiveRetentionNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func quietArchiveRetention(store ArchiveRetentionStore, cfg ArchiveRetentionConfig) *ArchiveRetention {
	cfg.Clock = func() time.Time { return archiveRetentionNow }
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewArchiveRetention(store, cfg)
}

func TestArchiveRetentionRunOnceStopsAtAShortBatch(t *testing.T) {
	store := &fakeArchiveRetentionStore{results: []int64{50, 50, 7, 50}}
	j := quietArchiveRetention(store, ArchiveRetentionConfig{Retention: 14 * 24 * time.Hour, Batch: 50})

	if err := j.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.cutoffs) != 3 {
		t.Fatalf("batches = %d, want 3 (the third came back short)", len(store.cutoffs))
	}
	want := archiveRetentionNow.Add(-14 * 24 * time.Hour)
	for i, cutoff := range store.cutoffs {
		if !cutoff.Equal(want) || store.limits[i] != 50 {
			t.Fatalf("batch %d = (%v, %d), want (%v, 50)", i, cutoff, store.limits[i], want)
		}
	}
}

// A backlog is worked off over several passes; one pass never holds the writer
// for more than its bounded number of batches.
func TestArchiveRetentionRunOnceBoundsBatchesPerPass(t *testing.T) {
	results := make([]int64, 100)
	for i := range results {
		results[i] = 10
	}
	store := &fakeArchiveRetentionStore{results: results}
	j := quietArchiveRetention(store, ArchiveRetentionConfig{Batch: 10})

	if err := j.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.cutoffs) != defaultArchiveRetentionBatchesPerRun {
		t.Fatalf("batches = %d, want %d", len(store.cutoffs), defaultArchiveRetentionBatchesPerRun)
	}
}

func TestArchiveRetentionDefaults(t *testing.T) {
	store := &fakeArchiveRetentionStore{}
	j := quietArchiveRetention(store, ArchiveRetentionConfig{})

	if err := j.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.cutoffs) != 1 {
		t.Fatalf("batches = %d, want 1", len(store.cutoffs))
	}
	if want := archiveRetentionNow.Add(-DefaultArchiveDeltaRetention); !store.cutoffs[0].Equal(want) {
		t.Fatalf("cutoff = %v, want %v", store.cutoffs[0], want)
	}
	if DefaultArchiveDeltaRetention != 14*24*time.Hour {
		t.Fatalf("default retention = %v, want 14 days", DefaultArchiveDeltaRetention)
	}
	if store.limits[0] != DefaultArchiveRetentionBatch {
		t.Fatalf("batch = %d, want %d", store.limits[0], DefaultArchiveRetentionBatch)
	}
}

func TestArchiveRetentionRunOnceReturnsStoreError(t *testing.T) {
	want := errors.New("busy")
	j := quietArchiveRetention(&fakeArchiveRetentionStore{err: want}, ArchiveRetentionConfig{})
	if err := j.RunOnce(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestArchiveRetentionStartRunsAtOnceAndStopsWithContext(t *testing.T) {
	store := &fakeArchiveRetentionStore{called: make(chan struct{}, 1)}
	j := quietArchiveRetention(store, ArchiveRetentionConfig{Interval: time.Hour})
	ctx, cancel := context.WithCancel(context.Background())
	done := j.Start(ctx)
	select {
	case <-store.called:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("first pass did not run at start")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("janitor did not stop after cancellation")
	}
}
