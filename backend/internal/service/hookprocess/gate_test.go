package hookprocess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/procmem"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// The session's tmux panes lead pids 10 and 11. Its terminal Claude is 20; the
// agent started an interactive claude, 40, from a tool (via a shell, 30); the
// user started another claude, 50, in the second pane; 70 runs outside the
// session.
const processTable = `
   10     1  1000 0:00.01 -zsh
   20    10  1000 0:01.00 claude --session-id 511e68be
   30    20  1000 0:00.01 /bin/zsh -c claude
   40    30  1000 0:00.50 claude
   11     1  1000 0:00.01 -zsh
   50    11  1000 0:00.50 claude
   70     1  1000 0:00.50 claude
`

type fakeApplier struct{ applied []ports.ActivitySignal }

func (f *fakeApplier) ApplyActivitySignal(_ context.Context, _ domain.SessionID, s ports.ActivitySignal) error {
	f.applied = append(f.applied, s)
	return nil
}

type fakeSessions map[domain.SessionID]domain.SessionRecord

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f[id]
	return rec, ok, nil
}

type fakeRuntime struct {
	roots []int
	err   error
}

func (f *fakeRuntime) ProcessRootPIDs(context.Context, ports.RuntimeHandle) ([]int, error) {
	return f.roots, f.err
}

type gateHarness struct {
	gate     *Gate
	next     *fakeApplier
	sessions fakeSessions
	runtime  *fakeRuntime
	table    string
	tableErr error
	now      time.Time
}

