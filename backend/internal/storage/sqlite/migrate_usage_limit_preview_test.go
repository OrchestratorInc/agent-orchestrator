package sqlite

import (
	"database/sql"
	"strings"
	"testing"
	"time"
)

func TestMigrateUsageLimitPreviewPreservesDataAndReceipts(t *testing.T) {
	for _, tc := range []struct {
		name         string
		base, quota  int64
		resetPreview bool
		mainReceipts bool
	}{
		{name: "quota preview 0213", base: 212, quota: 213},
		{name: "quota previews 0213 and 0214", base: 212, quota: 213, resetPreview: true},
		{name: "quota 0201 with main 0213 and 0214", base: 200, quota: 201, mainReceipts: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, tc.base)
			migrations := []legacyCodexProfileMigration{{
				version: tc.quota, canonicalPath: "migrations/20261019220421171_usage_limit_auto_resume.sql", legacyName: "usage_limit_auto_resume.sql",
			}}
			if tc.resetPreview {
				migrations = append(migrations, legacyCodexProfileMigration{
					version: 214, canonicalPath: "migrations/20261019220421514_usage_limit_reset_cdc.sql", legacyName: "usage_limit_reset_cdc.sql",
				})
			}
			if tc.mainReceipts {
				migrations = append(migrations,
					legacyCodexProfileMigration{version: 213, canonicalPath: "migrations/0213_session_stale_binary.sql", legacyName: "session_stale_binary.sql"},
					legacyCodexProfileMigration{version: 214, canonicalPath: "migrations/0214_app_settings_version_refresh.sql", legacyName: "app_settings_version_refresh.sql"},
				)
			}
			applyLegacyCodexProfileMigrations(t, db, migrations)
			seedUsageLimitPreviewTurn(t, db)
			if tc.mainReceipts {
				mustExec(t, db, `UPDATE app_settings SET version_refresh = 0 WHERE id = 1`)
			}
			wantReceipts := make(map[int64]int64, len(migrations))
			for _, migration := range migrations {
				version := migration.version
				if !tc.mainReceipts {
					version = usageLimitMigrationVersion
					if migration.version == 214 {
						version = usageLimitResetMigrationVersion
					}
				}
				var receipt int64
				if err := db.QueryRow(`SELECT id FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, migration.version).Scan(&receipt); err != nil {
					t.Fatal(err)
				}
				wantReceipts[version] = receipt
			}
			var firstLedgerSize int
			for pass := range 2 {
				if err := migrate(db); err != nil {
					t.Fatalf("migrate pass %d: %v", pass+1, err)
				}
				assertAppliedMigrations(t, db, 213, 214, usageLimitMigrationVersion, usageLimitResetMigrationVersion)
				assertUsageLimitMigrationApplied(t, db)
				assertUsageLimitPreviewTurn(t, db)
				for version, receipt := range wantReceipts {
					var actual int64
					if err := db.QueryRow(`SELECT id FROM goose_db_version WHERE version_id = ? AND is_applied = 1`, version).Scan(&actual); err != nil || actual != receipt {
						t.Fatalf("version %d receipt = %d, want original %d: %v", version, actual, receipt, err)
					}
				}
				var staleColumns, refresh, ledgerSize int
				if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions')
WHERE name IN ('stale_binary_since', 'stale_binary_version', 'stale_binary_launch', 'stale_binary_switch_failed_at')`).Scan(&staleColumns); err != nil || staleColumns != 4 {
					t.Fatalf("main stale-binary columns = %d, %v", staleColumns, err)
				}
				wantRefresh := 1
				if tc.mainReceipts {
					wantRefresh = 0
				}
				if err := db.QueryRow(`SELECT version_refresh FROM app_settings WHERE id = 1`).Scan(&refresh); err != nil || refresh != wantRefresh {
					t.Fatalf("main version_refresh = %d, want %d: %v", refresh, wantRefresh, err)
				}
				var trigger string
				if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE name = 'conversation_turns_auto_resume_cdc_update'`).Scan(&trigger); err != nil || !strings.Contains(trigger, "OLD.usage_limit_resets_at IS NOT NEW.usage_limit_resets_at") {
					t.Fatalf("deadline CDC missing: %s, %v", trigger, err)
				}
				if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version`).Scan(&ledgerSize); err != nil {
					t.Fatal(err)
				}
				if pass == 0 {
					firstLedgerSize = ledgerSize
				} else if ledgerSize != firstLedgerSize {
					t.Fatalf("second migration pass changed ledger size: %d, want %d", ledgerSize, firstLedgerSize)
				}
			}
		})
	}
}

