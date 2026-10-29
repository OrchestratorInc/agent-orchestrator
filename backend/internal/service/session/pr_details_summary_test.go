package session

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

type prDetailsFakeStore struct {
	*multiPRFakeStore
	details map[string]domain.PullRequestDetails
}

func (f *prDetailsFakeStore) GetPRDetails(_ context.Context, prURL string) (domain.PullRequestDetails, bool, error) {
	details, ok := f.details[prURL]
	return details, ok, nil
}

func newPRDetailsStore(prURL string) *prDetailsFakeStore {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	return &prDetailsFakeStore{
		multiPRFakeStore: &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{URL: prURL, SessionID: "mer-1", Number: 7, Provider: "github"}}},
		details:          map[string]domain.PullRequestDetails{},
	}
}

func TestListPRSummariesCarriesStoredPRDetails(t *testing.T) {
	prURL := "https://github.com/acme/repo/pull/7"
	observed := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	st := newPRDetailsStore(prURL)
	st.details[prURL] = domain.PullRequestDetails{
		Description:          "## Summary\nbody",
		DescriptionTruncated: true,
		Labels:               []domain.PullRequestLabel{{Name: "bug", Color: "d73a4a"}},
		RequestedReviewers:   []domain.PullRequestRequestedReviewer{{Login: "whoisasx"}, {Login: "core", IsTeam: true}},
		Commits:              []domain.PullRequestCommit{{SHA: "aaa111", Headline: "fix", Author: "devone", CommittedAt: observed}},
		CommitCount:          120,
		ObservedAt:           observed,
	}

	got, err := (&Service{store: st}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListPRSummaries = %+v, err %v", got, err)
	}
	pr := got[0]
	if pr.Description != "## Summary\nbody" || !pr.DescriptionTruncated || pr.CommitCount != 120 || !pr.DetailsObservedAt.Equal(observed) {
		t.Fatalf("details = %+v", pr)
	}
	if len(pr.Labels) != 1 || pr.Labels[0].Name != "bug" || pr.Labels[0].Color != "d73a4a" {
		t.Fatalf("labels = %+v", pr.Labels)
	}
	if len(pr.RequestedReviewers) != 2 || !pr.RequestedReviewers[1].IsTeam || pr.RequestedReviewers[0].Login != "whoisasx" {
		t.Fatalf("requested reviewers = %+v", pr.RequestedReviewers)
	}
	if len(pr.Commits) != 1 || pr.Commits[0].SHA != "aaa111" || pr.Commits[0].Author != "devone" || !pr.Commits[0].CommittedAt.Equal(observed) {
		t.Fatalf("commits = %+v", pr.Commits)
	}
}

// Before any observation has carried details the summary says so, instead of
// reporting a PR that has no description, labels or commits.
func TestListPRSummariesLeavesDetailsUnobservedUntilStored(t *testing.T) {
	prURL := "https://github.com/acme/repo/pull/7"
	got, err := (&Service{store: newPRDetailsStore(prURL)}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListPRSummaries = %+v, err %v", got, err)
	}
	pr := got[0]
	if !pr.DetailsObservedAt.IsZero() {
		t.Fatalf("detailsObservedAt = %v, want zero for never-observed details", pr.DetailsObservedAt)
	}
	if pr.Labels == nil || pr.RequestedReviewers == nil || pr.Commits == nil {
		t.Fatalf("lists must be empty, not nil, so the wire shape is []: %+v", pr)
	}
	if pr.Description != "" || pr.CommitCount != 0 {
		t.Fatalf("details = %+v, want zero values", pr)
	}
}

// A store with no details support (older fakes, other stores) must not fail the
// whole PR listing.
func TestListPRSummariesWorksWhenTheStoreHasNoDetails(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker}
	plain := &multiPRFakeStore{fakeStore: st, prs: []domain.PullRequest{{URL: "https://github.com/acme/repo/pull/7", SessionID: "mer-1", Number: 7}}}
	got, err := (&Service{store: plain}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil || len(got) != 1 || !got[0].DetailsObservedAt.IsZero() {
		t.Fatalf("ListPRSummaries = %+v, err %v", got, err)
	}
}

