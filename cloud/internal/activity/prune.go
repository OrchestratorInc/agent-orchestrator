package activity

import (
	"context"
	"log/slog"
	"time"
)

// Retention is how long the activity trail is kept.
const Retention = 90 * 24 * time.Hour

// DefaultPruneInterval is how often the pruner runs. Deleting a day's worth of
// rows at a time keeps each delete small.
const DefaultPruneInterval = time.Hour

// PruneStore deletes expired activity.
type PruneStore interface {
	PruneActivityEvents(ctx context.Context, before time.Time) (int64, error)
}

// RunPruner deletes activity older than retention every interval until ctx is
// canceled. Several control-plane replicas may run it at once: the delete is
// idempotent.
func RunPruner(ctx context.Context, store PruneStore, retention, interval time.Duration, log *slog.Logger) {
	if retention <= 0 {
		retention = Retention
	}
	if interval <= 0 {
		interval = DefaultPruneInterval
	}
	if log == nil {
		log = slog.Default()
	}
	prune := func() {
		deleted, err := store.PruneActivityEvents(ctx, time.Now().Add(-retention))
		if err != nil {
			if ctx.Err() == nil {
				log.Warn("prune activity events", "error", err)
			}
			return
		}
		if deleted > 0 {
			log.Info("pruned activity events", "deleted", deleted, "retention_days", int(retention.Hours()/24))
		}
	}
	prune()
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			prune()
		}
	}
}
