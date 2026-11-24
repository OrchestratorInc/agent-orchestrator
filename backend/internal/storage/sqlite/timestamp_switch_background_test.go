package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestTimestampSwitchBackgroundFailureKeepsHandoffAvailable(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	const legacy = "2026-10-21 09:00:00 +0530 +0530"
	const later = "2026-10-21 04:00:00 +0000 UTC"
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 VALUES('switch-background',1,'codex','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
 INSERT INTO agent_native_sessions(id,ao_session_id,harness,native_session_id,created_at,last_used_at)
 VALUES('switch-native','switch-background','codex','native',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	for _, row := range []struct{ id, at, path string }{{"legacy", legacy, "older-handoff"}, {"later", later, "later-handoff"}} {
		if _, err := db.Exec(`INSERT INTO agent_switches(id,session_id,idempotency_key,request_fingerprint,from_harness,target_harness,
 source_generation_id,target_native_session_ref,state,requested_at,updated_at,final_handoff_path)
 VALUES(?,'switch-background',?,?,'aider','codex','source','switch-native','completed',?,?,?)`, row.id, row.id, "v1:"+strings.Repeat("0", 64), row.at, row.at, row.path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TRIGGER block_switch_repair BEFORE UPDATE ON agent_switches BEGIN SELECT RAISE(ABORT,'switch history is busy'); END`); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	s, err := openWithTimestampOptions(dir, timestampOpenOptions{fenceBudget: time.Minute, repairDone: done})
	if err != nil {
		t.Fatalf("background switch repair prevented Open: %v", err)
	}
	defer s.Close()
	<-done
	var raw string
	if err := db.QueryRow(`SELECT CAST(requested_at AS TEXT) FROM agent_switches WHERE id='legacy'`).Scan(&raw); err != nil || raw != legacy {
		t.Fatalf("blocked switch row changed: %q err=%v", raw, err)
	}
	path, err := s.LegacySwitchHandoffPath(context.Background(), "switch-background", domain.HarnessCodex, "native")
	if err != nil || path != "later-handoff" {
		t.Fatalf("handoff during pending repair=%q want later-handoff: %v", path, err)
	}
}
