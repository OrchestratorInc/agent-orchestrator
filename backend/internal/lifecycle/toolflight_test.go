package lifecycle

import (
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func sig(state domain.ActivityState, event, toolName, toolUseID string) ports.ActivitySignal {
	return ports.ActivitySignal{Valid: true, State: state, Event: event, ToolName: toolName, ToolUseID: toolUseID}
}

func seedSignaled(st *fakeStore, id domain.SessionID, state domain.ActivityState) {
	st.sessions[id] = domain.SessionRecord{
		ID: id, ProjectID: "mer",
		Activity:      domain.Activity{State: state, LastActivityAt: time.Now()},
		FirstSignalAt: time.Now(),
	}
}

func mustApply(t *testing.T, m *Manager, id domain.SessionID, s ports.ActivitySignal) {
	t.Helper()
	if err := m.ApplyActivitySignal(ctx, id, s); err != nil {
		t.Fatal(err)
	}
}

func stateOf(st *fakeStore, id domain.SessionID) domain.ActivityState {
	return st.sessions[id].Activity.State
}

func blockOnDialog(t *testing.T, m *Manager, st *fakeStore, id domain.SessionID, toolName, toolUseID string) {
	t.Helper()
	mustApply(t, m, id, sig(domain.ActivityActive, "pre-tool-use", toolName, toolUseID))
	mustApply(t, m, id, sig(domain.ActivityBlocked, "permission-request", toolName, ""))
	if got := stateOf(st, id); got != domain.ActivityBlocked {
		t.Fatalf("setup: state = %q, want blocked", got)
	}
}

func blockOnNotificationCorrelation(t *testing.T, m *Manager, st *fakeStore, id domain.SessionID, toolName, toolUseID string) {
	t.Helper()
	mustApply(t, m, id, sig(domain.ActivityActive, "pre-tool-use", toolName, toolUseID))
	mustApply(t, m, id, ports.ActivitySignal{
		Valid: true, State: domain.ActivityBlocked, Event: "notification",
		ToolName: strings.ToLower(toolName), ToolUseID: toolUseID,
	})
	if got := stateOf(st, id); got != domain.ActivityBlocked {
		t.Fatalf("setup: state = %q, want blocked", got)
	}
}

func TestToolPrecedence_ApprovedToolPostClearsBlocked(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after approved tool's post = %q, want active", got)
	}
}

func TestToolPrecedence_ApprovedToolFailurePostAlsoClears(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use-failure", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after approved tool's failure post = %q, want active", got)
	}
}

func TestToolPrecedence_CursorMatchingAfterClearsBlocked(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-shell-execution", "git status", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "after-shell-execution", "git status", ""))

	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after matching Cursor shell completion = %q, want active", got)
	}
}

func TestToolPrecedence_CursorUnrelatedAfterDoesNotClearBlocked(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-shell-execution", "git push", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "after-mcp-execution", "search", ""))

	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after unrelated Cursor completion = %q, want blocked", got)
	}
}

func TestToolPrecedence_CursorConcurrentDialogsRemainBlockedUntilAllComplete(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-shell-execution", "git push", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-mcp-execution", "deploy", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "after-shell-execution", "git push", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after only one Cursor dialog completed = %q, want blocked", got)
	}

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "after-mcp-execution", "deploy", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after all Cursor dialogs completed = %q, want active", got)
	}
}

func TestToolPrecedence_CursorTerminalFailureClearsOnlyMatchingPendingRequest(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-shell-execution", "git push", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-mcp-execution", "deploy", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "cursor-shell-terminal-failure", "git push", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after one Cursor denial = %q, want blocked while MCP dialog remains", got)
	}

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "after-mcp-execution", "deploy", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after final Cursor dialog completed = %q, want active", got)
	}
}

