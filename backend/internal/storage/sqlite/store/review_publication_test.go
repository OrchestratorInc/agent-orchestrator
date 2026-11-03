package store_test

import (
	"cmp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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
			require.NoError(t, s.UpsertProject(t.Context(), domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: now}))
			session, err := s.CreateSession(t.Context(), sampleRecord("mer"))
			require.NoError(t, err)
			require.NoError(t, s.UpsertReview(t.Context(), domain.Review{ID: "review", SessionID: session.ID, ProjectID: "mer", Harness: "claude-code", PRURL: runURL, CreatedAt: now, UpdatedAt: now}))
			running := domain.ReviewRun{ID: "run", ReviewID: "review", SessionID: session.ID, BatchID: "batch", Harness: "claude-code", PRURL: runURL, TargetSHA: "head", Status: domain.ReviewRunRunning, CreatedAt: now}
			running.Publication = tc.publication
			require.NoError(t, s.InsertReviewRun(t.Context(), running))
			pending, err := s.ListRunningReviewRunsBySession(t.Context(), session.ID)
			require.NoError(t, err)
			require.Len(t, pending, 1)
			assert.Equal(t, tc.publication, pending[0].Publication)

			updated, err := s.UpdateReviewRunResult(t.Context(), "run", domain.ReviewRunComplete, domain.VerdictChangesRequested, "Fix main.go", tc.githubID, false, tc.publication)

			require.NoError(t, err)
			require.True(t, updated)
			run, found, err := s.GetReviewRun(t.Context(), "run")
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, tc.publication, run.Publication)
			assert.Equal(t, tc.githubID, run.GithubReviewID)
			assert.False(t, run.AutoInjectReview)
			byTarget, found, err := s.GetReviewRunBySessionPRAndSHA(t.Context(), session.ID, runURL, "head")
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, run, byTarget)
			byHarness, found, err := s.GetReviewRunBySessionPRSHAAndHarness(t.Context(), session.ID, runURL, "head", "claude-code")
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, run, byHarness)
			batch, err := s.ListReviewRunsByBatch(t.Context(), session.ID, "batch")
			require.NoError(t, err)
			assert.Equal(t, []domain.ReviewRun{run}, batch)
			runs, err := s.ListReviewRunsBySession(t.Context(), session.ID)
			require.NoError(t, err)
			assert.Equal(t, batch, runs)

			updated, err = s.UpdateReviewRunResult(t.Context(), "run", domain.ReviewRunComplete, domain.VerdictApproved, "replacement", "other", true)

			require.NoError(t, err)
			assert.False(t, updated)
			unchanged, found, err := s.GetReviewRun(t.Context(), "run")
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, run, unchanged)
		})
	}
}
