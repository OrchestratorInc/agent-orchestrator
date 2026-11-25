// Package terminaltitle retains the title each terminal's program last set, for
// as long as the terminal runs and whether or not any UI is attached.
//
// The runtime parses and normalizes titles; this package follows the live
// terminals and stores what they report. A title is a fact about the running
// program: it never changes a session's name or identity, and read models decide
// whether to show it.
//
// Nothing polls. Agent sessions are read once at start, then one at a time when
// a change event names them; shell terminals are tracked by the shell terminal
// service as they open and close.
package terminaltitle

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Variables so tests can shorten them.
var (
	// minWriteInterval limits one terminal's durable writes, however often its
	// program changes its title. The newest title is written when it elapses.
	minWriteInterval       = 2 * time.Second
	initialWatchRetryDelay = 500 * time.Millisecond
	watchRetryMax          = 30 * time.Second
	// maxWatchFailures ends a watch whose host refuses that many connections in
	// a row. The watch restarts when its session gets a new terminal.
	maxWatchFailures = 6
	// readRetryDelay is how soon a failed session read is tried again. Nothing
	// rescans, so a read that failed must not be dropped.
	readRetryDelay = 5 * time.Second
)

const flushTimeout = 5 * time.Second

// Store is the persistence the manager needs.
type Store interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	UpsertSessionTerminalTitle(ctx context.Context, id domain.SessionID, title string, updatedAt time.Time) error
	UpdateShellTerminalAppTitle(ctx context.Context, handleID, title string) error
}

// Events delivers session change events. Callbacks must not block.
type Events interface {
	Subscribe(fn func(cdc.Event)) (unsubscribe func())
}

type terminalKey struct {
	session domain.SessionID // set for an agent session's terminal
	shell   string           // set for a shell terminal
}

// terminal is one run of a terminal host. A restored session keeps its handle
// but gets a new launch, so both name the run.
type terminal struct {
	handle string
	launch string
}

type watch struct {
	key    terminalKey
	term   terminal
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
	rt    ports.TerminalTitleWatcher
	store Store
	log   *slog.Logger
	base  context.Context

	// writeMu orders title writes, so a replaced run's late write cannot land
	// after its successor's. Taken before mu, never while holding it.
	writeMu sync.Mutex

	mu      sync.Mutex
	watches map[terminalKey]*watch
	// pending holds sessions named by change events, read by Run.
	pending map[domain.SessionID]struct{}
	poke    chan struct{}
}

// NewManager builds a manager whose watches live while ctx is active. Shell
// terminals can be tracked at once; call Run to follow agent sessions.
func NewManager(ctx context.Context, rt ports.TerminalTitleWatcher, store Store, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		rt: rt, store: store, log: log, base: ctx,
		watches: map[terminalKey]*watch{},
		pending: map[domain.SessionID]struct{}{},
		poke:    make(chan struct{}, 1),
	}
}

// TrackShellTerminal starts following a shell terminal's title.
func (m *Manager) TrackShellTerminal(handleID string) {
	if handleID != "" {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.setLocked(terminalKey{shell: handleID}, terminal{handle: handleID})
	}
}

// UntrackShellTerminal stops following a shell terminal, storing a title that
// was still waiting on the write limit.
func (m *Manager) UntrackShellTerminal(handleID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setLocked(terminalKey{shell: handleID}, terminal{})
}

// Run follows agent sessions until ctx ends: every live session once, then each
// session a change event names. A session's spawn, restore and termination
// all write fields that emit an event, so no rescan is needed.
func (m *Manager) Run(ctx context.Context, events Events) {
	if events != nil {
		unsubscribe := events.Subscribe(m.observeEvent)
		defer unsubscribe()
	}
	for !m.loadSessions(ctx) {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-time.After(readRetryDelay):
		}
	}
	failed := map[domain.SessionID]struct{}{}
	var retry <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-m.poke:
		case <-retry:
			retry = nil
		}
		m.mu.Lock()
		for id := range m.pending {
			failed[id] = struct{}{}
		}
		clear(m.pending)
		m.mu.Unlock()
		for id := range failed {
			if m.refreshSession(ctx, id) {
				delete(failed, id)
			}
		}
		if len(failed) > 0 && retry == nil {
			retry = time.After(readRetryDelay)
		}
	}
}

// observeEvent queues the session an event names. It runs on the change
// poller's goroutine, so it only records the id.
func (m *Manager) observeEvent(e cdc.Event) {
	if e.Type != cdc.EventSessionCreated && e.Type != cdc.EventSessionUpdated {
		return
	}
	var payload struct {
		DeletedSessionID      string  `json:"deletedSessionId"`
		TerminalTitle         *string `json:"terminalTitle"`
		ShellTerminalHandleID *string `json:"shellTerminalHandleId"`
	}
	_ = json.Unmarshal(e.Payload, &payload)
	// This manager's own title writes emit events; they change no terminal.
	if payload.TerminalTitle != nil || payload.ShellTerminalHandleID != nil {
		return
	}
	id := domain.SessionID(e.SessionID)
	if id == "" {
		id = domain.SessionID(payload.DeletedSessionID)
	}
	if id == "" {
		return
	}
	m.mu.Lock()
	m.pending[id] = struct{}{}
	m.mu.Unlock()
	select {
	case m.poke <- struct{}{}:
	default:
	}
}

