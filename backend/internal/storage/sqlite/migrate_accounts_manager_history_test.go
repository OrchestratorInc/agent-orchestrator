package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/pressly/goose/v3"
)

func TestMigrateRepairsRenumberedAccountsManagerHistory(t *testing.T) {
	for count := 1; count <= 8; count++ {
		t.Run(fmt.Sprintf("legacy_through_%d", 160+count), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 160)
			now := time.Now().UTC()
			mustExec(t, db, `INSERT INTO projects(id,path,display_name,registered_at) VALUES ('p','/tmp/accounts-history','history',?)`, now)
			for i, id := range []string{"chosen", "unrelated"} {
				mustExec(t, db, `INSERT INTO sessions(id,project_id,num,harness,activity_last_at,created_at,updated_at) VALUES (?,'p',?,'codex',?,?,?)`, id, i+1, now, now, now)
			}
			mustExec(t, db, `INSERT INTO conversations(id,scope,project_id,session_id,current_session_id,created_at,updated_at) VALUES ('c','session','p','chosen','chosen',?,?)`, now, now)
			mustExec(t, db, `INSERT INTO conversation_turns(id,conversation_id,handled_by_session_id,state,requested_at) VALUES ('queued','c','chosen','queued',?)`, now)
			applyLegacyAccountsManagerMigrations(t, db, count)
			mustExec(t, db, `INSERT INTO accounts_manager_routing_policies(provider,enabled,updated_at) VALUES ('codex',1,?)`, now)
			mustExec(t, db, `INSERT INTO accounts_manager_routing_policy_accounts(provider,account_id,position) VALUES ('codex','account-a',0)`)
			mustExec(t, db, `INSERT INTO accounts_manager_session_routes(session_id,provider,account_id,created_at,updated_at) VALUES ('chosen','codex','account-a',?,?)`, now, now)
			if count >= 2 {
				mustExec(t, db, `UPDATE accounts_manager_session_bindings SET connection_mode='managed',account_id='account-a',revision=9 WHERE session_id='chosen'`)
			}
			if count >= 3 {
				mustExec(t, db, `INSERT INTO accounts_manager_switches(id,session_id,provider,source_mode,source_account_id,source_revision,source_owner,source_runtime_handle_id,target_mode,target_account_id,target_generation,policy,phase,created_at,updated_at) VALUES ('switch','chosen','codex','managed','account-a',9,'{"Generation":"source"}','source-handle','managed','account-b','target','drain','waiting',?,?)`, now, now)
			}
			if count >= 4 {
				mustExec(t, db, `INSERT INTO accounts_manager_removals(id,account_id,impact,phase,created_at,updated_at) VALUES ('removal','removed','{"Revision":8,"Sessions":[]}','revoked',?,?)`, now, now)
			}
			if count >= 5 {
				mustExec(t, db, `UPDATE accounts_manager_switches SET retired_target_generation='retired',retired_target_handle_id='retired-handle' WHERE id='switch'`)
			}
			if count >= 6 {
				mustExec(t, db, `UPDATE accounts_manager_switches SET empty_source=1 WHERE id='switch'`)
			}
			if count >= 8 {
				mustExec(t, db, `UPDATE accounts_manager_removals SET stop_started=1,bindings_revoked=1 WHERE id='removal'`)
				mustExec(t, db, `INSERT INTO accounts_manager_chat_hosts(session_id,generation,host_identity) VALUES ('chosen','source',?)`, strings.Repeat("a", 64))
			}

			if err := migrate(db); err != nil {
				t.Fatalf("upgrade legacy account history: %v", err)
			}
			assertAccountsManagerUpgradeHistory(t, db)
			wantRevision := 1
			if count >= 2 {
				wantRevision = 9
			}
			var mode, account string
			var revision int
			if err := db.QueryRow(`SELECT connection_mode,account_id,revision FROM accounts_manager_session_bindings WHERE session_id='chosen'`).Scan(&mode, &account, &revision); err != nil {
				t.Fatal(err)
			}
			if mode != "managed" || account != "account-a" || revision != wantRevision {
				t.Fatalf("selected binding changed: %s/%s/%d", mode, account, revision)
			}
			if err := db.QueryRow(`SELECT connection_mode,account_id,revision FROM accounts_manager_session_bindings WHERE session_id='unrelated'`).Scan(&mode, &account, &revision); err != nil {
				t.Fatal(err)
			}
			if mode != "native" || account != "" || revision != 1 {
				t.Fatalf("unrelated native binding changed: %s/%s/%d", mode, account, revision)
			}
			if count >= 3 {
				var phase, owner, generation string
				if err := db.QueryRow(`SELECT phase,source_owner,target_generation FROM accounts_manager_switches WHERE id='switch'`).Scan(&phase, &owner, &generation); err != nil {
					t.Fatal(err)
				}
				if phase != "waiting" || owner != `{"Generation":"source"}` || generation != "target" {
					t.Fatal("upgrade changed the pending switch owner or decision")
				}
				var preserved int
				if err := db.QueryRow(`SELECT COUNT(*) FROM accounts_manager_queue_obligations WHERE turn_id='queued'`).Scan(&preserved); err != nil || preserved != 1 {
					t.Fatalf("queued obligation lost: count=%d err=%v", preserved, err)
				}
			}
			if count >= 4 {
				var phase, impact string
				var stopped, revoked int
				if err := db.QueryRow(`SELECT phase,impact,stop_started,bindings_revoked FROM accounts_manager_removals WHERE id='removal'`).Scan(&phase, &impact, &stopped, &revoked); err != nil {
					t.Fatal(err)
				}
				wantRevoked := 0
				if count == 8 {
					wantRevoked = 1
				}
				if phase != "revoked" || impact != `{"Revision":8,"Sessions":[]}` || stopped != 1 || revoked != wantRevoked {
					t.Fatalf("removal evidence changed: %s %s %d/%d", phase, impact, stopped, revoked)
				}
			}
			if count >= 5 {
				var generation, handle string
				if err := db.QueryRow(`SELECT retired_target_generation,retired_target_handle_id FROM accounts_manager_switches WHERE id='switch'`).Scan(&generation, &handle); err != nil || generation != "retired" || handle != "retired-handle" {
					t.Fatalf("retired owner lost: %s/%s err=%v", generation, handle, err)
				}
			}
			if count >= 6 {
				var empty int
				if err := db.QueryRow(`SELECT empty_source FROM accounts_manager_switches WHERE id='switch'`).Scan(&empty); err != nil || empty != 1 {
					t.Fatalf("empty-history proof lost: %d err=%v", empty, err)
				}
			}
			if count >= 8 {
				var identity string
				if err := db.QueryRow(`SELECT host_identity FROM accounts_manager_chat_hosts WHERE session_id='chosen' AND generation='source'`).Scan(&identity); err != nil || identity != strings.Repeat("a", 64) {
					t.Fatalf("host identity lost: %q err=%v", identity, err)
				}
			}
			if err := migrate(db); err != nil {
				t.Fatalf("second upgrade: %v", err)
			}
			assertAccountsManagerUpgradeHistory(t, db)
		})
	}
}

