package sqlite

import (
	"testing"
	"time"
)

// TestMigrationAllowGooseUsageDownPreservesEarlierUsage catches rolling back
// Goose by deleting sibling-provider usage owned by an earlier migration. Goose
// is stacked on Qwen, so rollback must keep Qwen usage and still admit Qwen
// bindings.
func TestMigrationAllowGooseUsageDownPreservesEarlierUsage(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261102174702749)
	upTo(t, db, 20261102185757162)
	now := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at) VALUES ('usage-migration', '/tmp/usage-migration', 'usage', ?)`, now); err != nil {
		t.Fatal(err)
	}
	harnesses := []string{"kimi", "pi", "qwen", "goose"}
	kinds := map[string]string{
		"kimi": "kimi_wire", "pi": "pi_session", "qwen": "qwen_monthly", "goose": "goose_ledger",
	}
	for index, harness := range harnesses {
		if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, harness, activity_state, activity_last_at, is_terminated, created_at, updated_at) VALUES (?, 'usage-migration', ?, 'worker', ?, 'active', ?, 0, ?, ?)`, "session-"+harness, index+1, harness, now, now, now); err != nil {
			t.Fatalf("seed %s session: %v", harness, err)
		}
		result, err := db.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES (?, ?, ?, 'active', ?)`, "session-"+harness, harness, "native-"+harness, now)
		if err != nil {
			t.Fatalf("seed %s binding: %v", harness, err)
		}
		bindingID, _ := result.LastInsertId()
		result, err = db.Exec(`INSERT INTO usage_sources (binding_id, kind, native_session_id, artifact_path, state, updated_at) VALUES (?, ?, ?, ?, 'active', ?)`, bindingID, kinds[harness], "native-"+harness, "/tmp/"+harness+".jsonl", now)
		if err != nil {
			t.Fatalf("seed %s source: %v", harness, err)
		}
		sourceID, _ := result.LastInsertId()
		_, err = db.Exec(`INSERT INTO model_usage_events (
            binding_id, usage_source_id, provider_id, model_id,
            usage_measurement_kind, input_tokens, cached_input_tokens,
            uncached_input_tokens, output_tokens, provider_usage_json,
            source_event_key, created_at
        ) VALUES (?, ?, 'openai', 'model', 'native_reported', 3, 1, 2, 1,
                  '{"totalTokens":4}', ?, ?)`, bindingID, sourceID, "event-"+harness, now)
		if err != nil {
			t.Fatalf("seed %s event: %v", harness, err)
		}
	}

	downTo(t, db, 20261102174702749)
	counts := map[string]int{}
	for _, harness := range harnesses {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM model_usage_events event JOIN usage_bindings binding ON binding.id = event.binding_id WHERE binding.harness = ?`, harness).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[harness] = count
	}
	if counts["kimi"] != 1 || counts["pi"] != 1 || counts["qwen"] != 1 || counts["goose"] != 0 {
		t.Fatalf("events after Goose rollback = %+v, want kimi:1 pi:1 qwen:1 goose:0", counts)
	}
	if _, err := db.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES ('session-kimi', 'goose', 'new-goose', 'active', ?)`, now); err == nil {
		t.Fatal("rollback still admits Goose usage bindings")
	}
	if _, err := db.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES ('session-kimi', 'qwen', 'new-qwen', 'active', ?)`, now); err != nil {
		t.Fatalf("rollback rejects Qwen usage allowed by the predecessor schema: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO usage_sources (binding_id, kind, native_session_id, artifact_path, state, updated_at) SELECT id, 'qwen_monthly', 'native-qwen', '/tmp/qwen-2.jsonl', 'active', ? FROM usage_bindings WHERE harness = 'qwen' LIMIT 1`, now); err != nil {
		t.Fatalf("rollback rejects Qwen usage sources allowed by the predecessor schema: %v", err)
	}
}
