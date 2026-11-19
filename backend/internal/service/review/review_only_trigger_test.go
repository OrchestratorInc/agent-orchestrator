package review

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
)

type reviewOnlyTriggerSessions struct{ *fakeStore }

func (s reviewOnlyTriggerSessions) GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok, err := s.fakeStore.GetSession(ctx, id)
	rec.Metadata.WorkspacePath = "/ws"
	return rec, ok, err
}

func TestTriggerRequestedReviewOnlyTargetsLeaveAutoInjectOff(t *testing.T) {
	off := false
	st := &fakeStore{
		sessionAutoInjectReview: &off,
		prs:                     []domain.PullRequest{{URL: "https://github.com/o/r/pull/1", SessionID: "worker-1", ReviewOnly: true}},
	}
	engine := reviewcore.New(reviewcore.Deps{Sessions: reviewOnlyTriggerSessions{st}, PRs: st})
	refresher := &fakePRRefresher{}
	svc := New(nil, st, WithPRRefresher(refresher))
	svc.engineTrigger = engine.TriggerWithOptions
	_, err := svc.TriggerRequested(context.Background(), "worker-1", TriggerRequest{EnableAutoInject: true})
	if !errors.Is(err, ErrInvalid) {
		t.Errorf("err = %v, want an invalid trigger with no eligible PR", err)
	}
	if len(st.autoInjectSets) != 0 || *st.sessionAutoInjectReview || !st.prs[0].ReviewOnly {
		t.Errorf("rejected trigger changed policy: auto-inject=%v sets=%v PR=%+v", *st.sessionAutoInjectReview, st.autoInjectSets, st.prs[0])
	}
	// A review-only PR is not the worker's own, so its branch is looked up for
	// a PR it may have just opened; finding none, nothing is refreshed.
	if len(refresher.calls) != 0 || refresher.finds != 1 {
		t.Fatalf("rejected trigger: refreshed=%v branch lookups=%d, want no refresh and one lookup", refresher.calls, refresher.finds)
	}
}
