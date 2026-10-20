package sqlite

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

// TestMigrateAppliesMissingMigrationsBeforeCurrentVersion covers databases
// created by builds whose migration history advanced past migrations that were
// later added or renumbered. Goose rejects that history by default, which
// prevents the daemon from starting even though the missing migrations can be
// applied normally.
func TestMigrateAppliesMissingMigrationsBeforeCurrentVersion(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 27)
	if _, err := db.Exec(`
INSERT INTO projects (
	id, path, repo_origin_url, display_name, registered_at, config, kind
) VALUES (?, ?, ?, ?, ?, ?, ?)
`,
		"project-1",
		`C:\src\project-1`,
		"https://example.com/project-1.git",
		"Project One",
		"2026-07-25T00:00:00Z",
		`{"worker":{"agent":"codex"}}`,
		"single_repo",
	); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if _, err := db.Exec(
		`INSERT INTO goose_db_version (version_id, is_applied) VALUES (46, 1)`,
	); err != nil {
		t.Fatalf("seed later migration version: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with missing earlier versions: %v", err)
	}

	var applied int
	if err := db.QueryRow(`
SELECT COUNT(DISTINCT version_id)
FROM goose_db_version
WHERE is_applied = 1 AND version_id IN (28, 29, 30, 31)
`).Scan(&applied); err != nil {
		t.Fatalf("query applied migrations: %v", err)
	}
	if applied != 4 {
		t.Fatalf("applied migrations 28-31 = %d, want 4", applied)
	}

	var path, displayName string
	if err := db.QueryRow(
		`SELECT path, display_name FROM projects WHERE id = ?`,
		"project-1",
	).Scan(&path, &displayName); err != nil {
		t.Fatalf("query preserved project: %v", err)
	}
	if path != `C:\src\project-1` || displayName != "Project One" {
		t.Fatalf("preserved project = (%q, %q), want Windows path and display name unchanged", path, displayName)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
}

// TestMigrateAppliesBrowserVerifierAfterUpstreamVersion41 guards the migration
// number collision with upstream's 0041_notification_resolution migration.
// A database that already recorded version 41 must still receive the browser
// capability column from the append-only 0081 migration.
func TestMigrateAppliesBrowserVerifierAfterUpstreamVersion41(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 40)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (41, 1)`); err != nil {
		t.Fatalf("seed upstream migration version 41: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with upstream version 41: %v", err)
	}

	var schema string
	if err := db.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name='sessions'",
	).Scan(&schema); err != nil {
		t.Fatalf("read sessions schema: %v", err)
	}
	if !strings.Contains(schema, "browser_capability_verifier") {
		t.Fatalf("sessions schema missing browser capability verifier after migration:\n%s", schema)
	}

	var applied int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM goose_db_version WHERE version_id = 81 AND is_applied = 1",
	).Scan(&applied); err != nil {
		t.Fatalf("query browser verifier migration version: %v", err)
	}
	if applied != 1 {
		t.Fatalf("browser verifier migration applied rows = %d, want 1", applied)
	}
}

func TestMigrateRecognizesBrowserVerifierFromEarlierBranchBuild(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 48)
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN browser_capability_verifier TEXT NOT NULL DEFAULT ''`); err != nil {
		t.Fatalf("seed verifier from earlier branch build: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with pre-existing verifier: %v", err)
	}

	var columns, applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name = 'browser_capability_verifier'`,
	).Scan(&columns); err != nil {
		t.Fatalf("query browser verifier column: %v", err)
	}
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM goose_db_version WHERE version_id = 81 AND is_applied = 1",
	).Scan(&applied); err != nil {
		t.Fatalf("query browser verifier migration version: %v", err)
	}
	if columns != 1 || applied != 1 {
		t.Fatalf("browser verifier state = columns %d, applied rows %d; want 1, 1", columns, applied)
	}
}

func TestMigrateRepairsQueuedTurnPromotionColumnsWhenVersionAlreadyClaimed(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 85)
	if _, err := db.Exec(`ALTER TABLE conversation_turns ADD COLUMN promotion_started_at TIMESTAMP`); err != nil {
		t.Fatalf("seed promotion timestamp from earlier branch build: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE conversation_turns ADD COLUMN promoted_to_turn_id TEXT REFERENCES conversation_turns(id) ON DELETE SET NULL`); err != nil {
		t.Fatalf("seed promoted turn from earlier branch build: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (86, 1)`); err != nil {
		t.Fatalf("seed claimed promotion migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with claimed promotion version: %v", err)
	}

	for _, column := range []string{"promotion_started_at", "promoted_to_turn_id"} {
		var columns int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info('conversation_turns') WHERE name = ?`, column,
		).Scan(&columns); err != nil {
			t.Fatalf("query %s column: %v", column, err)
		}
		if columns != 1 {
			t.Errorf("conversation_turns.%s count = %d, want 1", column, columns)
		}
	}
	var applied int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM goose_db_version WHERE version_id = 89 AND is_applied = 1",
	).Scan(&applied); err != nil {
		t.Fatalf("query renumbered promotion migration version: %v", err)
	}
	if applied != 1 {
		t.Fatalf("promotion migration 89 applied rows = %d, want 1", applied)
	}
	var defaultBranchColumns int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('workspace_repos') WHERE name = 'default_branch'`,
	).Scan(&defaultBranchColumns); err != nil {
		t.Fatalf("query workspace default branch column: %v", err)
	}
	if defaultBranchColumns != 1 {
		t.Fatalf("workspace_repos.default_branch count = %d, want 1", defaultBranchColumns)
	}
}

