package terminaltitle

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestMain shortens the timings once: goroutines from an earlier test can
// still read them while a later test starts, so tests never write them.
func TestMain(m *testing.M) {
	minWriteInterval, readRetryDelay = 200*time.Millisecond, 10*time.Millisecond
	os.Exit(m.Run())
}

type fakeStore struct {
	mu       sync.Mutex
	sessions map[domain.SessionID]domain.SessionRecord
	gets     int
	titles   map[string][]string // key: session id or shell handle
	// listFails and getFails fail that many reads before succeeding.
	listFails, getFails int
}

func newFakeStore(recs ...domain.SessionRecord) *fakeStore {
	s := &fakeStore{sessions: map[domain.SessionID]domain.SessionRecord{}, titles: map[string][]string{}}
	for _, rec := range recs {
		s.sessions[rec.ID] = rec
	}
	return s
}

func (s *fakeStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listFails > 0 {
		s.listFails--
		return nil, errors.New("database is locked")
	}
	out := make([]domain.SessionRecord, 0, len(s.sessions))
	for _, rec := range s.sessions {
		out = append(out, rec)
	}
	return out, nil
}

func (s *fakeStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gets++
	if s.getFails > 0 {
		s.getFails--
		return domain.SessionRecord{}, false, errors.New("database is locked")
	}
	rec, ok := s.sessions[id]
	return rec, ok, nil
}

func (s *fakeStore) put(rec domain.SessionRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[rec.ID] = rec
}

func (s *fakeStore) remove(id domain.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
}

func (s *fakeStore) UpsertSessionTerminalTitle(_ context.Context, id domain.SessionID, title string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titles[string(id)] = append(s.titles[string(id)], title)
	return nil
}

func (s *fakeStore) UpdateShellTerminalAppTitle(_ context.Context, handleID, title string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.titles[handleID] = append(s.titles[handleID], title)
	return nil
}

func (s *fakeStore) written(key string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.titles[key]...)
}

func (s *fakeStore) getCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gets
}

// fakeRuntime holds a watch open per handle and lets the test push titles.
type fakeRuntime struct {
	mu          sync.Mutex
	dials       map[string]int
	live        map[string]*liveWatch
	unavailable map[string]bool
}

// liveWatch is one open watch; a pointer so a replaced watch on the same
// handle cannot remove its successor.
type liveWatch struct{ fn func(string) }

func newFakeRuntime() *fakeRuntime {
	return &fakeRuntime{dials: map[string]int{}, live: map[string]*liveWatch{}, unavailable: map[string]bool{}}
}

func (f *fakeRuntime) WatchTerminalTitle(ctx context.Context, handle ports.RuntimeHandle, fn func(string)) error {
	f.mu.Lock()
	f.dials[handle.ID]++
	if f.unavailable[handle.ID] {
		f.mu.Unlock()
		return ports.ErrTerminalTitleUnavailable
	}
	mine := &liveWatch{fn: fn}
	f.live[handle.ID] = mine
	f.mu.Unlock()
	<-ctx.Done()
	f.mu.Lock()
	if f.live[handle.ID] == mine {
		delete(f.live, handle.ID)
	}
	f.mu.Unlock()
	return ctx.Err()
}

func (f *fakeRuntime) watching(handle string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[handle] != nil
}

func (f *fakeRuntime) dialCount(handle string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.dials[handle]
}

func (f *fakeRuntime) emit(t *testing.T, handle, title string) {
	t.Helper()
	waitFor(t, "a watch on "+handle, func() bool { return f.watching(handle) })
	f.mu.Lock()
	w := f.live[handle]
	f.mu.Unlock()
	w.fn(title)
}

type fakeEvents struct {
	mu sync.Mutex
	fn func(cdc.Event)
}

func (e *fakeEvents) Subscribe(fn func(cdc.Event)) func() {
	e.mu.Lock()
	e.fn = fn
	e.mu.Unlock()
	return func() {}
}

