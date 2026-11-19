package sqlite

import (
	"fmt"
	"testing"
	"time"
)

func TestMigrationUsageRepricingQueuesUntieredSources(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261103094403671)
	now := time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at) VALUES ('repricing', '/tmp/repricing', 'repricing', ?)`, now); err != nil {
		t.Fatal(err)
	}
	seeds := []struct {
		harness, kind string
		tier          any
	}{
		{"codex", "codex_rollout", nil},
		{"claude-code", "claude_main", nil},
		{"codex", "codex_rollout", "priority"},
		{"pi", "pi_session", nil},
	}
	sourceIDs := make([]int64, len(seeds))
	for index, seed := range seeds {
		sessionID := "session-" + string(rune('a'+index))
		if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, harness, activity_state, activity_last_at, is_terminated, created_at, updated_at) VALUES (?, 'repricing', ?, 'worker', ?, 'active', ?, 0, ?, ?)`, sessionID, index+1, seed.harness, now, now, now); err != nil {
			t.Fatalf("seed session %d: %v", index, err)
		}
		result, err := db.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES (?, ?, ?, 'active', ?)`, sessionID, seed.harness, sessionID, now)
		if err != nil {
			t.Fatalf("seed binding %d: %v", index, err)
		}
		bindingID, _ := result.LastInsertId()
		result, err = db.Exec(`INSERT INTO usage_sources (binding_id, kind, native_session_id, artifact_path, state, updated_at) VALUES (?, ?, ?, ?, 'active', ?)`, bindingID, seed.kind, sessionID, "/tmp/"+sessionID+".jsonl", now)
		if err != nil {
			t.Fatalf("seed source %d: %v", index, err)
		}
		sourceIDs[index], _ = result.LastInsertId()
		if _, err := db.Exec(`INSERT INTO model_usage_events (
            binding_id, usage_source_id, provider_id, model_id,
            usage_measurement_kind, input_tokens, cached_input_tokens,
            uncached_input_tokens, output_tokens, source_event_key, created_at, service_tier
        ) VALUES (?, ?, 'openai', 'model', 'native_reported', 3, 1, 2, 1, ?, ?, ?)`, bindingID, sourceIDs[index], sessionID, now, seed.tier); err != nil {
			t.Fatalf("seed event %d: %v", index, err)
		}
	}

	upTo(t, db, 20261105105237146)
	countObjects := func() int {
		t.Helper()
		var count int
		if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('usage_tier_repairs', 'usage_price_overrides', 'idx_model_usage_events_model', 'idx_model_usage_events_priced_version')`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if got := countObjects(); got != 4 {
		t.Fatalf("migration created %d of 4 repricing objects", got)
	}
	var queued string
	if err := db.QueryRow(`SELECT coalesce(group_concat(usage_source_id, ','), '') FROM (SELECT usage_source_id FROM usage_tier_repairs ORDER BY usage_source_id)`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("%d,%d", sourceIDs[0], sourceIDs[1]); queued != want {
		t.Fatalf("queued sources = %q, want the untiered Codex and Claude sources %q", queued, want)
	}

	downTo(t, db, 20261103094403671)
	if got := countObjects(); got != 0 {
		t.Fatalf("rollback left %d repricing objects", got)
	}
}
