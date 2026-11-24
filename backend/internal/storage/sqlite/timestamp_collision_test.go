package sqlite

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestTimestampFenceAutomationOccurrence(t *testing.T) {
	for _, twin := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy occurrence", true: "UTC twin"}[twin], func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			const legacy = "2026-10-21 09:00:00 +0530 IST"
			const utc = "2026-10-21 03:30:00 +0000 UTC"
			if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('p','/tmp/collision',?);
 INSERT INTO automations(id,project_id,display_name,prompt,kind,rrule_text,timezone,next_run_at,created_at,updated_at) VALUES('a','p','test','test','worker','FREQ=DAILY','UTC',?,?,?);`, utc, utc, utc, utc); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO automation_runs(id,automation_id,scheduled_for,status,attempt_count,finished_at,created_at,updated_at) VALUES('executed','a',?,'completed',1,?,?,?);`, legacy, legacy, legacy, legacy); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at,automation_run_id)
 VALUES('execution-session',1,'aider','idle',?,0,?,?,'executed');`, utc, utc, utc); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`UPDATE automation_runs SET session_id='execution-session' WHERE id='executed'`); err != nil {
				t.Fatal(err)
			}
			if twin {
				if _, err := db.Exec(`INSERT INTO automation_runs(id,automation_id,scheduled_for,status,created_at,updated_at) VALUES('duplicate','a',?,'pending',?,?)`, utc, utc, utc); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`CREATE TABLE aaa_repair(at TIMESTAMP); INSERT INTO aaa_repair VALUES('2026-10-21 09:00:00 +0530 IST'); CREATE TRIGGER block_background BEFORE UPDATE ON aaa_repair BEGIN SELECT RAISE(ABORT,'background blocked'); END`); err != nil {
				t.Fatal(err)
			}
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			at := time.Date(2026, 10, 21, 3, 30, 0, 0, time.UTC)
			got, created, err := s.CreateAutomationRun(context.Background(), domain.AutomationRun{ID: "new", AutomationID: "a", ScheduledFor: at, Status: "pending", CreatedAt: at, UpdatedAt: at})
			if err != nil || created || got.ID != "executed" {
				t.Fatalf("duplicate occurrence: %+v created=%v err=%v", got, created, err)
			}
			var linked string
			if err := db.QueryRow(`SELECT automation_run_id FROM sessions WHERE id='execution-session'`).Scan(&linked); err != nil || linked != "executed" {
				t.Fatalf("execution link=%q err=%v", linked, err)
			}
			var count int
			if err := db.QueryRow(`SELECT count(*) FROM automation_runs WHERE automation_id='a'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("occurrences=%d err=%v", count, err)
			}
		})
	}
}

