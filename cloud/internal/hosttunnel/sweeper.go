package hosttunnel

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

const (
	// OfflineRetention is how long a registered machine's tunnel survives
	// without a connector before the sweep deletes it.
	OfflineRetention = 90 * 24 * time.Hour
	// DefaultSweepInterval runs the sweep once a day.
	DefaultSweepInterval = 24 * time.Hour
)

// SweepStore is what the sweep reads and writes.
type SweepStore interface {
	ListRemoteHostTunnelSweepCandidates(context.Context, time.Time) ([]domain.RemoteHostTunnelCandidate, error)
	TouchRemoteHostTunnel(context.Context, string) error
	DeleteRemoteHostTunnel(context.Context, string, string) error
}

// RunSweeper sweeps at once and then every interval until ctx ends.
func RunSweeper(ctx context.Context, store SweepStore, cf *Cloudflare, interval time.Duration, log *slog.Logger) {
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	if log == nil {
		log = slog.Default()
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if deleted, err := Sweep(ctx, store, cf, time.Now()); err != nil {
			if ctx.Err() == nil {
				log.Warn("sweep host tunnels", "error", err, "deleted", deleted)
			}
		} else if deleted > 0 {
			log.Info("swept host tunnels", "deleted", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Sweep deletes the tunnels no account registers any more, the ones Cloudflare
// no longer has, and the ones offline for OfflineRetention. It asks Cloudflare
// before deleting a registered machine's tunnel, because a machine that runs
// for months without restarting never fetches its token. A deleted machine
// that comes back gets a new tunnel at the same address on its next start.
func Sweep(ctx context.Context, store SweepStore, cf *Cloudflare, now time.Time) (int, error) {
	cutoff := now.Add(-OfflineRetention)
	candidates, err := store.ListRemoteHostTunnelSweepCandidates(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	deleted := 0
	var errs []error
	for _, c := range candidates {
		if c.Registered {
			status, err := cf.Status(ctx, c.TunnelID)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if status.Online {
				if err := store.TouchRemoteHostTunnel(ctx, c.HostID); err != nil {
					errs = append(errs, err)
				}
				continue
			}
			if !status.Missing && status.OfflineSince.After(cutoff) {
				continue
			}
		}
		if err := cf.Delete(ctx, c.TunnelID, c.Hostname); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := store.DeleteRemoteHostTunnel(ctx, c.HostID, c.TunnelID); err != nil {
			errs = append(errs, err)
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
}
