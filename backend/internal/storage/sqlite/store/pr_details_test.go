package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func detailsFixture(observedAt time.Time) *domain.PullRequestDetails {
	return &domain.PullRequestDetails{
		Description:          "## Summary\nFixes the checkpoint.",
		DescriptionTruncated: true,
		Labels:               []domain.PullRequestLabel{{Name: "bug", Color: "d73a4a"}},
		RequestedReviewers: []domain.PullRequestRequestedReviewer{
			{Login: "whoisasx", AvatarURL: "https://a/1"},
			{Login: "core", IsTeam: true},
		},
		Commits: []domain.PullRequestCommit{
			{SHA: "aaa111", Headline: "fix: settle checkpoints", Author: "devone", CommittedAt: observedAt.Add(-time.Hour)},
		},
		CommitCount: 120,
		ObservedAt:  observedAt,
	}
}

func TestPRDetailsRoundTripWithTheObservation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", SessionID: r.ID, Number: 1, UpdatedAt: now, Details: detailsFixture(now)}

	if _, ok, err := s.GetPRDetails(ctx, pr.URL); err != nil || ok {
		t.Fatalf("details before any observation: ok=%v err=%v, want not observed", ok, err)
	}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPRDetails(ctx, pr.URL)
	if err != nil || !ok {
		t.Fatalf("get details: ok=%v err=%v", ok, err)
	}
	want := detailsFixture(now)
	if got.Description != want.Description || !got.DescriptionTruncated || got.CommitCount != 120 || !got.ObservedAt.Equal(now) {
		t.Fatalf("details = %+v, want %+v", got, want)
	}
	if len(got.Labels) != 1 || got.Labels[0] != want.Labels[0] {
		t.Fatalf("labels = %+v", got.Labels)
	}
	if len(got.RequestedReviewers) != 2 || got.RequestedReviewers[0] != want.RequestedReviewers[0] || got.RequestedReviewers[1] != want.RequestedReviewers[1] {
		t.Fatalf("requested reviewers = %+v", got.RequestedReviewers)
	}
	if len(got.Commits) != 1 || got.Commits[0].SHA != "aaa111" || got.Commits[0].Author != "devone" || !got.Commits[0].CommittedAt.Equal(want.Commits[0].CommittedAt) {
		t.Fatalf("commits = %+v", got.Commits)
	}
}

// A pass that did not fetch details (metadata-only, local-only, or another
// provider) must not wipe what an earlier pass stored.
func TestPRObservationWithoutDetailsKeepsTheStoredOnes(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", SessionID: r.ID, Number: 1, UpdatedAt: now, Details: detailsFixture(now)}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}

	pr.Details = nil
	pr.Title = "renamed"
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPRDetails(ctx, pr.URL)
	if err != nil || !ok || got.Description == "" || len(got.Commits) != 1 {
		t.Fatalf("details after a details-less write = %+v ok=%v err=%v, want the stored ones", got, ok, err)
	}
}

func TestPRDetailsAreReplacedByTheNextObservation(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", SessionID: r.ID, Number: 1, UpdatedAt: now, Details: detailsFixture(now)}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}

	// The PR lost its labels and reviewers and its description was emptied.
	pr.Details = &domain.PullRequestDetails{ObservedAt: now.Add(time.Minute)}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPRDetails(ctx, pr.URL)
	if err != nil || !ok {
		t.Fatalf("get details: ok=%v err=%v", ok, err)
	}
	if got.Description != "" || got.DescriptionTruncated || len(got.Labels) != 0 || len(got.RequestedReviewers) != 0 || len(got.Commits) != 0 || got.CommitCount != 0 {
		t.Fatalf("details = %+v, want everything cleared", got)
	}
	if got.Labels == nil || got.RequestedReviewers == nil || got.Commits == nil {
		t.Fatalf("empty lists read back as nil: %+v", got)
	}
	if !got.ObservedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("observedAt = %v, want the newer observation", got.ObservedAt)
	}
}

func TestPRDescriptionKeepsMarkdownAndNonASCII(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()
	seedProject(t, s, "mer")
	r, _ := s.CreateSession(ctx, sampleRecord("mer"))
	now := time.Now().UTC().Truncate(time.Second)
	body := "# タイトル\n```go\nfmt.Println(\"é\")\n```\n" + strings.Repeat("x", 4096)
	pr := domain.PullRequest{URL: "https://github.com/o/r/pull/1", SessionID: r.ID, Number: 1, UpdatedAt: now, Details: &domain.PullRequestDetails{Description: body, ObservedAt: now}}
	if err := s.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.GetPRDetails(ctx, pr.URL)
	if err != nil || got.Description != body {
		t.Fatalf("description round-trip: err=%v equal=%v", err, got.Description == body)
	}
}