func (m *Manager) loadSessions(ctx context.Context) bool {
	sessions, err := m.store.ListAllSessions(ctx)
	if err != nil {
		m.log.Warn("terminal titles: list sessions failed; retrying", "err", err)
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range sessions {
		m.setLocked(terminalKey{session: rec.ID}, sessionTerminal(rec))
	}
	return true
}

// refreshSession reports false when the read failed and must be retried.
func (m *Manager) refreshSession(ctx context.Context, id domain.SessionID) bool {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		m.log.Debug("terminal titles: read session failed; retrying", "session", id, "err", err)
		return false
	}
	var term terminal
	if found {
		term = sessionTerminal(rec)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setLocked(terminalKey{session: id}, term)
	return true
}

// sessionTerminal is the terminal to follow for a session, or none. Chat
// sessions have no terminal program, so they never get a title.
func sessionTerminal(rec domain.SessionRecord) terminal {
	if rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || rec.Metadata.RuntimeHandleID == "" {
		return terminal{}
	}
	return terminal{handle: rec.Metadata.RuntimeHandleID, launch: rec.Metadata.RuntimeLaunchID}
}

// setLocked makes key follow term, or nothing when term has no handle. A
// watch that ended on its own stays in place so the same run is not dialed
// again; a new run replaces it.
func (m *Manager) setLocked(key terminalKey, term terminal) {
	if w, ok := m.watches[key]; ok {
		if w.term == term {
			return
		}
		m.stopLocked(w)
		delete(m.watches, key)
	}
	if term.handle == "" || m.base.Err() != nil {
		return
	}
	ctx, cancel := context.WithCancel(m.base)
	w := &watch{key: key, term: term, cancel: cancel}
	m.watches[key] = w
	go m.follow(ctx, w)
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for key, w := range m.watches {
		m.stopLocked(w)
		delete(m.watches, key)
	}
}

// follow reconnects while the host is reachable. It ends, leaving the watch
// parked, when the runtime cannot report titles for the terminal (it is gone,
// or predates titles) or the host refuses repeated connections. A host that
// was replaced reports its own title on reconnect, clearing the previous one.
func (m *Manager) follow(ctx context.Context, w *watch) {
	handle := ports.RuntimeHandle{ID: w.term.handle}
	failures := 0
	for backoff := initialWatchRetryDelay; ; backoff = min(backoff*2, watchRetryMax) {
		var connected atomic.Bool
		err := m.rt.WatchTerminalTitle(ctx, handle, func(title string) {
			connected.Store(true)
			m.observe(w, title)
		})
		if ctx.Err() != nil {
			return
		}
		if connected.Load() {
			failures, backoff = 0, initialWatchRetryDelay
		} else {
			failures++
		}
		if errors.Is(err, ports.ErrTerminalTitleUnavailable) || failures >= maxWatchFailures {
			m.log.Debug("terminal titles: watch ended", "session", w.key.session, "shell", w.key.shell, "err", err)
			m.mu.Lock()
			m.stopLocked(w)
			m.mu.Unlock()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func (m *Manager) observe(w *watch, title string) {
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
		m.scheduleLocked(w, max(0, minWriteInterval-time.Since(w.lastWrite)))
	}
}

func (m *Manager) scheduleLocked(w *watch, delay time.Duration) {
	w.timer = time.AfterFunc(delay, func() { m.flush(w) })
}

func (m *Manager) flush(w *watch) {
	m.mu.Lock()
	w.timer = nil
	if w.stopped || (w.hasSaved && w.saved == w.latest) {
		m.mu.Unlock()
		return
	}
	title := w.latest
	w.hasSaved, w.saved, w.lastWrite = true, title, time.Now()
	m.mu.Unlock()

	err := m.save(m.base, w, title)
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
		m.scheduleLocked(w, minWriteInterval)
	}
}

// save stores w's title unless a newer run of the same terminal replaced w;
// the newer run owns the title from then on. A watch that only stopped (its
// session ended or its shell closed) still stores its last title.
func (m *Manager) save(ctx context.Context, w *watch, title string) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	m.mu.Lock()
	current, ok := m.watches[w.key]
	m.mu.Unlock()
	if ok && current != w {
		return nil
	}
	if w.key.shell != "" {
		return m.store.UpdateShellTerminalAppTitle(ctx, w.key.shell, title)
	}
	return m.store.UpsertSessionTerminalTitle(ctx, w.key.session, title, time.Now().UTC())
}

// stopLocked ends a watch and stores a title still waiting on the write limit.
// The stored title stays after the terminal ends, as its last known title.
func (m *Manager) stopLocked(w *watch) {
	if w.stopped {
		return
	}
	w.cancel()
	w.stopped = true
	if w.timer != nil {
		w.timer.Stop()
		w.timer = nil
	}
	if !w.seen || (w.hasSaved && w.saved == w.latest) {
		return
	}
	title := w.latest
	w.hasSaved, w.saved = true, title
	go func() {
		flushCtx, cancel := context.WithTimeout(context.WithoutCancel(m.base), flushTimeout)
		defer cancel()
		if err := m.save(flushCtx, w, title); err != nil {
			m.log.Debug("terminal titles: final save failed", "session", w.key.session, "shell", w.key.shell, "err", err)
		}
	}()
}
