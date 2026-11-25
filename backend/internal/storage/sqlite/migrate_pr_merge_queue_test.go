package sqlite

import (
	"database/sql"
	"strings"
	"testing"
)

// The merge queue migration re-creates pr_cdc_update. It must keep what the
// trigger already carried: the startup repair releases migration 0111 when the
// trigger doesn't mention auto_inject_ci, and re-running 0111 would silently
// drop the queue conditions again on the next start.
func TestMergeQueueMigrationKeepsThePRUpdateTriggerAcrossRestarts(t *testing.T) {
	dataDir := t.TempDir()
	triggerSQL := func() string {
		t.Helper()
		db, err := sql.Open("sqlite", databaseURI(dataDir)+pragmas)
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var got string
		if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'trigger' AND name = 'pr_cdc_update'`).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	for start := 1; start <= 2; start++ {
		store, err := Open(dataDir)
		if err != nil {
			t.Fatalf("start %d: %v", start, err)
		}
		if err := store.Close(); err != nil {
			t.Fatal(err)
		}
		got := triggerSQL()
		for _, want := range []string{"OLD.auto_inject_ci <> NEW.auto_inject_ci", "'autoInjectCI'", "OLD.merge_queue_required <> NEW.merge_queue_required", "OLD.merge_queue_entry <> NEW.merge_queue_entry"} {
			if !strings.Contains(got, want) {
				t.Fatalf("start %d: pr_cdc_update is missing %q:\n%s", start, want, got)
			}
		}
	}
}
