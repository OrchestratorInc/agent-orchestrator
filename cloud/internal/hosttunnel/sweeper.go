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
	// UnusedRetention is how long a tunnel no connector has ever served
	// survives. A machine connects within seconds of being linked, or minutes
	// on a slow download, and one that comes later gets its tunnel made again.
	// Without it, registering machines that never run would hold tunnels and
	// DNS records for OfflineRetention.
	UnusedRetention = time.Hour
	// DefaultSweepInterval runs the sweep once an hour, so an unused tunnel
	// goes within two hours.
	DefaultSweepInterval = time.Hour
)

// SweepStore is what the sweep reads and writes.
type SweepStore interface {
	ListRemoteHostTunnelSweepCandidates(context.Context, time.Time) ([]domain.RemoteHostTunnelCandidate, error)
	GetRemoteHostTunnel(context.Context, string) (domain.RemoteHostTunnel, bool, error)
	TouchRemoteHostTunnel(context.Context, string, time.Time) error
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
// no longer has, the ones offline for OfflineRetention, and the ones never
// connected for UnusedRetention. It asks Cloudflare
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
			// Recorded as seen when Cloudflare last saw it, so a kept tunnel
			// leaves the candidates until it could be due, and the batch
			// limit never fills with machines that are not.
			seen := now
			if !status.Online {
				seen = status.OfflineSince
			}
			if status.Online || (!status.Missing && seen.After(cutoff)) {
				if err := store.TouchRemoteHostTunnel(ctx, c.HostID, seen); err != nil {
					errs = append(errs, err)
				}
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
	unused, err := cf.NeverConnected(ctx, now.Add(-UnusedRetention))
	if err != nil {
		errs = append(errs, err)
	}
	for _, t := range unused {
		row, found, err := store.GetRemoteHostTunnel(ctx, t.HostID)
		if err != nil {
			errs = append(errs, err)
			continue
		}
		// Only tunnels this AO Cloud made and still uses: another one may
		// share the Cloudflare account.
		if !found || row.TunnelID != t.ID {
			continue
		}
		// Asked again, so a machine that connected since the listing keeps it.
		if status, err := cf.Status(ctx, t.ID); err != nil || !status.NeverConnected {
			if err != nil {
				errs = append(errs, err)
			}
			continue
		}
		if err := cf.Delete(ctx, t.ID, row.Hostname); err != nil {
			errs = append(errs, err)
			continue
		}
		if err := store.DeleteRemoteHostTunnel(ctx, t.HostID, t.ID); err != nil {
			errs = append(errs, err)
			continue
		}
		deleted++
	}
	return deleted, errors.Join(errs...)
}
