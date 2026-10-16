package chat

import (
	"context"
	"log/slog"
	"time"
)

const (
	// DefaultArchiveDeltaRetention is how long a settled turn keeps its raw
	// streamed text deltas in the provider-event archive. Nothing reads them once
	// the turn has settled; they are kept only as evidence while someone may still
	// be looking into a projection bug in a recent conversation.
	DefaultArchiveDeltaRetention = 14 * 24 * time.Hour
	// DefaultArchiveRetentionInterval matches the change_log janitor: often enough
	// to keep up with a busy day, rare enough to stay out of the way of streaming.
	DefaultArchiveRetentionInterval = 15 * time.Minute
	// DefaultArchiveRetentionBatch bounds each delete statement, and with it how
	// long one batch holds SQLite's single writer: deleted deltas are scattered
	// through the archive, so each costs about 0.1 ms of page I/O (measured on a
	// 3.9 GB database), and 250 keep a batch to tens of milliseconds.
	DefaultArchiveRetentionBatch         int64 = 250
	defaultArchiveRetentionBatchesPerRun int   = 40
)

// ArchiveRetentionStore is the storage surface the archive janitor needs.
type ArchiveRetentionStore interface {
	PruneSettledProviderDeltas(ctx context.Context, settledBefore time.Time, limit int64) (int64, error)
}

// ArchiveRetentionConfig customizes archive cleanup. Zero values use the
// production defaults.
type ArchiveRetentionConfig struct {
	Retention time.Duration
	Interval  time.Duration
	Batch     int64
	Clock     func() time.Time
	Logger    *slog.Logger
}

// ArchiveRetention deletes the raw text deltas of turns that settled more than
// the retention window ago, so the provider-event archive stops growing with
// every streamed token. It runs in the background in small batches, like the
// change_log janitor, so streaming writes never wait behind a large delete.
// Freed pages are reused by later writes; the file is not shrunk.
type ArchiveRetention struct {
	store      ArchiveRetentionStore
	retention  time.Duration
	interval   time.Duration
	batch      int64
	clock      func() time.Time
	logger     *slog.Logger
	maxBatches int
}

// NewArchiveRetention constructs the archive janitor.
func NewArchiveRetention(store ArchiveRetentionStore, cfg ArchiveRetentionConfig) *ArchiveRetention {
	j := &ArchiveRetention{
		store:      store,
		retention:  cfg.Retention,
		interval:   cfg.Interval,
		batch:      cfg.Batch,
		clock:      cfg.Clock,
		logger:     cfg.Logger,
		maxBatches: defaultArchiveRetentionBatchesPerRun,
	}
	if j.retention <= 0 {
		j.retention = DefaultArchiveDeltaRetention
	}
	if j.interval <= 0 {
		j.interval = DefaultArchiveRetentionInterval
	}
	if j.batch <= 0 {
		j.batch = DefaultArchiveRetentionBatch
	}
	if j.clock == nil {
		j.clock = time.Now
	}
	if j.logger == nil {
		j.logger = slog.Default()
	}
	return j
}

// Start runs a pass immediately and then at the configured interval. The
// returned channel closes after cancellation and is safe to wait on before
// closing the store.
func (j *ArchiveRetention) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		j.run(ctx)
		t := time.NewTicker(j.interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				j.run(ctx)
			}
		}
	}()
	return done
}

// RunOnce deletes at most a bounded number of batches. A backlog, such as the
// first pass after upgrading a large database, is worked off over later passes.
func (j *ArchiveRetention) RunOnce(ctx context.Context) error {
	settledBefore := j.clock().UTC().Add(-j.retention)
	var total int64
	for i := 0; i < j.maxBatches; i++ {
		removed, err := j.store.PruneSettledProviderDeltas(ctx, settledBefore, j.batch)
		if err != nil {
			return err
		}
		total += removed
		if removed < j.batch {
			break
		}
	}
	if total > 0 {
		j.logger.Info("provider event archive retention pruned settled deltas", "rows", total)
	}
	return nil
}

func (j *ArchiveRetention) run(ctx context.Context) {
	if err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
		j.logger.Warn("provider event archive retention failed", "err", err)
	}
}
