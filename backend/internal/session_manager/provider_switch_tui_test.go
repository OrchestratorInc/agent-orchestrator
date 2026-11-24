package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// childProbeRuntime adds the child-process probe the production runtime has.
type childProbeRuntime struct {
	*fakeRuntime
	childAlive bool
}

func (r *childProbeRuntime) IsChildAlive(context.Context, ports.RuntimeHandle) (bool, error) {
	return r.childAlive, nil
}

func TestSwitchTargetThatExitsAtOnceIsReportedWithItsLastLine(t *testing.T) {
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })

	rec := domain.SessionRecord{ID: "s-1", Metadata: domain.SessionMetadata{RuntimeHandleID: "handle-1"}}
	dead := &childProbeRuntime{fakeRuntime: &fakeRuntime{outputs: []string{"starting\nError: Session ID 123 is already in use.\n\n"}}}
	err := (&Manager{runtime: dead}).confirmTUISwitchTargetAlive(context.Background(), rec)
	if err == nil || !strings.Contains(err.Error(), "Error: Session ID 123 is already in use.") {
		t.Fatalf("error = %v, want the agent's last line", err)
	}

	alive := &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}
	if err := (&Manager{runtime: alive}).confirmTUISwitchTargetAlive(context.Background(), rec); err != nil {
		t.Fatalf("a running target was reported as failed: %v", err)
	}
	// A runtime that cannot say whether the child is alive never fails a switch.
	if err := (&Manager{runtime: &fakeRuntime{}}).confirmTUISwitchTargetAlive(context.Background(), rec); err != nil {
		t.Fatalf("a runtime without the probe failed the switch: %v", err)
	}
}

// launchOptionsFakeStore keeps a session's launch options, as the real store does.
type launchOptionsFakeStore struct {
	*fakeStore
	options domain.AgentLaunchOptions
}

func (s *launchOptionsFakeStore) SessionLaunchOptions(context.Context, domain.SessionID) (domain.AgentLaunchOptions, error) {
	return s.options, nil
}

func (s *launchOptionsFakeStore) SetSessionLaunchOptions(_ context.Context, _ domain.SessionID, options domain.AgentLaunchOptions) error {
	s.options = options
	return nil
}

func (s *launchOptionsFakeStore) UpdateSessionWithLaunchOptions(ctx context.Context, rec domain.SessionRecord, options domain.AgentLaunchOptions) error {
	if err := s.UpdateSession(ctx, rec); err != nil {
		return err
	}
	s.options = options
	return nil
}

func TestRelaunchAfterACutShortSwitchHandsOverInsteadOfReplayingTheTask(t *testing.T) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	// The row as a switch leaves it before its target starts: the target owns the
	// session with no native session yet, and its note is saved.
	seedTerminal(st, "mer-1", domain.SessionMetadata{WorkspacePath: "/ws/mer-1", Branch: "b", Prompt: "ORIGINAL TASK"})
	store := &launchOptionsFakeStore{fakeStore: st, options: domain.AgentLaunchOptions{HandoffNote: "HANDOFF NOTE"}}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: agent}, Workspace: &fakeWorkspace{}, Store: store,
		Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st}, LookPath: func(string) (string, error) { return "/bin/true", nil }})

	if _, err := m.RestoreWithMode(context.Background(), "mer-1"); err != nil {
		t.Fatal(err)
	}
	if agent.lastLaunch.Prompt != "HANDOFF NOTE" {
		t.Fatalf("relaunched prompt = %q, want the handoff note", agent.lastLaunch.Prompt)
	}
	if agent.lastLaunch.Config.Launch.HandoffNote != "" {
		t.Fatal("the note reached the agent's launch options")
	}
	if got := st.sessions["mer-1"].Metadata.Prompt; got != "ORIGINAL TASK" {
		t.Fatalf("session prompt = %q, want the task kept", got)
	}
	// No native session has been reported yet, so a later restore would start
	// fresh again: the note stays for it.
	if store.options.HandoffNote != "HANDOFF NOTE" {
		t.Fatalf("saved note = %q before a native session exists, want it kept", store.options.HandoffNote)
	}
	// Once the target has a native session, a restore resumes it and the note goes.
	rec := st.sessions["mer-1"]
	rec.Metadata.AgentSessionID = "target-native"
	st.sessions["mer-1"] = rec
	m.clearHandoffNote(context.Background(), "mer-1")
	if store.options.HandoffNote != "" {
		t.Fatalf("saved note = %q with a native session, want it cleared", store.options.HandoffNote)
	}
}

