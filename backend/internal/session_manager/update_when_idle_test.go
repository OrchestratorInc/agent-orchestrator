package sessionmanager

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
