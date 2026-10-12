package githubapp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

type automaticReviewFixture struct {
	Store
	prs      []domain.PullRequest
	attempts []string
	opened   []string
}

func (f *automaticReviewFixture) AutomaticReviewCandidates(context.Context) ([]domain.PullRequest, error) {
	return f.prs, nil
}
func (f *automaticReviewFixture) CreateReviewRun(_ context.Context, orgID, prID, sessionID, sha, harness, source string) (domain.ReviewRun, bool, error) {
	f.attempts = append(f.attempts, prID)
	if source != "auto" || harness != "" {
		return domain.ReviewRun{}, false, errors.New("unexpected trigger configuration")
	}
	if prID == "failed" {
		return domain.ReviewRun{}, false, errors.New("unavailable provider")
	}
	return domain.ReviewRun{ID: prID, OrgID: orgID, ReviewSessionID: sessionID, TargetSHA: sha}, true, nil
}
func (f *automaticReviewFixture) OpenReviewTerminal(_ context.Context, _, _, runID, _, _ string) (string, error) {
	f.opened = append(f.opened, runID)
	return "terminal", nil
}

func TestAutomaticReviewsUseStoredFactsAndIsolateFailures(t *testing.T) {
	fixture := &automaticReviewFixture{prs: []domain.PullRequest{
		{ID: "failed", OrgID: "org", SessionID: "worker", HeadSHA: "sha"},
		{ID: "next", OrgID: "org", SessionID: "worker", HeadSHA: "sha"},
	}}
	service := &Service{store: fixture, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := service.triggerAutomaticReviews(context.Background(), fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.attempts) != 2 || len(fixture.opened) != 1 || fixture.opened[0] != "next" {
		t.Fatalf("attempts=%v opened=%v", fixture.attempts, fixture.opened)
	}
	// The embedded Store is nil: any GitHub status refresh or other lookup
	// would panic, so this also exercises the absence of a status polling path.
}
