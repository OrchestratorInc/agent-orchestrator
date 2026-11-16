package review

import (
	"context"
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
			res, _ := eng.TriggerWithOptions(context.Background(), "mer-1", TriggerOptions{Source: source})
			if res.Created || launcher.spawned || len(store.runs) != 0 {
				t.Fatalf("untargeted trigger reviewed a review-only PR: result=%+v runs=%+v", res, store.runs)
			}
		})
	}
}