func TestToolPrecedence_CursorSameKeyDialogsDecrementOneAtATime(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)

	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-shell-execution", "git push", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "before-shell-execution", "git push", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "cursor-shell-terminal-failure", "git push", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after one same-key Cursor dialog completed = %q, want blocked", got)
	}

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "after-shell-execution", "git push", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after both same-key Cursor dialogs completed = %q, want active", got)
	}
}

func TestToolPrecedence_LegacyKimchiFailurePostAlsoClears(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use-fail", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after legacy failure post = %q, want active", got)
	}
}

func TestToolPrecedence_SubagentTrafficDoesNotClearBlocked(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Read", "toolu_sub"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after subagent pre = %q, want blocked", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Read", "toolu_sub"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after subagent post = %q, want blocked", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after approved post = %q, want active", got)
	}
}

func TestToolPrecedence_TurnBoundariesClearBlocked(t *testing.T) {
	cases := []struct {
		name string
		s    ports.ActivitySignal
		want domain.ActivityState
	}{
		{"user-prompt-submit", sig(domain.ActivityActive, "user-prompt-submit", "", ""), domain.ActivityActive},
		{"stop", sig(domain.ActivityIdle, "stop", "", ""), domain.ActivityIdle},
		{"stop-failure", sig(domain.ActivityIdle, "stop-failure", "", ""), domain.ActivityIdle},
		{"interrupt", sig(domain.ActivityIdle, "interrupt", "", ""), domain.ActivityIdle},
		{"session-end", sig(domain.ActivityExited, "session-end", "", ""), domain.ActivityExited},
		{"chat-controller-stopped", sig(domain.ActivityExited, "chat.controller.stopped", "", ""), domain.ActivityExited},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _ := newManager()
			seedSignaled(st, "mer-1", domain.ActivityActive)
			blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")
			mustApply(t, m, "mer-1", tc.s)
			if got := stateOf(st, "mer-1"); got != tc.want {
				t.Fatalf("state = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestToolPrecedence_NotificationSubtypesDoNotClearBlocked(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityIdle, "notification", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after notification idle = %q, want blocked", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityWaitingInput, "notification", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after notification waiting_input = %q, want blocked", got)
	}
}

func TestToolPrecedence_IdentitylessBlockedDuplicatePreservesCandidate(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "notification", "", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after duplicate notification and approved post = %q, want active", got)
	}
}

func TestToolPrecedence_NoCandidatesFailsSafe(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "permission-request", "", ""))

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after uncorrelated post = %q, want blocked (fail safe)", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityIdle, "stop", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityIdle {
		t.Fatalf("state after stop = %q, want idle", got)
	}
}

func TestToolPrecedence_DefinitivePermissionResolutionClearsBlocked(t *testing.T) {
	for _, want := range []domain.ActivityState{domain.ActivityActive, domain.ActivityIdle, domain.ActivityWaitingInput} {
		t.Run(string(want), func(t *testing.T) {
			m, st, _ := newManager()
			seedSignaled(st, "mer-1", domain.ActivityActive)
			mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "permission-blocked", "", ""))
			if _, tracked := m.flights["mer-1"]; !tracked {
				t.Fatal("blocked modal did not create a tool flight")
			}
			mustApply(t, m, "mer-1", sig(want, "permission-resolved", "", ""))
			if got := stateOf(st, "mer-1"); got != want {
				t.Fatalf("state after definitive permission resolution = %q, want %q", got, want)
			}
			if _, tracked := m.flights["mer-1"]; tracked {
				t.Fatalf("permission-resolved with state %q left blocked tool flight behind", want)
			}
		})
	}
}

func TestToolPrecedence_LegacySignalsKeepLastWriterWins(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", ports.ActivitySignal{Valid: true, State: domain.ActivityActive})
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("legacy active over blocked = %q, want active (last-writer-wins preserved)", got)
	}
}

