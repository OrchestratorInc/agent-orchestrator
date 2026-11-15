package review

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// noInjectMarkingStore records MarkReviewRunNoInject on the fake store.
type noInjectMarkingStore struct {
	*fakeStore
	marked []string
}

func (s *noInjectMarkingStore) MarkReviewRunNoInject(_ context.Context, id string) (bool, error) {
	for i := range s.runs {
		if s.runs[i].ID == id && s.runs[i].Status == domain.ReviewRunRunning {
			s.runs[i].NoInject, s.runs[i].AutoInjectReview = true, false
			s.marked = append(s.marked, id)
			return true, nil
		}
	}
	return false, nil
}

// #6810: a --no-inject trigger that restarts a running pass with another model
// makes that pass never delivered before its reviewer relaunches.
func TestTriggerNoInjectRestartMarksRunningPass(t *testing.T) {
	store := &noInjectMarkingStore{fakeStore: &fakeStore{
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1", AgentSessionID: "native-reviewer-1"},
		runs: []domain.ReviewRun{{
			ID: "run-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1",
			Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone,
			AutoInjectReview: true,
		}},
	}}
	worker := liveWorker()
	worker.ReviewerHarness = domain.ReviewerClaudeCode
	worker.ReviewerConfig = domain.AgentConfig{Model: "gpt-5"}
	launcher := &fakeLauncher{alive: true, handle: "review-mer-2"}
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{
		Harness: domain.ReviewerClaudeCode, Config: domain.AgentConfig{Model: "gpt-5-mini"}, NoInject: true,
	})
	if err != nil {
		t.Fatalf("Trigger: %v", err)
	}
	if !res.Created || res.Run.ID != "run-1" || !launcher.spawned {
		t.Fatalf("expected the running pass to restart: res=%+v launcher=%+v", res, launcher)
	}
	if len(store.marked) != 1 || store.marked[0] != "run-1" {
		t.Fatalf("marked = %v, want the restarted run", store.marked)
	}
	if got := store.runs[0]; !got.NoInject || got.AutoInjectReview {
		t.Fatalf("run = %+v, want never delivered", got)
	}
}

// A pass that ended after the trigger read it is not relaunched: the trigger
// fails with a conflict rather than restart a run whose result is recorded.
func TestTriggerNoInjectRestartFailsWhenRunAlreadyEnded(t *testing.T) {
	store := &endedBeforeMarkStore{noInjectMarkingStore: &noInjectMarkingStore{fakeStore: &fakeStore{
		review: &domain.Review{ID: "rev-1", SessionID: "mer-1", Harness: domain.ReviewerClaudeCode, ReviewerHandleID: "review-mer-1", AgentSessionID: "native-reviewer-1"},
		runs: []domain.ReviewRun{{
			ID: "run-1", SessionID: "mer-1", PRURL: "https://github.com/o/r/pull/1", TargetSHA: "sha1",
			Harness: domain.ReviewerClaudeCode, Status: domain.ReviewRunRunning, Verdict: domain.VerdictNone,
		}},
	}}}
	worker := liveWorker()
	worker.ReviewerHarness = domain.ReviewerClaudeCode
	worker.ReviewerConfig = domain.AgentConfig{Model: "gpt-5"}
	launcher := &fakeLauncher{alive: true, handle: "review-mer-2"}
	eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, prAt("sha1"), fakeProjects{}, launcher)

	_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{
		Harness: domain.ReviewerClaudeCode, Config: domain.AgentConfig{Model: "gpt-5-mini"}, NoInject: true,
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
	if launcher.spawned {
		t.Fatal("relaunched a run that had already ended")
	}
}

// endedBeforeMarkStore models the run completing between the trigger reading
// it and marking it.
type endedBeforeMarkStore struct{ *noInjectMarkingStore }

func (s *endedBeforeMarkStore) MarkReviewRunNoInject(context.Context, string) (bool, error) {
	return false, nil
}
