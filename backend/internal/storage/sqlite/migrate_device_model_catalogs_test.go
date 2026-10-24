package sqlite

import (
	"slices"
	"testing"
	"time"
)

// Model catalogs are device-wide. Older builds wrote a row per project (and a
// Settings browse scope); the migration drops those and keeps the device row
// and cloud credential scopes.
func TestMigrationDropsPerProjectModelCatalogs(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261021230230412)

	now := time.Now().UTC()
	for _, scope := range []string{"", "@cred:openai_api_key", "agent-orchestrator", "@browse:all"} {
		if _, err := db.Exec(`INSERT INTO agent_model_catalog (agent_id, project_id, catalog_json, source, fetched_at)
			VALUES ('codex', ?, '{}', 'cli', ?)`, scope, now); err != nil {
			t.Fatalf("seed scope %q: %v", scope, err)
		}
	}

	upTo(t, db, 20261022054735000)

	rows, err := db.Query(`SELECT project_id FROM agent_model_catalog WHERE agent_id = 'codex' ORDER BY project_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var got []string
	for rows.Next() {
		var scope string
		if err := rows.Scan(&scope); err != nil {
			t.Fatal(err)
		}
		got = append(got, scope)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"", "@cred:openai_api_key"}; !slices.Equal(got, want) {
		t.Fatalf("remaining scopes = %q, want %q", got, want)
	}
}