func TestToolPrecedence_ToolEventsDoNotDemoteWaitingInput(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityWaitingInput)

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Read", "toolu_bg"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after background pre = %q, want waiting_input", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Read", "toolu_bg"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityWaitingInput {
		t.Fatalf("state after background post = %q, want waiting_input", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "user-prompt-submit", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after user prompt = %q, want active", got)
	}
}

func TestToolPrecedence_AmbiguousSameNameBlockFailsClosed(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_1"))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_2"))
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "permission-request", "Bash", ""))

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_2"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after ambiguous sibling post = %q, want blocked (fail closed)", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("state after second ambiguous post = %q, want blocked (fail closed)", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityIdle, "stop", "", ""))
	if got := stateOf(st, "mer-1"); got != domain.ActivityIdle {
		t.Fatalf("state after stop = %q, want idle", got)
	}
}

func TestToolPrecedence_SequentialDialogsResetAmbiguity(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_1"))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_2"))
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "permission-request", "Bash", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_2"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("dialog 1 ambiguous: state = %q, want blocked", got)
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_3"))
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "permission-request", "Bash", ""))
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_3"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("dialog 2 unique: state = %q, want active (ambiguity must reset)", got)
	}
}

func TestToolPrecedence_UniqueSameNameToolClears(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_1"))
	mustApply(t, m, "mer-1", sig(domain.ActivityBlocked, "permission-request", "Bash", ""))

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("state after unique tool's post = %q, want active", got)
	}
}

func TestToolPrecedence_ReaperTerminationReleasesFlight(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID: "mer-1", ProjectID: "mer",
		Activity:      domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now().Add(-2 * time.Minute)},
		FirstSignalAt: time.Now(),
	}
	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "pre-tool-use", "Bash", "toolu_1"))
	if _, ok := m.flights["mer-1"]; !ok {
		t.Fatal("setup: expected a flights entry after pre-tool-use")
	}
	if err := m.ApplyRuntimeObservation(ctx, "mer-1", ports.RuntimeFacts{Runtime: ports.ProbeDead, Workload: ports.ProbeFailed}); err != nil {
		t.Fatal(err)
	}
	if !st.sessions["mer-1"].IsTerminated {
		t.Fatal("reaper did not terminate the session")
	}
	if _, ok := m.flights["mer-1"]; ok {
		t.Fatal("flights entry leaked after reaper termination")
	}
}

func TestToolPrecedence_SuppressedSignalEmitsNoNotification(t *testing.T) {
	st := newFakeStore()
	sink := &fakeNotificationSink{}
	m := New(st, nil, WithNotificationSink(sink))
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnDialog(t, m, st, "mer-1", "Bash", "toolu_1")
	entered := len(sink.intents)

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Read", "toolu_sub"))
	if len(sink.intents) != entered {
		t.Fatalf("suppressed signal emitted a notification: %d -> %d", entered, len(sink.intents))
	}
}

func TestToolPrecedence_ToolUseIDMatchBridgesCasingMismatch(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnNotificationCorrelation(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("after post-tool-use: state = %q, want active", got)
	}
}

func TestToolPrecedence_NotificationCorrelationFailurePostClearsBlocked(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnNotificationCorrelation(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use-failure", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("after post-tool-use-failure: state = %q, want active (blocked cleared)", got)
	}
}

func TestToolPrecedence_NotificationCorrelationSiblingDoesNotClear(t *testing.T) {
	m, st, _ := newManager()
	seedSignaled(st, "mer-1", domain.ActivityActive)
	blockOnNotificationCorrelation(t, m, st, "mer-1", "Bash", "toolu_1")

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use", "Read", "toolu_sibling"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityBlocked {
		t.Fatalf("after sibling post: state = %q, want blocked (sibling must not clear)", got)
	}

	mustApply(t, m, "mer-1", sig(domain.ActivityActive, "post-tool-use-failure", "Bash", "toolu_1"))
	if got := stateOf(st, "mer-1"); got != domain.ActivityActive {
		t.Fatalf("after approved failure post: state = %q, want active", got)
	}
}
