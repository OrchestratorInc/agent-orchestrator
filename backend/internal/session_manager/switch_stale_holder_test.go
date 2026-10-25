package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fixedClockManager builds a manager whose clock never moves, so a seeded
// holder's age is exact. base is both the clock's answer and the reference a
// test seeds startedAt from.
func fixedClockManager(base time.Time, t *testing.T, store *fakeStore, agent *recordingAgent) *Manager {
	t.Helper()
	return New(Deps{
		Runtime:   &fakeRuntime{},
		Agents:    singleAgent{agent: agent},
		Workspace: &fakeWorkspace{},
		Store:     store,
		Messenger: &fakeMessenger{},
		Lifecycle: &fakeLCM{store: store},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
		Clock:     func() time.Time { return base },
	})
}

func switchFixture(t *testing.T) (*fakeStore, *recordingAgent, domain.SessionRecord) {
	t.Helper()
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode = domain.HarnessClaudeCode, domain.SessionModeTUI
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.Branch, rec.Metadata.AgentSessionID, rec.Metadata.RuntimeLaunchID = "branch", "source-native", "source-launch"
	st.sessions[rec.ID] = rec
	return st, &recordingAgent{}, rec
}

// A switch refused by another operation names the holder: the operation kind
// and when it started, so the UI can offer cancel/wait instead of a bare busy.
func TestSwitchTUIProviderBusyRefusalNamesTheHolder(t *testing.T) {
	base := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	st, agent, rec := switchFixture(t)
	m := fixedClockManager(base, t, st, agent)
	m.agentOperations[rec.ID] = agentOperationHolder{kind: agentOperationExit, startedAt: base.Add(-2 * time.Minute)}

	err := m.SwitchTUIProvider(context.Background(), rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessCodex})
	var refusal *domain.ProviderSwitchError
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalBusy {
		t.Fatalf("error = %v, want a busy ProviderSwitchError", err)
	}
	if !strings.Contains(refusal.Detail, "exit") || !strings.Contains(refusal.Detail, "11:58:00") {
		t.Fatalf("busy detail = %q, want the holder kind and its started-at", refusal.Detail)
	}
	if !errors.Is(err, errAgentOperationInProgress) {
		t.Fatalf("refusal must stay in the exclusive-operation error family: %v", err)
	}
	// A live holder is named, not reaped.
	if holder := m.agentOperations[rec.ID]; holder.kind != agentOperationExit {
		t.Fatalf("live holder was disturbed: %+v", holder)
	}
}

// A holder whose lease outlived every budget its kind is given is stale: the
// switch reaps it and proceeds instead of refusing forever.
func TestSwitchTUIProviderReapsAStaleHolderAndProceeds(t *testing.T) {
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })

	base := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	st, agent, rec := switchFixture(t)
	m := fixedClockManager(base, t, st, agent)
	// A previous claude process was never released: its exit operation is
	// still on the books, but far past every legitimate budget.
	m.agentOperations[rec.ID] = agentOperationHolder{kind: agentOperationExit, startedAt: base.Add(-defaultAgentOperationStaleAfter - time.Minute)}

	if err := m.SwitchTUIProvider(context.Background(), rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessCodex, HandoffNote: "handoff"}); err != nil {
		t.Fatalf("switch over a stale holder = %v, want it to proceed", err)
	}
	if agent.lastLaunch.Prompt != "handoff" {
		t.Fatalf("target did not start from the switch: %+v", agent.lastLaunch)
	}
	if holder := m.agentOperations[rec.ID]; holder.kind != "" {
		t.Fatalf("holder after a completed switch = %+v, want the slot released", holder)
	}
}

// A live holder still refuses a begin, and a holder reaped by one caller
// cannot take out a newer operation that reused the slot.
func TestBeginAgentOperationReapsOnlyTheStaleHolderItSaw(t *testing.T) {
	base := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	st := newFakeStore()
	m := fixedClockManager(base, t, st, &recordingAgent{})
	m.operationStaleAfter = time.Minute

	// A live holder refuses with a typed, sentinel-compatible error.
	live := agentOperationHolder{kind: agentOperationRestore, startedAt: base.Add(-30 * time.Second)}
	m.agentOperations["s-1"] = live
	err := m.beginAgentOperation(context.Background(), "s-1", agentOperationKill)
	var busy *AgentOperationBusyError
	if !errors.As(err, &busy) || busy.Kind != agentOperationRestore {
		t.Fatalf("error = %v, want a busy error naming restore", err)
	}
	if !errors.Is(err, errAgentOperationInProgress) {
		t.Fatalf("busy error must unwrap to the sentinel: %v", err)
	}
	if m.agentOperations["s-1"] != live {
		t.Fatal("a live holder was reaped")
	}

	// A stale holder is reaped and the begin proceeds.
	m.agentOperations["s-2"] = agentOperationHolder{kind: agentOperationExit, startedAt: base.Add(-2 * time.Minute)}
	if err := m.beginAgentOperation(context.Background(), "s-2", agentOperationKill); err != nil {
		t.Fatalf("begin over a stale holder = %v, want it to proceed", err)
	}
	if holder := m.agentOperations["s-2"]; holder.kind != agentOperationKill {
		t.Fatalf("holder after the begin = %+v, want kill", holder)
	}
	m.endAgentOperation("s-2", agentOperationKill)

	// The reap's compare under the lock: once the stale holder is gone, a
	// second caller holding the old snapshot must not reap the new operation.
	m.agentOperations["s-3"] = agentOperationHolder{kind: agentOperationExit, startedAt: base.Add(-2 * time.Minute)}
	stale := m.agentOperations["s-3"]
	if err := m.beginAgentOperation(context.Background(), "s-3", agentOperationKill); err != nil {
		t.Fatal(err)
	}
	if m.reapStaleAgentOperation("s-3", stale) {
		t.Fatal("a reaped snapshot took out the live operation that replaced it")
	}
	if holder := m.agentOperations["s-3"]; holder.kind != agentOperationKill {
		t.Fatalf("holder = %+v, want kill kept", holder)
	}
	m.endAgentOperation("s-3", agentOperationKill)
}

func TestAgentOperationBusyDetailFallsBackWithoutAHolder(t *testing.T) {
	if detail := agentOperationBusyDetail(errAgentOperationInProgress); detail != "another session operation is in progress" {
		t.Fatalf("detail = %q, want the generic fallback", detail)
	}
	base := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	detail := agentOperationBusyDetail(&AgentOperationBusyError{Kind: agentOperationReconcile, StartedAt: base})
	if !strings.Contains(detail, "reconcile") || !strings.Contains(detail, "12:00:00") {
		t.Fatalf("detail = %q, want the holder kind and started time", detail)
	}
}
