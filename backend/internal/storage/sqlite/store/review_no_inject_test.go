package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// #6810: a --no-inject AO review's observed rows are suppressed, and only its
// provider review id is listed.
func TestNoInjectAOReviewIDsAndSuppression(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	url := "https://github.com/acme/repo/pull/1"
	reviews := []domain.PullRequestReview{
		{ID: "PRR_ao", Author: "me", State: domain.ReviewChangesRequest, URL: url + "#pullrequestreview-987", SubmittedAt: now, AutoInjectReview: true},
		{ID: "PRR_human", Author: "you", State: domain.ReviewChangesRequest, URL: url + "#pullrequestreview-555", SubmittedAt: now, AutoInjectReview: true},
	}
	threads := []domain.PullRequestReviewThread{{ThreadID: "t1", Path: "a.go", Line: 1, UpdatedAt: now}, {ThreadID: "t2", Path: "b.go", Line: 2, UpdatedAt: now}}
	comments := []domain.PullRequestComment{
		{ID: "c1", ThreadID: "t1", ReviewID: "987", Author: "me", File: "a.go", Line: 1, Body: "fix", CreatedAt: now, AutoInjectReview: true},
		{ID: "c2", ThreadID: "t2", ReviewID: "555", Author: "you", File: "b.go", Line: 2, Body: "fix", CreatedAt: now, AutoInjectReview: true},
	}
	if err := s.WriteSCMObservation(ctx, domain.PullRequest{URL: url, SessionID: r.ID, Number: 1, HeadSHA: "h", UpdatedAt: now, ObservedAt: now, ReviewObservedAt: now},
		nil, reviews, threads, comments, ports.ReviewWriteReplace); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertReview(ctx, domain.Review{ID: "rev", SessionID: r.ID, ProjectID: "mer", Harness: "claude-code", PRURL: url, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.ReviewRun{
		{ID: "quiet", ReviewID: "rev", SessionID: r.ID, Harness: "claude-code", PRURL: url, TargetSHA: "h", Status: domain.ReviewRunRunning, CreatedAt: now, NoInject: true},
		{ID: "loud", ReviewID: "rev", SessionID: r.ID, Harness: "codex", PRURL: url, TargetSHA: "h", Status: domain.ReviewRunRunning, CreatedAt: now},
	} {
		if err := s.InsertReviewRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	got, ok, err := s.GetReviewRun(ctx, "quiet")
	if err != nil || !ok || !got.NoInject {
		t.Fatalf("run = %+v ok=%v err=%v, want NoInject persisted", got, ok, err)
	}
	if _, err := s.UpdateReviewRunResult(ctx, "quiet", domain.ReviewRunComplete, domain.VerdictChangesRequested, "b", "987", false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateReviewRunResult(ctx, "loud", domain.ReviewRunComplete, domain.VerdictChangesRequested, "b", "555", true); err != nil {
		t.Fatal(err)
	}
	ids, err := s.NoInjectAOReviewIDs(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 1 || !ids["987"] {
		t.Fatalf("ids = %v, want only the --no-inject review", ids)
	}

	if err := s.SuppressAOReviewInject(ctx, url, "987"); err != nil {
		t.Fatal(err)
	}
	storedReviews, err := s.ListPRReviews(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	for _, rv := range storedReviews {
		if want := rv.ID != "PRR_ao"; rv.AutoInjectReview != want {
			t.Fatalf("review %s AutoInjectReview = %v, want %v", rv.ID, rv.AutoInjectReview, want)
		}
	}
	storedComments, err := s.ListPRComments(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range storedComments {
		if want := c.ID != "c1"; c.AutoInjectReview != want {
			t.Fatalf("comment %s AutoInjectReview = %v, want %v", c.ID, c.AutoInjectReview, want)
		}
	}
}

func TestPRReviewOnlyRoundTripAndTakeoverClearsIt(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	reviewer, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	url := "https://github.com/acme/repo/pull/7"
	pr := domain.PullRequest{URL: url, SessionID: reviewer.ID, Number: 7, UpdatedAt: time.Now().UTC()}
	if _, err := s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, false); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.SetPRReviewOnly(ctx, url, owner.ID, true); err != nil || ok {
		t.Fatalf("set on a PR of another session: ok=%v err=%v, want no change", ok, err)
	}
	if ok, err := s.SetPRReviewOnly(ctx, url, reviewer.ID, true); err != nil || !ok {
		t.Fatalf("set review-only: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetPR(ctx, url)
	if err != nil || !got.ReviewOnly {
		t.Fatalf("pr = %+v err=%v, want review-only", got, err)
	}
	// A re-claim by the same session (a review trigger refresh) keeps it.
	if _, err := s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, false); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.GetPR(ctx, url); !got.ReviewOnly {
		t.Fatal("a same-session re-claim cleared review-only")
	}
	// Another session taking the PR over is its real owner.
	pr.SessionID = owner.ID
	if _, err := s.ClaimPR(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve, true); err != nil {
		t.Fatal(err)
	}
	if got, _, _ = s.GetPR(ctx, url); got.ReviewOnly || got.SessionID != owner.ID {
		t.Fatalf("pr = %+v, want the new owner and review-only cleared", got)
	}
}
