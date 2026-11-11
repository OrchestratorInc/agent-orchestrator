// Package terminaltitle retains the title each terminal's program last set, for
// as long as the terminal runs and whether or not any UI is attached.
//
// The runtime parses and normalizes titles; this package follows the live
// terminals and stores what they report. A title is a fact about the running
// program: it never changes a session's name or identity, and read models decide
// whether to show it.
package terminaltitle

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

// Variables so tests can shorten them.
var (
	// reconcileInterval is how soon a new or replaced terminal gets watched.
	// The host keeps the latest title, so a late watcher misses nothing.
	reconcileInterval = 3 * time.Second
	// minWriteInterval limits one terminal's durable writes, however often its
	// program changes its title. The newest title is written when it elapses.
	minWriteInterval       = 2 * time.Second
	initialWatchRetryDelay = 500 * time.Millisecond
	watchRetryMax          = 30 * time.Second
)

const flushTimeout = 5 * time.Second

// Store is the persistence the manager needs.
type Store interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	SelectRestorableShellTerminals(ctx context.Context, appRunID string) ([]shellterm.ShellTerminalRecord, error)
	UpsertSessionTerminalTitle(ctx context.Context, id domain.SessionID, title string, updatedAt time.Time) error
	UpdateShellTerminalAppTitle(ctx context.Context, handleID, title string) error
}

type terminalKey struct {
	session domain.SessionID // set for an agent session's terminal
	shell   string           // set for a shell terminal
}

type watch struct {
	key    terminalKey
	handle string
	cancel context.CancelFunc

	// Guarded by Manager.mu.
	stopped   bool
	seen      bool
	latest    string
	hasSaved  bool
	saved     string
	lastWrite time.Time
	timer     *time.Timer
}

// Manager keeps one title watch open per live terminal.
type Manager struct {
	rt       ports.TerminalTitleWatcher
	store    Store
	appRunID string
	log      *slog.Logger

	mu      sync.Mutex
	watches map[terminalKey]*watch
}

// NewManager builds a manager; call Run to start it.
func NewManager(rt ports.TerminalTitleWatcher, store Store, appRunID string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{rt: rt, store: store, appRunID: appRunID, log: log, watches: map[terminalKey]*watch{}}
}

// Run follows the live terminals until ctx ends. Reconciling from storage covers
// spawn, restore, close and daemon restart without hooks in each of those paths.
func (m *Manager) Run(ctx context.Context) {
	ticker := time.NewTicker(reconcileInterval)
	defer ticker.Stop()
	for {
		m.reconcile(ctx)
		select {
		case <-ctx.Done():
			m.stopAll(ctx)
			return
		case <-ticker.C:
		}
	}
}

func (m *Manager) stopAll(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, w := range m.watches {
		m.stopLocked(ctx, w)
		delete(m.watches, key)
	}
}

func (m *Manager) reconcile(ctx context.Context) {
	sessions, err := m.store.ListAllSessions(ctx)
	if err != nil {
		m.log.Warn("terminal titles: list sessions failed", "err", err)
		return
	}
	shells, err := m.store.SelectRestorableShellTerminals(ctx, m.appRunID)
	if err != nil {
		m.log.Warn("terminal titles: list shell terminals failed", "err", err)
		return
	}
	want := make(map[terminalKey]string, len(sessions)+len(shells))
	for _, rec := range sessions {
		// Chat sessions have no terminal program, so they never get a title.
		if !rec.IsTerminated && domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeTUI && rec.Metadata.RuntimeHandleID != "" {
			want[terminalKey{session: rec.ID}] = rec.Metadata.RuntimeHandleID
		}
	}
	for _, rec := range shells {
		if !rec.Transient {
			want[terminalKey{shell: rec.HandleID}] = rec.HandleID
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	for key, w := range m.watches {
		if want[key] != w.handle {
			m.stopLocked(ctx, w)
			delete(m.watches, key)
		}
	}
	for key, handle := range want {
		if _, ok := m.watches[key]; ok {
			continue
		}
		watchCtx, cancel := context.WithCancel(ctx)
		w := &watch{key: key, handle: handle, cancel: cancel}
		m.watches[key] = w
		go m.follow(watchCtx, w)
	}
}

// follow reconnects until reconcile stops the watch. A host that was replaced
// reports its own title on reconnect, which clears the previous program's.
func (m *Manager) follow(ctx context.Context, w *watch) {
	handle := ports.RuntimeHandle{ID: w.handle}
	for backoff := initialWatchRetryDelay; ; backoff = min(backoff*2, watchRetryMax) {
		err := m.rt.WatchTerminalTitle(ctx, handle, func(title string) { m.observe(ctx, w, title) })
		if ctx.Err() != nil {
			return
		}
		delay := backoff
		if errors.Is(err, ports.ErrTerminalTitleUnavailable) {
			delay = watchRetryMax
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

func (m *Manager) observe(ctx context.Context, w *watch, title string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w.stopped {
		return
	}
	w.seen, w.latest = true, title
	if w.hasSaved && w.saved == title {
		if w.timer != nil {
			w.timer.Stop()
			w.timer = nil
		}
		return
	}
	if w.timer == nil {
		m.scheduleLocked(ctx, w, max(0, minWriteInterval-time.Since(w.lastWrite)))
	}
}

func (m *Manager) scheduleLocked(ctx context.Context, w *watch, delay time.Duration) {
	w.timer = time.AfterFunc(delay, func() { m.flush(ctx, w) })
}

func (m *Manager) flush(ctx context.Context, w *watch) {
	m.mu.Lock()
	w.timer = nil
	if w.stopped || (w.hasSaved && w.saved == w.latest) {
		m.mu.Unlock()
		return
	}
	title := w.latest
	w.hasSaved, w.saved, w.lastWrite = true, title, time.Now()
	m.mu.Unlock()

	err := m.save(ctx, w.key, title)
	if err == nil {
		return
	}
	m.log.Warn("terminal titles: save failed", "session", w.key.session, "shell", w.key.shell, "err", err)
	m.mu.Lock()
	defer m.mu.Unlock()
	if w.saved == title {
		w.hasSaved = false
	}
	if !w.stopped && w.timer == nil {
		m.scheduleLocked(ctx, w, minWriteInterval)
	}
}

func (m *Manager) save(ctx context.Context, key terminalKey, title string) error {
	if key.shell != "" {
		return m.store.UpdateShellTerminalAppTitle(ctx, key.shell, title)
	}
	return m.store.UpsertSessionTerminalTitle(ctx, key.session, title, time.Now().UTC())
}

// stopLocked ends a watch and stores a title still waiting on the write limit.
// The stored title stays after the terminal ends, as its last known title.
func (m *Manager) stopLocked(ctx context.Context, w *watch) {
	w.cancel()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if !w.seen || (w.hasSaved && w.saved == w.latest) {
		return
	}
	key, title := w.key, w.latest
	go func() {
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
		defer cancel()
		if err := m.save(flushCtx, key, title); err != nil {
			m.log.Debug("terminal titles: final save failed", "session", key.session, "shell", key.shell, "err", err)
		}
	}()
}
