package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sqlitestore "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestProviderAccountsUpgradePreservesExistingPreviewData(t *testing.T) {
	for _, tc := range []struct {
		name     string
		versions []int
		managed  bool
		pending  bool
	}{
		{name: "main database"},
		{name: "old account manager 171", versions: []int{171}},
		{name: "old account manager 171 and 172", versions: []int{171, 172}},
		{name: "early CLIProxy state", versions: []int{171}, managed: true},
		{name: "early CLIProxy pending cleanup", versions: []int{171, 172}, managed: true, pending: true},
		// Later development builds recorded the account manager at numbers main
		// has since given to other features.
		{name: "account manager at 173 and 180 to 182", versions: []int{173, 180, 181, 182}, managed: true},
		{name: "every number an account manager build used", versions: []int{171, 172, 173, 180, 181, 182}, managed: true, pending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := openMigratedDatabaseCopy(t, 170)
			if _, err := db.Exec(`
INSERT INTO projects (id,path,registered_at) VALUES ('kept-project','/kept-project',CURRENT_TIMESTAMP);
INSERT INTO sessions (id,project_id,num,harness,activity_last_at,created_at,updated_at)
VALUES ('kept-session','kept-project',1,'codex',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
CREATE TABLE accounts_manager_routing_policies (id TEXT PRIMARY KEY, choice TEXT);
INSERT INTO accounts_manager_routing_policies VALUES ('kept-policy','kept-choice');
`); err != nil {
				t.Fatal(err)
			}
			for _, version := range tc.versions {
				if _, err := db.Exec(`INSERT INTO goose_db_version(version_id,is_applied) VALUES (?,1)`, version); err != nil {
					t.Fatal(err)
				}
			}
			want := domain.ProviderAccountState{Accounts: []domain.ProviderAccount{}, Primaries: []domain.ProviderPrimary{}, Routes: []domain.ProviderSessionRoute{}}
			var wantPending *domain.ProviderAccountIntent
			if tc.managed {
				migration, err := migrationsFS.ReadFile("migrations/0195_provider_accounts.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(strings.Split(string(migration), "-- +goose Down")[0]); err != nil {
					t.Fatal(err)
				}
				want.Revision = 7
				want.Accounts = []domain.ProviderAccount{{ID: "kept-account", Provider: "codex", Email: "kept@example.com", CredentialRef: "kept.json", AuthID: "kept-auth"}}
				want.Primaries = []domain.ProviderPrimary{{Provider: "codex", PrimaryID: "kept-account"}}
				facts, err := json.Marshal(want)
				if err != nil {
					t.Fatal(err)
				}
				var pending sql.NullString
				if tc.pending {
					next := want
					next.Revision++
					wantPending = &domain.ProviderAccountIntent{Next: next, DeleteCredential: "old.json"}
					encoded, err := json.Marshal(wantPending)
					if err != nil {
						t.Fatal(err)
					}
					pending = sql.NullString{String: string(encoded), Valid: true}
				}
				if _, err := db.Exec(`UPDATE provider_account_state SET revision=?,facts=?,pending=? WHERE id=1`, want.Revision, string(facts), pending); err != nil {
					t.Fatal(err)
				}
			}
			for attempt := 0; attempt < 2; attempt++ {
				if err := migrate(db); err != nil {
					t.Fatalf("upgrade %d: %v", attempt, err)
				}
				store := sqlitestore.NewStore(db, db)
				got, pending, err := store.LoadProviderAccountState(context.Background())
				if err != nil || !reflect.DeepEqual(got, want) || !reflect.DeepEqual(pending, wantPending) {
					t.Fatalf("upgrade changed account facts or cleanup: state=%+v pending=%+v error=%v", got, pending, err)
				}
				var session, policy string
				if err := db.QueryRow(`SELECT id FROM sessions WHERE id='kept-session'`).Scan(&session); err != nil || session != "kept-session" {
					t.Fatal("upgrade changed the existing session", err)
				}
				if err := db.QueryRow(`SELECT choice FROM accounts_manager_routing_policies WHERE id='kept-policy'`).Scan(&policy); err != nil || policy != "kept-choice" {
					t.Fatal("upgrade changed the older manager's data", err)
				}
				var versions, triggers int
				if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id=195 AND is_applied=1`).Scan(&versions); err != nil || versions != 1 {
					t.Fatal("account upgrade was not recorded exactly once", err)
				}
				// A build that recorded the account manager under one of main's
				// numbers must still get main's migration with that number.
				var mainSchema int
				if err := db.QueryRow(`
SELECT (SELECT COUNT(*) FROM pragma_table_info('shell_terminals') WHERE name='preview_capability_verifier')
     + (SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name IN ('client_request_id','provision_steps','artifact_dir'))
     + (SELECT COUNT(*) FROM pragma_table_info('conversation_messages') WHERE name='client_payload_hash')`).Scan(&mainSchema); err != nil || mainSchema != 5 {
					t.Fatalf("upgrade skipped a migration main owns: found %d of 5 columns, error=%v", mainSchema, err)
				}
				for _, version := range []int{171, 172, 173, 180, 181} {
					if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id=? AND is_applied=1`, version).Scan(&versions); err != nil || versions != 1 {
						t.Fatalf("migration %d recorded %d times, want once: %v", version, versions, err)
					}
				}
				if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='trigger' AND name='provider_account_routes_cdc'`).Scan(&triggers); err != nil || triggers != 1 {
					t.Fatal("account upgrade lost its session change trigger", err)
				}
			}
		})
	}
}

// A development database that recorded the Cue migration without creating its
// table must still take the later migrations that alter that table.
func TestMigrateReplaysCueMigrationRecordedWithoutItsTable(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 170)
	if _, err := db.Exec(`DROP TABLE cues`); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if err := migrate(db); err != nil {
			t.Fatalf("upgrade %d: %v", attempt, err)
		}
	}
	var cues, recorded int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='cues'`).Scan(&cues); err != nil || cues != 1 {
		t.Fatalf("cues table missing after upgrade: %d %v", cues, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM goose_db_version WHERE version_id=168 AND is_applied=1`).Scan(&recorded); err != nil || recorded != 1 {
		t.Fatalf("migration 168 recorded %d times, want once: %v", recorded, err)
	}
}
