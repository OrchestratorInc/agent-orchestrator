package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
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
	if _, err := transitionNoInjectTestRun(ctx, t, s, "quiet", domain.ReviewRunCompleted, domain.VerdictChangesRequested, "b", "987", false); err != nil {
		t.Fatal(err)
	}
	if _, err := transitionNoInjectTestRun(ctx, t, s, "loud", domain.ReviewRunCompleted, domain.VerdictChangesRequested, "b", "555", true); err != nil {
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

// #6810: every --no-inject pass is listed with when it ran: open while running,
// closed once it completes, fails, or is cancelled. A normal pass is not.
func TestNoInjectAOReviewWindows(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	url := "https://github.com/acme/repo/pull/2"
	if err := s.UpsertReview(ctx, domain.Review{ID: "rev", SessionID: r.ID, ProjectID: "mer", Harness: "claude-code", PRURL: url, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.ReviewRun{
		{ID: "exited", ReviewID: "rev", SessionID: r.ID, Harness: "claude-code", PRURL: url, TargetSHA: "h1", Status: domain.ReviewRunRunning, CreatedAt: now, NoInject: true},
		{ID: "submitted", ReviewID: "rev", SessionID: r.ID, Harness: "claude-code", PRURL: url, TargetSHA: "h2", Status: domain.ReviewRunRunning, CreatedAt: now, NoInject: true},
		{ID: "loud", ReviewID: "rev", SessionID: r.ID, Harness: "codex", PRURL: url, TargetSHA: "h1", Status: domain.ReviewRunRunning, CreatedAt: now},
	} {
		if err := s.InsertReviewRun(ctx, run); err != nil {
			t.Fatal(err)
		}
	}
	windows, err := s.NoInjectAOReviewWindows(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 || !windows[0].End.IsZero() || !windows[1].End.IsZero() || !windows[0].Start.Equal(now) {
		t.Fatalf("windows = %+v, want both running --no-inject passes open from %v", windows, now)
	}

	if _, err := transitionNoInjectTestRun(ctx, t, s, "submitted", domain.ReviewRunCompleted, domain.VerdictChangesRequested, "b", "987", false); err != nil {
		t.Fatal(err)
	}
	if _, err := transitionNoInjectTestRun(ctx, t, s, "exited", domain.ReviewRunFailed, domain.VerdictNone, "reviewer exited", "", false); err != nil {
		t.Fatal(err)
	}
	windows, err = s.NoInjectAOReviewWindows(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	if len(windows) != 2 {
		t.Fatalf("windows = %+v, want the submitted and the exited pass", windows)
	}
	for _, w := range windows {
		if w.End.IsZero() || w.End.Before(now) {
			t.Fatalf("windows = %+v, want both closed at or after %v", windows, now)
		}
	}

	// Cancelling a running pass closes its window too.
	if err := s.InsertReviewRun(ctx, domain.ReviewRun{ID: "cancelled", ReviewID: "rev", SessionID: r.ID, Harness: "claude-code", PRURL: url, TargetSHA: "h3", Status: domain.ReviewRunRunning, CreatedAt: now, NoInject: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TransitionReviewRunsBySession(ctx, r.ID, nil, domain.ActiveReviewRunStatuses, domain.ReviewRunCancelled, domain.ReviewRunPatch{EndedAt: &now}); err != nil {
		t.Fatal(err)
	}
	windows, err = s.NoInjectAOReviewWindows(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range windows {
		if w.End.IsZero() {
			t.Fatalf("windows = %+v, want every ended pass closed", windows)
		}
	}
}

// #6810: a --no-inject trigger restarting a running pass makes it never
// delivered; an ended pass is left alone.
func TestMarkReviewRunNoInject(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	url := "https://github.com/acme/repo/pull/3"
	if err := s.UpsertReview(ctx, domain.Review{ID: "rev", SessionID: r.ID, ProjectID: "mer", Harness: "claude-code", PRURL: url, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertReviewRun(ctx, domain.ReviewRun{ID: "run", ReviewID: "rev", SessionID: r.ID, Harness: "claude-code", PRURL: url, TargetSHA: "h", Status: domain.ReviewRunRunning, CreatedAt: now, AutoInjectReview: true}); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkReviewRunNoInject(ctx, "run"); err != nil || !ok {
		t.Fatalf("mark running run: ok=%v err=%v", ok, err)
	}
	got, _, err := s.GetReviewRun(ctx, "run")
	if err != nil || !got.NoInject || got.AutoInjectReview {
		t.Fatalf("run = %+v err=%v, want no-inject and not auto-injected", got, err)
	}
	if _, err := transitionNoInjectTestRun(ctx, t, s, "run", domain.ReviewRunFailed, domain.VerdictNone, "x", "", false); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkReviewRunNoInject(ctx, "run"); err != nil || ok {
		t.Fatalf("mark ended run: ok=%v err=%v, want no change", ok, err)
	}
}

// #6810: a result submitted with auto-inject on, for a run a --no-inject
// restart marked meanwhile, is stored with auto-inject off.
func TestUpdateReviewRunResultKeepsNoInjectRunQuiet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	url := "https://github.com/acme/repo/pull/4"
	if err := s.UpsertReview(ctx, domain.Review{ID: "rev", SessionID: r.ID, ProjectID: "mer", Harness: "claude-code", PRURL: url, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, run := range []domain.ReviewRun{
		{ID: "quiet", ReviewID: "rev", SessionID: r.ID, Harness: "claude-code", PRURL: url, TargetSHA: "h1", Status: domain.ReviewRunRunning, CreatedAt: now, NoInject: true},
		{ID: "loud", ReviewID: "rev", SessionID: r.ID, Harness: "codex", PRURL: url, TargetSHA: "h1", Status: domain.ReviewRunRunning, CreatedAt: now},
	} {
		if err := s.InsertReviewRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if ok, err := transitionNoInjectTestRun(ctx, t, s, run.ID, domain.ReviewRunCompleted, domain.VerdictChangesRequested, "b", "", true); err != nil || !ok {
			t.Fatalf("update %s: ok=%v err=%v", run.ID, ok, err)
		}
		got, _, err := s.GetReviewRun(ctx, run.ID)
		if err != nil {
			t.Fatal(err)
		}
		if want := !run.NoInject; got.AutoInjectReview != want {
			t.Fatalf("run %s AutoInjectReview = %v, want %v", run.ID, got.AutoInjectReview, want)
		}
	}
}

func transitionNoInjectTestRun(ctx context.Context, t *testing.T, s *store.Store, id string, status domain.ReviewRunStatus, verdict domain.ReviewVerdict, body, reviewID string, inject bool) (bool, error) {
	t.Helper()
	now := time.Now().UTC()
	return s.TransitionReviewRun(ctx, id, domain.ActiveReviewRunStatuses, status, domain.ReviewRunPatch{Verdict: verdict, Body: &body, GithubReviewID: &reviewID, AutoInjectReview: &inject, EndedAt: &now})
}

// #6810: a --no-inject pass never reaches the worker, so a worker that
// triggered one gets no review-done notice; an orchestrator that triggered it
// for the worker still does. Both at insert and when a restart marks a run.
func TestNoInjectKeepsNonWorkerRequester(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	worker, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	orch, err := s.CreateSession(ctx, sampleRecord("mer"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	url := "https://github.com/acme/repo/pull/5"
	if err := s.UpsertReview(ctx, domain.Review{ID: "rev", SessionID: worker.ID, ProjectID: "mer", Harness: "claude-code", PRURL: url, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		id        string
		sha       string
		requester domain.SessionID
		noInject  bool
		want      domain.SessionID
	}{
		{id: "self-insert", sha: "h1", requester: worker.ID, noInject: true, want: ""},
		{id: "orch-insert", sha: "h2", requester: orch.ID, noInject: true, want: orch.ID},
		{id: "self-mark", sha: "h3", requester: worker.ID, want: ""},
		{id: "orch-mark", sha: "h4", requester: orch.ID, want: orch.ID},
	}
	for _, c := range cases {
		run := domain.ReviewRun{ID: c.id, ReviewID: "rev", SessionID: worker.ID, Harness: "claude-code", PRURL: url, TargetSHA: c.sha, Status: domain.ReviewRunRunning, CreatedAt: now, NoInject: c.noInject, RequesterSessionID: c.requester}
		if err := s.InsertReviewRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if !c.noInject {
			if ok, err := s.MarkReviewRunNoInject(ctx, c.id); err != nil || !ok {
				t.Fatalf("mark %s: ok=%v err=%v", c.id, ok, err)
			}
		}
		got, _, err := s.GetReviewRun(ctx, c.id)
		if err != nil {
			t.Fatal(err)
		}
		if !got.NoInject || got.RequesterSessionID != c.want {
			t.Fatalf("run %s = no_inject %v requester %q, want no_inject and requester %q", c.id, got.NoInject, got.RequesterSessionID, c.want)
		}
	}
}
