package hosttunnel

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

const (
	// DisconnectedRetention is how long a machine's tunnel survives once its
	// last connector left: asleep, offline or quit. Only the tunnel goes; its
	// DNS record stays, so the address does, and the machine gets a new
	// tunnel under it when it comes back. Cloudflare counts every tunnel,
	// connected or not, against the account's 1,000.
	DisconnectedRetention = 5 * time.Minute
	// UnusedRetention is how long a tunnel no connector has ever served
	// survives. A machine connects within seconds of being linked, or minutes
	// on a slow download, and one that comes later gets its tunnel made again.
	// Its DNS record goes with it, so registering machines that never run
	// holds neither.
	UnusedRetention = time.Hour
	// OfflineRetention is how long a registered machine keeps its DNS record,
	// and with it its row, after AO Cloud last saw it.
	OfflineRetention = 90 * 24 * time.Hour
	// UnregisteredGrace keeps a tunnel no account lists for a while after it
	// was written: a registration makes its tunnel first and lists the host
	// right after.
	UnregisteredGrace = 10 * time.Minute
	// DefaultSweepInterval runs the sweep every five minutes, so a
	// disconnected tunnel goes within ten.
	DefaultSweepInterval = 5 * time.Minute
)

// SweepStore is what the sweep reads and writes.
type SweepStore interface {
	ListRemoteHostTunnelSweepCandidates(ctx context.Context, cutoff, unregisteredBefore time.Time) ([]domain.RemoteHostTunnelCandidate, error)
	FindRemoteHostTunnel(ctx context.Context, tunnelID string) (domain.RemoteHostTunnel, bool, error)
	TouchRemoteHostTunnel(ctx context.Context, tunnelID string, seenAt time.Time) error
	ReleaseRemoteHostTunnel(ctx context.Context, tunnelID string) error
	DeleteRemoteHostTunnel(ctx context.Context, tunnelID string) error
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

// Sweep releases the tunnels of machines that went offline, deletes the
// tunnels no connector ever served, and deletes the tunnels and DNS records
// whose account no longer lists the machine, or whose machine AO Cloud has not
// seen for OfflineRetention. It returns how many tunnels it released or
// deleted.
//
// Cloudflare's listings name every host tunnel in the account, including other
// AO Clouds' sharing it, so a listed tunnel is acted on only when this AO
// Cloud's row still names it.
func Sweep(ctx context.Context, store SweepStore, cf *Cloudflare, now time.Time) (int, error) {
	done := 0
	var errs []error
	fail := func(err error) { errs = append(errs, err) }

	cutoff := now.Add(-OfflineRetention)
	candidates, err := store.ListRemoteHostTunnelSweepCandidates(ctx, cutoff, now.Add(-UnregisteredGrace))
	if err != nil {
		return 0, err
	}
	for _, c := range candidates {
		// A machine whose daemon cannot reach AO Cloud may still be serving.
		if c.Registered && !c.Released {
			status, err := cf.Status(ctx, c.TunnelID)
			if err != nil {
				fail(err)
				continue
			}
			if status.Online {
				if err := store.TouchRemoteHostTunnel(ctx, c.TunnelID, now); err != nil {
					fail(err)
				}
				continue
			}
		}
		// Delete also removes the DNS record, when it still names this tunnel.
		if err := cf.Delete(ctx, c.TunnelID, c.Hostname); err != nil {
			fail(err)
			continue
		}
		if err := store.DeleteRemoteHostTunnel(ctx, c.TunnelID); err != nil {
			fail(err)
			continue
		}
		done++
	}

	ours := func(tunnelID string) (domain.RemoteHostTunnel, bool) {
		row, found, err := store.FindRemoteHostTunnel(ctx, tunnelID)
		if err != nil {
			fail(err)
			return row, false
		}
		return row, found && !row.Released
	}

	disconnected, err := cf.Disconnected(ctx, now.Add(-DisconnectedRetention))
	if err != nil {
		fail(err)
	}
	for _, id := range disconnected {
		if _, ok := ours(id); !ok {
			continue
		}
		released, err := cf.Release(ctx, id)
		if err != nil {
			fail(err)
			continue
		}
		if !released { // its machine came back since the listing
			continue
		}
		if err := store.ReleaseRemoteHostTunnel(ctx, id); err != nil {
			fail(err)
			continue
		}
		done++
	}

	unused, err := cf.NeverConnected(ctx, now.Add(-UnusedRetention))
	if err != nil {
		fail(err)
	}
	for _, id := range unused {
		row, ok := ours(id)
		if !ok {
			continue
		}
		// Asked again, so a machine that connected since the listing keeps it.
		if status, err := cf.Status(ctx, id); err != nil || !status.NeverConnected {
			if err != nil {
				fail(err)
			}
			continue
		}
		if err := cf.Delete(ctx, id, row.Hostname); err != nil {
			fail(err)
			continue
		}
		if err := store.DeleteRemoteHostTunnel(ctx, id); err != nil {
			fail(err)
			continue
		}
		done++
	}
	return done, errors.Join(errs...)
}
