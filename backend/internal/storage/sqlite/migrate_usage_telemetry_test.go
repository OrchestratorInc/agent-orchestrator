package sqlite

import "testing"

func TestUsageTelemetryMigrationBackfillsExistingWorkerPRs(t *testing.T) {
	dataDir := t.TempDir()
	db := openMigratedDatabaseCopyAt(t, dataDir, 20261024194546592, pragmas)
	for _, query := range []string{
		`INSERT INTO projects (id, path, registered_at) VALUES ('project', '/tmp/repo', CURRENT_TIMESTAMP)`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_last_at, created_at, updated_at)
		 VALUES ('worker', 'project', 1, 'worker', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO sessions (id, project_id, num, kind, activity_last_at, created_at, updated_at)
		 VALUES ('orchestrator', 'project', 2, 'orchestrator', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`,
		`INSERT INTO pr (url, session_id, number, pr_state, updated_at)
		 VALUES ('https://github.com/org/repo/pull/1', 'worker', 1, 'merged', CURRENT_TIMESTAMP)`,
		`INSERT INTO pr (url, session_id, number, pr_state, updated_at)
		 VALUES ('https://github.com/org/repo/pull/2', 'worker', 2, 'open', CURRENT_TIMESTAMP)`,
		`INSERT INTO pr (url, session_id, number, pr_state, updated_at)
		 VALUES ('https://github.com/org/repo/pull/3', 'orchestrator', 3, 'merged', CURRENT_TIMESTAMP)`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	facts, err := store.PendingWorkerPRUsage(t.Context(), 10)
	if err != nil || len(facts) != 2 {
		t.Fatalf("historical facts = %+v, err=%v", facts, err)
	}
	merged, open := 0, 0
	for _, fact := range facts {
		if !fact.Historical || !fact.AssociationPending {
			t.Fatalf("missing historical association: %+v", fact)
		}
		if fact.MergePending {
			if !fact.HistoricalMerged {
				t.Fatalf("merged PR lacked historical outcome: %+v", fact)
			}
			merged++
		} else {
			if fact.HistoricalMerged {
				t.Fatalf("open PR marked historical merge: %+v", fact)
			}
			open++
		}
	}
	if merged != 1 || open != 1 {
		t.Fatalf("historical merged=%d open=%d", merged, open)
	}
}
