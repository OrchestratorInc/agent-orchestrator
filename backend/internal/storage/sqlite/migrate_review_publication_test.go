package sqlite

import (
	"database/sql"
	"encoding/json"
	"testing"
	"testing/fstest"

	"github.com/pressly/goose/v3"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestReceiptMigrationAppliesAfterNewerMigrations(t *testing.T) {
	const receiptVersion = int64(20261102073811895)
	dir := t.TempDir()
	db := openMigratedDatabaseCopyAt(t, dir, 20261101055144579, pragmas)
	applyMigrationsWithoutReceipt(t, db)

	latest, err := expectedMigrationVersion()
	if err != nil {
		t.Fatalf("read latest migration version: %v", err)
	}
	var current int64
	var present, applied int
	if err := db.QueryRow(`SELECT
		(SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1),
		(SELECT COUNT(*) FROM pragma_table_info('review_run') WHERE name = 'publication_json'),
		(SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1)`, receiptVersion).Scan(&current, &present, &applied); err != nil {
		t.Fatalf("read schema before receipt upgrade: %v", err)
	}
	if current != latest || current <= receiptVersion || present != 0 || applied != 0 {
		t.Fatalf("schema before upgrade: version=%d latest=%d column=%d receipt migration=%d", current, latest, present, applied)
	}
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
		t.Fatalf("apply receipt after newer migrations: %v", err)
	}
	var publication string
	if err := db.QueryRow(`SELECT publication_json FROM review_run WHERE id = 'run'`).Scan(&publication); err != nil {
		t.Fatalf("read upgraded receipt default: %v", err)
	}
	if publication != "null" {
		t.Errorf("upgraded receipt default = %q, want null", publication)
	}
	s, err := OpenPreMigrated(dir)
	if err != nil {
		t.Fatalf("open upgraded store: %v", err)
	}
	legacy, found, readErr := s.GetReviewRun(t.Context(), "run")
	closeErr := s.Close()
	if readErr != nil || !found || closeErr != nil {
		t.Fatalf("read upgraded result: found=%v read=%v close=%v", found, readErr, closeErr)
	}
	if legacy.Publication != nil || legacy.Body != "legacy body" || legacy.GithubReviewID != "legacy-review" {
		t.Errorf("upgraded legacy result = %+v", legacy)
	}

	receipt := `{"provider":"github","status":"blocked","prUrl":"https://github.com/o/r/pull/1","targetSha":"head","error":"denied"}`
	if _, err := db.Exec(`UPDATE review_run SET publication_json = ? WHERE id = 'run'`, receipt); err != nil {
		t.Fatalf("update upgraded receipt: %v", err)
	}
	var payload string
	if err := db.QueryRow(`SELECT payload FROM change_log WHERE session_id = 'session' AND event_type = 'review_run_updated' ORDER BY seq DESC LIMIT 1`).Scan(&payload); err != nil {
		t.Fatalf("read upgraded receipt CDC: %v", err)
	}
	var event struct {
		Publication domain.ReviewPublication `json:"publication"`
	}
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode upgraded receipt CDC: %v", err)
	}
	if event.Publication.Status != "blocked" {
		t.Errorf("upgraded receipt CDC status = %q, want blocked", event.Publication.Status)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("repeat receipt migration: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, receiptVersion).Scan(&applied); err != nil {
		t.Fatalf("read receipt migration history: %v", err)
	}
	if applied != 1 {
		t.Errorf("receipt migration applied %d times, want 1", applied)
	}
}

func applyMigrationsWithoutReceipt(t *testing.T, db *sql.DB) {
	t.Helper()
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatalf("read embedded migrations: %v", err)
	}
	beforeReceipt := fstest.MapFS{}
	for _, entry := range entries {
		if entry.Name() == "20261102073811895_review_publication_receipt.sql" {
			continue
		}
		name := "migrations/" + entry.Name()
		body, err := migrationsFS.ReadFile(name)
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		beforeReceipt[name] = &fstest.MapFile{Data: body}
	}

	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(beforeReceipt)
	defer goose.SetBaseFS(migrationsFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set migration dialect: %v", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("apply migrations without receipt: %v", err)
	}
}

func TestReceiptMigrationUpgradeDownUp(t *testing.T) {
	const previous = int64(20261101055144579)
	const receiptVersion = int64(20261102073811895)
	dir := t.TempDir()
	db := openMigratedDatabaseCopyAt(t, dir, previous, pragmas)
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
	s, err := OpenPreMigrated(dir)
	if err != nil {
		t.Fatalf("open upgraded store: %v", err)
	}
	legacy, found, readErr := s.GetReviewRun(t.Context(), "run")
	closeErr := s.Close()
	if readErr != nil || !found {
		t.Fatalf("read upgraded legacy result: found=%v err=%v", found, readErr)
	}
	if closeErr != nil {
		t.Fatalf("close upgraded store: %v", closeErr)
	}
	if legacy.Publication != nil || legacy.Body != "legacy body" || legacy.GithubReviewID != "legacy-review" {
		t.Errorf("upgraded legacy result = %+v", legacy)
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
	if err := migrate(db); err != nil {
		t.Fatalf("upgrade remaining schema before store readback: %v", err)
	}
	s, err = OpenPreMigrated(dir)
	if err != nil {
		t.Fatalf("open store after second upgrade: %v", err)
	}
	afterUp, found, readErr := s.GetReviewRun(t.Context(), "run")
	closeErr = s.Close()
	if readErr != nil || !found {
		t.Fatalf("read result after second upgrade: found=%v err=%v", found, readErr)
	}
	if closeErr != nil {
		t.Fatalf("close store after second upgrade: %v", closeErr)
	}
	if afterUp.Publication != nil || afterUp.Body != "after down" {
		t.Errorf("result after second upgrade = %+v, want absent receipt and after down body", afterUp)
	}
}