func seedUsageLimitPreviewTurn(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
INSERT INTO projects (id, path, registered_at) VALUES ('preview-quota', '/preview-quota', CURRENT_TIMESTAMP);
INSERT INTO sessions (id, project_id, num, harness, session_mode, activity_last_at, created_at, updated_at)
VALUES ('preview-session', 'preview-quota', 1, 'codex', 'chat', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO conversations (id, scope, project_id, session_id, current_session_id, active_branch_id, created_at, updated_at)
VALUES ('preview-conversation', 'session', 'preview-quota', 'preview-session', 'preview-session', 'preview-branch', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO conversation_branches (id, conversation_id, session_id, provider_conversation_id, created_at)
VALUES ('preview-branch', 'preview-conversation', 'preview-session', 'native-preview-thread', CURRENT_TIMESTAMP);
INSERT INTO conversation_turns (id, conversation_id, handled_by_session_id, provider_turn_id, state, error_message,
    requested_at, completed_at, branch_id, usage_limit_resets_at, usage_limit_auto_resume, usage_limit_resume_error)
VALUES ('preview-turn', 'preview-conversation', 'preview-session', 'native-preview-turn', 'failed', 'Usage limit reached',
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'preview-branch', '2026-10-18 12:00:00+00:00', 1, 'saved preview diagnostic');
INSERT INTO conversation_messages (id, conversation_id, turn_id, sequence, role, origin, text, created_at, updated_at, branch_id)
VALUES ('preview-message', 'preview-conversation', 'preview-turn', 1, 'user', 'human', 'finish this preview task',
    CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'preview-branch');`)
}

func assertUsageLimitPreviewTurn(t *testing.T, db *sql.DB) {
	t.Helper()
	var reset time.Time
	var autoResume int
	var state, failure, diagnostic, providerTurn, prompt, providerThread string
	if err := db.QueryRow(`SELECT t.state, t.error_message, t.usage_limit_resets_at, t.usage_limit_auto_resume,
    t.usage_limit_resume_error, t.provider_turn_id, m.text, b.provider_conversation_id
FROM conversation_turns t
JOIN conversation_messages m ON m.turn_id = t.id AND m.id = 'preview-message'
JOIN conversation_branches b ON b.id = t.branch_id AND b.id = 'preview-branch'
JOIN conversations c ON c.id = t.conversation_id AND c.id = 'preview-conversation'
WHERE t.id = 'preview-turn' AND t.handled_by_session_id = 'preview-session'
    AND c.current_session_id = 'preview-session' AND c.active_branch_id = b.id`).Scan(
		&state, &failure, &reset, &autoResume, &diagnostic, &providerTurn, &prompt, &providerThread,
	); err != nil {
		t.Fatal(err)
	}
	wantReset := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	if state != "failed" || failure != "Usage limit reached" || !reset.Equal(wantReset) || autoResume != 1 ||
		diagnostic != "saved preview diagnostic" || providerTurn != "native-preview-turn" || prompt != "finish this preview task" || providerThread != "native-preview-thread" {
		t.Fatalf("preview history changed: state=%q failure=%q reset=%v auto=%d diagnostic=%q turn=%q prompt=%q thread=%q",
			state, failure, reset, autoResume, diagnostic, providerTurn, prompt, providerThread)
	}
}
