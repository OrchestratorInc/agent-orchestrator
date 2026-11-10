package sessionmanager

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func asyncTerminalConfig(prompt string) ports.SpawnConfig {
	return ports.SpawnConfig{
		ProjectID:     "mer",
		Kind:          domain.KindWorker,
		Prompt:        prompt,
		RequestedMode: domain.SessionModeTUI,
		Async:         true,
	}
}

// newAsyncTerminalManager has an agent that takes its prompt after start, the
// path where "ready" has to wait for prompt delivery, not just the runtime.
func newAsyncTerminalManager(rt runtimeController, msg *fakeMessenger) (*Manager, *fakeStore, *fakeWorkspace, *[]domain.SessionProvisionState) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	ws := &fakeWorkspace{}
	m := New(Deps{
		Runtime:   rt,
		Agents:    singleAgent{agent: afterStartAgent{recordingAgent: &recordingAgent{}}},
		Workspace: ws,
		Store:     st,
		Messenger: msg,
		Lifecycle: &fakeLCM{store: st},
		LookPath:  func(string) (string, error) { return "/bin/true", nil },
	})
	// The service turns these writes into ao.sessions.spawned or spawn_failed,
	// so each start must write provisioning once and then settle exactly once.
	states := &[]domain.SessionProvisionState{}
	m.SetProvisionObserver(func(_ context.Context, _ domain.SessionRecord, state domain.SessionProvisionState, _ error) {
		*states = append(*states, state)
	})
	return m, st, ws, states
}

func wantStates(t *testing.T, got *[]domain.SessionProvisionState, want ...domain.SessionProvisionState) {
	t.Helper()
	if !slices.Equal(*got, want) {
		t.Fatalf("provision writes = %v, want %v", *got, want)
	}
}

// A large repository makes the worktree, Setup and agent start slow. With
// async the caller gets a starting session before any of that runs; the
// session reads ready only once the agent holds its opening prompt.
func TestSpawnAsyncTerminal_AnswersBeforeWorkspaceAndAgent(t *testing.T) {
	rt := &fakeRuntime{}
	msg := &fakeMessenger{}
	m, st, ws, states := newAsyncTerminalManager(rt, msg)
	deferred := deferredBackground(m)

	rec, _, _, err := m.Spawn(ctx, asyncTerminalConfig("fix the button"))
	if err != nil {
		t.Fatalf("spawn: %v", err)
	}
	if rec.Mode != domain.SessionModeTUI || rec.ProvisionState != domain.SessionProvisionProvisioning {
		t.Fatalf("answered %q/%q, want a starting Terminal session", rec.Mode, rec.ProvisionState)
	}
	if ws.createCount != 0 || rt.created != 0 || len(msg.msgs) != 0 {
		t.Fatalf("before the answer: worktrees=%d runtimes=%d prompts=%v, want none", ws.createCount, rt.created, msg.msgs)
	}
	// The row keeps the opening prompt so a failed start can be retried.
	if got := st.sessions[rec.ID].Metadata.Prompt; got != "fix the button" {
		t.Fatalf("stored prompt = %q", got)
	}
	if len(*deferred) != 1 {
		t.Fatalf("background work = %d, want 1", len(*deferred))
	}
	(*deferred)[0]()

	stored := st.sessions[rec.ID]
	if ws.createCount != 1 || rt.created != 1 {
		t.Fatalf("worktrees=%d runtimes=%d, want 1/1", ws.createCount, rt.created)
	}
	if !slices.Equal(msg.msgs, []string{"fix the button"}) {
		t.Fatalf("delivered prompts = %v, want the opening prompt once", msg.msgs)
	}
	if stored.ProvisionState != domain.SessionProvisionReady || stored.Metadata.RuntimeHandleID != "h1" || stored.IsTerminated {
		t.Fatalf("after start: state=%q handle=%q terminated=%t", stored.ProvisionState, stored.Metadata.RuntimeHandleID, stored.IsTerminated)
	}
	if got := runningProvisionStep(stored.ProvisionSteps); got != "" {
		t.Fatalf("step still running after ready: %q", got)
	}
	wantStates(t, states, domain.SessionProvisionProvisioning, domain.SessionProvisionReady)
}

