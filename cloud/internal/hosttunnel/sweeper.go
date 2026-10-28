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
	// on a slow download, and one that comes later gets its tunnel made again
	// under the record, which stays while the account lists the machine.
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
	RemoteHostListed(ctx context.Context, userID, hostID string) (bool, error)
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

// Sweep releases the tunnels of machines that went offline and of tunnels no
// connector ever served, and deletes the tunnels and DNS records whose account
// no longer lists the machine, or whose machine AO Cloud has not seen for
// OfflineRetention. It returns how many tunnels it released or deleted.
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
		if sweepCandidate(ctx, store, cf, c, cutoff, now, fail) {
			done++
		}
	}

	// Machines asleep, offline or quit, and tunnels no connector ever served,
	// lose the tunnel and keep the address: the account may still list it,
	// and a device that looked up a name with no record would cache the miss.
	// Cloudflare refuses to delete a tunnel a connector serves again, so a
	// machine back since the listing keeps it, and one that fetched its token
	// moments ago is about to connect.
	disconnected, err := cf.Disconnected(ctx, now.Add(-DisconnectedRetention))
	if err != nil {
		fail(err)
	}
	unused, err := cf.NeverConnected(ctx, now.Add(-UnusedRetention))
	if err != nil {
		fail(err)
	}
	for _, id := range append(disconnected, unused...) {
		if releaseIdle(ctx, store, cf, id, now, fail) {
			done++
		}
	}
	return done, errors.Join(errs...)
}

// sweepCandidate deletes one candidate's tunnel and DNS record, unless its
// machine still serves it or its account lists the host again. It reports
// whether it deleted them.
func sweepCandidate(ctx context.Context, store SweepStore, cf *Cloudflare, c domain.RemoteHostTunnelCandidate, cutoff, now time.Time, fail func(error)) bool {
	defer cf.Lock(c.UserID, c.HostID)()
	// Read again under the lock: the machine may have fetched its token, or
	// got a new tunnel, since the candidates were read.
	row, found, err := store.FindRemoteHostTunnel(ctx, c.TunnelID)
	if err != nil || !found || (c.Registered && !row.LastSeenAt.Before(cutoff)) {
		if err != nil {
			fail(err)
		}
		return false
	}
	// A machine whose daemon cannot reach AO Cloud may still be serving.
	if c.Registered && !c.Released {
		online, err := cf.Online(ctx, c.TunnelID)
		if err != nil {
			fail(err)
			return false
		}
		if online {
			if err := store.TouchRemoteHostTunnel(ctx, c.TunnelID, now); err != nil {
				fail(err)
			}
			return false
		}
	}
	// Asked again under the lock: the account may have listed the host again
	// since the candidates were read.
	if !c.Registered {
		if listed, err := store.RemoteHostListed(ctx, c.UserID, c.HostID); err != nil || listed {
			if err != nil {
				fail(err)
			}
			return false
		}
	}
	// Delete also removes the DNS record, when it still names this tunnel. A
	// released tunnel is already deleted.
	deleteAll := cf.Delete
	if row.Released {
		deleteAll = cf.DeleteRecord
	}
	if err := deleteAll(ctx, c.TunnelID, c.Hostname); err != nil {
		fail(err)
		return false
	}
	if err := store.DeleteRemoteHostTunnel(ctx, c.TunnelID); err != nil {
		fail(err)
		return false
	}
	return true
}

// releaseIdle releases one listed idle tunnel this AO Cloud's row names, and
// reports whether it did.
func releaseIdle(ctx context.Context, store SweepStore, cf *Cloudflare, id string, now time.Time, fail func(error)) bool {
	row, found, err := store.FindRemoteHostTunnel(ctx, id)
	if err != nil {
		fail(err)
		return false
	}
	if !found {
		return false
	}
	defer cf.Lock(row.UserID, row.HostID)()
	// Read again under the lock, which a new tunnel for the host is made under.
	if row, found, err = store.FindRemoteHostTunnel(ctx, id); err != nil || !found || row.Released ||
		now.Sub(row.LastSeenAt) < DisconnectedRetention {
		if err != nil {
			fail(err)
		}
		return false
	}
	released, err := cf.Release(ctx, id)
	if err != nil {
		fail(err)
		return false
	}
	if !released {
		return false
	}
	if err := store.ReleaseRemoteHostTunnel(ctx, id); err != nil {
		fail(err)
		return false
	}
	return true
}
