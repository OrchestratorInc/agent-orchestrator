package activity

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/lifecycle"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func claudeUserRecord(at time.Time, content string) string {
	return `{"isSidechain":false,"type":"user","message":{"role":"user","content":` + content +
		`},"timestamp":"` + at.UTC().Format("2006-01-02T15:04:05.000Z") + `"}` + "\n"
}

func claudeInterrupt(at time.Time) string {
	return claudeUserRecord(at, `[{"type":"text","text":"[Request interrupted by user for tool use]"}]`)
}

type interruptHarness struct {
	ctx        context.Context
	store      *sqlite.Store
	manager    *lifecycle.Manager
	observer   *TurnFailureObserver
	session    domain.SessionRecord
	transcript string
}

// newInterruptHarness runs the real chain for one terminal session: hook
// signals through the lifecycle manager into SQLite, and the observer reading
// the native transcript through the harness's adapter.
func newInterruptHarness(t *testing.T, harness domain.AgentHarness, transcriptName string, createdAt time.Time) *interruptHarness {
	t.Helper()
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(ctx, domain.ProjectRecord{ID: "esc", Path: t.TempDir(), RegisteredAt: createdAt}); err != nil {
		t.Fatal(err)
	}
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "esc",
		Kind:      domain.KindWorker,
		Harness:   harness,
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
	transcript := filepath.Join(t.TempDir(), transcriptName)
	if err := os.WriteFile(transcript, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	manager := lifecycle.New(store, nil)
	return &interruptHarness{
		ctx: ctx, store: store, manager: manager, session: session, transcript: transcript,
		observer: NewTurnFailureObserver(store, manager,
			fakeAgents{domain.HarnessClaudeCode: claudecode.New(), domain.HarnessCodex: codex.New()},
			TurnFailureConfig{Logger: testLogger()}),
	}
}

func (h *interruptHarness) hook(t *testing.T, signal ports.ActivitySignal) {
	t.Helper()
	signal.Valid, signal.LaunchID = true, "launch-1"
	if signal.TranscriptPath == "" {
		signal.TranscriptPath = h.transcript
	}
	if err := h.manager.ApplyActivitySignal(h.ctx, h.session.ID, signal); err != nil {
		t.Fatal(err)
	}
}

func (h *interruptHarness) write(t *testing.T, records ...string) {
	t.Helper()
	appendTranscript(t, h.transcript, records...)
}

func appendTranscript(t *testing.T, path string, records ...string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	for _, record := range records {
		if _, err := file.WriteString(record); err != nil {
			t.Fatal(err)
		}
	}
}

func (h *interruptHarness) poll(t *testing.T) domain.SessionRecord {
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

// Esc at a Claude permission prompt ends the turn but fires no hook. The
// session read blocked ("needs input") until the stale-activity observer
// proved the screen idle.
func TestTurnFailureObserverIdlesAnEscInterruptedClaudeTurn(t *testing.T) {
	promptAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	h := newInterruptHarness(t, domain.HarnessClaudeCode, "511e68be-f634-4b26-b7c7-7f4d258144c6.jsonl", promptAt.Add(-time.Minute))
	h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt, Event: "user-prompt-submit"})
	h.write(t, claudeUserRecord(promptAt, `"fix CI"`))
	h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt.Add(time.Second), Event: "pre-tool-use",
		ToolName: "Bash", ToolUseID: "toolu_1"})
	h.hook(t, ports.ActivitySignal{State: domain.ActivityBlocked, Timestamp: promptAt.Add(1100 * time.Millisecond),
		Event: "permission-request", ToolName: "Bash"})
	if got := h.poll(t); got.Activity.State != domain.ActivityBlocked {
		t.Fatalf("permission prompt: state = %q, want blocked", got.Activity.State)
	}

	escAt := promptAt.Add(5 * time.Second)
	h.write(t, claudeUserRecord(escAt, `[{"type":"tool_result","content":"rejected","is_error":true,"tool_use_id":"toolu_1"}]`), claudeInterrupt(escAt))
	got := h.poll(t)
	if got.Activity.State != domain.ActivityIdle || !got.Activity.LastActivityAt.Equal(escAt) {
		t.Fatalf("after Esc: activity = %+v, want idle at %v", got.Activity, escAt)
	}

	// The next prompt starts a turn before Claude records it; the old
	// interrupt is not replayed.
	nextAt := escAt.Add(10 * time.Second)
	h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: nextAt, Event: "user-prompt-submit"})
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("next turn: state = %q, want active", got.Activity.State)
	}
	h.write(t, claudeUserRecord(nextAt, `"try again"`))
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("next turn recorded: state = %q, want active", got.Activity.State)
	}
}