// Without async the contract is unchanged: the request does all the work and
// answers with a live agent that has its prompt, and no start is reported.
func TestSpawnTerminalWithoutAsyncStillStartsBeforeAnswering(t *testing.T) {
	rt := &fakeRuntime{}
	msg := &fakeMessenger{}
	m, _, ws, states := newAsyncTerminalManager(rt, msg)
	deferred := deferredBackground(m)
	cfg := asyncTerminalConfig("fix the button")
	cfg.Async = false

	rec, _, _, err := m.Spawn(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ProvisionState.WithDefault() != domain.SessionProvisionReady || rec.Metadata.RuntimeHandleID == "" {
		t.Fatalf("answered %+v, want a ready session with a runtime", rec)
	}
	if ws.createCount != 1 || rt.created != 1 || len(msg.msgs) != 1 || len(*deferred) != 0 {
		t.Fatalf("worktrees=%d runtimes=%d prompts=%d background=%d", ws.createCount, rt.created, len(msg.msgs), len(*deferred))
	}
	wantStates(t, states)
}

func TestSpawnAsyncTerminal_WorktreeFailureKeepsSessionAndReason(t *testing.T) {
	rt := &fakeRuntime{}
	msg := &fakeMessenger{}
	m, st, ws, states := newAsyncTerminalManager(rt, msg)
	deferred := deferredBackground(m)
	ws.createErr = errors.New("branch already checked out")

	rec, _, _, err := m.Spawn(ctx, asyncTerminalConfig("fix the button"))
	if err != nil {
		t.Fatal(err)
	}
	(*deferred)[0]()

	stored, ok := st.sessions[rec.ID]
	if !ok || stored.IsTerminated || stored.ProvisionState != domain.SessionProvisionFailed {
		t.Fatalf("after failed worktree: %+v, want the session kept and failed", stored)
	}
	if !strings.Contains(stored.ProvisionError, "branch already checked out") {
		t.Fatalf("provision error = %q", stored.ProvisionError)
	}
	if rt.created != 0 || len(msg.msgs) != 0 {
		t.Fatal("agent started despite the worktree failing")
	}
	wantStates(t, states, domain.SessionProvisionProvisioning, domain.SessionProvisionFailed)
}

func TestSpawnAsyncTerminal_AgentStartFailureKeepsWorktree(t *testing.T) {
	rt := &fakeRuntime{createErr: errors.New("runtime refused the session")}
	msg := &fakeMessenger{}
	m, st, ws, states := newAsyncTerminalManager(rt, msg)
	deferred := deferredBackground(m)

	rec, _, _, err := m.Spawn(ctx, asyncTerminalConfig("fix the button"))
	if err != nil {
		t.Fatal(err)
	}
	(*deferred)[0]()

	stored := st.sessions[rec.ID]
	if stored.ProvisionState != domain.SessionProvisionFailed || !strings.Contains(stored.ProvisionError, "runtime refused the session") {
		t.Fatalf("after failed agent start: %q %q", stored.ProvisionState, stored.ProvisionError)
	}
	if ws.destroyed != 0 || stored.Metadata.WorkspacePath == "" {
		t.Fatalf("worktree destroyed=%d path=%q, want it kept for a retry", ws.destroyed, stored.Metadata.WorkspacePath)
	}
	if len(msg.msgs) != 0 || stored.IsTerminated {
		t.Fatalf("prompts=%v terminated=%t", msg.msgs, stored.IsTerminated)
	}
	if got := runningProvisionStep(stored.ProvisionSteps); got != domain.SessionProvisionStepAgent {
		t.Fatalf("failed step = %q, want agent", got)
	}
	wantStates(t, states, domain.SessionProvisionProvisioning, domain.SessionProvisionFailed)
}

// A started agent that never took its prompt is not a usable session: the
// start fails, the agent is stopped, and the worktree stays.
func TestSpawnAsyncTerminal_PromptFailureStopsAgentAndKeepsWorktree(t *testing.T) {
	rt := &fakeRuntime{}
	msg := &fakeMessenger{err: errors.New("pane unavailable")}
	m, st, ws, states := newAsyncTerminalManager(rt, msg)
	deferred := deferredBackground(m)

	rec, _, _, err := m.Spawn(ctx, asyncTerminalConfig("fix the button"))
	if err != nil {
		t.Fatal(err)
	}
	(*deferred)[0]()

	stored := st.sessions[rec.ID]
	if stored.ProvisionState != domain.SessionProvisionFailed || !strings.Contains(stored.ProvisionError, "pane unavailable") {
		t.Fatalf("after failed prompt: %q %q", stored.ProvisionState, stored.ProvisionError)
	}
	if rt.created != 1 || rt.destroyed != 1 {
		t.Fatalf("runtimes created=%d destroyed=%d, want the agent stopped", rt.created, rt.destroyed)
	}
	if stored.Metadata.RuntimeHandleID != "" || stored.Metadata.WorkspacePath == "" || ws.destroyed != 0 || stored.IsTerminated {
		t.Fatalf("after failed prompt: handle=%q path=%q worktrees destroyed=%d terminated=%t",
			stored.Metadata.RuntimeHandleID, stored.Metadata.WorkspacePath, ws.destroyed, stored.IsTerminated)
	}
	wantStates(t, states, domain.SessionProvisionProvisioning, domain.SessionProvisionFailed)
}

// Retry starts one fresh agent with the saved prompt. An agent the failed
// attempt could not stop is stopped first, so two never run side by side.
func TestResumeFailedAsyncTerminalStartReplacesTheAgentAndDeliversOnce(t *testing.T) {
	rt := &fakeRuntime{destroyErr: errors.New("runtime busy")}
	sends := 0
	msg := &fakeMessenger{errFor: func(domain.SessionID, string) error {
		sends++
		if sends == 1 {
			return errors.New("pane unavailable")
		}
		return nil
	}}
	m, st, _, states := newAsyncTerminalManager(rt, msg)
	deferred := deferredBackground(m)

	rec, _, _, err := m.Spawn(ctx, asyncTerminalConfig("fix the button"))
	if err != nil {
		t.Fatal(err)
	}
	(*deferred)[0]()
	if got := st.sessions[rec.ID]; got.ProvisionState != domain.SessionProvisionFailed || got.Metadata.RuntimeHandleID != "h1" {
		t.Fatalf("first attempt: state=%q handle=%q, want failed with the unstopped agent recorded", got.ProvisionState, got.Metadata.RuntimeHandleID)
	}

	// Still unstoppable: retry refuses rather than start a second agent.
	if _, err := m.ResumeAgentWithMode(ctx, rec.ID); err == nil || !strings.Contains(err.Error(), "stop the previous agent") {
		t.Fatalf("retry with a live previous agent = %v, want refusal", err)
	}
	if rt.created != 1 || len(*deferred) != 1 {
		t.Fatalf("runtimes=%d background=%d, want no new start", rt.created, len(*deferred))
	}

	rt.destroyErr = nil
	retried, err := m.ResumeAgentWithMode(ctx, rec.ID)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retried.Session.ID != rec.ID || retried.Session.ProvisionState != domain.SessionProvisionProvisioning {
		t.Fatalf("retry answered %+v, want the same session starting", retried.Session)
	}
	if !slices.Contains(rt.destroyedIDs, "h1") {
		t.Fatalf("destroyed = %v, want the previous agent stopped before the retry", rt.destroyedIDs)
	}
	(*deferred)[1]()

	stored := st.sessions[rec.ID]
	if stored.ProvisionState != domain.SessionProvisionReady || rt.created != 2 {
		t.Fatalf("after retry: state=%q runtimes=%d", stored.ProvisionState, rt.created)
	}
	// One failed delivery to the first agent, one to the fresh one.
	if !slices.Equal(msg.msgs, []string{"fix the button", "fix the button"}) {
		t.Fatalf("deliveries = %v", msg.msgs)
	}
	wantStates(t, states,
		domain.SessionProvisionProvisioning, domain.SessionProvisionFailed,
		domain.SessionProvisionProvisioning, domain.SessionProvisionReady)
}

// The read model withholds the terminal until ready, so a daemon that died
// after launching the agent leaves one nobody can see. Restart marks the start
// failed, stops that agent, and never relaunches it or resends the prompt.
func TestFailInterruptedTerminalStartStopsItsAgentWithoutRelaunching(t *testing.T) {
	rt := &fakeRuntime{}
	msg := &fakeMessenger{}
	m, st, _, states := newAsyncTerminalManager(rt, msg)
	rec := domain.SessionRecord{ID: "mer-7", ProjectID: "mer", Kind: domain.KindWorker, Mode: domain.SessionModeTUI,
		ProvisionState: domain.SessionProvisionProvisioning}
	rec.Metadata.WorkspacePath, rec.Metadata.Branch = "/tmp/worktrees/mer-7", "ao/mer-7/root"
	rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID = "h7", "launch-7"
	rec.Metadata.Prompt = "fix the button"
	st.sessions[rec.ID] = rec
	beforeAgent := domain.SessionRecord{ID: "mer-8", ProjectID: "mer", Kind: domain.KindWorker, Mode: domain.SessionModeTUI,
		ProvisionState: domain.SessionProvisionProvisioning}
	st.sessions[beforeAgent.ID] = beforeAgent

	if err := m.FailInterruptedProvisioning(ctx); err != nil {
		t.Fatal(err)
	}
	for _, id := range []domain.SessionID{rec.ID, beforeAgent.ID} {
		if got := st.sessions[id]; got.ProvisionState != domain.SessionProvisionFailed || got.IsTerminated {
			t.Fatalf("%s after restart: state=%q terminated=%t, want failed and kept", id, got.ProvisionState, got.IsTerminated)
		}
	}
	if !slices.Equal(rt.destroyedIDs, []string{"h7"}) || st.sessions[rec.ID].Metadata.RuntimeHandleID != "" {
		t.Fatalf("destroyed=%v handle=%q, want only the launched agent stopped", rt.destroyedIDs, st.sessions[rec.ID].Metadata.RuntimeHandleID)
	}
	if st.sessions[rec.ID].Metadata.WorkspacePath == "" || rt.created != 0 || len(msg.msgs) != 0 {
		t.Fatalf("restart touched the worktree or relaunched: path=%q runtimes=%d prompts=%v",
			st.sessions[rec.ID].Metadata.WorkspacePath, rt.created, msg.msgs)
	}
	// Restart reconciliation never relaunches a start that is not ready.
	if err := m.reconcileLive(ctx, st.sessions[rec.ID]); err != nil || rt.created != 0 {
		t.Fatalf("reconcileLive = %v, runtimes=%d, want no relaunch", err, rt.created)
	}
	if len(*states) != 2 {
		t.Fatalf("provision writes = %v, want one failure per interrupted start", *states)
	}
}

// pollSignalRuntime reports each prompt-readiness poll so a test can act while
// the background start is waiting on the agent.
type pollSignalRuntime struct {
	*fakeRuntime
	polled chan struct{}
}

func (r *pollSignalRuntime) GetOutput(context.Context, ports.RuntimeHandle, int) (string, error) {
	select {
	case r.polled <- struct{}{}:
	default:
	}
	return "", nil
}

// Kill cancels a start that is waiting for the agent to take its prompt. The
// prompt is never delivered, the agent is stopped, the worktree stays, and the
// start settles once as failed (the service drops a cancelled start).
func TestKillCancelsTerminalStartWaitingForPrompt(t *testing.T) {
	inner := &fakeRuntime{}
	rt := &pollSignalRuntime{fakeRuntime: inner, polled: make(chan struct{}, 1)}
	msg := &fakeMessenger{}
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	ws := &fakeWorkspace{}
	agent := readinessAgent{
		afterStartAgent: afterStartAgent{recordingAgent: &recordingAgent{}},
		hints:           ports.PromptReadinessHints{Patterns: []string{"never shown"}, Timeout: time.Minute, PollInterval: time.Millisecond},
	}
	m := New(Deps{
		Runtime: rt, Agents: singleAgent{agent: agent}, Workspace: ws, Store: st, Messenger: msg,
		Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil },
	})
	var causes []error
	m.SetProvisionObserver(func(_ context.Context, _ domain.SessionRecord, state domain.SessionProvisionState, cause error) {
		if state != domain.SessionProvisionProvisioning {
			causes = append(causes, cause)
		}
	})

	rec, _, _, err := m.Spawn(ctx, asyncTerminalConfig("fix the button"))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-rt.polled:
	case <-time.After(10 * time.Second):
		t.Fatal("background start never reached prompt readiness")
	}
	if err := m.cancelAsyncChatSpawn(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}

	stored := st.sessions[rec.ID]
	if stored.ProvisionState != domain.SessionProvisionFailed || len(msg.msgs) != 0 {
		t.Fatalf("after cancel: state=%q prompts=%v", stored.ProvisionState, msg.msgs)
	}
	if inner.created != 1 || inner.destroyed != 1 || ws.destroyed != 0 {
		t.Fatalf("runtimes created=%d destroyed=%d, worktrees destroyed=%d", inner.created, inner.destroyed, ws.destroyed)
	}
	if len(causes) != 1 || !errors.Is(causes[0], context.Canceled) {
		t.Fatalf("settled causes = %v, want one cancellation", causes)
	}
	if err := m.WaitBackgroundWorkers(ctx); err != nil {
		t.Fatal(err)
	}
}
