package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestBusyHarnessSessionsNamesWhyEachMustWait(t *testing.T) {
	mgr, store, _ := newChatManager(&recordingLauncher{})
	now := time.Now()
	seedChatResumeSession(store, domain.ActivityIdle)
	idle := store.sessions["mer-1"]
	for id, change := range map[domain.SessionID]func(*domain.SessionRecord){
		"working":  func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityActive },
		"approval": func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityWaitingInput },
		"starting": func(r *domain.SessionRecord) { r.ProvisionState = domain.SessionProvisionProvisioning },
		// Sessions without a process never hold the update back.
		"asleep":     func(r *domain.SessionRecord) { r.HibernatedAt = &now },
		"exited":     func(r *domain.SessionRecord) { r.Activity.State = domain.ActivityExited },
		"terminated": func(r *domain.SessionRecord) { r.IsTerminated = true },
		"other": func(r *domain.SessionRecord) {
			r.Harness = domain.HarnessClaudeCode
			r.Activity.State = domain.ActivityActive
		},
	} {
		rec := idle
		rec.ID = id
		change(&rec)
		store.sessions[id] = rec
	}
	busy, err := mgr.BusyHarnessSessions(context.Background(), domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	got := map[domain.SessionID]domain.SessionWaitReason{}
	for _, wait := range busy {
		got[wait.SessionID] = wait.Reason
	}
	want := map[domain.SessionID]domain.SessionWaitReason{
		"working": domain.SessionWaitWorking, "approval": domain.SessionWaitApproval, "starting": domain.SessionWaitStarting,
	}
	if len(got) != len(want) {
		t.Fatalf("busy = %v, want %v", got, want)
	}
	for id, reason := range want {
		if got[id] != reason {
			t.Fatalf("busy = %v, want %v", got, want)
		}
	}
}

// idleWorkloadRuntime reports that no terminal runs a command.
type idleWorkloadRuntime struct{ *fakeRuntime }

func (idleWorkloadRuntime) RunningWorkloadCommands(context.Context, ports.RuntimeHandle, ports.SupervisedProcessRef) (int, error) {
	return 0, nil
}

func TestBusyHarnessSessionsKeepsATerminalThatCannotResume(t *testing.T) {
	mgr, store, rt := newChatManager(&recordingLauncher{})
	mgr.runtime = idleWorkloadRuntime{rt}
	terminal := func(id domain.SessionID, agentSessionID string) domain.SessionRecord {
		return domain.SessionRecord{ID: id, ProjectID: chatTestProject, Kind: domain.KindWorker,
			Harness: domain.HarnessCodex, Mode: domain.SessionModeTUI,
			Activity: domain.Activity{State: domain.ActivityIdle},
			Metadata: domain.SessionMetadata{WorkspacePath: "/ws/" + string(id), Branch: "ao/" + string(id),
				RuntimeHandleID: "pane-" + string(id), AgentSessionID: agentSessionID}}
	}
	store.sessions["resumable"] = terminal("resumable", "native-1")
	// No native conversation id: stopping it would leave nothing to resume.
	store.sessions["fresh-only"] = terminal("fresh-only", "")

	busy, err := mgr.BusyHarnessSessions(context.Background(), domain.HarnessCodex)
	if err != nil {
		t.Fatal(err)
	}
	if len(busy) != 1 || busy[0].SessionID != "fresh-only" || busy[0].Reason != domain.SessionWaitResume {
		t.Fatalf("busy = %+v, want only fresh-only waiting to resume", busy)
	}
}