// The user re-prompts within a poll of an Esc. The prompt hook lands while
// the session still reads active, so the state and its LastActivityAt stay
// put, and a poll before Claude records the prompt sees the interrupt as the
// newest record. It belongs to the turn before the prompt and must not idle
// the new one.
func TestTurnFailureObserverKeepsATurnPromptedRightAfterAnInterruptActive(t *testing.T) {
	cases := []struct {
		name       string
		harness    domain.AgentHarness
		transcript string
		start      func(at time.Time) []string
		interrupt  func(at time.Time) string
		prompt     func(at time.Time) string
	}{
		{
			name: "claude", harness: domain.HarnessClaudeCode, transcript: "511e68be-f634-4b26-b7c7-7f4d258144c6.jsonl",
			start:     func(at time.Time) []string { return []string{claudeUserRecord(at, `"fix CI"`)} },
			interrupt: claudeInterrupt,
			prompt:    func(at time.Time) string { return claudeUserRecord(at, `"try again"`) },
		},
		{
			name: "codex", harness: domain.HarnessCodex, transcript: "rollout-2026-10-19T04-39-19-01a15274-eee2-7a60-848f-bbc06024ed54.jsonl",
			start: func(at time.Time) []string {
				return []string{`{"type":"session_meta","payload":{"source":"cli"}}` + "\n", startedTurn(at)}
			},
			interrupt: func(at time.Time) string {
				return rolloutRecord(at, `{"type":"turn_aborted","turn_id":"t","reason":"interrupted"}`)
			},
			prompt: startedTurn,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			promptAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
			h := newInterruptHarness(t, tc.harness, tc.transcript, promptAt.Add(-time.Minute))
			h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt, Event: "user-prompt-submit", LatestUserPrompt: "fix CI"})
			h.write(t, tc.start(promptAt)...)
			if got := h.poll(t); got.Activity.State != domain.ActivityActive {
				t.Fatalf("running turn: state = %q, want active", got.Activity.State)
			}

			escAt := promptAt.Add(5 * time.Second)
			h.write(t, tc.interrupt(escAt))
			nextAt := escAt.Add(time.Second)
			h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: nextAt, Event: "user-prompt-submit", LatestUserPrompt: "try again"})
			got := h.poll(t)
			if got.Activity.State != domain.ActivityActive || !got.Activity.LastActivityAt.Equal(promptAt) {
				t.Fatalf("re-prompted after Esc: activity = %+v, want active since %v", got.Activity, promptAt)
			}
			h.write(t, tc.prompt(nextAt))
			if got := h.poll(t); got.Activity.State != domain.ActivityActive {
				t.Fatalf("re-prompt recorded: state = %q, want active", got.Activity.State)
			}
		})
	}
}

// Esc pressed with no turn running (dismissing a menu or autocomplete) writes
// nothing to the transcript, so a running turn stays active.
func TestTurnFailureObserverKeepsAClaudeTurnWithoutAnInterruptActive(t *testing.T) {
	promptAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	h := newInterruptHarness(t, domain.HarnessClaudeCode, "511e68be-f634-4b26-b7c7-7f4d258144c6.jsonl", promptAt.Add(-time.Minute))
	// An earlier turn was interrupted; this one is running.
	h.write(t, claudeUserRecord(promptAt.Add(-30*time.Second), `"first"`), claudeInterrupt(promptAt.Add(-20*time.Second)))
	h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt, Event: "user-prompt-submit"})
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("state = %q, want active: the interrupt predates the current prompt", got.Activity.State)
	}
	h.write(t, claudeUserRecord(promptAt, `"second"`))
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("state = %q, want active", got.Activity.State)
	}
}