func TestSwitchTargetWithoutANativeSessionYetRestoresFromTheHandoff(t *testing.T) {
	ctx := context.Background()
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode, rec.Kind = domain.HarnessClaudeCode, domain.SessionModeTUI, domain.KindWorker
	rec.Activity.State = domain.ActivityExited
	rec.Metadata.Branch, rec.Metadata.Prompt = "branch", "ORIGINAL TASK ALREADY COMPLETED"
	rec.Metadata.AgentSessionID, rec.Metadata.RuntimeLaunchID = "source-native", "source-launch"
	st.sessions[rec.ID] = rec
	store := &launchOptionsFakeStore{fakeStore: st}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}, Agents: singleAgent{agent: agent},
		Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})
	const handoff = "HANDOFF: original task done; the user wants a follow-up"
	if err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessMuse, HandoffNote: handoff}); err != nil {
		t.Fatal(err)
	}
	// The target stays alive but reports no native session before it exits.
	target := st.sessions[rec.ID]
	target.IsTerminated, target.Activity.State, target.Metadata.RuntimeHandleID = true, domain.ActivityExited, ""
	st.sessions[rec.ID] = target
	if _, err := m.RestoreWithMode(ctx, rec.ID); err != nil {
		t.Fatal(err)
	}
	if agent.lastLaunch.Prompt != handoff {
		t.Fatalf("restored prompt = %q, want the handoff instead of the original task", agent.lastLaunch.Prompt)
	}
}

func TestSwitchTargetThatMissesItsPromptLeavesTheSessionToTheRollback(t *testing.T) {
	for _, tc := range []struct {
		name     string
		override *string
		ended    int

		savedNote string
	}{
		{"ordinary relaunch", nil, 1, ""},
		// A restore that hands over a cut-short switch's saved note is not a switch:
		// nothing rolls it back.
		{"restore with a saved note", nil, 1, "SAVED NOTE"},
		{"provider switch", ptr("HANDOFF NOTE"), 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newFakeStore()
			st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
			rec := mkLive("mer-1")
			rec.Metadata.Prompt = "TASK"
			rec.Metadata.AgentSessionID = ""
			st.sessions[rec.ID] = rec
			store := &launchOptionsFakeStore{fakeStore: st, options: domain.AgentLaunchOptions{HandoffNote: tc.savedNote}}
			lcm := &fakeLCM{store: st}
			m := New(Deps{Runtime: &fakeRuntime{}, Agents: singleAgent{agent: afterStartAgent{recordingAgent: &recordingAgent{}}},
				Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{err: errors.New("delivery failed")},
				Lifecycle: lcm, LookPath: func(string) (string, error) { return "/bin/true", nil }})
			_, err := m.relaunchSessionWithPolicyAndGeneration(context.Background(), "relaunch", rec, st.projects["mer"],
				ports.WorkspaceInfo{Path: rec.Metadata.WorkspacePath}, nil, true, false, "", domain.SessionInterfaceTransitionHistoryStrict, tc.override)
			if err == nil || !strings.Contains(err.Error(), "deliver prompt") {
				t.Fatalf("error = %v, want the delivery failure", err)
			}
			if got := lcm.terminated[rec.ID]; got != tc.ended {
				t.Fatalf("session ended %d times, want %d", got, tc.ended)
			}
		})
	}
}

func ptr(s string) *string { return &s }

// runtimeAuthSwitchTarget is signed in only through a login AO saved itself,
// like Claude Code with a setup-token login and no native login.
type runtimeAuthSwitchTarget struct {
	fakeAgent
	dataDir string
}

func (runtimeAuthSwitchTarget) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	return ports.AgentAuthStatusUnauthorized, nil
}

func (a runtimeAuthSwitchTarget) AuthStatusForRuntime(_ context.Context, dataDir string) (ports.AgentAuthStatus, domain.AgentAccountIdentity, []domain.AgentAuthDiagnostic, error) {
	if dataDir != a.dataDir {
		return ports.AgentAuthStatusUnauthorized, domain.AgentAccountIdentity{}, nil, nil
	}
	return ports.AgentAuthStatusAuthorized, domain.AgentAccountIdentity{}, nil, nil
}

func TestPreflightTUISwitchTargetAcceptsAOSavedLogin(t *testing.T) {
	target := runtimeAuthSwitchTarget{dataDir: "/daemon/data"}
	if err := preflightTUISwitchTarget(context.Background(), domain.HarnessClaudeCode, target, "/daemon/data"); err != nil {
		t.Fatalf("preflight refused a target signed in through an AO-saved login: %v", err)
	}
	if err := preflightTUISwitchTarget(context.Background(), domain.HarnessClaudeCode, target, ""); !errors.Is(err, ports.ErrAgentAuthRequired) {
		t.Fatalf("preflight without a data directory = %v, want the native sign-in refusal", err)
	}
}

