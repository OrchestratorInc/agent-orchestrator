package sqlite

import (
	"encoding/json"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReceiptMigrationUpgradeDownUp(t *testing.T) {
	const previous = int64(20261101055144579)
	const receiptVersion = int64(20261102073811895)
	db := openMigratedDatabaseCopy(t, previous)
	if _, err := db.Exec(`
INSERT INTO projects (id, path, registered_at) VALUES ('project', '/project', '2026-11-01T00:00:00Z');
INSERT INTO sessions (id, num, project_id, kind, activity_last_at, created_at, updated_at)
VALUES ('session', 1, 'project', 'worker', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z');
INSERT INTO review (id, session_id, project_id, harness, created_at, updated_at)
VALUES ('review', 'session', 'project', 'codex', '2026-11-01T00:00:00Z', '2026-11-01T00:00:00Z');
INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, github_review_id, created_at)
VALUES ('run', 'review', 'session', 'codex', 'https://github.com/o/r/pull/1', 'head', 'complete', 'approved', 'legacy body', 'legacy-review', '2026-11-01T00:00:00Z');`); err != nil {
		t.Fatalf("seed legacy result: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("upgrade receipt schema: %v", err)
	}
	var body, githubID, publication string
	if err := db.QueryRow(`SELECT body, github_review_id, publication_json FROM review_run WHERE id = 'run'`).Scan(&body, &githubID, &publication); err != nil {
		t.Fatalf("read upgraded result: %v", err)
	}
	if body != "legacy body" || githubID != "legacy-review" || publication != "null" {
		t.Errorf("upgraded result body=%q GitHub ID=%q publication=%q", body, githubID, publication)
	}
	var legacyPublication *domain.ReviewPublication
	if err := json.Unmarshal([]byte(publication), &legacyPublication); err != nil {
		t.Fatalf("decode legacy receipt: %v", err)
	}
	if legacyPublication != nil {
		t.Errorf("legacy receipt = %+v, want absent", legacyPublication)
	}
	if _, err := db.Exec(`UPDATE review_run SET publication_json = '{' WHERE id = 'run'`); err == nil {
		t.Fatal("receipt column accepted invalid JSON")
	}

	// A receipt-only update must emit the trigger-owned change event.
	receipt := `{"provider":"github","status":"blocked","prUrl":"https://github.com/o/r/pull/1","targetSha":"head","error":"denied"}`
	if _, err := db.Exec(`UPDATE review_run SET publication_json = ? WHERE id = 'run'`, receipt); err != nil {
		t.Fatalf("update receipt: %v", err)
	}
	var payload string
	if err := db.QueryRow(`SELECT payload FROM change_log WHERE session_id = 'session' AND event_type = 'review_run_updated' ORDER BY seq DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatalf("read receipt change event: %v", err)
	}
	var event struct {
		Publication domain.ReviewPublication `json:"publication"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode receipt change event: %v", err)
	}
	if event.Publication.Status != "blocked" {
		t.Errorf("event receipt status = %q, want blocked", event.Publication.Status)
	}

	downTo(t, db, previous)
	if _, err := db.Exec(`UPDATE review_run SET body = 'after down' WHERE id = 'run'`); err != nil {
		t.Fatalf("update result after down: %v", err)
	}
	if err := db.QueryRow(`SELECT payload FROM change_log WHERE session_id = 'session' AND event_type = 'review_run_updated' ORDER BY seq DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatalf("read pre-receipt event: %v", err)
	}
	var oldEvent map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &oldEvent); err != nil {
		t.Fatalf("decode pre-receipt event: %v", err)
	}
	if _, found := oldEvent["publication"]; found {
		t.Error("receipt field survived migration rollback in CDC")
	}

	upTo(t, db, receiptVersion)
	if err := db.QueryRow(`SELECT body, publication_json FROM review_run WHERE id = 'run'`).Scan(&body, &publication); err != nil {
		t.Fatalf("read result after second upgrade: %v", err)
	}
	if body != "after down" || publication != "null" {
		t.Errorf("second upgrade body=%q publication=%q, want after down/null", body, publication)
	}
}
