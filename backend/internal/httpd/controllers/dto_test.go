package controllers_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
)

func TestNewSessionPRSummaryMapsProviderReviewEntries(t *testing.T) {
	submitted := time.Date(2026, 7, 23, 12, 0, 0, 0, time.UTC)
	in := sessionsvc.PRSummary{
		URL:             "https://github.com/o/r/pull/7",
		Author:          "alice",
		AuthorAvatarURL: "https://avatars.githubusercontent.com/u/123?v=4",
		Review: sessionsvc.PRReviewSummary{
			Decision:              domain.ReviewChangesRequest,
			UnresolvedThreadCount: intPtrForTest(2),
			UnresolvedBy: []sessionsvc.PRUnresolvedReviewer{{
				ReviewerID: "bob",
				Count:      1,
				Links:      []sessionsvc.PRReviewCommentLink{{URL: "comment-url", ReviewID: "4876751117", Body: "please fix this", AutoInjectReview: false}},
			}},
			Reviews: []sessionsvc.PRReviewEntry{{
				Reviewer:         "alice",
				Verdict:          domain.ReviewApproved,
				Body:             "looks good to me",
				URL:              "https://github.com/o/r/pull/7#pullrequestreview-1",
				SubmittedAt:      submitted,
				IsBot:            true,
				AutoInjectReview: false,
			}},
		},
	}

	got := controllers.NewSessionPRSummary(in)
	if got.Author != "alice" || got.AuthorAvatarURL != "https://avatars.githubusercontent.com/u/123?v=4" {
		t.Fatalf("author metadata = %+v", got)
	}
	if len(got.Review.Reviews) != 1 {
		t.Fatalf("review entries = %+v, want 1", got.Review.Reviews)
	}
	entry := got.Review.Reviews[0]
	if entry.ReviewerID != "alice" {
		t.Fatalf("reviewerId = %q, want alice", entry.ReviewerID)
	}
	if entry.Verdict != domain.ReviewApproved {
		t.Fatalf("verdict = %q, want approved", entry.Verdict)
	}
	if entry.Body != "looks good to me" {
		t.Fatalf("body = %q", entry.Body)
	}
	if entry.ReviewURL != "https://github.com/o/r/pull/7#pullrequestreview-1" {
		t.Fatalf("reviewUrl = %q", entry.ReviewURL)
	}
	if !entry.SubmittedAt.Equal(submitted) {
		t.Fatalf("submittedAt = %v, want %v", entry.SubmittedAt, submitted)
	}
	if !entry.IsBot {
		t.Fatalf("isBot = false, want true")
	}
	if entry.AutoInjectReview {
		t.Fatal("autoInjectReview = true, want false")
	}
	if got.Review.UnresolvedBy[0].Links[0].ReviewID != "4876751117" {
		t.Fatalf("reviewId = %q, want 4876751117", got.Review.UnresolvedBy[0].Links[0].ReviewID)
	}
	if len(got.Review.UnresolvedBy) != 1 || len(got.Review.UnresolvedBy[0].Links) != 1 || got.Review.UnresolvedBy[0].Links[0].AutoInjectReview {
		t.Fatalf("unresolved comment links = %+v, want one not-injected link", got.Review.UnresolvedBy)
	}
	if got.Review.UnresolvedBy[0].Links[0].Body != "please fix this" {
		t.Fatalf("unresolved comment body = %q, want body text", got.Review.UnresolvedBy[0].Links[0].Body)
	}
	if got.Review.UnresolvedThreadCount == nil || *got.Review.UnresolvedThreadCount != 2 {
		t.Fatalf("unresolvedThreadCount = %v, want 2", got.Review.UnresolvedThreadCount)
	}
}