func TestForcedTUISwitchInterruptsAWorkingAgent(t *testing.T) {
	ctx := context.Background()
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode, rec.Kind = domain.HarnessClaudeCode, domain.SessionModeTUI, domain.KindWorker
	rec.Metadata.Branch, rec.Metadata.AgentSessionID = "branch", "source-native"
	st.sessions[rec.ID] = rec
	runtime := &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}
	runtime.onInterrupt = func(ports.RuntimeHandle) {
		current := st.sessions[rec.ID]
		current.Activity.State = domain.ActivityExited
		st.sessions[rec.ID] = current
	}
	m := New(Deps{Runtime: runtime, Agents: singleAgent{agent: &recordingAgent{}},
		Workspace: &fakeWorkspace{}, Store: &launchOptionsFakeStore{fakeStore: st}, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})

	var refusal *domain.ProviderSwitchError
	err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessMuse})
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalBusy {
		t.Fatalf("plain switch = %v, want a busy refusal", err)
	}
	err = m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode})
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalAlreadyActive || !errors.Is(err, domain.ErrProviderAlreadyActive) {
		t.Fatalf("switch to the running agent = %v, want already_active", err)
	}
	if err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessMuse, Force: true}); err != nil {
		t.Fatalf("forced switch: %v", err)
	}
	if len(runtime.interrupts) != 1 {
		t.Fatalf("interrupts = %v, want the working agent interrupted once", runtime.interrupts)
	}
	if got := st.sessions[rec.ID].Harness; got != domain.HarnessMuse {
		t.Fatalf("harness = %q, want the switch target", got)
	}
}

func TestTUISwitchToTheRunningAgentWithNewSettingsResumesItsOwnThread(t *testing.T) {
	ctx := context.Background()
	settle, poll := tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll
	tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = 20*time.Millisecond, 5*time.Millisecond
	t.Cleanup(func() { tuiSwitchTargetSettle, tuiSwitchTargetSettlePoll = settle, poll })
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := mkLive("mer-1")
	rec.Harness, rec.Mode, rec.Kind = domain.HarnessClaudeCode, domain.SessionModeTUI, domain.KindWorker
	rec.Activity.State = domain.ActivityIdle
	rec.Metadata.Branch, rec.Metadata.Model, rec.Metadata.Effort, rec.Metadata.AgentSessionID = "branch", "sonnet", "low", "own-native"
	st.sessions[rec.ID] = rec
	store := &launchOptionsFakeStore{fakeStore: st, options: domain.AgentLaunchOptions{FastMode: true}}
	agent := &recordingAgent{}
	m := New(Deps{Runtime: &childProbeRuntime{fakeRuntime: &fakeRuntime{}, childAlive: true}, Agents: singleAgent{agent: agent},
		Workspace: &fakeWorkspace{}, Store: store, Messenger: &fakeMessenger{}, Lifecycle: &fakeLCM{store: st},
		LookPath: func(string) (string, error) { return "/bin/true", nil }})

	same := domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode, Model: "Sonnet", PermissionMode: domain.PermissionModeDefault}
	if err := m.SwitchTUIProvider(ctx, rec.ID, same); !errors.Is(err, domain.ErrProviderAlreadyActive) {
		t.Fatalf("unchanged settings = %v, want already_active", err)
	}
	if err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{
		TargetHarness: domain.HarnessClaudeCode, Model: "opus", Effort: "high", PermissionMode: domain.PermissionModeAcceptEdits,
	}); err != nil {
		t.Fatal(err)
	}
	got := st.sessions[rec.ID]
	if got.Harness != domain.HarnessClaudeCode || got.Metadata.AgentSessionID != rec.Metadata.AgentSessionID {
		t.Fatalf("harness = %q native = %q, want the same agent on its own thread", got.Harness, got.Metadata.AgentSessionID)
	}
	if got.Metadata.Permissions != domain.PermissionModeAcceptEdits || agent.lastRestore.Config.Permissions != domain.PermissionModeAcceptEdits {
		t.Fatalf("permission mode = %q, launched with %q", got.Metadata.Permissions, agent.lastRestore.Config.Permissions)
	}
	if agent.restoreCalls != 1 || agent.lastRestore.Config.Model != "opus" || agent.lastRestore.Config.Effort != "high" || agent.launchCalls != 0 {
		t.Fatalf("restores = %d launches = %d config = %+v, want one resume on the new settings", agent.restoreCalls, agent.launchCalls, agent.lastRestore.Config)
	}
	if !store.options.FastMode {
		t.Fatal("the restart dropped the session's launch options")
	}

	fresh := st.sessions[rec.ID]
	fresh.Metadata.AgentSessionID = ""
	st.sessions[rec.ID] = fresh
	var refusal *domain.ProviderSwitchError
	err := m.SwitchTUIProvider(ctx, rec.ID, domain.ProviderSwitchRequest{TargetHarness: domain.HarnessClaudeCode, Model: "haiku"})
	if !errors.As(err, &refusal) || refusal.Kind != domain.ProviderSwitchRefusalUnsupported || agent.restoreCalls != 1 {
		t.Fatalf("restart with no thread to resume = %v, restores = %d; want a refusal before anything stops", err, agent.restoreCalls)
	}
}
