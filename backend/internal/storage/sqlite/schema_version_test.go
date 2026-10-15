package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrateStampsSchemaAppVersion(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	latest, err := latestMigrationVersion()
	if err != nil {
		t.Fatalf("latest migration: %v", err)
	}
	var version int64
	if err := db.QueryRow(`SELECT version FROM schema_app_version WHERE id = 1`).Scan(&version); err != nil {
		t.Fatalf("read schema app version: %v", err)
	}
	if version != latest {
		t.Fatalf("schema app version = %d, want latest migration %d", version, latest)
	}
}

func TestMigrateRepairsPriorSchemaAppVersionMigrationNumber(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 148)
	if _, err := db.Exec(`
CREATE TABLE schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL CHECK (version >= 0),
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_app_version (id, version) VALUES (1, 149);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (149, 1);
`); err != nil {
		t.Fatalf("seed prior PR migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate prior PR database: %v", err)
	}
	var reviewerColumns, applied149, version int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('review') WHERE name = 'interface_mode'`).Scan(&reviewerColumns); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 149 AND is_applied = 1`).Scan(&applied149); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT version FROM schema_app_version WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	latest, err := latestMigrationVersion()
	if err != nil {
		t.Fatalf("latest migration: %v", err)
	}
	if reviewerColumns != 1 || applied149 != 1 || version != latest {
		t.Fatalf("reviewer columns = %d, applied 0149 = %d, stamped version = %d; want 1, 1, %d", reviewerColumns, applied149, version, latest)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestMigrateRepairsPriorSchemaAppVersionMigrationNumber130(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 129)
	if _, err := db.Exec(`
CREATE TABLE schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL CHECK (version >= 0),
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_app_version (id, version) VALUES (1, 130);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (130, 1);
`); err != nil {
		t.Fatalf("seed prior PR migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate prior PR database: %v", err)
	}
	var reviewPartialColumn, applied130, version int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name = 'review_partial'`).Scan(&reviewPartialColumn); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 130 AND is_applied = 1`).Scan(&applied130); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT version FROM schema_app_version WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	latest, err := latestMigrationVersion()
	if err != nil {
		t.Fatalf("latest migration: %v", err)
	}
	if reviewPartialColumn != 1 || applied130 != 1 || version != latest {
		t.Fatalf("review_partial = %d, applied 0130 = %d, stamp = %d; want 1, 1, %d", reviewPartialColumn, applied130, version, latest)
	}
}

func TestMigrateRepairsPriorSchemaAppVersionMigrationNumber148(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 147)
	if _, err := db.Exec(`
CREATE TABLE schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL CHECK (version >= 0),
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_app_version (id, version) VALUES (1, 148);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (148, 1);
`); err != nil {
		t.Fatalf("seed prior PR migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate prior PR database: %v", err)
	}
	var dismissedAtColumn, applied148, version int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('notifications') WHERE name = 'dismissed_at'`).Scan(&dismissedAtColumn); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 148 AND is_applied = 1`).Scan(&applied148); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT version FROM schema_app_version WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	latest, err := latestMigrationVersion()
	if err != nil {
		t.Fatalf("latest migration: %v", err)
	}
	if dismissedAtColumn != 1 || applied148 != 1 || version != latest {
		t.Fatalf("dismissed_at = %d, applied 0148 = %d, stamp = %d; want 1, 1, %d", dismissedAtColumn, applied148, version, latest)
	}
}

func TestMigrateRepairsPriorSchemaAppVersionMigrationNumber163(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 162)
	if _, err := db.Exec(`
CREATE TABLE schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL CHECK (version >= 0),
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_app_version (id, version) VALUES (1, 163);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (163, 1);
`); err != nil {
		t.Fatalf("seed prior PR migration: %v", err)
	}

	if err := migrate(db); err != nil {
		t.Fatalf("migrate prior PR database: %v", err)
	}
	var fxHarness, applied163, version int64
	if err := db.QueryRow(`SELECT instr(sql, '''fx''') FROM sqlite_master WHERE type = 'table' AND name = 'sessions'`).Scan(&fxHarness); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 163 AND is_applied = 1`).Scan(&applied163); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT version FROM schema_app_version WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	latest, err := latestMigrationVersion()
	if err != nil {
		t.Fatalf("latest migration: %v", err)
	}
	if fxHarness == 0 || applied163 != 1 || version != latest {
		t.Fatalf("fx harness = %d, applied 0163 = %d, stamped version = %d; want nonzero, 1, %d", fxHarness, applied163, version, latest)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}

func TestMigrateRepairsPriorSchemaAppVersionMigrationNumber179(t *testing.T) {
	for _, senderPresent := range []bool{false, true} {
		name := "old_stamp"
		if senderPresent {
			name = "upstream_sender"
		}
		t.Run(name, func(t *testing.T) {
			baseVersion := int64(178)
			if senderPresent {
				baseVersion = 179
			}
			db := openMigratedDatabaseCopy(t, baseVersion)
			if _, err := db.Exec(`
