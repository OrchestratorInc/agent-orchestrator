package review

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestUntargetedTriggerSkipsReviewOnlyPR(t *testing.T) {
	for _, source := range []domain.ReviewTriggerSource{domain.ReviewTriggerManual, domain.ReviewTriggerAuto} {
		t.Run(string(source), func(t *testing.T) {
			store := &fakeStore{}
			launcher := &fakeLauncher{}
			pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", HeadSHA: "sha1", ReviewOnly: true}
			worker := liveWorker()
			eng := newEngineForTest(store, fakeSessions{rec: worker, ok: true}, fakePRs{prs: []domain.PullRequest{pr}}, fakeProjects{}, launcher)
			res, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: source})
			if source == domain.ReviewTriggerAuto && (err != nil || res.SkipReason != "review_only") {
				t.Fatalf("automatic trigger = %+v, err = %v, want a silent review-only skip", res, err)
			}
			if res.Created || launcher.spawned || len(store.runs) != 0 {
				t.Fatalf("untargeted trigger reviewed a review-only PR: result=%+v runs=%+v", res, store.runs)
			}
		})
	}
}

func TestUntargetedManualTriggerRejectsReviewOnlyPR(t *testing.T) {
	for _, source := range []domain.ReviewTriggerSource{"", domain.ReviewTriggerManual, domain.ReviewTriggerAgent} {
		t.Run(string(source), func(t *testing.T) {
			store := &fakeStore{}
			launcher := &fakeLauncher{}
			pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", HeadSHA: "sha1", ReviewOnly: true}
			eng := newEngineForTest(store, fakeSessions{rec: liveWorker(), ok: true}, fakePRs{prs: []domain.PullRequest{pr}}, fakeProjects{}, launcher)
			_, err := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: source})
			if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "review-only") {
				t.Errorf("err = %v, want an invalid trigger explaining the review-only targets", err)
			}
			if launcher.spawned || len(store.runs) != 0 {
				t.Fatalf("rejected trigger started a review: %+v", store.runs)
			}
		})
	}
}