func (e *fakeEvents) send(t *testing.T, ev cdc.Event) {
	t.Helper()
	waitFor(t, "the event subscription", func() bool {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.fn != nil
	})
	e.mu.Lock()
	fn := e.fn
	e.mu.Unlock()
	fn(ev)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(2 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func tuiSession(id, handle string) domain.SessionRecord {
	return domain.SessionRecord{ID: domain.SessionID(id), Mode: domain.SessionModeTUI, Metadata: domain.SessionMetadata{RuntimeHandleID: handle, RuntimeLaunchID: "launch-1"}}
}

func updated(id string, payload map[string]any) cdc.Event {
	raw, _ := json.Marshal(payload)
	return cdc.Event{Type: cdc.EventSessionUpdated, SessionID: id, Payload: raw}
}

func startManager(t *testing.T, store *fakeStore, unavailable ...string) (*Manager, *fakeRuntime, *fakeEvents) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	rt, events := newFakeRuntime(), &fakeEvents{}
	for _, handle := range unavailable {
		rt.unavailable[handle] = true
	}
	m := NewManager(ctx, rt, store, nil)
	go m.Run(ctx, events)
	return m, rt, events
}

func TestManagerWatchesLiveTerminalSessionsAtStart(t *testing.T) {
	terminated := tuiSession("done", "h-done")
	terminated.IsTerminated = true
	chat := tuiSession("chat", "h-chat")
	chat.Mode = domain.SessionModeChat
	_, rt, _ := startManager(t, newFakeStore(tuiSession("live", "h-live"), terminated, chat))

	waitFor(t, "the live session's watch", func() bool { return rt.watching("h-live") })
	time.Sleep(20 * time.Millisecond)
	if rt.dialCount("h-done") != 0 || rt.dialCount("h-chat") != 0 {
		t.Fatal("watched a terminated or chat session")
	}
}

func TestManagerFollowsSessionsFromChangeEvents(t *testing.T) {
	store := newFakeStore()
	_, rt, events := startManager(t, store)

	store.put(tuiSession("s1", "h1"))
	events.send(t, updated("s1", map[string]any{"id": "s1", "activity": "active"}))
	waitFor(t, "a watch after spawn", func() bool { return rt.watching("h1") })

	// Moving to another terminal replaces the watch.
	store.put(tuiSession("s1", "h2"))
	events.send(t, updated("s1", map[string]any{"id": "s1"}))
	waitFor(t, "the watch to move to the new terminal", func() bool { return rt.watching("h2") && !rt.watching("h1") })

	terminated := tuiSession("s1", "h2")
	terminated.IsTerminated = true
	store.put(terminated)
	events.send(t, updated("s1", map[string]any{"id": "s1", "isTerminated": true}))
	waitFor(t, "the watch to stop", func() bool { return !rt.watching("h2") })
}

func TestManagerStopsWatchingADeletedSession(t *testing.T) {
	store := newFakeStore(tuiSession("s1", "h1"))
	_, rt, events := startManager(t, store)
	waitFor(t, "the watch", func() bool { return rt.watching("h1") })

	store.remove("s1")
	events.send(t, updated("", map[string]any{"deletedSessionId": "s1"}))
	waitFor(t, "the watch to stop", func() bool { return !rt.watching("h1") })
}

func TestManagerIgnoresEventsFromItsOwnTitleWrites(t *testing.T) {
	store := newFakeStore(tuiSession("s1", "h1"))
	_, rt, events := startManager(t, store)
	waitFor(t, "the watch", func() bool { return rt.watching("h1") })

	events.send(t, updated("s1", map[string]any{"id": "s1", "terminalTitle": "vim"}))
	events.send(t, updated("s1", map[string]any{"id": "s1", "shellTerminalHandleId": "shell-1"}))
	time.Sleep(30 * time.Millisecond)
	if got := store.getCount(); got != 0 {
		t.Fatalf("read the session %d times for title events, want 0", got)
	}
}

func TestManagerWritesOnlyChangedTitlesAtMostOncePerInterval(t *testing.T) {
	store := newFakeStore(tuiSession("s1", "h1"))
	_, rt, _ := startManager(t, store)

	rt.emit(t, "h1", "vim a.go")
	waitFor(t, "the first write", func() bool { return len(store.written("s1")) == 1 })
	for _, title := range []string{"vim b.go", "vim c.go", "vim d.go"} {
		rt.emit(t, "h1", title)
	}
	waitFor(t, "the coalesced write", func() bool { return len(store.written("s1")) == 2 })
	rt.emit(t, "h1", "vim d.go")
	time.Sleep(3 * minWriteInterval)
	if got := store.written("s1"); len(got) != 2 || got[0] != "vim a.go" || got[1] != "vim d.go" {
		t.Fatalf("writes = %q, want the first title then only the newest", got)
	}
}

func TestManagerFollowsTrackedShellTerminals(t *testing.T) {
	store := newFakeStore()
	m, rt, _ := startManager(t, store)

	m.TrackShellTerminal("shell-1")
	rt.emit(t, "shell-1", "htop")
	waitFor(t, "the shell title write", func() bool { return len(store.written("shell-1")) == 1 })

	m.UntrackShellTerminal("shell-1")
	waitFor(t, "the shell watch to stop", func() bool { return !rt.watching("shell-1") })
}

func TestManagerDoesNotRedialAnUnavailableTerminal(t *testing.T) {
	store := newFakeStore(tuiSession("s1", "legacy"))
	_, rt, events := startManager(t, store, "legacy")
	waitFor(t, "the first dial", func() bool { return rt.dialCount("legacy") >= 1 })

	for range 5 {
		events.send(t, updated("s1", map[string]any{"id": "s1", "activity": "idle"}))
	}
	waitFor(t, "the events to be read", func() bool { return store.getCount() >= 1 })
	time.Sleep(30 * time.Millisecond)
	if got := rt.dialCount("legacy"); got != 1 {
		t.Fatalf("dialed an unavailable terminal %d times, want 1", got)
	}
}

// Restore keeps the session's handle but starts a new host; a watch that ended
// because the old host was gone must start again.
func TestManagerRestartsAnEndedWatchWhenTheSessionIsRestored(t *testing.T) {
	store := newFakeStore(tuiSession("s1", "s1"))
	_, rt, events := startManager(t, store, "s1")
	waitFor(t, "the first dial", func() bool { return rt.dialCount("s1") == 1 })

	rt.mu.Lock()
	delete(rt.unavailable, "s1")
	rt.mu.Unlock()
	restored := tuiSession("s1", "s1")
	restored.Metadata.RuntimeLaunchID = "launch-2"
	store.put(restored)
	events.send(t, updated("s1", map[string]any{"id": "s1"}))
	waitFor(t, "a watch on the restored host", func() bool { return rt.watching("s1") })
}

func TestManagerRetriesAFailedStartupRead(t *testing.T) {
	store := newFakeStore(tuiSession("s1", "h1"))
	store.listFails = 2
	_, rt, _ := startManager(t, store)
	waitFor(t, "the watch after the read succeeds", func() bool { return rt.watching("h1") })
}

func TestManagerRetriesAFailedSessionRead(t *testing.T) {
	store := newFakeStore()
	_, rt, events := startManager(t, store)
	store.put(tuiSession("s1", "h1"))
	store.mu.Lock()
	store.getFails = 2
	store.mu.Unlock()
	events.send(t, updated("s1", map[string]any{"id": "s1"}))
	waitFor(t, "the watch after the read succeeds", func() bool { return rt.watching("h1") })
}

// A restored session's new run owns its title; the old run's pending title
// must not be written after it.
func TestManagerDropsAReplacedRunsPendingTitle(t *testing.T) {
	store := newFakeStore(tuiSession("s1", "s1"))
	_, rt, events := startManager(t, store)
	rt.emit(t, "s1", "old task")
	waitFor(t, "the first write", func() bool { return len(store.written("s1")) == 1 })
	rt.emit(t, "s1", "old task, renamed") // held by the write limit

	restored := tuiSession("s1", "s1")
	restored.Metadata.RuntimeLaunchID = "launch-2"
	store.put(restored)
	events.send(t, updated("s1", map[string]any{"id": "s1"}))
	waitFor(t, "the new run's watch", func() bool { return rt.dialCount("s1") == 2 && rt.watching("s1") })
	rt.emit(t, "s1", "new task")
	waitFor(t, "the new run's write", func() bool {
		got := store.written("s1")
		return len(got) > 0 && got[len(got)-1] == "new task"
	})
	time.Sleep(3 * minWriteInterval)
	if got := store.written("s1"); len(got) != 2 || got[0] != "old task" || got[1] != "new task" {
		t.Fatalf("writes = %q, want the old run's first title then only the new run's", got)
	}
}
