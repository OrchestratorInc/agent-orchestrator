package cdc

import (
	"context"
	"log/slog"
	"time"
)

const (
	DefaultRetention         = 7 * 24 * time.Hour
	DefaultRetentionInterval = 15 * time.Minute
)

const (
	DefaultMaxRows                int64 = 100_000
	DefaultRetentionBatch         int64 = 10_000
	defaultRetentionBatchesPerRun int   = 8
)

type RetentionStore interface {
	PruneChangeLogBefore(context.Context, time.Time, int64) (int64, error)
	PruneChangeLogToMaxRows(context.Context, int64, int64) (int64, error)
}

type RetentionConfig struct {
	Retention time.Duration
	MaxRows   int64
	Interval  time.Duration
	Batch     int64
	Clock     func() time.Time
	Logger    *slog.Logger
}

type RetentionJanitor struct {
	store      RetentionStore
	retention  time.Duration
	maxRows    int64
	interval   time.Duration
	batch      int64
	clock      func() time.Time
	logger     *slog.Logger
	maxBatches int
}

func NewRetentionJanitor(store RetentionStore, cfg RetentionConfig) *RetentionJanitor {
	j := &RetentionJanitor{
		store:      store,
		retention:  cfg.Retention,
		maxRows:    cfg.MaxRows,
		interval:   cfg.Interval,
		batch:      cfg.Batch,
		clock:      cfg.Clock,
		logger:     cfg.Logger,
		maxBatches: defaultRetentionBatchesPerRun,
	}
	if j.retention <= 0 {
		j.retention = DefaultRetention
	}
	if j.maxRows <= 0 {
		j.maxRows = DefaultMaxRows
	}
	if j.interval <= 0 {
		j.interval = DefaultRetentionInterval
	}
	if j.batch <= 0 {
		j.batch = DefaultRetentionBatch
	}
	if j.clock == nil {
		j.clock = time.Now
	}
	if j.logger == nil {
		j.logger = slog.Default()
	}
	return j
}

func (j *RetentionJanitor) Start(ctx context.Context) <-chan struct{} {
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

func (j *RetentionJanitor) RunOnce(ctx context.Context) error {
	before := j.clock().UTC().Add(-j.retention)
	var total int64
	for i := 0; i < j.maxBatches; i++ {
		removedByAge, err := j.store.PruneChangeLogBefore(ctx, before, j.batch)
		if err != nil {
			return err
		}
		removedBySize, err := j.store.PruneChangeLogToMaxRows(ctx, j.maxRows, j.batch)
		if err != nil {
			return err
		}
		removed := removedByAge + removedBySize
		total += removed
		if removed == 0 {
			break
		}
	}
	if total > 0 {
		j.logger.Info("cdc change_log retention pruned rows", "rows", total)
	}
	return nil
}

func (j *RetentionJanitor) run(ctx context.Context) {
	if err := j.RunOnce(ctx); err != nil && ctx.Err() == nil {
		j.logger.Warn("cdc change_log retention failed", "err", err)
	}
}