func TestMigrateAccountsManagerFromMainHistory(t *testing.T) {
	for _, version := range []int64{160, 161, 162, 163, 164, 172} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, version)
			if err := migrate(db); err != nil {
				t.Fatalf("upgrade main database: %v", err)
			}
			assertAccountsManagerUpgradeHistory(t, db)
		})
	}
}

func TestAccountsManagerHistoryRepairRejectsIncompleteProof(t *testing.T) {
	for name, damage := range map[string]string{
		"routing":          `DROP TABLE accounts_manager_session_routes`,
		"binding clock":    `DROP TRIGGER accounts_manager_bindings_update`,
		"switch ownership": `ALTER TABLE accounts_manager_switches DROP COLUMN retired_target_handle_id`,
		"queue":            `DROP TRIGGER accounts_manager_queue_adopted`,
		"deletion":         `DROP TABLE accounts_manager_chat_hosts`,
		"history":          `DELETE FROM goose_db_version WHERE version_id IN (164,168)`,
	} {
		t.Run(name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 160)
			applyLegacyAccountsManagerMigrations(t, db, 8)
			mustExec(t, db, damage)
			before := accountsManagerHistorySnapshot(t, db)
			if err := migrate(db); err == nil {
				t.Fatal("incomplete proof allowed account migration repair")
			}
			if after := accountsManagerHistorySnapshot(t, db); after != before {
				t.Fatal("failed proof changed migration history")
			}
		})
	}
}