// Codex fires no Stop for an interrupted turn, and the local hooks do not
// include Codex 0.150's Interrupt hook; the rollout records turn_aborted.
func TestTurnFailureObserverIdlesAnEscInterruptedCodexTurn(t *testing.T) {
	promptAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	h := newInterruptHarness(t, domain.HarnessCodex, "rollout-2026-10-19T04-39-19-01a15274-eee2-7a60-848f-bbc06024ed54.jsonl", promptAt.Add(-time.Minute))
	h.write(t, `{"type":"session_meta","payload":{"source":"cli"}}`+"\n", startedTurn(promptAt))
	h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt, Event: "user-prompt-submit"})
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("running turn: state = %q, want active", got.Activity.State)
	}
	escAt := promptAt.Add(4 * time.Second)
	h.write(t, rolloutRecord(escAt, `{"type":"turn_aborted","turn_id":"t","reason":"interrupted"}`))
	got := h.poll(t)
	if got.Activity.State != domain.ActivityIdle || !got.Activity.LastActivityAt.Equal(escAt) {
		t.Fatalf("after Esc: activity = %+v, want idle at %v", got.Activity, escAt)
	}
}

// The interrupt reports its own event, a turn boundary that clears a pending
// permission dialog.
func TestTurnFailureObserverReportsAnInterruptEvent(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	transcript := filepath.Join(t.TempDir(), "511e68be-f634-4b26-b7c7-7f4d258144c6.jsonl")
	if err := os.WriteFile(transcript, []byte(claudeUserRecord(now.Add(-2*time.Second), `"go"`)+claudeInterrupt(now.Add(-time.Second))), 0o600); err != nil {
		t.Fatal(err)
	}
	sink := &fakeSink{}
	observer := NewTurnFailureObserver(fakeTurnFailureSessions{rows: []domain.TurnFailureCandidate{{
		ID: "ao-1", Harness: domain.HarnessClaudeCode, Revision: 3, RuntimeLaunchID: "launch-1",
		Activity:             domain.Activity{State: domain.ActivityBlocked, LastActivityAt: now.Add(-3 * time.Second)},
		NativeTranscriptPath: transcript,
	}}}, sink, fakeAgents{domain.HarnessClaudeCode: claudecode.New()}, TurnFailureConfig{Logger: testLogger()})
	if err := observer.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sink.signals) != 1 {
		t.Fatalf("signals = %#v, want one", sink.signals)
	}
	if signal := sink.signals[0]; !signal.Valid || signal.State != domain.ActivityIdle || signal.Event != TurnInterruptEvent ||
		!signal.Timestamp.Equal(now.Add(-time.Second)) || signal.ExpectedHarness != domain.HarnessClaudeCode {
		t.Fatalf("signal = %#v", signal)
	}
}