func TestTimestampFenceAccountBindingCollision(t *testing.T) {
	for name, legacy := range map[string]string{"positive": "2026-10-21 09:00:00 +0530 IST", "negative": "2026-10-20 23:30:00 -0400 EDT"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('s',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO session_agent_accounts(session_id,provider,account_id,bound_at) VALUES('s','codex','first','2026-10-21 03:30:00 +0000 UTC'),('s','codex','last',?)`, legacy); err != nil {
				t.Fatal(err)
			}
			var last int64
			if err := db.QueryRow(`SELECT max(rowid) FROM session_agent_accounts`).Scan(&last); err != nil {
				t.Fatal(err)
			}
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
			defer slog.SetDefault(previous)
			s, err := Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			var count int
			var account string
			var survivor int64
			if err := db.QueryRow(`SELECT count(*),account_id,rowid FROM session_agent_accounts`).Scan(&count, &account, &survivor); err != nil || count != 1 || account != "last" || survivor != last {
				t.Fatalf("bindings=%d account=%q rowid=%d want=%d: %v", count, account, survivor, last, err)
			}
			current, bound, err := s.GetSessionAgentAccount(context.Background(), "s", domain.AgentAccountProviderCodex)
			if err != nil || !bound || current != "last" {
				t.Fatalf("effective account=%q bound=%v: %v", current, bound, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := runTimestampRepair(context.Background(), db, true); err != nil {
				t.Fatal(err)
			}
			if n := strings.Count(logs.String(), "timestamp repair dropped same-instant account binding"); n != 1 {
				t.Fatalf("drop warning occurred %d times: %s", n, logs.String())
			}
			for _, field := range []string{"session=s", "provider=codex", "kept_account=last", "dropped_account=first", "dropped_rowid=1"} {
				if !strings.Contains(logs.String(), field) {
					t.Errorf("drop warning missing %q: %s", field, logs.String())
				}
			}
		})
	}
}

func TestTimestampFencePreservesDistinctExecutionsAndOpensStore(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('p','/tmp/twins','2026-10-21 09:00:00 +0530 IST');
 INSERT INTO automations(id,project_id,display_name,prompt,kind,rrule_text,timezone,next_run_at,created_at,updated_at) VALUES('a','p','test','test','worker','FREQ=DAILY','UTC','2026-10-21 03:30:00 +0000 UTC','2026-10-21 03:30:00 +0000 UTC','2026-10-21 03:30:00 +0000 UTC');
 INSERT INTO automation_runs(id,automation_id,scheduled_for,status,attempt_count,created_at,updated_at) VALUES('local','a','2026-10-21 09:00:00 +0530 IST','completed',1,'2026-10-21 09:00:00 +0530 IST','2026-10-21 09:00:00 +0530 IST'),('utc','a','2026-10-21 03:30:00 +0000 UTC','completed',1,'2026-10-21 03:30:00 +0000 UTC','2026-10-21 03:30:00 +0000 UTC');
 INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at,automation_run_id) VALUES('linked-local',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'local'),('linked-utc',2,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,'utc');`); err != nil {
		t.Fatal(err)
	}
	snapshot := func() string {
		var value string
		if err := db.QueryRow(`SELECT json_group_array(json_array(id,automation_id,CAST(scheduled_for AS TEXT),status,attempt_count,CAST(created_at AS TEXT),CAST(updated_at AS TEXT))) FROM (SELECT * FROM automation_runs ORDER BY id)`).Scan(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := snapshot()
	s, err := Open(dir)
	if err != nil {
		t.Fatalf("preserved execution history blocked startup: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("execution history changed: %s", after)
	}
	var linked int
	if err := db.QueryRow(`SELECT count(*) FROM sessions WHERE automation_run_id IN ('local','utc')`).Scan(&linked); err != nil || linked != 2 {
		t.Fatalf("links=%d err=%v", linked, err)
	}
	result, err := runTimestampRepair(context.Background(), db, true)
	if err != nil || result.Skipped != 0 || result.Repaired != 0 || result.Deleted != 0 {
		t.Fatalf("completed collision must not be rescanned: %+v %v", result, err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("retry changed execution history: %s", after)
	}
}

func TestTimestampExecutionCollisionCanRecoverAfterKeyChanges(t *testing.T) {
	db := openMigratedTestDB(t)
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('retry-policy','/tmp/retry-policy',CURRENT_TIMESTAMP);
INSERT INTO automations(id,project_id,display_name,prompt,kind,rrule_text,timezone,next_run_at,created_at,updated_at) VALUES('a','retry-policy','test','test','worker','FREQ=DAILY','UTC',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO automation_runs(id,automation_id,scheduled_for,status,attempt_count,created_at,updated_at) VALUES('local','a','2026-10-21 09:00:00 +0530 IST','completed',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),('utc','a','2026-10-21 03:30:00 +0000 UTC','completed',1,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	if result, err := runTimestampRepair(context.Background(), db, true); err != nil || result.Skipped != 1 {
		t.Fatalf("collision was not preserved: %+v %v", result, err)
	}
	if _, err := db.Exec(`UPDATE automation_runs SET scheduled_for='2026-10-22 03:30:00 +0000 UTC' WHERE id='utc'`); err != nil {
		t.Fatal(err)
	}
	if _, err := runTimestampRepair(context.Background(), db, true); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.QueryRow(`SELECT CAST(scheduled_for AS TEXT) FROM automation_runs WHERE id='local'`).Scan(&raw); err != nil || raw != "2026-10-21 03:30:00 +0000 UTC" {
		t.Fatalf("policy audit prevented retry: %q %v", raw, err)
	}
}

func TestTimestampFencePreservesCheckRejectedSwitchAndOpensStore(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	const requested = "2026-10-21 09:00:00 -0400 EDT"
	const updated = "2026-10-21 10:00:00 +0000 UTC"
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at)
 VALUES('check-switch',1,'aider','idle',?,0,?,?)`, requested, requested, requested); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO agent_switches(id,session_id,idempotency_key,request_fingerprint,from_harness,target_harness,source_generation_id,requested_at,updated_at)
 VALUES('check-switch','check-switch','key',?,'aider','codex','source',?,?)`, "v1:"+strings.Repeat("0", 64), requested, updated); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(previous)
	for range 2 {
		done := make(chan struct{})
		s, err := openWithTimestampOptions(dir, timestampOpenOptions{fenceBudget: time.Minute, repairDone: done})
		if err != nil {
			t.Fatalf("CHECK-rejected legacy switch blocked startup: %v", err)
		}
		<-done
		_, found, readErr := s.GetSession(context.Background(), "check-switch")
		closeErr := s.Close()
		if readErr != nil || !found || closeErr != nil {
			t.Fatalf("store unavailable after preserving switch: found=%v read=%v close=%v", found, readErr, closeErr)
		}
		var gotRequested, gotUpdated string
		if err := db.QueryRow(`SELECT CAST(requested_at AS TEXT),CAST(updated_at AS TEXT) FROM agent_switches WHERE id='check-switch'`).Scan(&gotRequested, &gotUpdated); err != nil || gotRequested != requested || gotUpdated != updated {
			t.Fatalf("rejected switch changed: requested=%q updated=%q err=%v", gotRequested, gotUpdated, err)
		}
		var sessionUpdated string
		if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions WHERE id='check-switch'`).Scan(&sessionUpdated); err != nil || sessionUpdated != "2026-10-21 13:00:00 +0000 UTC" {
			t.Fatalf("repair stopped before sessions: %q %v", sessionUpdated, err)
		}
	}
	if !strings.Contains(logs.String(), "timestamp repair skipped row") || !strings.Contains(logs.String(), "table=agent_switches") {
		t.Fatalf("preserved switch was not reported: %s", logs.String())
	}
}