func TestMigrateRepairsQueuedTurnPromotionWhenVersion88WasClaimed(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 87)
	if _, err := db.Exec(`ALTER TABLE conversation_turns ADD COLUMN promotion_started_at TIMESTAMP`); err != nil {
		t.Fatalf("seed promotion timestamp from version 88 branch build: %v", err)
	}
	if _, err := db.Exec(`ALTER TABLE conversation_turns ADD COLUMN promoted_to_turn_id TEXT REFERENCES conversation_turns(id) ON DELETE SET NULL`); err != nil {
		t.Fatalf("seed promoted turn from version 88 branch build: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (88, 1)`); err != nil {
		t.Fatalf("seed version 88 promotion migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with version 88 promotion schema: %v", err)
	}

	for table, column := range map[string]string{
		"sessions": "auto_inject_ci",
		"pr":       "auto_inject_ci",
	} {
		var columns int
		if err := db.QueryRow(
			`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column,
		).Scan(&columns); err != nil {
			t.Fatalf("query %s.%s: %v", table, column, err)
		}
		if columns != 1 {
			t.Fatalf("%s.%s count = %d, want 1", table, column, columns)
		}
	}
	for _, version := range []int64{88, 89} {
		var applied int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM goose_db_version WHERE version_id = ? AND is_applied = 1", version,
		).Scan(&applied); err != nil {
			t.Fatalf("query migration %d: %v", version, err)
		}
		if applied != 1 {
			t.Fatalf("migration %d applied rows = %d, want 1", version, applied)
		}
	}
}

// TestMigrateRecognizesGatewayEntriesFromEarlierBuild reproduces the
// 2026-10-14 startup failure: gateway_entries shipped in self-builds as
// migration 9010 before the 2026-10-13 sync settle renumbered the file to
// 9011, and a live install holds the table (with the provider profile data)
// while goose_db_version records no row for it. goose replayed CREATE TABLE
// into "table gateway_entries already exists" and the daemon wedged at
// startup. Migration must recognize the existing table, record 9011, keep the
// data, and start.
func TestMigrateRecognizesGatewayEntriesFromEarlierBuild(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	upTo(t, db, 9006)

	if _, err := db.Exec(`
CREATE TABLE gateway_entries (
    scope TEXT NOT NULL CHECK (scope IN ('app', 'project')),
    project_id TEXT NOT NULL DEFAULT '',
    base_url TEXT NOT NULL DEFAULT '',
    auth_token TEXT NOT NULL DEFAULT '',
    model TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (scope, project_id)
)`); err != nil {
		t.Fatalf("seed gateway_entries table from earlier build: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO gateway_entries (scope, project_id, base_url, auth_token, model, updated_at)
VALUES ('app', '', 'https://example.invalid', '', 'gateway-model', '2026-10-14T00:00:00Z')
`); err != nil {
		t.Fatalf("seed gateway profile: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with pre-existing gateway_entries: %v", err)
	}

	var applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 9011 AND is_applied = 1`,
	).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("gateway migration 9011 applied rows = %d, want 1", applied)
	}
	var profiles int
	if err := db.QueryRow(`SELECT COUNT(*) FROM gateway_entries`).Scan(&profiles); err != nil {
		t.Fatal(err)
	}
	if profiles != 1 {
		t.Fatalf("gateway_entries rows = %d, want the profile preserved", profiles)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
}

// TestMigrateRecognizesReviewRunPublicationFromEarlierBuild reproduces the
// 2026-10-20 startup failure: the review_run publication columns shipped in
// self-builds as migration 9004 before the 2026-10-13 delta drop removed the
// file, so the live install holds the columns (and runs backfilled by nothing)
// while the ledger records no 9012 row for the PR #91 re-introduction. The
// runner replayed ALTER TABLE into "duplicate column name: findings" and the
// daemon wedged at startup. Migration must recognize the existing columns,
// replay only the backfill, record 9012, and start.
func TestMigrateRecognizesReviewRunPublicationFromEarlierBuild(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 9011)
	if _, err := db.Exec(`
ALTER TABLE review_run ADD COLUMN findings TEXT NOT NULL DEFAULT '';
ALTER TABLE review_run ADD COLUMN publish_state TEXT NOT NULL DEFAULT 'pending' CHECK (publish_state IN ('pending', 'publishing', 'published', 'failed', 'uncertain'));
ALTER TABLE review_run ADD COLUMN publish_error TEXT NOT NULL DEFAULT '';`); err != nil {
		t.Fatalf("seed publication columns from earlier build: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO review_run (id, review_id, session_id, harness, pr_url, target_sha, status, verdict, body, created_at, github_review_id)
VALUES ('run-old', 'rev-1', 's1', 'claude-code', '', '', 'complete', '', '', '2026-10-01T00:00:00Z', '12345'),
       ('run-new', 'rev-1', 's1', 'claude-code', '', '', 'running', '', '', '2026-10-02T00:00:00Z', '')`); err != nil {
		t.Fatalf("seed review runs: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with pre-existing publication columns: %v", err)
	}

	var applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 9012 AND is_applied = 1`,
	).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if applied != 1 {
		t.Fatalf("publication migration 9012 applied rows = %d, want 1", applied)
	}
	// The backfill marks reviewer-published runs (github_review_id set) as
	// published and leaves everything else alone.
	var published, pending int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM review_run WHERE id = 'run-old' AND publish_state = 'published'`,
	).Scan(&published); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM review_run WHERE id = 'run-new' AND publish_state = 'pending'`,
	).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if published != 1 || pending != 1 {
		t.Fatalf("backfill result: published=%d pending=%d, want 1/1", published, pending)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
}

// TestMigrateRecognizesProcessInventoryFromEarlierBuild covers databases from
// the parked proc-inventory line: they hold the app_settings.process_inventory
// column (applied there as fork migration 9002, renumbered 9003, both burned)
// while the ledger records no 9013 row for the feature's return (issue #40).
// Replaying the ALTER TABLE would fail with "duplicate column name:
// process_inventory" and wedge daemon startup; the prepare repair must record
// 9013 instead.
func TestMigrateRecognizesProcessInventoryFromEarlierBuild(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 9012)
	if _, err := db.Exec(`
