package store_test

import (
	"cmp"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Receipt URLs are inert persistence fixtures; no provider requests occur.
func TestReviewPublicationRoundTrip(t *testing.T) {
	const target = "https://gitlab.example/group/sub/repo/-/merge_requests/39"
	for _, tc := range []struct {
		name        string
		publication *domain.ReviewPublication
		githubID    string
		runURL      string
	}{
		{name: "published", publication: &domain.ReviewPublication{Provider: "gitlab", Status: "published", PRURL: target, TargetSHA: "head", Refs: []domain.ReviewPublicationRef{{Kind: "discussion", ID: "discussion-1"}, {Kind: "note", ID: "note-1"}, {Kind: "note", ID: "summary-1"}}}},
		{name: "blocked", publication: &domain.ReviewPublication{Provider: "gitlab", Status: "blocked", PRURL: target, TargetSHA: "head", Error: "denied"}},
		{name: "unconfirmed", publication: &domain.ReviewPublication{Provider: "gitlab", Status: "unconfirmed", PRURL: target, TargetSHA: "head", Error: "unknown outcome", Refs: []domain.ReviewPublicationRef{{Kind: "draft_note", ID: "42"}}}},
		{name: "absent"},
		{name: "legacy GitHub ID", githubID: "gh-review", runURL: "https://github.com/o/r/pull/1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runURL := cmp.Or(tc.runURL, target)
			s := newTestStore(t)
			now := time.Now().UTC().Truncate(time.Second)
			seedProject(t, s, "mer")
			session, err := s.CreateSession(t.Context(), sampleRecord("mer"))
			if err != nil {
				t.Fatal(err)
			}
			if err := s.UpsertReview(t.Context(), domain.Review{ID: "review", SessionID: session.ID, ProjectID: "mer", Harness: "claude-code", PRURL: runURL, CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatalf("seed review: %v", err)
			}
			running := domain.ReviewRun{ID: "run", ReviewID: "review", SessionID: session.ID, BatchID: "batch", Harness: "claude-code", PRURL: runURL, TargetSHA: "head", Status: domain.ReviewRunRunning, CreatedAt: now}
			running.Publication = tc.publication
			if err := s.InsertReviewRun(t.Context(), running); err != nil {
				t.Fatalf("insert running review: %v", err)
			}
			pending, err := s.ListRunningReviewRunsBySession(t.Context(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(pending) != 1 {
				t.Fatalf("running reviews = %+v, want one", pending)
			}
			if !reflect.DeepEqual(tc.publication, pending[0].Publication) {
				t.Errorf("running publication = %+v, want %+v", pending[0].Publication, tc.publication)
			}

			updated, err := s.UpdateReviewRunResult(t.Context(), "run", domain.ReviewRunComplete, domain.VerdictChangesRequested, "Fix main.go", tc.githubID, false, tc.publication)

			if err != nil {
				t.Fatal(err)
			}
			if !updated {
				t.Fatal("first result did not update the running review")
			}
			run, found, err := s.GetReviewRun(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatal("review run not found")
			}
			if !reflect.DeepEqual(tc.publication, run.Publication) {
				t.Errorf("publication = %+v, want %+v", run.Publication, tc.publication)
			}
			if run.GithubReviewID != tc.githubID {
				t.Errorf("GitHub review ID = %q, want %q", run.GithubReviewID, tc.githubID)
			}
			if run.AutoInjectReview {
				t.Error("stored result did not snapshot the disabled policy")
			}
			byTarget, found, err := s.GetReviewRunBySessionPRAndSHA(t.Context(), session.ID, runURL, "head")
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatal("review run not found")
			}
			if !reflect.DeepEqual(run, byTarget) {
				t.Errorf("target lookup = %+v, want %+v", byTarget, run)
			}
			byHarness, found, err := s.GetReviewRunBySessionPRSHAAndHarness(t.Context(), session.ID, runURL, "head", "claude-code")
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatal("review run not found")
			}
			if !reflect.DeepEqual(run, byHarness) {
				t.Errorf("harness lookup = %+v, want %+v", byHarness, run)
			}
			batch, err := s.ListReviewRunsByBatch(t.Context(), session.ID, "batch")
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual([]domain.ReviewRun{run}, batch) {
				t.Errorf("batch reviews = %+v, want %+v", batch, run)
			}
			runs, err := s.ListReviewRunsBySession(t.Context(), session.ID)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(batch, runs) {
				t.Errorf("session reviews = %+v, want %+v", runs, batch)
			}

			updated, err = s.UpdateReviewRunResult(t.Context(), "run", domain.ReviewRunComplete, domain.VerdictApproved, "replacement", "other", true)

			if err != nil {
				t.Fatal(err)
			}
			if updated {
				t.Error("completed result was replaced")
			}
			unchanged, found, err := s.GetReviewRun(t.Context(), "run")
			if err != nil {
				t.Fatal(err)
			}
			if !found {
				t.Fatal("review run not found")
			}
			if !reflect.DeepEqual(run, unchanged) {
				t.Errorf("completed result changed: got %+v, want %+v", unchanged, run)
			}
		})
	}
}
