package artifactstore

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// Sweeper defaults.
const (
	DefaultSweepInterval = 5 * time.Minute
	sweepBatch           = 100
	sweepLease           = 10 * time.Minute
)

// VersionQueue is the queue of object versions no committed artifact serves.
type VersionQueue interface {
	ClaimArtifactObjectVersions(ctx context.Context, limit int, lease time.Duration) ([]domain.ArtifactObjectVersion, error)
	ForgetArtifactObjectVersions(ctx context.Context, versions []domain.ArtifactObjectVersion) error
}

// RunSweeper deletes queued object versions from the bucket every interval
// until ctx ends: uploads never committed within ArtifactUploadTTL, and
// versions a commit replaced or deleted. A version whose delete fails stays
// queued and is retried after its claim lapses.
func RunSweeper(ctx context.Context, queue VersionQueue, store *Store, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if deleted, err := Sweep(ctx, queue, store); err != nil {
			if ctx.Err() == nil {
				log.Warn("sweep artifact object versions", "error", err, "deleted", deleted)
			}
		} else if deleted > 0 {
			log.Info("swept artifact object versions", "deleted", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep deletes every queued object version that is due, in batches, and
// returns how many it deleted.
func Sweep(ctx context.Context, queue VersionQueue, store *Store) (int, error) {
	deleted := 0
	for {
		versions, err := queue.ClaimArtifactObjectVersions(ctx, sweepBatch, sweepLease)
		if err != nil {
			return deleted, err
		}
		if len(versions) == 0 {
			return deleted, nil
		}
		collected := make([]domain.ArtifactObjectVersion, 0, len(versions))
		var deleteErr error
		for _, version := range versions {
			if err := store.DeleteVersion(ctx, version.S3Key, version.VersionID); err != nil {
				deleteErr = err
				continue
			}
			collected = append(collected, version)
		}
		if err := queue.ForgetArtifactObjectVersions(ctx, collected); err != nil {
			return deleted, err
		}
		deleted += len(collected)
		if deleteErr != nil {
			return deleted, deleteErr
		}
		if len(versions) < sweepBatch {
			return deleted, nil
		}
	}
}
