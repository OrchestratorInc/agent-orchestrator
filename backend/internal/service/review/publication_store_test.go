package review

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestSubmitPublicationSnapshotsFirstResultAtomically(t *testing.T) {
	for _, tc := range []struct {
		name    string
		enabled bool
	}{
		{name: "disabled"},
		{name: "enabled", enabled: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			s := sqlitetest.MustOpenAt(t, dir)
			session, run := seedPublicationRun(t, s, !tc.enabled)
			changed, err := s.SetSessionAutoInjectReview(t.Context(), session.ID, tc.enabled, time.Now().UTC())
			if err != nil || !changed {
				t.Fatalf("change session policy before submit: changed=%v err=%v", changed, err)
			}
			publication := &domain.ReviewPublication{
				Provider: "github", Status: "published", PRURL: run.PRURL, TargetSHA: run.TargetSHA,
				Refs: []domain.ReviewPublicationRef{{Kind: "review", ID: "123"}},
			}
			input := SubmittedReview{RunID: run.ID, Verdict: domain.VerdictApproved, Body: "review complete", Publication: publication}
			svc := New(nil, s)

			result, err := svc.SubmitMany(t.Context(), session.ID, []SubmittedReview{input})

			if err != nil || len(result) != 1 {
				t.Fatalf("submit result = %+v, err=%v", result, err)
			}
			if result[0].AutoInjectReview != tc.enabled || result[0].GithubReviewID != "123" {
				t.Errorf("first result = %+v, want policy=%v and GitHub review 123", result[0], tc.enabled)
			}
			db, err := sql.Open("sqlite", filepath.Join(dir, "ao.db"))
			if err != nil {
				t.Fatalf("open change log: %v", err)
			}
			t.Cleanup(func() {
				if err := db.Close(); err != nil {
					t.Errorf("close change log: %v", err)
				}
			})
			var payload string
			if err := db.QueryRowContext(t.Context(), `SELECT payload FROM change_log WHERE session_id = ? AND event_type = 'review_run_updated' ORDER BY seq DESC LIMIT 1`, session.ID).Scan(&payload); err != nil {
				t.Fatalf("read first-result event: %v", err)
			}
			var event struct {
				Status           string                    `json:"status"`
				Verdict          string                    `json:"verdict"`
				AutoInjectReview bool                      `json:"autoInjectReview"`
				Publication      *domain.ReviewPublication `json:"publication"`
			}
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatalf("decode first-result event: %v", err)
			}
			if event.Status != "complete" || event.Verdict != "approved" || event.AutoInjectReview != tc.enabled || !reflect.DeepEqual(publication, event.Publication) {
				t.Errorf("first-result event = %+v, want complete/approved/policy=%v/publication=%+v", event, tc.enabled, publication)
			}

			changed, err = s.SetSessionAutoInjectReview(t.Context(), session.ID, !tc.enabled, time.Now().UTC())
			if err != nil || !changed {
				t.Fatalf("change session policy before replay: changed=%v err=%v", changed, err)
			}
			if _, err := svc.SubmitMany(t.Context(), session.ID, []SubmittedReview{input}); err != nil {
				t.Fatalf("replay identical result: %v", err)
			}
			stored, found, err := s.GetReviewRun(t.Context(), run.ID)
			if err != nil || !found {
				t.Fatalf("read replayed result: found=%v err=%v", found, err)
			}
			if stored.AutoInjectReview != tc.enabled || !reflect.DeepEqual(publication, stored.Publication) {
				t.Errorf("replay changed original snapshot: %+v", stored)
			}
			var updates int
			if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM change_log WHERE session_id = ? AND event_type = 'review_run_updated'`, session.ID).Scan(&updates); err != nil {
				t.Fatalf("count result updates: %v", err)
			}
			if updates != 1 {
				t.Errorf("result update events = %d, want one", updates)
			}
		})
	}
}

func seedPublicationRun(t *testing.T, s *sqlite.Store, enabled bool) (domain.SessionRecord, domain.ReviewRun) {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	if err := s.UpsertProject(t.Context(), domain.ProjectRecord{ID: "project", Path: t.TempDir(), RegisteredAt: now}); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	session, err := s.CreateSession(t.Context(), domain.SessionRecord{
		ProjectID: "project", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		AutoInjectReview: enabled, Activity: domain.Activity{State: domain.ActivityActive}, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := s.UpsertReview(t.Context(), domain.Review{ID: "review", SessionID: session.ID, ProjectID: "project", Harness: domain.ReviewerCodex, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("seed review: %v", err)
	}
	run := domain.ReviewRun{
		ID: "run", ReviewID: "review", SessionID: session.ID, Harness: domain.ReviewerCodex,
		PRURL: "https://github.com/o/r/pull/1", TargetSHA: "head", Status: domain.ReviewRunRunning,
		AutoInjectReview: enabled, CreatedAt: now,
	}
	if err := s.InsertReviewRun(t.Context(), run); err != nil {
		t.Fatalf("seed running review: %v", err)
	}
	return session, run
}
