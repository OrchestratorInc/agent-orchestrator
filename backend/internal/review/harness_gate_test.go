package review

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type harnessGateFunc func(domain.AgentHarness) (func(), bool)

func (f harnessGateFunc) TryBeginHarnessUse(harness domain.AgentHarness) (func(), bool) {
	return f(harness)
}

func TestTriggerDoesNotLaunchReviewerWhileItsHarnessUpdates(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	eng.harnessUse = harnessGateFunc(func(domain.AgentHarness) (func(), bool) { return nil, false })
	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); !errors.Is(err, ErrHarnessInstallActive) {
		t.Fatalf("Trigger error = %v, want ErrHarnessInstallActive", err)
	}
	if launcher.spawned {
		t.Fatal("reviewer launched while its harness was being updated")
	}
}

func TestTriggerHoldsHarnessUntilReviewerIsRecorded(t *testing.T) {
	store := &fakeStore{}
	launcher := &fakeLauncher{handle: "review-mer-1"}
	eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, prAt("sha1"), fakeProjects{}, launcher)
	var gated domain.AgentHarness
	released := false
	eng.harnessUse = harnessGateFunc(func(harness domain.AgentHarness) (func(), bool) {
		gated = harness
		return func() {
			// An update that starts after this must find the reviewer's handle.
			if store.review == nil || store.review.ReviewerHandleID != "review-mer-1" {
				t.Errorf("harness released before the reviewer was recorded: %+v", store.review)
			}
			released = true
		}, true
	})
	if _, err := trigger(context.Background(), eng, "mer-1", "", domain.AgentConfig{}); err != nil {
		t.Fatal(err)
	}
	if gated != domain.HarnessClaudeCode || !released || !launcher.spawned {
		t.Fatalf("gated=%q released=%t spawned=%t", gated, released, launcher.spawned)
	}
}
