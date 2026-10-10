package review

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAccountMutationRequiresPositiveIdleProofForLiveReviewerTerminal(t *testing.T) {
	for _, activity := range []domain.ActivityState{"", "unrecognized", domain.ActivityBlocked, domain.ActivityExited, domain.ActivityActive, domain.ActivityIdle, domain.ActivityWaitingInput} {
		for _, harness := range []domain.ReviewerHarness{domain.ReviewerCodex, domain.ReviewerClaudeCode} {
			t.Run(string(harness)+"/"+string(activity), func(t *testing.T) {
				store := &routingGuardStore{fakeStore: &fakeStore{review: &domain.Review{ID: "review", SessionID: "worker", Harness: harness, ReviewerHandleID: "live-terminal", ReviewerActivityState: activity}}}
				launcher := &fakeLauncher{}
				engine := newEngineForTest(store, nil, nil, nil, launcher)
				release, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
				idle := activity == domain.ActivityIdle || activity == domain.ActivityWaitingInput
				if idle {
					if err != nil || release == nil {
						t.Fatalf("proved idle terminal refused: %v", err)
					}
					release()
				} else if release != nil || !errors.Is(err, ports.ErrProviderAccountBusy) {
					t.Fatalf("unproved idle terminal accepted: %v", err)
				}
				if launcher.cancelled || launcher.destroyed || launcher.spawned {
					t.Fatal("idle proof interrupted or replaced a reviewer")
				}
				if !engine.workerMutex("worker").TryLock() {
					t.Fatal("reviewer proof left launch admission locked")
				}
				engine.workerMutex("worker").Unlock()
				if store.review.ReviewerActivityState != activity || store.review.ReviewerHandleID != "live-terminal" {
					t.Fatal("account operation rewrote reviewer lifecycle facts")
				}
				store.review.ReviewerActivityState = domain.ActivityIdle
				retry, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
				if err != nil {
					t.Fatal(err)
				}
				defer retry()
			})
		}
	}
}

func TestAccountMutationDoesNotRequireActivityForAbsentHistoricalReviewerTerminal(t *testing.T) {
	for _, activity := range []domain.ActivityState{"", domain.ActivityIdle, domain.ActivityExited} {
		t.Run(string(activity), func(t *testing.T) {
			store := &routingGuardStore{fakeStore: &fakeStore{review: &domain.Review{ID: "historical-review", SessionID: "worker", Harness: domain.ReviewerCodex, ReviewerActivityState: activity}}}
			engine := newEngineForTest(store, nil, nil, nil, &fakeLauncher{})
			release, err := engine.AcquireAccountRoutingPause(context.Background(), "worker")
			if err != nil || release == nil {
				t.Fatalf("absent terminal blocked account switch: %v", err)
			}
			release()
			if store.review.ReviewerHandleID != "" {
				t.Fatal("account guard restored a historical reviewer")
			}
		})
	}
}