func TestNewSessionPRSummaryThreadCountUnknownVsObservedZero(t *testing.T) {
	// An unknown count must stay absent from the wire, while an observed zero
	// must serialize explicitly rather than being dropped by omitempty.
	unknown, err := json.Marshal(controllers.NewSessionPRSummary(sessionsvc.PRSummary{}))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(unknown), "unresolvedThreadCount") {
		t.Fatalf("unknown count serialized: %s", unknown)
	}

	zero, err := json.Marshal(controllers.NewSessionPRSummary(sessionsvc.PRSummary{
		Review: sessionsvc.PRReviewSummary{UnresolvedThreadCount: intPtrForTest(0)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(zero), `"unresolvedThreadCount":0`) {
		t.Fatalf("observed zero not serialized: %s", zero)
	}
}

func intPtrForTest(v int) *int { return &v }

func TestNewSessionPRSummaryExposesCIFailureInjectionPolicy(t *testing.T) {
	got := controllers.NewSessionPRSummary(sessionsvc.PRSummary{
		CI: sessionsvc.PRCISummary{State: domain.CIFailing, AutoInjectCI: false},
	})
	payload, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if got.CI.State != domain.CIFailing {
		t.Fatalf("CI state = %q, want failing", got.CI.State)
	}
	if !strings.Contains(string(payload), `"autoInjectCI":false`) {
		t.Fatalf("CI summary payload = %s, want explicit disabled injection policy", payload)
	}
}

func TestNewSessionPRSummaryMapsPRDetailsAndKeepsListsAsArrays(t *testing.T) {
	committed := time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)
	observed := committed.Add(time.Hour)
	got := controllers.NewSessionPRSummary(sessionsvc.PRSummary{
		Description:          "## Summary",
		DescriptionTruncated: true,
		Labels:               []contract.PullRequestLabel{{Name: "bug", Color: "d73a4a"}},
		RequestedReviewers:   []contract.PullRequestRequestedReviewer{{Login: "core", IsTeam: true}},
		Commits:              []contract.PullRequestCommit{{SHA: "aaa111", Headline: "fix", Author: "devone", CommittedAt: committed}},
		CommitCount:          120,
		DetailsObservedAt:    observed,
	})
	if got.Description != "## Summary" || !got.DescriptionTruncated || got.CommitCount != 120 {
		t.Fatalf("details = %+v", got)
	}
	if got.DetailsObservedAt == nil || !got.DetailsObservedAt.Equal(observed) {
		t.Fatalf("detailsObservedAt = %v, want %v", got.DetailsObservedAt, observed)
	}
	if len(got.Labels) != 1 || got.Labels[0].Name != "bug" || len(got.RequestedReviewers) != 1 || !got.RequestedReviewers[0].IsTeam {
		t.Fatalf("labels/reviewers = %+v / %+v", got.Labels, got.RequestedReviewers)
	}
	if len(got.Commits) != 1 || got.Commits[0].CommittedAt == nil || !got.Commits[0].CommittedAt.Equal(committed) {
		t.Fatalf("commits = %+v", got.Commits)
	}

	// Never observed: the lists stay [] on the wire (clients map over them) and
	// detailsObservedAt is absent, so "not loaded" is distinguishable from "none".
	raw, err := json.Marshal(controllers.NewSessionPRSummary(sessionsvc.PRSummary{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"labels":[]`, `"requestedReviewers":[]`, `"commits":[]`, `"commitCount":0`} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("empty summary is missing %s: %s", want, raw)
		}
	}
	if strings.Contains(string(raw), "detailsObservedAt") {
		t.Errorf("unobserved details serialized detailsObservedAt: %s", raw)
	}
}

func TestNewSessionPRSummaryMapsEveryCheckAndKeepsTheListAnArray(t *testing.T) {
	got := controllers.NewSessionPRSummary(sessionsvc.PRSummary{CI: sessionsvc.PRCISummary{
		State: domain.CIFailing,
		Checks: []contract.PullRequestCheckEntry{
			{Name: "unit", Status: domain.PRCheckFailed, URL: "https://ci/unit"},
			{Name: "lint", Status: domain.PRCheckPassed},
		},
	}})
	if len(got.CI.Checks) != 2 || got.CI.Checks[0].Name != "unit" || got.CI.Checks[0].Status != domain.PRCheckFailed || got.CI.Checks[0].URL != "https://ci/unit" {
		t.Fatalf("checks = %+v", got.CI.Checks)
	}
	raw, err := json.Marshal(controllers.NewSessionPRSummary(sessionsvc.PRSummary{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"checks":[]`) {
		t.Fatalf("an unobserved PR must serialize checks as [], got %s", raw)
	}
}
