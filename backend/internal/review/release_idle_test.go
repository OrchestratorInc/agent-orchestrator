package review

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReleaseIdleReviewers(t *testing.T) {
	now := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	const idleFor = 30 * time.Minute
	for _, tc := range []struct {
		name        string
		state       domain.ActivityState
		idle        time.Duration
		mode        domain.ReviewerInterfaceMode
		runningPass bool
		wantRelease bool
	}{
		{name: "idle past the limit", state: domain.ActivityIdle, idle: 31 * time.Minute, mode: domain.ReviewerInterfaceTUI, wantRelease: true},
		{name: "exited past the limit", state: domain.ActivityExited, idle: 31 * time.Minute, mode: domain.ReviewerInterfaceTUI, wantRelease: true},
		{name: "idle under the limit", state: domain.ActivityIdle, idle: 10 * time.Minute, mode: domain.ReviewerInterfaceTUI},
		{name: "still working", state: domain.ActivityActive, idle: 2 * time.Hour, mode: domain.ReviewerInterfaceTUI},
		{name: "waiting on a prompt", state: domain.ActivityWaitingInput, idle: 2 * time.Hour, mode: domain.ReviewerInterfaceTUI},
		{name: "review running", state: domain.ActivityIdle, idle: 2 * time.Hour, mode: domain.ReviewerInterfaceTUI, runningPass: true},
		{name: "chat reviewer", state: domain.ActivityIdle, idle: 2 * time.Hour, mode: domain.ReviewerInterfaceChat},
	} {
		t.Run(tc.name, func(t *testing.T) {
			review := domain.Review{
				ID: "review-1", SessionID: "worker-1", Harness: domain.ReviewerClaudeCode,
				ReviewerHandleID: "pane-1", AgentSessionID: "native-1",
				ReviewerActivityState: tc.state, InterfaceMode: tc.mode,
				UpdatedAt: now.Add(-tc.idle),
			}
			store := &fakeStore{reviews: map[domain.ReviewerHarness]domain.Review{review.Harness: review}}
			if tc.runningPass {
				store.runs = []domain.ReviewRun{{ID: "run-1", ReviewID: review.ID, SessionID: review.SessionID, Harness: review.Harness, Status: domain.ReviewRunRunning}}
			}
			launcher := &fakeLauncher{}
			engine := New(Deps{Store: store, Launcher: launcher, Clock: func() time.Time { return now }})

			released, err := engine.ReleaseIdleReviewers(context.Background(), idleFor)
			if err != nil {
				t.Fatalf("ReleaseIdleReviewers: %v", err)
			}
			after := store.reviews[review.Harness]
			if tc.wantRelease {
				if released != 1 || launcher.destroyedHandle != "pane-1" || after.ReviewerHandleID != "" {
					t.Fatalf("released=%d destroyed=%q handle=%q, want the pane destroyed and its handle cleared", released, launcher.destroyedHandle, after.ReviewerHandleID)
				}
				if after.AgentSessionID != "native-1" {
					t.Fatalf("agent session id = %q, want it kept so the next review resumes", after.AgentSessionID)
				}
				return
			}
			if released != 0 || launcher.destroyCalls != 0 || after.ReviewerHandleID != "pane-1" {
				t.Fatalf("released=%d destroyCalls=%d handle=%q, want the reviewer left running", released, launcher.destroyCalls, after.ReviewerHandleID)
			}
		})
	}
}