func newGateHarness() *gateHarness {
	h := &gateHarness{
		next: &fakeApplier{},
		sessions: fakeSessions{"ao-1": {
			ID: "ao-1", Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
			Metadata: domain.SessionMetadata{RuntimeHandleID: "handle-1", RuntimeLaunchID: "launch-1"},
		}},
		runtime: &fakeRuntime{roots: []int{10, 11}},
		table:   processTable,
		now:     time.Unix(1_800_000_000, 0),
	}
	h.gate = New(h.next, h.sessions, h.runtime, func(context.Context) (*procmem.Table, error) {
		if h.tableErr != nil {
			return nil, h.tableErr
		}
		return procmem.Parse(h.table)
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.gate.now = func() time.Time { return h.now }
	return h
}

// hook reports whether a hook from pid reached the lifecycle.
func (h *gateHarness) hook(t *testing.T, pid int, mutate ...func(*ports.ActivitySignal)) bool {
	t.Helper()
	signal := ports.ActivitySignal{Valid: true, State: domain.ActivityActive, Event: "pre-tool-use", LaunchID: "launch-1", ProcessID: pid}
	for _, change := range mutate {
		change(&signal)
	}
	before := len(h.next.applied)
	if err := h.gate.ApplyActivitySignal(context.Background(), "ao-1", signal); err != nil {
		t.Fatal(err)
	}
	return len(h.next.applied) > before
}

func TestGateAppliesOnlyTheTerminalClaudeProcess(t *testing.T) {
	h := newGateHarness()
	if !h.hook(t, 20) {
		t.Fatal("the terminal's Claude was dropped")
	}
	if h.hook(t, 40) {
		t.Fatal("a claude the agent started from a tool was applied")
	}
	if h.hook(t, 50) {
		t.Fatal("a claude in another pane of the session was applied")
	}
	if h.hook(t, 70) {
		t.Fatal("a claude outside the session was applied")
	}
	// A subagent runs in the terminal's process.
	if !h.hook(t, 20, func(s *ports.ActivitySignal) { s.SubagentID = "child-1"; s.Event = "subagent-start" }) {
		t.Fatal("a subagent hook of the terminal's Claude was dropped")
	}
	// The pinned process is answered without another process table read.
	h.tableErr = errors.New("ps failed")
	if !h.hook(t, 20) || h.hook(t, 40) {
		t.Fatal("pinned and remembered processes must not need the process table")
	}
}

// After a daemon restart the first hook can come from a nested claude. The
// terminal's next hook takes over, and the nested one is dropped from then on.
func TestGateRepinsToTheOutermostClaudeProcess(t *testing.T) {
	h := newGateHarness()
	if !h.hook(t, 40) {
		t.Fatal("with nothing pinned, a claude under the session applies")
	}
	if !h.hook(t, 20) {
		t.Fatal("the terminal's Claude, an ancestor of the pinned one, was dropped")
	}
	if h.hook(t, 40) {
		t.Fatal("the nested claude still applies after the terminal's took over")
	}
}

// Once the terminal's Claude exits, a new Claude under the session is its.
func TestGatePinsANewClaudeAfterTheTerminalsExits(t *testing.T) {
	h := newGateHarness()
	h.hook(t, 20)
	if h.hook(t, 50) {
		t.Fatal("a second pane's claude applied while the terminal's runs")
	}
	h.table = strings.Replace(processTable, "   20    10", "   21    10", 1)
	h.now = h.now.Add(time.Minute)
	if !h.hook(t, 50) {
		t.Fatal("after the terminal's Claude exited, the session's next Claude was dropped")
	}
	if h.hook(t, 20) {
		t.Fatal("the exited process's late hook applied")
	}
}

func TestGatePassesHooksItCannotPlace(t *testing.T) {
	for name, change := range map[string]func(*gateHarness, *ports.ActivitySignal){
		"no process id":    func(_ *gateHarness, s *ports.ActivitySignal) { s.ProcessID = 0 },
		"chat controller":  func(_ *gateHarness, s *ports.ActivitySignal) { s.ControllerGeneration = "gen-1" },
		"other launch":     func(_ *gateHarness, s *ports.ActivitySignal) { s.LaunchID = "launch-0" },
		"no process table": func(h *gateHarness, _ *ports.ActivitySignal) { h.tableErr = errors.New("ps failed") },
		"no runtime roots": func(h *gateHarness, _ *ports.ActivitySignal) { h.runtime.roots = nil },
		"runtime error":    func(h *gateHarness, _ *ports.ActivitySignal) { h.runtime.err = errors.New("tmux down") },
		"codex session": func(h *gateHarness, _ *ports.ActivitySignal) {
			rec := h.sessions["ao-1"]
			rec.Harness = domain.HarnessCodex
			h.sessions["ao-1"] = rec
		},
		"chat session": func(h *gateHarness, _ *ports.ActivitySignal) {
			rec := h.sessions["ao-1"]
			rec.Mode = domain.SessionModeChat
			h.sessions["ao-1"] = rec
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newGateHarness()
			h.hook(t, 20)
			if !h.hook(t, 70, func(s *ports.ActivitySignal) { change(h, s) }) {
				t.Fatal("dropped a hook the gate cannot place")
			}
		})
	}
	// Nothing pinned and outside the session: as before the gate.
	if h := newGateHarness(); !h.hook(t, 70) {
		t.Fatal("dropped a hook with nothing to place it against")
	}
}

// Through the real lifecycle and store: a nested claude's hooks change
// neither the session's activity nor its transcript path.
func TestGateKeepsAnotherClaudeFromTheSession(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "gate", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "gate", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: at}, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	session.Metadata.RuntimeHandleID, session.Metadata.RuntimeLaunchID = "handle-1", "launch-1"
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	gate := New(lifecycle.New(store, nil), store, &fakeRuntime{roots: []int{10, 11}},
		func(context.Context) (*procmem.Table, error) { return procmem.Parse(processTable) }, nil)
	dir := t.TempDir()
	terminalTranscript, nestedTranscript := filepath.Join(dir, "511e68be.jsonl"), filepath.Join(dir, "0f1e2d3c.jsonl")
	for _, path := range []string{terminalTranscript, nestedTranscript} {
		if err := os.WriteFile(path, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	apply := func(pid int, state domain.ActivityState, event, native, transcript string, offset time.Duration) {
		t.Helper()
		if err := gate.ApplyActivitySignal(ctx, session.ID, ports.ActivitySignal{
			Valid: true, State: state, Event: event, LaunchID: "launch-1", ProcessID: pid,
			AgentSessionID: native, TranscriptPath: transcript, Timestamp: at.Add(offset),
		}); err != nil {
			t.Fatal(err)
		}
	}
	apply(20, domain.ActivityActive, "user-prompt-submit", "511e68be", terminalTranscript, time.Second)
	apply(40, domain.ActivityIdle, "stop", "0f1e2d3c", nestedTranscript, 2*time.Second)
	got, ok, err := store.GetSession(ctx, session.ID)
	if err != nil || !ok {
		t.Fatalf("GetSession: ok=%v err=%v", ok, err)
	}
	if got.Activity.State != domain.ActivityActive || got.Metadata.NativeTranscriptPath != terminalTranscript ||
		got.Metadata.AgentSessionID != "511e68be" {
		t.Fatalf("session = %+v / transcript %q / native %q, want the terminal's turn still active on its transcript",
			got.Activity, got.Metadata.NativeTranscriptPath, got.Metadata.AgentSessionID)
	}
}

// Through the real lifecycle and store: a Stop from another Claude process,
// even a malformed one with no prompt id, never reaches the session's native
// checkpoint evidence, so it cannot invalidate a later Chat switch. The same
// promptless Stop from the terminal's own process still fails the journal
// closed: that is the terminal itself misreporting its turn.
func TestGateKeepsForeignStopsOutOfTheNativeCheckpointEvidence(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "gate", Path: t.TempDir(), RegisteredAt: at}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "gate", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeTUI,
		Activity: domain.Activity{State: domain.ActivityIdle, LastActivityAt: at}, CreatedAt: at, UpdatedAt: at,
	})
	if err != nil {
		t.Fatal(err)
	}
	session.Metadata.RuntimeHandleID, session.Metadata.RuntimeLaunchID = "handle-1", "launch-1"
	if err := store.UpdateSession(ctx, session); err != nil {
		t.Fatal(err)
	}
	gate := New(lifecycle.New(store, nil), store, &fakeRuntime{roots: []int{10, 11}},
		func(context.Context) (*procmem.Table, error) { return procmem.Parse(processTable) }, nil)
	apply := func(pid int, signal ports.ActivitySignal, offset time.Duration) {
		t.Helper()
		signal.Valid, signal.LaunchID, signal.ProcessID, signal.Timestamp = true, "launch-1", pid, at.Add(offset)
		if err := gate.ApplyActivitySignal(ctx, session.ID, signal); err != nil {
			t.Fatal(err)
		}
	}
	evidence := func() domain.NativeCheckpointEvidence {
		t.Helper()
		got, ok, err := store.GetSession(ctx, session.ID)
		if err != nil || !ok {
			t.Fatalf("GetSession: ok=%v err=%v", ok, err)
		}
		var journal domain.NativeCheckpointEvidence
		if err := json.Unmarshal([]byte(got.Metadata.NativeCheckpointEvidence), &journal); err != nil {
			t.Fatalf("evidence %q: %v", got.Metadata.NativeCheckpointEvidence, err)
		}
		return journal
	}
	apply(20, ports.ActivitySignal{
		State: domain.ActivityActive, Event: "user-prompt-submit", AgentSessionID: "511e68be",
		ProviderTurnID: "prompt-1", SubmissionID: "submission-1", LatestUserPrompt: "run it",
	}, time.Second)
	if journal := evidence(); journal.Invalid || len(journal.Events) != 1 {
		t.Fatalf("evidence after the terminal's prompt = %+v", journal)
	}
	// A nested claude, another pane's claude, one outside the session, and a
	// process gone before its hook arrived, each posting the session's launch id
	// and nothing else, as a hand-made hook does.
	for i, pid := range []int{40, 50, 70, 999} {
		apply(pid, ports.ActivitySignal{State: domain.ActivityIdle, Event: "stop"}, time.Duration(2+i)*time.Second)
	}
	if journal := evidence(); journal.Invalid || len(journal.Events) != 1 {
		t.Fatalf("a foreign Stop changed the evidence: %+v", journal)
	}
	apply(20, ports.ActivitySignal{State: domain.ActivityIdle, Event: "stop"}, 10*time.Second)
	if journal := evidence(); !journal.Invalid {
		t.Fatalf("the terminal's own promptless Stop was accepted as a checkpoint: %+v", journal)
	}
}
