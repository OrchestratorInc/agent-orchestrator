package activity

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func rolloutRecord(at time.Time, payload string) string {
	return `{"timestamp":"` + at.UTC().Format("2006-01-02T15:04:05.000Z") + `","type":"event_msg","payload":` + payload + "}\n"
}

func failedTurn(at time.Time) string {
	return rolloutRecord(at, `{"type":"task_complete","turn_id":"t","last_agent_message":null,"error":{"message":"You've hit your usage limit.","codex_error_info":"usage_limit_exceeded"}}`)
}

func succeededTurn(at time.Time) string {
	return rolloutRecord(at, `{"type":"task_complete","turn_id":"t","last_agent_message":"done"}`)
}

func startedTurn(at time.Time) string {
	return rolloutRecord(at, `{"type":"task_started","turn_id":"t"}`)
}

type turnFailureHarness struct {
	ctx      context.Context
	store    *sqlite.Store
	manager  *lifecycle.Manager
	observer *TurnFailureObserver
	session  domain.SessionRecord
	rollout  string
}

// newTurnFailureHarness runs the real chain: a Codex UserPromptSubmit hook
// signal makes the session active and records its rollout path, then the
// observer reads that rollout through the Codex adapter and writes through
// the lifecycle manager into SQLite.
func newTurnFailureHarness(t *testing.T, promptAt time.Time) *turnFailureHarness {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	workspace := t.TempDir()
	createdAt := promptAt.Add(-time.Minute)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "turnfail", Path: workspace, RegisteredAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "turnfail",
		Kind:      domain.KindWorker,
		Harness:   domain.HarnessCodex,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: createdAt},
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	session.Metadata.RuntimeHandleID = "handle-1"
	session.Metadata.RuntimeLaunchID = "launch-1"
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "sessions", "2026", "10", "18")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(dir, "rollout-2026-10-18T10-00-00-0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b.jsonl")
	if err := os.WriteFile(rollout, []byte(`{"type":"session_meta","payload":{"source":"cli"}}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := lifecycle.New(store, nil)
	h := &turnFailureHarness{
		ctx: ctx, store: store, manager: manager, session: session, rollout: rollout,
		observer: NewTurnFailureObserver(store, manager, fakeAgents{domain.HarnessCodex: codex.New()},
			TurnFailureConfig{Logger: testLogger()}),
	}
	if err := manager.ApplyActivitySignal(ctx, session.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Timestamp: promptAt, Event: "user-prompt-submit",
		LaunchID: "launch-1", AgentSessionID: "0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", TranscriptPath: rollout,
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *turnFailureHarness) appendRollout(t *testing.T, records ...string) {
	t.Helper()
	file, err := os.OpenFile(h.rollout, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.WriteString(strings.Join(records, "")); err != nil {
		t.Fatal(err)
	}
}

func (h *turnFailureHarness) poll(t *testing.T) domain.SessionRecord {
	t.Helper()
	if err := h.observer.Poll(h.ctx); err != nil {
		t.Fatal(err)
	}
	got, ok, err := h.store.GetSession(h.ctx, h.session.ID)
	if err != nil || !ok {
		t.Fatalf("GetSession: ok=%v err=%v", ok, err)
	}
	return got
}

func TestTurnFailureObserverIdlesAFailedCodexTurn(t *testing.T) {
	promptAt := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Millisecond)
	h := newTurnFailureHarness(t, promptAt)
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("running turn: state = %q, want active", got.Activity.State)
	}
	if got, _, _ := h.store.GetSession(h.ctx, h.session.ID); got.Metadata.NativeTranscriptPath != h.rollout {
		t.Fatalf("hook did not record the rollout path: %q", got.Metadata.NativeTranscriptPath)
	}

	failedAt := promptAt.Add(2 * time.Second)
	h.appendRollout(t, startedTurn(promptAt.Add(100*time.Millisecond)), failedTurn(failedAt))
	got := h.poll(t)
	if got.Activity.State != domain.ActivityIdle {
		t.Fatalf("failed turn: state = %q, want idle", got.Activity.State)
	}
	// Dated when the turn ended, so terminal input typed after the failure
	// still outranks it for a Chat switch's idle proof.
	if !got.Activity.LastActivityAt.Equal(failedAt) {
		t.Fatalf("idle LastActivityAt = %v, want the failure time %v", got.Activity.LastActivityAt, failedAt)
	}

	// The next prompt starts a new turn; the old failure is not replayed.
	nextPromptAt := failedAt.Add(5 * time.Second)
	h.appendRollout(t, startedTurn(nextPromptAt))
	if err := h.manager.ApplyActivitySignal(h.ctx, h.session.ID, ports.ActivitySignal{
		Valid: true, State: domain.ActivityActive, Timestamp: nextPromptAt, Event: "user-prompt-submit", LaunchID: "launch-1",
	}); err != nil {
		t.Fatal(err)
	}
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("next turn: state = %q, want active", got.Activity.State)
	}
}

// A resumed conversation whose last turn failed before this prompt must not
// idle the new turn, even before Codex writes the new turn's start.
func TestTurnFailureObserverDoesNotReplayAnEarlierFailure(t *testing.T) {
	promptAt := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Millisecond)
	h := newTurnFailureHarness(t, promptAt)
	h.appendRollout(t, startedTurn(promptAt.Add(-time.Hour)), failedTurn(promptAt.Add(-time.Hour+time.Second)))
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("state = %q, want active", got.Activity.State)
	}
}

func TestTurnFailureObserverLeavesSuccessfulTurnsToTheStopHook(t *testing.T) {
	promptAt := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Millisecond)
	h := newTurnFailureHarness(t, promptAt)
	h.appendRollout(t, startedTurn(promptAt.Add(100*time.Millisecond)), succeededTurn(promptAt.Add(3*time.Second)))
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("state = %q, want active (Codex's Stop hook reports a successful turn)", got.Activity.State)
	}
}

// Chat sessions report turn ends through their controller, so the store
// query that feeds the observer leaves them out.
func TestTurnFailureObserverIgnoresChatSessions(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "turnfail", Path: t.TempDir(), RegisteredAt: now}); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(t.TempDir(), "rollout-2026-10-18T10-00-00-0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b.jsonl")
	if err := os.WriteFile(rollout, []byte(startedTurn(now.Add(-2*time.Second))+failedTurn(now.Add(-time.Second))), 0o600); err != nil {
		t.Fatal(err)
	}
	ids := map[domain.SessionMode]domain.SessionID{}
	for _, mode := range []domain.SessionMode{domain.SessionModeChat, domain.SessionModeTUI} {
		session, err := store.CreateSession(ctx, domain.SessionRecord{
			ProjectID: "turnfail",
			Kind:      domain.KindWorker,
			Harness:   domain.HarnessCodex,
			Mode:      mode,
			Activity:  domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-3 * time.Second)},
			Metadata:  domain.SessionMetadata{RuntimeLaunchID: "launch-1", NativeTranscriptPath: rollout},
			CreatedAt: now.Add(-time.Minute),
			UpdatedAt: now.Add(-time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		ids[mode] = session.ID
	}
	observer := NewTurnFailureObserver(store, lifecycle.New(store, nil),
		fakeAgents{domain.HarnessCodex: codex.New()}, TurnFailureConfig{Logger: testLogger()})
	if err := observer.Poll(ctx); err != nil {
		t.Fatal(err)
	}
	for mode, want := range map[domain.SessionMode]domain.ActivityState{
		domain.SessionModeChat: domain.ActivityActive,
		domain.SessionModeTUI:  domain.ActivityIdle,
	} {
		got, ok, err := store.GetSession(ctx, ids[mode])
		if err != nil || !ok {
			t.Fatalf("GetSession(%s): ok=%v err=%v", mode, ok, err)
		}
		if got.Activity.State != want {
			t.Fatalf("%s session state = %q, want %q", mode, got.Activity.State, want)
		}
	}
}

type fakeTurnFailureSessions struct {
	rows []domain.TurnFailureCandidate
}

func (f fakeTurnFailureSessions) ListTurnFailureCandidates(_ context.Context, harness domain.AgentHarness) ([]domain.TurnFailureCandidate, error) {
	var out []domain.TurnFailureCandidate
	for _, row := range f.rows {
		if row.Harness == harness {
			out = append(out, row)
		}
	}
	return out, nil
}

// countingHandler counts log records per level.
type countingHandler struct {
	mu     sync.Mutex
	counts map[slog.Level]int
}

func (h *countingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *countingHandler) Handle(_ context.Context, record slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.counts == nil {
		h.counts = make(map[slog.Level]int)
	}
	h.counts[record.Level]++
	return nil
}

func (h *countingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

func (h *countingHandler) WithGroup(string) slog.Handler { return h }

func (h *countingHandler) count(level slog.Level) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.counts[level]
}

type erroringSink struct {
	calls int
}

func (f *erroringSink) ApplyActivitySignal(context.Context, domain.SessionID, ports.ActivitySignal) error {
	f.calls++
	return errors.New("database is locked")
}

func turnFailureCandidate(t *testing.T, now time.Time) domain.TurnFailureCandidate {
	t.Helper()
	rollout := filepath.Join(t.TempDir(), "rollout-2026-10-18T10-00-00-0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b.jsonl")
	if err := os.WriteFile(rollout, []byte(startedTurn(now.Add(-2*time.Second))+failedTurn(now.Add(-time.Second))), 0o600); err != nil {
		t.Fatal(err)
	}
	return domain.TurnFailureCandidate{
		ID:                   "ao-1",
		Harness:              domain.HarnessCodex,
		Activity:             domain.Activity{State: domain.ActivityActive, LastActivityAt: now.Add(-3 * time.Second)},
		Revision:             7,
		RuntimeLaunchID:      "launch-1",
		NativeTranscriptPath: rollout,
	}
}

// A failure the lifecycle accepted but did not act on (a running subagent
// keeps the parent active, a stale launch is fenced) is sent and logged once,
// even as later writes move the session revision. A new launch or a newer
// failed turn is reported again.
func TestTurnFailureObserverReportsAFailureOnce(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	session := turnFailureCandidate(t, now)
	sessions := fakeTurnFailureSessions{rows: []domain.TurnFailureCandidate{session}}
	sink := &fakeSink{}
	logs := &countingHandler{}
	observer := NewTurnFailureObserver(sessions, sink,
		fakeAgents{domain.HarnessCodex: codex.New()}, TurnFailureConfig{Logger: slog.New(logs)})
	poll := func() {
		t.Helper()
		if err := observer.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	for range 3 {
		poll()
		sessions.rows[0].Revision++
	}
	if len(sink.signals) != 1 {
		t.Fatalf("signals = %d, want 1", len(sink.signals))
	}
	if got := logs.count(slog.LevelInfo); got != 1 {
		t.Fatalf("info logs = %d, want 1", got)
	}
	signal := sink.signals[0]
	if !signal.Valid || signal.State != domain.ActivityIdle || signal.Event != TurnFailureEvent ||
		signal.LaunchID != "launch-1" || signal.ExpectedHarness != domain.HarnessCodex ||
		signal.ExpectedRevision == nil || *signal.ExpectedRevision != 7 {
		t.Fatalf("signal = %#v", signal)
	}

	sessions.rows[0].RuntimeLaunchID = "launch-2"
	poll()
	poll()
	if len(sink.signals) != 2 || sink.signals[1].LaunchID != "launch-2" {
		t.Fatalf("after relaunch signals = %#v, want one more for launch-2", sink.signals)
	}

	file, err := os.OpenFile(session.NativeTranscriptPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	newerAt := now.Add(time.Second)
	_, err = file.WriteString(startedTurn(now) + failedTurn(newerAt))
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	poll()
	poll()
	if len(sink.signals) != 3 || !sink.signals[2].Timestamp.Equal(newerAt) {
		t.Fatalf("after a newer failure signals = %#v, want one more dated %v", sink.signals, newerAt)
	}
	if got := logs.count(slog.LevelInfo); got != 3 {
		t.Fatalf("info logs = %d, want 3", got)
	}
}

// A report that keeps failing is retried every tick but logged once.
func TestTurnFailureObserverRateLimitsTheErrorLog(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	sink := &erroringSink{}
	logs := &countingHandler{}
	observer := NewTurnFailureObserver(fakeTurnFailureSessions{rows: []domain.TurnFailureCandidate{turnFailureCandidate(t, now)}},
		sink, fakeAgents{domain.HarnessCodex: codex.New()}, TurnFailureConfig{Logger: slog.New(logs)})
	for range 3 {
		if err := observer.Poll(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if sink.calls != 3 {
		t.Fatalf("report attempts = %d, want 3", sink.calls)
	}
	if got := logs.count(slog.LevelError); got != 1 {
		t.Fatalf("error logs = %d, want 1", got)
	}
}
