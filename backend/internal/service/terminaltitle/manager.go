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

var (
	minWriteInterval       = 2 * time.Second
	initialWatchRetryDelay = 500 * time.Millisecond
	watchRetryMax          = 30 * time.Second
	maxWatchFailures       = 6
	readRetryDelay         = 5 * time.Second
)

const flushTimeout = 5 * time.Second

type Store interface {
	ListAllSessions(ctx context.Context) ([]domain.SessionRecord, error)
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
	UpsertSessionTerminalTitle(ctx context.Context, id domain.SessionID, title string, updatedAt time.Time) error
	UpdateShellTerminalAppTitle(ctx context.Context, handleID, title string) error
}

type Events interface {
	Subscribe(fn func(cdc.Event)) (unsubscribe func())
}

type terminalKey struct {
	session domain.SessionID
	shell   string
}

type terminal struct {
	handle string
	launch string
}

type watch struct {
	key    terminalKey
	term   terminal
	cancel context.CancelFunc

	stopped   bool
	seen      bool
	latest    string
	hasSaved  bool
	saved     string
	lastWrite time.Time
	timer     *time.Timer
}

type Manager struct {
	rt    ports.TerminalTitleWatcher
	store Store
	log   *slog.Logger
	base  context.Context

	writeMu sync.Mutex

	mu      sync.Mutex
	watches map[terminalKey]*watch
	pending map[domain.SessionID]struct{}
	poke    chan struct{}
}

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

func (m *Manager) TrackShellTerminal(handleID string) {
	if handleID != "" {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.setLocked(terminalKey{shell: handleID}, terminal{handle: handleID})
	}
}

func (m *Manager) UntrackShellTerminal(handleID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setLocked(terminalKey{shell: handleID}, terminal{})
}

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

func sessionTerminal(rec domain.SessionRecord) terminal {
	if rec.IsTerminated || domain.NormalizeSessionMode(rec.Mode) != domain.SessionModeTUI || rec.Metadata.RuntimeHandleID == "" {
		return terminal{}
	}
	return terminal{handle: rec.Metadata.RuntimeHandleID, launch: rec.Metadata.RuntimeLaunchID}
}

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
