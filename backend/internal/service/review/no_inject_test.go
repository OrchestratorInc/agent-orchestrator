package review

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reviewcore "github.com/aoagents/agent-orchestrator/backend/internal/review"
)

// A --no-inject pass is never delivered, even when the session's auto-inject
// is on (the default) when the result arrives (#6810). Rows the observer may
// already hold for its provider review are suppressed too.
func TestSubmitNoInjectRunIsNeverDelivered(t *testing.T) {
	st := &fakeStore{
		ok: true,
		run: domain.ReviewRun{
			ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning, NoInject: true,
		},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}},
	}
	svc := New(nil, st)

	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987")
	if err != nil {
		t.Fatal(err)
	}
	if run.AutoInjectReview {
		t.Fatalf("no-inject run snapshotted auto-inject on: %+v", run)
	}
	if len(st.suppressedReviews) != 1 || st.suppressedReviews[0] != "pr1#987" {
		t.Fatalf("suppressed = %v, want the posted review suppressed", st.suppressedReviews)
	}
}

func TestSubmitInjectingRunDoesNotSuppress(t *testing.T) {
	st := &fakeStore{
		ok:  true,
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}},
	}
	svc := New(nil, st)
	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987")
	if err != nil {
		t.Fatal(err)
	}
	if !run.AutoInjectReview || len(st.suppressedReviews) != 0 {
		t.Fatalf("run=%+v suppressed=%v, want a normal delivered review", run, st.suppressedReviews)
	}
}

// attachingRefresher records a named PR on the session, like ClaimPR does.
type attachingRefresher struct{ st *fakeStore }

func (r attachingRefresher) RefreshPR(_ context.Context, workerID domain.SessionID, prURL string) error {
	for _, pr := range r.st.prs {
		if pr.URL == prURL {
			return nil
		}
	}
	r.st.prs = append(r.st.prs, domain.PullRequest{URL: prURL, SessionID: workerID})
	return nil
}

func TestTriggerNoInjectMarksAttachedPRReviewOnlyAndLeavesAutoInject(t *testing.T) {
	st := &fakeStore{}
	svc := New(nil, st, WithPRRefresher(attachingRefresher{st: st}))
	var got reviewcore.TriggerOptions
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, opts reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		got = opts
		return reviewcore.TriggerResult{Created: true}, nil
	}
	url := "https://github.com/o/r/pull/9"
	if _, err := svc.TriggerRequested(context.Background(), "mer-1", TriggerRequest{Source: domain.ReviewTriggerAgent, PRURL: url, NoInject: true}); err != nil {
		t.Fatalf("TriggerRequested: %v", err)
	}
	if !got.NoInject {
		t.Fatalf("engine opts = %+v, want NoInject forwarded", got)
	}
	if !st.reviewOnlySets[url] {
		t.Fatalf("review-only sets = %v, want the attached PR marked review-only", st.reviewOnlySets)
	}
	if len(st.autoInjectSets) != 0 {
		t.Fatalf("--no-inject changed the session's auto-inject: %v", st.autoInjectSets)
	}

	// A later trigger without --no-inject makes the session the PR's owner.
	if _, err := svc.TriggerRequested(context.Background(), "mer-1", TriggerRequest{Source: domain.ReviewTriggerAgent, PRURL: url, EnableAutoInject: true}); err != nil {
		t.Fatalf("second TriggerRequested: %v", err)
	}
	if st.reviewOnlySets[url] || st.prs[0].ReviewOnly {
		t.Fatalf("review-only sets = %v, want the PR owned again", st.reviewOnlySets)
	}
}

// A --no-inject review of a PR the session already owns must not disown it.
func TestTriggerNoInjectKeepsAlreadyOwnedPROwned(t *testing.T) {
	url := "https://github.com/o/r/pull/9"
	st := &fakeStore{prs: []domain.PullRequest{{URL: url, SessionID: "mer-1"}}}
	svc := New(nil, st, WithPRRefresher(attachingRefresher{st: st}))
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		return reviewcore.TriggerResult{Created: true}, nil
	}
	if _, err := svc.TriggerRequested(context.Background(), "mer-1", TriggerRequest{Source: domain.ReviewTriggerAgent, PRURL: url, NoInject: true}); err != nil {
		t.Fatalf("TriggerRequested: %v", err)
	}
	if len(st.reviewOnlySets) != 0 || st.prs[0].ReviewOnly {
		t.Fatalf("review-only sets = %v, want an owned PR left owned", st.reviewOnlySets)
	}
}

func TestTriggerRejectsNoInjectWithEnableAutoInject(t *testing.T) {
	svc := New(nil, &fakeStore{})
	svc.engineTrigger = func(context.Context, domain.SessionID, reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		t.Fatal("engine must not run")
		return reviewcore.TriggerResult{}, nil
	}
	_, err := svc.TriggerRequested(context.Background(), "mer-1", TriggerRequest{NoInject: true, EnableAutoInject: true})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("err = %v, want ErrInvalid", err)
	}
}
