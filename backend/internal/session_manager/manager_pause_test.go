package sessionmanager

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// turnEndingStore reports the session active for the first few reads, then
// idle with a fresh activity timestamp, modelling an agent finishing its turn
// while a drained pause waits.
type turnEndingStore struct {
	*transitionStore
	reads     atomic.Int32
	activeFor int32
	idleAfter time.Time
}

func (s *turnEndingStore) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok, err := s.transitionStore.GetSession(ctx, id)
	if err != nil || !ok || id != "session-1" {
		return rec, ok, err
	}
	switch {
	case s.reads.Add(1) <= s.activeFor:
		rec.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: s.idleAfter.Add(-time.Minute)}
	case rec.Activity.State != domain.ActivityExited:
		// Turn over; the lifecycle fake writes exited once the controller stops.
		rec.Activity = domain.Activity{State: domain.ActivityIdle, LastActivityAt: s.idleAfter}
	}
	return rec, true, nil
}

func newPauseManager(t *testing.T, activeFor int32) (*Manager, *turnEndingStore, *transitionRuntime, *[]string) {
	t.Helper()
	manager, base, runtime, _, log := newTransitionManager(t, domain.SessionModeTUI)
	useFastInterfaceTransitionTimings(manager)
	store := &turnEndingStore{transitionStore: base, activeFor: activeFor, idleAfter: time.Now().Add(time.Second)}
	manager.store = store
	runtime.aliveByHandle = map[string]bool{"runtime-1": true}
	manager.SetTerminalInputGate(&transitionInputGate{
		acquired:    make(chan string, 1),
		released:    make(chan string, 1),
		lastInputAt: time.Now(),
	})
	return manager, store, runtime, log
}

func TestExitAgentDrainWaitsForTheTurnToEnd(t *testing.T) {
	manager, store, runtime, log := newPauseManager(t, 3)

	got, err := manager.ExitAgent(context.Background(), "session-1", ExitAgentOptions{Policy: domain.SessionInterfaceTransitionDrain})
	if err != nil {
		t.Fatalf("ExitAgent: %v", err)
	}
	if store.reads.Load() <= 3 {
		t.Fatalf("drain stopped the controller after %d reads, before the turn ended", store.reads.Load())
	}
	if len(runtime.destroyedIDs) != 1 || runtime.destroyedIDs[0] != "runtime-1" {
		t.Fatalf("runtime teardown = %v", runtime.destroyedIDs)
	}
	for _, entry := range *log {
		if entry == "interrupt:tui:runtime-1" {
			t.Fatal("drain must not send an interrupt")
		}
	}
	if got.Activity.State != domain.ActivityExited {
		t.Fatalf("session after drained pause = %+v, want exited", got.Activity)
	}
}

func TestExitAgentInterruptStopsNow(t *testing.T) {
	manager, store, runtime, log := newPauseManager(t, 3)

	_, err := manager.ExitAgent(context.Background(), "session-1", ExitAgentOptions{Policy: domain.SessionInterfaceTransitionInterrupt})
	if err != nil {
		t.Fatalf("ExitAgent: %v", err)
	}
	interrupted := false
	for _, entry := range *log {
		if entry == "interrupt:tui:runtime-1" {
			interrupted = true
		}
	}
	if !interrupted {
		t.Fatal("interrupt policy did not send Ctrl-C")
	}
	if len(runtime.destroyedIDs) != 1 {
		t.Fatalf("runtime teardown = %v", runtime.destroyedIDs)
	}
	if store.reads.Load() == 0 {
		t.Fatal("interrupt never observed the session settle")
	}
}

func TestExitAgentDrainReportsBlockedWhenAgentWaitsOnADecision(t *testing.T) {
	manager, base, runtime, _, _ := newTransitionManager(t, domain.SessionModeTUI)
	useFastInterfaceTransitionTimings(manager)
	rec := base.sessions["session-1"]
	rec.Activity = domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now()}
	base.sessions["session-1"] = rec
	runtime.aliveByHandle = map[string]bool{"runtime-1": true}
	manager.SetTerminalInputGate(&transitionInputGate{acquired: make(chan string, 1), released: make(chan string, 1), lastInputAt: time.Now()})
	// The turn never ends: it flips to waiting for input instead.
	go func() {
		time.Sleep(10 * time.Millisecond)
		base.mu.Lock()
		waiting := base.sessions["session-1"]
		waiting.Activity = domain.Activity{State: domain.ActivityWaitingInput, LastActivityAt: time.Now()}
		base.sessions["session-1"] = waiting
		base.mu.Unlock()
	}()

	_, err := manager.ExitAgent(context.Background(), "session-1", ExitAgentOptions{Policy: domain.SessionInterfaceTransitionDrain})
	if !errors.Is(err, ErrAgentPauseDrainBlocked) {
		t.Fatalf("err = %v, want ErrAgentPauseDrainBlocked", err)
	}
	if len(runtime.destroyedIDs) != 0 {
		t.Fatalf("a blocked drain must leave the controller running, got teardown %v", runtime.destroyedIDs)
	}
}

func TestReconcileLive_LeavesPausedAgentStopped(t *testing.T) {
	st := newFakeStore()
	st.projects["p1"] = domain.ProjectRecord{ID: "p1", Config: testRoleAgents()}
	rt := &fakeRuntime{aliveByHandle: map[string]bool{}} // dead on purpose: the agent was paused
	ws := &fakeWorkspace{stashRef: "refs/ao/preserved/s1"}
	lcm := &fakeLCM{store: st}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{Runtime: rt, Agents: fakeAgents{}, Workspace: ws, Store: st, Messenger: &fakeMessenger{}, Lifecycle: lcm, LookPath: lookPath})

	pausedAt := time.Unix(1000, 0)
	rec := domain.SessionRecord{
		ID:          "s1",
		ProjectID:   "p1",
		Harness:     domain.HarnessClaudeCode,
		Activity:    domain.Activity{State: domain.ActivityExited},
		PausedAt:    &pausedAt,
		PauseReason: domain.SessionPauseUser,
		Metadata: domain.SessionMetadata{
			Branch: "ao/s1/root", WorkspacePath: "/wt/s1", RuntimeHandleID: "s1", AgentSessionID: "agent-s1",
		},
	}
	st.sessions[rec.ID] = rec

	if err := m.reconcileLive(context.Background(), rec); err != nil {
		t.Fatalf("reconcileLive: %v", err)
	}
	if rt.created != 0 {
		t.Fatalf("a paused agent was relaunched on boot: Create calls = %d", rt.created)
	}
	if lcm.terminated["s1"] != 0 {
		t.Fatalf("a paused agent was terminated on boot: MarkTerminated = %d", lcm.terminated["s1"])
	}
	after := st.sessions["s1"]
	if after.PausedAt == nil || after.IsTerminated || after.Activity.State != domain.ActivityExited {
		t.Fatalf("paused session after reconcile = %+v", after)
	}
	for _, c := range ws.calls {
		if c == "ForceDestroy:s1" {
			t.Fatalf("worktree destroyed for a paused session; calls = %v", ws.calls)
		}
	}
}