// Esc while a foreground Task subagent runs: Claude Code aborts the child and
// its runAgent cleanup fires SubagentStop for it ("SubagentStop on interrupted
// query", in the 2.1.287 and 2.1.302 bundles), then records the parent's
// interrupt marker. Whichever reaches AO first, the session ends idle: a
// running child keeps the interrupt's idle active only until its stop lands.
func TestTurnFailureObserverIdlesAClaudeTurnInterruptedDuringASubagent(t *testing.T) {
	for _, stopFirst := range []bool{false, true} {
		name := "interrupt observed first"
		if stopFirst {
			name = "subagent stop first"
		}
		t.Run(name, func(t *testing.T) {
			promptAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
			h := newInterruptHarness(t, domain.HarnessClaudeCode, "511e68be-f634-4b26-b7c7-7f4d258144c6.jsonl", promptAt.Add(-time.Minute))
			h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt, Event: "user-prompt-submit", AgentSessionID: "native-1"})
			h.write(t, claudeUserRecord(promptAt, `"research it"`))
			h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt.Add(time.Second), Event: "pre-tool-use",
				ToolName: "Agent", ToolUseID: "toolu_agent", AgentSessionID: "native-1"})
			h.hook(t, ports.ActivitySignal{Timestamp: promptAt.Add(2 * time.Second), Event: "subagent-start", SubagentID: "child-1", AgentSessionID: "native-1"})
			h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt.Add(3 * time.Second), Event: "pre-tool-use",
				SubagentID: "child-1", ToolName: "Bash", ToolUseID: "toolu_child", AgentSessionID: "native-1"})

			escAt := promptAt.Add(10 * time.Second)
			subagentStop := func() {
				h.hook(t, ports.ActivitySignal{Timestamp: escAt, Event: "subagent-stop", SubagentID: "child-1", AgentSessionID: "native-1"})
			}
			if stopFirst {
				subagentStop()
				if got := h.poll(t); got.Activity.State != domain.ActivityActive {
					t.Fatalf("child stopped, parent turn running: state = %q, want active", got.Activity.State)
				}
			}
			h.write(t, claudeUserRecord(escAt, `[{"type":"tool_result","content":"[Request interrupted by user for tool use]","is_error":true,"tool_use_id":"toolu_agent"}]`),
				claudeInterrupt(escAt))
			got := h.poll(t)
			if !stopFirst {
				if got.Activity.State != domain.ActivityActive {
					t.Fatalf("interrupt with the child's stop in flight: state = %q, want active", got.Activity.State)
				}
				subagentStop()
				got = h.poll(t)
			}
			if got.Activity.State != domain.ActivityIdle {
				t.Fatalf("after Esc: state = %q, want idle", got.Activity.State)
			}
		})
	}
}

// /clear (or /resume, or a conversation switch) inside the TUI moves Claude to
// another transcript without a restart. The next hook carries the new session
// id and transcript path, so the observer reads the new conversation, and the
// one left behind no longer counts.
func TestTurnFailureObserverFollowsAClaudeConversationSwitch(t *testing.T) {
	promptAt := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	h := newInterruptHarness(t, domain.HarnessClaudeCode, "511e68be-f634-4b26-b7c7-7f4d258144c6.jsonl", promptAt.Add(-time.Minute))
	h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: promptAt, Event: "user-prompt-submit",
		AgentSessionID: "511e68be-f634-4b26-b7c7-7f4d258144c6", LatestUserPrompt: "first"})
	h.write(t, claudeUserRecord(promptAt, `"first"`))
	h.hook(t, ports.ActivitySignal{State: domain.ActivityIdle, Timestamp: promptAt.Add(time.Second), Event: "stop",
		AgentSessionID: "511e68be-f634-4b26-b7c7-7f4d258144c6"})

	// /clear, then a prompt in the new conversation.
	cleared := filepath.Join(filepath.Dir(h.transcript), "7d0c1b2a-3948-4576-8a9b-0c1d2e3f4a5b.jsonl")
	nextAt := promptAt.Add(10 * time.Second)
	h.hook(t, ports.ActivitySignal{State: domain.ActivityActive, Timestamp: nextAt, Event: "user-prompt-submit",
		AgentSessionID: "7d0c1b2a-3948-4576-8a9b-0c1d2e3f4a5b", TranscriptPath: cleared, LatestUserPrompt: "second"})
	appendTranscript(t, cleared, claudeUserRecord(nextAt, `"second"`))
	got := h.poll(t)
	if got.Activity.State != domain.ActivityActive || got.Metadata.NativeTranscriptPath != cleared {
		t.Fatalf("after the switch: state %q transcript %q, want active on %q", got.Activity.State, got.Metadata.NativeTranscriptPath, cleared)
	}
	// The conversation left behind no longer counts.
	h.write(t, claudeInterrupt(nextAt.Add(time.Second)))
	if got := h.poll(t); got.Activity.State != domain.ActivityActive {
		t.Fatalf("the left conversation's interrupt idled the session: %q", got.Activity.State)
	}
	escAt := nextAt.Add(2 * time.Second)
	appendTranscript(t, cleared, claudeInterrupt(escAt))
	if got := h.poll(t); got.Activity.State != domain.ActivityIdle || !got.Activity.LastActivityAt.Equal(escAt) {
		t.Fatalf("after Esc: activity = %+v, want idle at %v", got.Activity, escAt)
	}
}
