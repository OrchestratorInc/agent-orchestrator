package chat

import (
	"context"
	"log/slog"
	"time"
)

const (
	// DefaultArchiveDeltaRetention is how long a settled turn keeps its raw
	// streamed message deltas in the provider-event archive. Nothing reads them
	// once the turn has settled; they are kept only as evidence while someone may
	// still be looking into a projection bug in a recent conversation.
	DefaultArchiveDeltaRetention = 14 * 24 * time.Hour
	// DefaultArchiveRetentionGrace is how long after the cutoff the cursor waits
	// before visiting a delta. The delete needs the delta's turn settled before the
	// cutoff, and in steady state the cursor would otherwise reach a delta within
	// one pass of it crossing the cutoff, before a turn that settles minutes after
	// its deltas had settled. A turn open for longer than the grace keeps them.
	DefaultArchiveRetentionGrace = 24 * time.Hour
	// DefaultArchiveRetentionFirstDelay leaves the writer to boot-time work
	// (chat resumes, host adoption, native history imports) before the first pass.
	DefaultArchiveRetentionFirstDelay = 5 * time.Minute
	// DefaultArchiveRetentionInterval matches the change_log janitor.
	DefaultArchiveRetentionInterval = 15 * time.Minute
	// DefaultArchiveRetentionWindow bounds how many archive ids one step reads on
	// the reader pool.
	DefaultArchiveRetentionWindow int64 = 2000
	// DefaultArchiveRetentionBatch bounds the rows one writer transaction
	// deletes. Deleted deltas are scattered through the archive, so each costs
	// page I/O while the transaction holds the single writer: on a 3.9 GB
	// database a 100-row batch held it for a median 12 ms, p99 224 ms, on a
	// heavily loaded machine.
	DefaultArchiveRetentionBatch int64 = 100
	// DefaultArchiveRetentionPause is the wait after a batch that deleted rows,
	// so streaming writes queued behind it get the writer first (measured: p99
	// of a concurrent write fell from 203 ms with back-to-back batches to 108 ms).
	DefaultArchiveRetentionPause           = 150 * time.Millisecond
	defaultArchiveRetentionStepsPerRun int = 100
)

// ArchiveRetentionStore is the storage surface the archive janitor needs.
type ArchiveRetentionStore interface {
	PruneSettledProviderDeltas(ctx context.Context, settledBefore time.Time, grace time.Duration, window, limit int64) (deleted int64, caughtUp bool, writerHold time.Duration, err error)
}

// ArchiveRetentionConfig customizes archive cleanup. Zero values use the
// production defaults.
type ArchiveRetentionConfig struct {
	Retention  time.Duration
	Grace      time.Duration
	FirstDelay time.Duration
	Interval   time.Duration
	Pause      time.Duration
	Window     int64
	Batch      int64
	Clock      func() time.Time
	Logger     *slog.Logger
}

// ArchiveRetention deletes the raw message deltas of turns that settled more
// than the retention window ago, walking the archive behind a persisted cursor
// in small steps, like the change_log janitor, so streaming writes never wait
// behind a large delete. Freed pages are reused by later writes; the file is not
// shrunk.
type ArchiveRetention struct {
	store      ArchiveRetentionStore
	retention  time.Duration
	grace      time.Duration
	firstDelay time.Duration
	interval   time.Duration
	pause      time.Duration
	window     int64
	batch      int64
	clock      func() time.Time
	logger     *slog.Logger
	maxSteps   int
}

// NewArchiveRetention constructs the archive janitor.
func NewArchiveRetention(store ArchiveRetentionStore, cfg ArchiveRetentionConfig) *ArchiveRetention {
	j := &ArchiveRetention{
		store:      store,
		retention:  cfg.Retention,
		grace:      cfg.Grace,
		firstDelay: cfg.FirstDelay,
		interval:   cfg.Interval,
		pause:      cfg.Pause,
		window:     cfg.Window,
		batch:      cfg.Batch,
		clock:      cfg.Clock,
		logger:     cfg.Logger,
		maxSteps:   defaultArchiveRetentionStepsPerRun,
	}
	if j.retention <= 0 {
		j.retention = DefaultArchiveDeltaRetention
	}
	if j.grace <= 0 {
		j.grace = DefaultArchiveRetentionGrace
	}
	if j.firstDelay <= 0 {
		j.firstDelay = DefaultArchiveRetentionFirstDelay
	}
	if j.interval <= 0 {
		j.interval = DefaultArchiveRetentionInterval
	}
	if j.pause <= 0 {
		j.pause = DefaultArchiveRetentionPause
	}
	if j.window <= 0 {
		j.window = DefaultArchiveRetentionWindow
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

// Start runs a pass after the first delay and then at the configured interval.
// The returned channel closes after cancellation and is safe to wait on before
// closing the store.
func (j *ArchiveRetention) Start(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		first := time.NewTimer(j.firstDelay)
		defer first.Stop()
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		}
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

// RunOnce takes at most a bounded number of steps and stops once the cursor has
// caught up. A backlog, such as the first passes after upgrading a large
// database, is worked off over later passes.
func (j *ArchiveRetention) RunOnce(ctx context.Context) error {
	settledBefore := j.clock().UTC().Add(-j.retention)
	var total int64
	var maxHold time.Duration
	steps := 0
	for steps < j.maxSteps {
		deleted, caughtUp, hold, err := j.store.PruneSettledProviderDeltas(ctx, settledBefore, j.grace, j.window, j.batch)
		steps++
		if err != nil {
			return err
		}
		total += deleted
		maxHold = max(maxHold, hold)
		if caughtUp {
			break
		}
		if deleted > 0 {
			pause := time.NewTimer(j.pause)
			select {
			case <-ctx.Done():
				pause.Stop()
				return ctx.Err()
			case <-pause.C:
			}
		}
	}
	if total > 0 {
		j.logger.Info("provider event archive retention pruned settled message deltas",
			"rows", total, "steps", steps, "max_writer_hold_ms", maxHold.Milliseconds())
	}
	return nil
}

func (j *ArchiveRetention) run(ctx context.Context) {
	if err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
		j.logger.Warn("provider event archive retention failed", "err", err)
	}
}
