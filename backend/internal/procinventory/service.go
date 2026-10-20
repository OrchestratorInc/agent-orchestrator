package procinventory

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Signal identifies the signal the kill path delivers to a process group.
type Signal int

const (
	SigTerm Signal = iota + 1
	SigKill
)

// Service builds inventories and kills orphaned trees. All dependencies are
// injected as plain functions so tests can drive classification and kill
// without processes or SQLite; the daemon wires the real ones once.
type Service struct {
	deps Deps
	// disabled is the AO_NO_PROCINVENTORY runtime gate, read once at
	// construction. A disabled service serves empty inventories without
	// scanning and refuses kills.
	disabled bool
	killMu   chan struct{}
	invMu    sync.Mutex
	// cached is the last successful inventory with its wall-clock stamp;
	// served instead of an error when a fresh scan fails or overruns its
	// budget, so a thrashing machine degrades to slightly-stale numbers
	// rather than a dead status bar.
	cached   *Inventory
	cachedAt time.Time
}

// Deps carries the service's collaborators. Zero fields select the defaults
// documented on each; see New.
type Deps struct {
	// Scan snapshots the process table; default SystemScan (unix only).
	Scan func(ctx context.Context) ([]Entry, error)
	// DaemonPID is the calling daemon's own PID; trees rooted at its children
	// are owned, never orphans.
	DaemonPID int
	// LiveSessions returns the daemon's live session set; default: none.
	LiveSessions func(ctx context.Context) (map[domain.SessionID]domain.SessionRecord, error)
	// TmuxSocketName disambiguates the AO tmux server from a user's personal
	// one; empty means the default socket.
	TmuxSocketName string
	// Grace is how long the kill path waits between SIGTERM and SIGKILL;
	// default 5s (the tmux runtime's reap grace).
	Grace time.Duration
	// Signal delivers a signal to a process group; default signalGroup.
	Signal func(pid int, sig Signal) error
	// Probe reports whether a PID is alive; default processalive.Alive.
	Probe func(pid int) bool
	// HostStats snapshots host memory; default per-platform defaultHostStats
	// (ErrHostStatsUnsupported on windows). An error is not fatal — the
	// inventory is returned without a host section.
	HostStats func(ctx context.Context) (HostStats, error)
	// ScanBudget bounds one scan/collect pass; on overrun the last good
	// inventory is served (nil if none yet). Default 15s.
	ScanBudget time.Duration
	// CacheTTL is how long a successful inventory is reused before a fresh
	// scan runs; overlapping pollers then share one pass. Default 3s.
	CacheTTL time.Duration
	// Unregister drops a killed orphan's PTY-host registry entry; the daemon
	// wires conpty's ptyregistry.Unregister. Nil means skip.
	Unregister func(ctx context.Context, sessionID string) error
	// Now is the clock; default time.Now.
	Now func() time.Time
	// Log receives kill outcomes; default slog.Default().
	Log *slog.Logger
}

// New wires a Service with defaults for unset dependencies.
func New(deps Deps) *Service {
	if deps.Scan == nil {
		deps.Scan = SystemScan
	}
	if deps.LiveSessions == nil {
		deps.LiveSessions = func(context.Context) (map[domain.SessionID]domain.SessionRecord, error) {
			return map[domain.SessionID]domain.SessionRecord{}, nil
		}
	}
	if deps.Grace <= 0 {
		deps.Grace = 5 * time.Second
	}
	if deps.Signal == nil {
		deps.Signal = signalGroup
	}
	if deps.Probe == nil {
		deps.Probe = defaultProbe
	}
	if deps.HostStats == nil {
		deps.HostStats = defaultHostStats
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.ScanBudget <= 0 {
		deps.ScanBudget = 15 * time.Second
	}
	if deps.CacheTTL <= 0 {
		deps.CacheTTL = 3 * time.Second
	}
	if deps.Log == nil {
		deps.Log = slog.Default()
	}
	return &Service{deps: deps, disabled: EnvDisabled(), killMu: make(chan struct{}, 1)}
}

// Disabled reports whether the AO_NO_PROCINVENTORY environment variable
// turned the surface off. A disabled service's Inventory returns an empty
// inventory without touching the process table; the controller presents the
// whole surface as unimplemented.
func (s *Service) Disabled() bool { return s.disabled }

// Inventory snapshots and classifies the current process table. A host
// memory fetch failure is not an error: the inventory is returned without
// its host section (the status bar hides that section on nil).
//
// Inventories are cached for Deps.CacheTTL and served single-flight: the
// status bar polls continuously, so overlapping clients share one scan pass
// instead of each spawning its own process storm. When a scan fails or
// overruns its budget, the last good inventory is served (slightly stale)
// rather than an error — a thrashing machine degrades to stale numbers, not
// to a dead status bar.
func (s *Service) Inventory(ctx context.Context) (Inventory, error) {
	if s.disabled {
		return Inventory{}, nil
	}
	s.invMu.Lock()
	defer s.invMu.Unlock()

	now := s.deps.Now()
	if s.cached != nil && now.Sub(s.cachedAt) < s.deps.CacheTTL {
		return *s.cached, nil
	}

	scanCtx, cancel := context.WithTimeout(ctx, s.deps.ScanBudget)
	defer cancel()
	entries, err := s.deps.Scan(scanCtx)
	if err != nil {
		if s.cached != nil {
			s.deps.Log.Debug("procinventory: scan failed, serving last good inventory", "err", err)
			return *s.cached, nil
		}
		return Inventory{}, err
	}
	live, err := s.deps.LiveSessions(scanCtx)
	if err != nil {
		if s.cached != nil {
			s.deps.Log.Debug("procinventory: live session read failed, serving last good inventory", "err", err)
			return *s.cached, nil
		}
		return Inventory{}, err
	}
	inv := BuildInventory(entries, live, s.deps.DaemonPID, s.deps.TmuxSocketName, s.deps.Now())
	if host, err := s.deps.HostStats(scanCtx); err != nil {
		s.deps.Log.Debug("procinventory: host stats unavailable", "err", err)
	} else {
		inv.Host = &host
	}
	s.cached = &inv
	s.cachedAt = now
	return inv, nil
}
