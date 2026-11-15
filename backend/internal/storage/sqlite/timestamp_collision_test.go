package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
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
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('s',1,'aider','idle','2026-10-21 03:30:00 +0000 UTC',0,'2026-10-21 03:30:00 +0000 UTC','2026-10-21 03:30:00 +0000 UTC');
 INSERT INTO session_agent_accounts(session_id,provider,account_id,bound_at) VALUES('s','codex','first','2026-10-21 03:30:00 +0000 UTC'),('s','codex','last','2026-10-21 09:00:00 +0530 IST');`); err != nil {
		t.Fatal(err)
	}
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var count int
	var account string
	if err := db.QueryRow(`SELECT count(*),account_id FROM session_agent_accounts`).Scan(&count, &account); err != nil || count != 1 || account != "last" {
		t.Fatalf("bindings=%d account=%q err=%v", count, account, err)
	}
}