func TestAccountsManagerHistoryRepairRollsBackAndReopens(t *testing.T) {
	for _, count := range []int{1, 4, 5, 8} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			dataDir := t.TempDir()
			db := openMigratedDatabaseCopyAt(t, dataDir, 160, pragmas)
			applyLegacyAccountsManagerMigrations(t, db, count)
			before := accountsManagerHistorySnapshot(t, db)
			mustExec(t, db, `CREATE TRIGGER fail_history_repair BEFORE DELETE ON goose_db_version WHEN OLD.version_id=161 BEGIN SELECT RAISE(ABORT,'synthetic history failure'); END`)
			if err := repairRenumberedAccountsManagerHistory(db); err == nil {
				t.Fatal("history repair ignored its commit failure")
			}
			if after := accountsManagerHistorySnapshot(t, db); after != before {
				t.Fatal("failed repair partially remapped history")
			}
			mustExec(t, db, `DROP TRIGGER fail_history_repair`)
			if err := repairRenumberedAccountsManagerHistory(db); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := sql.Open("sqlite", databaseURI(dataDir)+pragmas)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = reopened.Close() }()
			reopened.SetMaxOpenConns(1)
			if err := migrate(reopened); err != nil {
				t.Fatalf("restart after repair but before main migrations: %v", err)
			}
			assertAccountsManagerUpgradeHistory(t, reopened)
		})
	}
}

func accountsManagerHistorySnapshot(t *testing.T, db *sql.DB) string {
	t.Helper()
	var result string
	if err := db.QueryRow(`SELECT COALESCE(group_concat(entry,'|'),'') FROM (SELECT id || ':' || version_id || ':' || is_applied AS entry FROM goose_db_version ORDER BY id)`).Scan(&result); err != nil {
		t.Fatal(err)
	}
	return result
}

func applyLegacyAccountsManagerMigrations(t *testing.T, db *sql.DB, count int) {
	t.Helper()
	suffixes := []string{"routing", "bindings", "switches", "removals", "retry_owner", "history_decision", "queue_obligations", "deletion_coordination"}
	legacyFS := fstest.MapFS{}
	for i := 0; i < count; i++ {
		path := fmt.Sprintf("migrations/%04d_accounts_manager_%s.sql", 165+i, suffixes[i])
		contents, err := migrationsFS.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		legacyFS[fmt.Sprintf("migrations/%04d_accounts_manager_%s.sql", 161+i, suffixes[i])] = &fstest.MapFile{Data: contents}
	}
	gooseMu.Lock()
	defer gooseMu.Unlock()
	goose.SetBaseFS(legacyFS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatal(err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		t.Fatalf("apply legacy migrations: %v", err)
	}
}

func assertAccountsManagerUpgradeHistory(t *testing.T, db *sql.DB) {
	t.Helper()
	for version := 161; version <= 172; version++ {
		var applied int
		if err := db.QueryRow(`SELECT COALESCE((SELECT is_applied FROM goose_db_version WHERE version_id=? ORDER BY id DESC LIMIT 1),0)`, version).Scan(&applied); err != nil || applied != 1 {
			t.Fatalf("migration %d: applied=%d err=%v", version, applied, err)
		}
	}
	var tables, columns, harnesses, discussion int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name IN ('automations','automation_runs','accounts_manager_chat_hosts')`).Scan(&tables); err != nil || tables != 3 {
		t.Fatalf("main and account tables: %d err=%v", tables, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('automation_run_id','automation_launch_completed')`).Scan(&columns); err != nil || columns != 2 {
		t.Fatalf("automation columns: %d err=%v", columns, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='sessions' AND instr(sql,'''fx''')>0 AND instr(sql,'''gemini''')>0`).Scan(&harnesses); err != nil || harnesses != 1 {
		t.Fatalf("main harness constraints: %d err=%v", harnesses, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('pr') WHERE name IN ('discussion_commenters_json','discussion_comment_count')`).Scan(&discussion); err != nil || discussion != 0 {
		t.Fatalf("retired discussion columns: %d err=%v", discussion, err)
	}
}
