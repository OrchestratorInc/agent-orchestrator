package sessionmanager

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
