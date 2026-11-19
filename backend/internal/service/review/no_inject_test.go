package review

import (
	"context"
	"errors"
	"strings"
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

func (r attachingRefresher) RefreshPR(_ context.Context, workerID domain.SessionID, prURL string, reviewOnly bool) error {
	for _, pr := range r.st.prs {
		if pr.URL == prURL {
			return nil
		}
	}
	r.st.prs = append(r.st.prs, domain.PullRequest{URL: prURL, SessionID: workerID, ReviewOnly: reviewOnly})
	if r.st.reviewOnlySets == nil {
		r.st.reviewOnlySets = map[string]bool{}
	}
	r.st.reviewOnlySets[prURL] = reviewOnly
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

// A submit that completed a --no-inject run but failed to suppress its observed
// rows is finished by the reviewer's retry, which finds the run complete.
func TestSubmitNoInjectRetryFinishesSuppression(t *testing.T) {
	st := &fakeStore{
		ok: true,
		run: domain.ReviewRun{
			ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning, NoInject: true,
		},
		prs:         []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}},
		suppressErr: errors.New("db locked"),
	}
	svc := New(nil, st)
	if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987"); err == nil {
		t.Fatal("want the suppression error")
	}
	if st.run.Status != domain.ReviewRunCompleted {
		t.Fatalf("run = %+v, want completed by the first submit", st.run)
	}
	st.suppressErr = nil
	if _, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987"); err != nil {
		t.Fatal(err)
	}
	if len(st.suppressedReviews) != 1 || st.suppressedReviews[0] != "pr1#987" {
		t.Fatalf("suppressed = %v, want the retry to suppress the posted review", st.suppressedReviews)
	}
}

// markedDuringSubmitStore models a --no-inject restart marking the run after
// Submit read it and before its result was written.
type markedDuringSubmitStore struct{ *fakeStore }

func (s markedDuringSubmitStore) TransitionReviewRun(ctx context.Context, id string, from []domain.ReviewRunStatus, to domain.ReviewRunStatus, patch domain.ReviewRunPatch) (bool, error) {
	updated, err := s.fakeStore.TransitionReviewRun(ctx, id, from, to, patch)
	s.run.NoInject, s.run.AutoInjectReview = true, false
	return updated, err
}

func TestSubmitUsesNoInjectMarkedDuringSubmit(t *testing.T) {
	st := &fakeStore{
		ok:  true,
		run: domain.ReviewRun{ID: "run-1", SessionID: "mer-1", PRURL: "pr1", TargetSHA: "sha1", Status: domain.ReviewRunRunning},
		prs: []domain.PullRequest{{URL: "pr1", HeadSHA: "sha1"}},
	}
	svc := New(nil, markedDuringSubmitStore{st})
	run, err := svc.Submit(context.Background(), "mer-1", "run-1", domain.VerdictChangesRequested, "fix it", "987")
	if err != nil {
		t.Fatal(err)
	}
	if run.AutoInjectReview || !run.NoInject {
		t.Fatalf("run = %+v, want the stored --no-inject mark", run)
	}
	if len(st.suppressedReviews) != 1 || st.suppressedReviews[0] != "pr1#987" {
		t.Fatalf("suppressed = %v, want the posted review suppressed", st.suppressedReviews)
	}
}

// #6810: a PR the session only reviewed is not its own, so a worker that just
// opened its own PR and triggers a review before the next poll still has its
// branch looked up and the new PR attached.
func TestTriggerRequestedAttachesTheBranchPRBesideAReviewOnlyPR(t *testing.T) {
	var order []string
	url := "https://github.com/o/r/pull/9"
	st := &fakeStore{prs: []domain.PullRequest{{URL: "https://github.com/x/y/pull/3", SessionID: "mer-1", ReviewOnly: true}}}
	refresher := &fakePRRefresher{order: &order, branchPRs: []string{url}}
	svc := New(nil, st, WithPRRefresher(refresher))
	svc.engineTrigger = func(_ context.Context, _ domain.SessionID, _ reviewcore.TriggerOptions) (reviewcore.TriggerResult, error) {
		order = append(order, "trigger")
		return reviewcore.TriggerResult{Created: true}, nil
	}
	if _, err := svc.TriggerRequested(context.Background(), "mer-1", TriggerRequest{Source: domain.ReviewTriggerAgent}); err != nil {
		t.Fatalf("TriggerRequested: %v", err)
	}
	if refresher.finds != 1 {
		t.Fatalf("branch lookups = %d, want 1 despite the review-only PR", refresher.finds)
	}
	if got := strings.Join(order, ","); got != "refresh "+url+",trigger" {
		t.Fatalf("order = %v, want the worker's own branch PR attached before planning", order)
	}
}
