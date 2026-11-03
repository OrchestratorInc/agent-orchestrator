//go:build e2e_test

package reviewpublication_test

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	reviewsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/review"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// Goose uses process-global migration sources. Keep these scenarios serial.
func TestReceiptMigrationUpgradeDownUp(t *testing.T) {
	dir := t.TempDir()
	s := openReceiptStore(t, dir)
	session, run := seedReceiptRun(t, s, false)
	require.NoError(t, s.Close())

	db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, db.Close()) })
	require.NoError(t, goose.DownToContext(t.Context(), db, "migrations", 20261101055144579))
	_, err = db.ExecContext(t.Context(), `UPDATE review_run SET status = 'complete', verdict = 'approved', body = 'legacy body', github_review_id = 'legacy-review' WHERE id = ?`, run.ID)
	require.NoError(t, err)
	require.NoError(t, db.Close())

	s = openReceiptStore(t, dir)
	legacy, found, err := s.GetReviewRun(t.Context(), run.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Nil(t, legacy.Publication)
	assert.Equal(t, "legacy body", legacy.Body)
	assert.Equal(t, "legacy-review", legacy.GithubReviewID)
	require.NoError(t, s.Close())

	db, err = sql.Open("sqlite", filepath.Join(dir, "ao.db"))
	require.NoError(t, err)
	var publication string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT publication_json FROM review_run WHERE id = ?`, run.ID).Scan(&publication))
	assert.Equal(t, "null", publication)
	_, err = db.ExecContext(t.Context(), `UPDATE review_run SET publication_json = '{' WHERE id = ?`, run.ID)
	require.Error(t, err)

	// Updating the receipt alone must emit the trigger-owned change event.
	receipt := `{"provider":"github","status":"blocked","prUrl":"https://github.com/o/r/pull/1","targetSha":"head","error":"denied"}`
	_, err = db.ExecContext(t.Context(), `UPDATE review_run SET publication_json = ? WHERE id = ?`, receipt, run.ID)
	require.NoError(t, err)
	var payload string
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT payload FROM change_log WHERE session_id = ? AND event_type = 'review_run_updated' ORDER BY seq DESC LIMIT 1`, session.ID).Scan(&payload))
	var event struct {
		Publication domain.ReviewPublication `json:"publication"`
	}
	require.NoError(t, json.Unmarshal([]byte(payload), &event))
	assert.Equal(t, "blocked", event.Publication.Status)

	require.NoError(t, goose.DownToContext(t.Context(), db, "migrations", 20261101055144579))
	_, err = db.ExecContext(t.Context(), `UPDATE review_run SET body = 'after down' WHERE id = ?`, run.ID)
	require.NoError(t, err)
	require.NoError(t, db.QueryRowContext(t.Context(), `SELECT payload FROM change_log WHERE session_id = ? AND event_type = 'review_run_updated' ORDER BY seq DESC LIMIT 1`, session.ID).Scan(&payload))
	var oldEvent map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(payload), &oldEvent))
	assert.NotContains(t, oldEvent, "publication")
	require.NoError(t, db.Close())

	s = openReceiptStore(t, dir)
	afterUp, found, err := s.GetReviewRun(t.Context(), run.ID)
	require.NoError(t, err)
	require.True(t, found)
	assert.Nil(t, afterUp.Publication)
	assert.Equal(t, "after down", afterUp.Body)
}

func TestSubmitPublicationSnapshotsFirstResultAtomically(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			dir := t.TempDir()
			s := openReceiptStore(t, dir)
			session, run := seedReceiptRun(t, s, !enabled)
			changed, err := s.SetSessionAutoInjectReview(t.Context(), session.ID, enabled, time.Now().UTC())
			require.NoError(t, err)
			require.True(t, changed)
			// Target is inert receipt data; the service performs no SCM requests.
			publication := &domain.ReviewPublication{Provider: "github", Status: "published", PRURL: run.PRURL, TargetSHA: run.TargetSHA, Refs: []domain.ReviewPublicationRef{{Kind: "review", ID: "123"}}}
			input := reviewsvc.SubmittedReview{RunID: run.ID, Verdict: domain.VerdictApproved, Body: "review complete", Publication: publication}
			svc := reviewsvc.New(nil, s)

			result, err := svc.SubmitMany(t.Context(), session.ID, []reviewsvc.SubmittedReview{input})

			require.NoError(t, err)
			require.Len(t, result, 1)
			assert.Equal(t, enabled, result[0].AutoInjectReview)
			assert.Equal(t, "123", result[0].GithubReviewID)
			db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
			require.NoError(t, err)
			t.Cleanup(func() { assert.NoError(t, db.Close()) })
			var payload string
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT payload FROM change_log WHERE session_id = ? AND event_type = 'review_run_updated' ORDER BY seq DESC LIMIT 1`, session.ID).Scan(&payload))
			var event struct {
				Status           string                    `json:"status"`
				Verdict          string                    `json:"verdict"`
				AutoInjectReview bool                      `json:"autoInjectReview"`
				Publication      *domain.ReviewPublication `json:"publication"`
			}
			require.NoError(t, json.Unmarshal([]byte(payload), &event))
			assert.Equal(t, "complete", event.Status)
			assert.Equal(t, "approved", event.Verdict)
			assert.Equal(t, enabled, event.AutoInjectReview)
			assert.Equal(t, publication, event.Publication)

			changed, err = s.SetSessionAutoInjectReview(t.Context(), session.ID, !enabled, time.Now().UTC())
			require.NoError(t, err)
			require.True(t, changed)
			_, err = svc.SubmitMany(t.Context(), session.ID, []reviewsvc.SubmittedReview{input})
			require.NoError(t, err)
			stored, found, err := s.GetReviewRun(t.Context(), run.ID)
			require.NoError(t, err)
			require.True(t, found)
			assert.Equal(t, enabled, stored.AutoInjectReview)
			assert.Equal(t, publication, stored.Publication)
			var updates int
			require.NoError(t, db.QueryRowContext(t.Context(), `SELECT count(*) FROM change_log WHERE session_id = ? AND event_type = 'review_run_updated'`, session.ID).Scan(&updates))
			assert.Equal(t, 1, updates)
		})
	}
}

func openReceiptStore(t *testing.T, dir string) *sqlite.Store {
	t.Helper()
	s, err := sqlite.Open(dir)
	require.NoError(t, err)
	t.Cleanup(func() { assert.NoError(t, s.Close()) })
	return s
}

func seedReceiptRun(t *testing.T, s *sqlite.Store, enabled bool) (domain.SessionRecord, domain.ReviewRun) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, s.UpsertProject(t.Context(), domain.ProjectRecord{ID: "mer", Path: t.TempDir(), RegisteredAt: now}))
	session, err := s.CreateSession(t.Context(), domain.SessionRecord{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, AutoInjectReview: enabled, Activity: domain.Activity{State: domain.ActivityActive}, CreatedAt: now, UpdatedAt: now})
	require.NoError(t, err)
	require.NoError(t, s.UpsertReview(t.Context(), domain.Review{ID: "review", SessionID: session.ID, ProjectID: "mer", Harness: domain.ReviewerCodex, CreatedAt: now, UpdatedAt: now}))
	run := domain.ReviewRun{ID: "run", ReviewID: "review", SessionID: session.ID, Harness: domain.ReviewerCodex, PRURL: "https://github.com/o/r/pull/1", TargetSHA: "head", Status: domain.ReviewRunRunning, AutoInjectReview: enabled, CreatedAt: now}
	require.NoError(t, s.InsertReviewRun(t.Context(), run))
	return session, run
}