func TestSummarizeChecksListsHeadChecksFailingFirstThenByName(t *testing.T) {
	pr := domain.PullRequest{URL: "u", Number: 1, HeadSHA: "head1", CI: domain.CIFailing}
	checks := []domain.PullRequestCheck{
		{Name: "lint", CommitHash: "head1", Status: domain.PRCheckPassed},
		{Name: "unit", CommitHash: "head1", Status: domain.PRCheckFailed, URL: "https://ci/unit"},
		{Name: "build", CommitHash: "head1", Status: domain.PRCheckInProgress},
		{Name: "e2e", CommitHash: "head1", Status: domain.PRCheckCancelled},
		{Name: "zeta", CommitHash: "head1", Status: domain.PRCheckSkipped},
		{Name: "old-run", CommitHash: "oldsha", Status: domain.PRCheckFailed},
	}
	got := summarizePR(pr, checks, nil, nil, nil, true).CI.Checks
	want := []string{"e2e", "unit", "build", "lint", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("checks = %+v, want %v (the older head's check dropped)", got, want)
	}
	for i, name := range want {
		if got[i].Name != name {
			t.Fatalf("checks[%d] = %q, want %q (failing first, then unfinished, then passed or skipped, each by name): %+v", i, got[i].Name, name, got)
		}
	}
	if got[1].URL != "https://ci/unit" {
		t.Fatalf("check url = %q", got[1].URL)
	}
}

// A merged PR with all-green checks still lists them, so "23/23 passed" can show.
func TestSummarizeChecksListsChecksOfAMergedPassingPR(t *testing.T) {
	pr := domain.PullRequest{URL: "u", Number: 1, HeadSHA: "h", Merged: true, CI: domain.CIPassing}
	checks := []domain.PullRequestCheck{{Name: "unit", CommitHash: "h", Status: domain.PRCheckPassed}}
	ci := summarizePR(pr, checks, nil, nil, nil, true).CI
	if len(ci.Checks) != 1 || len(ci.FailingChecks) != 0 {
		t.Fatalf("ci = %+v, want 1 listed check and no failing ones", ci)
	}
}

func TestSummarizeChecksIsAlwaysAListAndCapped(t *testing.T) {
	if got := summarizePR(domain.PullRequest{URL: "u", Number: 1}, nil, nil, nil, nil, true).CI.Checks; got == nil || len(got) != 0 {
		t.Fatalf("no checks = %#v, want an empty non-nil list", got)
	}
	var many []domain.PullRequestCheck
	for i := 0; i < contract.MaxPullRequestChecks+25; i++ {
		many = append(many, domain.PullRequestCheck{Name: fmt.Sprintf("check-%04d", i), Status: domain.PRCheckPassed})
	}
	if got := summarizePR(domain.PullRequest{URL: "u", Number: 1}, many, nil, nil, nil, true).CI.Checks; len(got) != contract.MaxPullRequestChecks {
		t.Fatalf("listed %d checks, want the cap %d", len(got), contract.MaxPullRequestChecks)
	}
}

// A merged PR keeps who reviewed it, which is part of its record, while the
// unresolved-comment detail stays hidden: nothing can be acted on any more.
func TestSummarizeReviewKeepsSubmittedReviewsForAFinishedPR(t *testing.T) {
	submitted := time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)
	reviews := []domain.PullRequestReview{
		{ID: "r1", Author: "amy", State: domain.ReviewApproved, SubmittedAt: submitted},
		{ID: "r2", Author: "bob", State: domain.ReviewChangesRequest, Body: "needs a test", SubmittedAt: submitted.Add(-time.Hour)},
	}
	comments := []domain.PullRequestComment{{ID: "c1", Author: "bob", File: "a.go", Body: "unresolved note"}}

	for name, pr := range map[string]domain.PullRequest{
		"merged": {URL: "u", Number: 1, Merged: true, Review: domain.ReviewApproved},
		"closed": {URL: "u", Number: 1, Closed: true, Review: domain.ReviewApproved},
	} {
		got := summarizeReview(pr, comments, reviews, nil, true)
		if len(got.Reviews) != 2 || got.Reviews[0].Reviewer != "amy" || got.Reviews[0].Verdict != domain.ReviewApproved || got.Reviews[1].Reviewer != "bob" {
			t.Errorf("%s: reviews = %+v, want amy approved and bob", name, got.Reviews)
		}
		if len(got.UnresolvedBy) != 0 {
			t.Errorf("%s: unresolved-comment detail = %+v, want it hidden for a finished PR", name, got.UnresolvedBy)
		}
	}

	open := summarizeReview(domain.PullRequest{URL: "u", Number: 1}, comments, reviews, nil, true)
	if len(open.Reviews) != 2 || len(open.UnresolvedBy) == 0 {
		t.Fatalf("open PR: reviews = %+v, unresolved = %+v, want both", open.Reviews, open.UnresolvedBy)
	}
}
