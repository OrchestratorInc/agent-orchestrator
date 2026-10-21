package sessionmanager

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// teardownManager builds a manager whose store, runtime, and chat launcher the
// test inspects directly.
func teardownManager() (*Manager, *fakeStore, *fakeRuntime, *fakeLCM, *recordingLauncher) {
	st := newFakeStore()
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rt := &fakeRuntime{}
	lcm := &fakeLCM{store: st}
	chat := &recordingLauncher{}
	lookPath := func(string) (string, error) { return "/bin/true", nil }
	m := New(Deps{
		Runtime: rt, Agents: fakeAgents{}, Workspace: &fakeWorkspace{}, Store: st,
		Messenger: &fakeMessenger{}, Lifecycle: lcm, Chat: chat, LookPath: lookPath,
	})
	return m, st, rt, lcm, chat
}

func seedTeardownSessions(st *fakeStore) {
	// A live terminal session whose runtime process must be stopped.
	st.sessions["live-tui"] = domain.SessionRecord{
		ID: "live-tui", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "tui-handle-1", RuntimeLaunchID: "launch-tui-1"},
	}
	// A live chat session whose provider host process must be stopped.
	st.sessions["live-chat"] = domain.SessionRecord{
		ID: "live-chat", ProjectID: "mer", Mode: domain.SessionModeChat,
		Activity: domain.Activity{State: domain.ActivityActive},
		Metadata: domain.SessionMetadata{AgentSessionID: "native-chat-1"},
	}
	// An agent that exited but whose preserved pane shell still holds the
	// runtime: the leftover tmux session is exactly what a full stop removes.
	st.sessions["exited-tui"] = domain.SessionRecord{
		ID: "exited-tui", ProjectID: "mer", Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "tui-handle-2", RuntimeLaunchID: "launch-tui-2"},
	}
	// An already terminated session has no processes left to stop.
	st.sessions["terminated-tui"] = domain.SessionRecord{
		ID: "terminated-tui", ProjectID: "mer", IsTerminated: true,
		Activity: domain.Activity{State: domain.ActivityExited},
		Metadata: domain.SessionMetadata{RuntimeHandleID: "tui-handle-3", RuntimeLaunchID: "launch-tui-3"},
	}
}

// TestStopAllSessionProcessesStopsEveryLiveSessionRuntime pins the full-stop
// contract behind `ao stop` (issue #35): after the daemon's shutdown teardown
// no AO session process survives — terminal panes (live or exit-preserved) and
// chat provider hosts alike. Victims come only from session records, so a
// runtime on any other socket (the user's own tmux, another AO install) is
// never named to Destroy, and session rows stay alive and resumable.
func TestStopAllSessionProcessesStopsEveryLiveSessionRuntime(t *testing.T) {
	m, st, rt, lcm, chat := teardownManager()
	seedTeardownSessions(st)

	if err := m.StopAllSessionProcesses(context.Background()); err != nil {
		t.Fatalf("StopAllSessionProcesses: %v", err)
	}

	destroyed := slices.Clone(rt.destroyedIDs)
	slices.Sort(destroyed)
	if !slices.Equal(destroyed, []string{"tui-handle-1", "tui-handle-2"}) {
		t.Fatalf("destroyed runtimes = %v, want exactly the live and exit-preserved terminal handles", destroyed)
	}
	if !slices.Equal(chat.stopped, []domain.SessionID{"live-chat"}) {
		t.Fatalf("stopped chats = %v, want exactly the live chat session", chat.stopped)
	}
	// Terminal intent is not this method's job: rows stay resumable, and the
	// next boot's reconcile reads a process-free state.
	for _, id := range []domain.SessionID{"live-tui", "live-chat", "exited-tui"} {
		if st.sessions[id].IsTerminated {
			t.Fatalf("session %s was terminated by the teardown", id)
		}
	}
	if len(lcm.terminated) != 0 {
		t.Fatalf("lifecycle recorded terminations: %v", lcm.terminated)
	}
}

// TestStopAllSessionProcessesEmptyStoreIsNoop covers a daemon with no sessions.
func TestStopAllSessionProcessesEmptyStoreIsNoop(t *testing.T) {
	m, _, rt, _, chat := teardownManager()
	if err := m.StopAllSessionProcesses(context.Background()); err != nil {
		t.Fatalf("StopAllSessionProcesses on empty store: %v", err)
	}
	if len(rt.destroyedIDs) != 0 || len(chat.stopped) != 0 {
		t.Fatalf("empty store touched the runtime or chat: %v %v", rt.destroyedIDs, chat.stopped)
	}
}

// TestStopAllSessionProcessesReportsUnstoppableRuntime pins the failure side of
// "conclusive": a runtime that refuses to die after the retry is reported, not
// silently counted as stopped.
func TestStopAllSessionProcessesReportsUnstoppableRuntime(t *testing.T) {
	m, st, rt, _, chat := teardownManager()
	rt.destroyErr = errors.New("boom")
	rt.aliveByHandle = map[string]bool{"tui-handle-1": true}
	seedTeardownSessions(st)

	if err := m.StopAllSessionProcesses(context.Background()); err == nil {
		t.Fatal("StopAllSessionProcesses reported success while a runtime survived teardown")
	}
	_ = chat
}