CREATE TABLE schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL CHECK (version >= 0),
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_app_version (id, version) VALUES (1, 179);
`); err != nil {
				t.Fatalf("seed prior stamp: %v", err)
			}
			if !senderPresent {
				if _, err := db.Exec(`INSERT INTO goose_db_version (version_id, is_applied) VALUES (179, 1)`); err != nil {
					t.Fatal(err)
				}
			}
			for range 2 {
				if err := migrate(db); err != nil {
					t.Fatalf("migrate prior PR database: %v", err)
				}
			}
			var senderColumns, applied179, version int64
			if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name IN ('sender_session_id', 'sender_project_id', 'sender_display_name')`).Scan(&senderColumns); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id = 179 AND is_applied = 1`).Scan(&applied179); err != nil {
				t.Fatal(err)
			}
			if err := db.QueryRow(`SELECT version FROM schema_app_version WHERE id = 1`).Scan(&version); err != nil {
				t.Fatal(err)
			}
			latest, err := latestMigrationVersion()
			if err != nil {
				t.Fatal(err)
			}
			if senderColumns != 3 || applied179 != 1 || version != latest {
				t.Fatalf("sender columns = %d, applied 0179 = %d, stamp = %d; want 3, 1, %d", senderColumns, applied179, version, latest)
			}
		})
	}
}

func TestMigrateRepairsPriorSchemaAppVersionMigrationNumber197(t *testing.T) {
	db := openMigratedDatabaseCopyNoForeignKeys(t, 196)
	mustExec(t, db, `
CREATE TABLE schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL CHECK (version >= 0),
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_app_version (id, version) VALUES (1, 197);
INSERT INTO goose_db_version (version_id, is_applied) VALUES (197, 1);
INSERT INTO projects (id, path, display_name, registered_at)
VALUES ('project', '/tmp/project', 'Project', '2026-10-15T00:00:00Z');
INSERT INTO sessions (
    id, project_id, num, kind, harness, session_mode, provision_state,
    controller_generation, activity_last_at, created_at, updated_at
) VALUES (
    'orchestrator', 'project', 1, 'orchestrator', 'codex', 'chat', 'ready',
    'gen-1', '2026-10-15T00:00:00Z', '2026-10-15T00:00:00Z', '2026-10-15T00:00:00Z'
);
INSERT INTO conversations (
    id, scope, project_id, session_id, current_session_id, active_branch_id, created_at, updated_at
) VALUES
    ('provisioning', 'session', 'project', 'orchestrator', 'orchestrator', 'provisioning:root', '2026-10-15T00:00:00Z', '2026-10-15T00:00:00Z'),
    ('live', 'project', 'project', NULL, 'orchestrator', 'live:root', '2026-10-15T00:00:00Z', '2026-10-15T00:00:00Z');
`)

	for range 2 {
		if err := migrate(db); err != nil {
			t.Fatalf("migrate prior PR database: %v", err)
		}
	}
	var current sql.NullString
	if err := db.QueryRow(`SELECT current_session_id FROM conversations WHERE id = 'provisioning'`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	var version int64
	if err := db.QueryRow(`SELECT version FROM schema_app_version WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if current.Valid || version != 198 {
		t.Fatalf("provisioning session = %q, schema version = %d; want detached and 198", current.String, version)
	}
}

func TestMigrateRefusesNewerSchemaAppVersion(t *testing.T) {
	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })

	latest, err := latestMigrationVersion()
	if err != nil {
		t.Fatalf("latest migration: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_app_version (id, version) VALUES (1, ?);
`, latest+1); err != nil {
		t.Fatalf("seed newer schema version: %v", err)
	}

	err = migrate(db)
	if err == nil || !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Fatalf("migrate error = %v, want newer-schema refusal", err)
	}
}

func TestOpenReadOnlyRefusesNewerSchemaAppVersion(t *testing.T) {
	dataDir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db")+pragmas)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	latest, err := latestMigrationVersion()
	if err != nil {
		t.Fatalf("latest migration: %v", err)
	}
	if _, err := db.Exec(`
CREATE TABLE schema_app_version (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    version INTEGER NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
INSERT INTO schema_app_version (id, version) VALUES (1, ?);
`, latest+1); err != nil {
		_ = db.Close()
		t.Fatalf("seed newer schema version: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	_, err = OpenReadOnly(context.Background(), dataDir)
	if err == nil || !strings.Contains(err.Error(), "newer than this binary supports") {
		t.Fatalf("OpenReadOnly error = %v, want newer-schema refusal", err)
	}
}
