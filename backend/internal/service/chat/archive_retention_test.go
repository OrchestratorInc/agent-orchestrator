package chat

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

type pruneResult struct {
	deleted  int64
	caughtUp bool
}

type fakeArchiveRetentionStore struct {
	mu      sync.Mutex
	results []pruneResult
	repeat  *pruneResult
	err     error
	cutoffs []time.Time
	graces  []time.Duration
	windows []int64
	limits  []int64
}

func (s *fakeArchiveRetentionStore) PruneSettledProviderDeltas(_ context.Context, settledBefore time.Time, grace time.Duration, window, limit int64) (int64, bool, time.Duration, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cutoffs = append(s.cutoffs, settledBefore)
	s.graces = append(s.graces, grace)
	s.windows = append(s.windows, window)
	s.limits = append(s.limits, limit)
	if s.err != nil {
		return 0, false, 0, s.err
	}
	if len(s.results) == 0 {
		if s.repeat != nil {
			return s.repeat.deleted, s.repeat.caughtUp, time.Millisecond, nil
		}
		return 0, true, 0, nil
	}
	r := s.results[0]
	s.results = s.results[1:]
	return r.deleted, r.caughtUp, time.Millisecond, nil
}

func (s *fakeArchiveRetentionStore) calls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.cutoffs)
}

var archiveRetentionNow = time.Date(2026, 10, 16, 12, 0, 0, 0, time.UTC)

func quietArchiveRetention(store ArchiveRetentionStore, cfg ArchiveRetentionConfig) *ArchiveRetention {
	cfg.Clock = func() time.Time { return archiveRetentionNow }
	cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	return NewArchiveRetention(store, cfg)
}

func TestArchiveRetentionRunOnceStopsWhenCaughtUp(t *testing.T) {
	store := &fakeArchiveRetentionStore{results: []pruneResult{{100, false}, {100, false}, {7, true}, {100, false}}}
	j := quietArchiveRetention(store, ArchiveRetentionConfig{Retention: 14 * 24 * time.Hour, Grace: 6 * time.Hour, Window: 500, Batch: 100, Pause: time.Nanosecond})

	if err := j.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.calls() != 3 {
		t.Fatalf("steps = %d, want 3 (the third caught up)", store.calls())
	}
	want := archiveRetentionNow.Add(-14 * 24 * time.Hour)
	for i, cutoff := range store.cutoffs {
		if !cutoff.Equal(want) || store.graces[i] != 6*time.Hour || store.windows[i] != 500 || store.limits[i] != 100 {
			t.Fatalf("step %d = (%v, %v, %d, %d), want (%v, 6h, 500, 100)", i, cutoff, store.graces[i], store.windows[i], store.limits[i], want)
		}
	}
}

// Streaming writes get the writer back between batches: the janitor waits after
// every batch that deleted rows, and not after a window that had nothing to do.
func TestArchiveRetentionRunOncePausesAfterEachDeletingBatch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &fakeArchiveRetentionStore{results: []pruneResult{{100, false}, {0, false}, {100, false}, {5, true}}}
		j := quietArchiveRetention(store, ArchiveRetentionConfig{Pause: 150 * time.Millisecond})
		start := time.Now()
		if err := j.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed != 300*time.Millisecond {
			t.Fatalf("pass took %v, want two 150ms pauses", elapsed)
		}
	})
}

func TestArchiveRetentionRunOnceStopsWhenCancelledDuringAPause(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &fakeArchiveRetentionStore{repeat: &pruneResult{100, false}}
		j := quietArchiveRetention(store, ArchiveRetentionConfig{Pause: time.Second})
		ctx, cancel := context.WithCancel(context.Background())
		time.AfterFunc(1500*time.Millisecond, cancel)
		if err := j.RunOnce(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if store.calls() != 2 {
			t.Fatalf("steps = %d, want 2", store.calls())
		}
	})
}

// A backlog is worked off over several passes; one pass is bounded.
func TestArchiveRetentionRunOnceBoundsStepsPerPass(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &fakeArchiveRetentionStore{repeat: &pruneResult{100, false}}
		j := quietArchiveRetention(store, ArchiveRetentionConfig{})
		if err := j.RunOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if store.calls() != defaultArchiveRetentionStepsPerRun {
			t.Fatalf("steps = %d, want %d", store.calls(), defaultArchiveRetentionStepsPerRun)
		}
	})
}

func TestArchiveRetentionDefaults(t *testing.T) {
	store := &fakeArchiveRetentionStore{}
	j := quietArchiveRetention(store, ArchiveRetentionConfig{})

	if err := j.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.calls() != 1 {
		t.Fatalf("steps = %d, want 1", store.calls())
	}
	if want := archiveRetentionNow.Add(-DefaultArchiveDeltaRetention); !store.cutoffs[0].Equal(want) {
		t.Fatalf("cutoff = %v, want %v", store.cutoffs[0], want)
	}
	if DefaultArchiveDeltaRetention != 14*24*time.Hour {
		t.Fatalf("default retention = %v, want 14 days", DefaultArchiveDeltaRetention)
	}
	if store.graces[0] != DefaultArchiveRetentionGrace || DefaultArchiveRetentionGrace != 24*time.Hour {
		t.Fatalf("grace = %v (default %v), want one day", store.graces[0], DefaultArchiveRetentionGrace)
	}
	if store.windows[0] != DefaultArchiveRetentionWindow || store.limits[0] != DefaultArchiveRetentionBatch {
		t.Fatalf("window/batch = %d/%d, want %d/%d", store.windows[0], store.limits[0],
			DefaultArchiveRetentionWindow, DefaultArchiveRetentionBatch)
	}
}

func TestArchiveRetentionRunOnceReturnsStoreError(t *testing.T) {
	want := errors.New("busy")
	j := quietArchiveRetention(&fakeArchiveRetentionStore{err: want}, ArchiveRetentionConfig{})
	if err := j.RunOnce(context.Background()); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

// The first pass waits for FirstDelay, leaving the writer to boot-time work such
// as chat resumes and native history imports.
func TestArchiveRetentionStartWaitsForTheFirstDelay(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		store := &fakeArchiveRetentionStore{}
		j := quietArchiveRetention(store, ArchiveRetentionConfig{FirstDelay: 5 * time.Minute, Interval: time.Hour})
		ctx, cancel := context.WithCancel(context.Background())
		done := j.Start(ctx)

		time.Sleep(5*time.Minute - time.Second)
		synctest.Wait()
		if store.calls() != 0 {
			t.Fatalf("pass ran %d steps before the first delay", store.calls())
		}
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if store.calls() != 1 {
			t.Fatalf("steps after the first delay = %d, want 1", store.calls())
		}
		time.Sleep(time.Hour)
		synctest.Wait()
		if store.calls() != 2 {
			t.Fatalf("steps after one interval = %d, want 2", store.calls())
		}
		cancel()
		<-done
	})
}
