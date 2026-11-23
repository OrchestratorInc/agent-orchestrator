package sqlite

import (
	"testing"
	"time"
)

func TestMigrationOpenUsageSourceKinds(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 20261102164353715)
	upTo(t, db, 20261103030401452)
	now := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at) VALUES ('usage-migration', '/tmp/usage-migration', 'usage', ?)`, now); err != nil {
		t.Fatal(err)
	}
	seeds := []struct{ harness, kind string }{
		{"qwen", "qwen_monthly"},
		{"kimi", "kimi_wire"},
		{"goose", "goose_ledger"},
	}
	for index, seed := range seeds {
		if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, harness, activity_state, activity_last_at, is_terminated, created_at, updated_at) VALUES (?, 'usage-migration', ?, 'worker', ?, 'active', ?, 0, ?, ?)`, "session-"+seed.harness, index+1, seed.harness, now, now, now); err != nil {
			t.Fatalf("seed %s session: %v", seed.harness, err)
		}
		result, err := db.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES (?, ?, ?, 'active', ?)`, "session-"+seed.harness, seed.harness, "native-"+seed.harness, now)
		if err != nil {
			t.Fatalf("seed %s binding: %v", seed.harness, err)
		}
		bindingID, _ := result.LastInsertId()
		result, err = db.Exec(`INSERT INTO usage_sources (binding_id, kind, native_session_id, artifact_path, state, updated_at) VALUES (?, ?, ?, ?, 'active', ?)`, bindingID, seed.kind, "native-"+seed.harness, "/tmp/"+seed.harness+".jsonl", now)
		if err != nil {
			t.Fatalf("seed %s source: %v", seed.harness, err)
		}
		sourceID, _ := result.LastInsertId()
		if _, err := db.Exec(`INSERT INTO model_usage_events (
            binding_id, usage_source_id, provider_id, model_id,
            usage_measurement_kind, input_tokens, cached_input_tokens,
            uncached_input_tokens, output_tokens, provider_usage_json,
            source_event_key, created_at
        ) VALUES (?, ?, 'openai', 'model', 'native_reported', 3, 1, 2, 1,
                  '{"totalTokens":4}', ?, ?)`, bindingID, sourceID, "event-"+seed.harness, now); err != nil {
			t.Fatalf("seed %s event: %v", seed.harness, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES ('session-goose', '  ', 'blank', 'active', ?)`, now); err == nil {
		t.Fatal("open schema admitted a blank harness")
	}

	downTo(t, db, 20261102164353715)
	counts := map[string]int{}
	for _, seed := range seeds {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM model_usage_events event JOIN usage_bindings binding ON binding.id = event.binding_id WHERE binding.harness = ?`, seed.harness).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[seed.harness] = count
	}
	if counts["qwen"] != 0 || counts["kimi"] != 1 || counts["goose"] != 0 {
		t.Fatalf("events after rollback = %+v, want qwen:0 kimi:1 goose:0", counts)
	}
	if _, err := db.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES ('session-goose', 'goose', 'again', 'active', ?)`, now); err == nil {
		t.Fatal("rollback still admits harnesses outside the predecessor schema")
	}
	if _, err := db.Exec(`INSERT INTO usage_bindings (session_id, harness, native_root_id, state, updated_at) VALUES ('session-kimi', 'kimi', 'again', 'active', ?)`, now); err != nil {
		t.Fatalf("rollback rejects Kimi usage allowed by the predecessor schema: %v", err)
	}
}