ALTER TABLE app_settings ADD COLUMN process_inventory INTEGER NOT NULL DEFAULT 1
    CHECK (process_inventory IN (0, 1))`); err != nil {
		t.Fatalf("seed process_inventory column from earlier build: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with pre-existing process_inventory column: %v", err)
	}

	var columns, applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('app_settings') WHERE name = 'process_inventory'`,
	).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 9013 AND is_applied = 1`,
	).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if columns != 1 || applied != 1 {
		t.Fatalf("process inventory state = columns %d, applied rows %d; want 1, 1", columns, applied)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("repeat migrate: %v", err)
	}
}

// TestMigrateReleasesForeignProcessInventoryLedgerEntry covers a database that
// records 9013 as applied without holding the column — a foreign version-table
// record left by a build that claimed the number for something else. The
// repair must release the entry so the real migration applies and the column
// appears.
func TestMigrateReleasesForeignProcessInventoryLedgerEntry(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 9012)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (9013, 1)`); err != nil {
		t.Fatalf("seed foreign 9013 ledger entry: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with foreign 9013 record: %v", err)
	}

	var columns, applied int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('app_settings') WHERE name = 'process_inventory'`,
	).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(
		`SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id = 9013 ORDER BY id DESC LIMIT 1), 0)`,
	).Scan(&applied); err != nil {
		t.Fatal(err)
	}
	if columns != 1 || applied != 1 {
		t.Fatalf("process inventory state after release = columns %d, applied %d; want 1, 1", columns, applied)
	}
}

func TestMigrateRepairsRetrySourceBeforeCancelledTurnRebuild(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 107)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (108, 1)`); err != nil {
		t.Fatalf("seed claimed retry-source migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with missing retry-source schema: %v", err)
	}

	var columns, indexes int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('conversation_turns') WHERE name = 'retry_of_turn_id'`,
	).Scan(&columns); err != nil {
		t.Fatalf("query retry source column: %v", err)
	}
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = 'idx_conversation_turns_retry_source'`,
	).Scan(&indexes); err != nil {
		t.Fatalf("query retry source index: %v", err)
	}
	if columns != 1 || indexes != 1 {
		t.Fatalf("retry source schema = column %d, index %d; want 1, 1", columns, indexes)
	}
	assertTableSQLContains(t, db, "conversation_turns", "'cancelled'")
}

func TestMigrateRepairsConversationOpenCodeModeWhenVersionAlreadyClaimed(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	upTo(t, db, 131)
	if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (132, 1)`); err != nil {
		t.Fatalf("seed claimed OpenCode mode migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database with claimed OpenCode mode version: %v", err)
	}

	var columns int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('conversations') WHERE name = 'opencode_mode'`,
	).Scan(&columns); err != nil {
		t.Fatalf("query conversation OpenCode mode column: %v", err)
	}
	if columns != 1 {
		t.Fatalf("conversations.opencode_mode count = %d, want 1", columns)
	}
}

// TestMigrateAppliesAgentModelCatalogAfterUpstreamMigration covers a database
// that has already applied main's version 41. The catalog must use the next
// migration version so goose applies it independently.
func TestMigrateAppliesAgentModelCatalogAfterUpstreamMigration(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 41)

	if err := migrate(db); err != nil {
		t.Fatalf("migrate database after upstream migration: %v", err)
	}

	var table string
	if err := db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'agent_model_catalog'`,
	).Scan(&table); err != nil {
		t.Fatalf("query agent model catalog table: %v", err)
	}
	if table != "agent_model_catalog" {
		t.Fatalf("model catalog table = %q, want agent_model_catalog", table)
	}
}
