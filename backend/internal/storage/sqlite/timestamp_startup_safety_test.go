package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestTimestampFailedFenceWithholdsTimeQueries(t *testing.T) {
	for _, failure := range []string{"deadline", "constraint"} {
		t.Run(failure, func(t *testing.T) {
			dir := t.TempDir()
			db := openMigratedTestDBIn(t, dir)
			const legacy = "2026-10-21 09:00:00 +0530 IST"
			if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at) VALUES('p','/tmp/fence-safety',?);
INSERT INTO sessions(id,project_id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('s','p',1,'aider','idle',?,0,?,?);
INSERT INTO reports(id,session_id,project_id,created_at,available_at) VALUES('due','s','p',?,?);
INSERT INTO notifications(id,session_id,project_id,type,title,pr_url,created_at) VALUES('n','s','p','needs_input','pending','https://example.test/p',?);`, legacy, legacy, legacy, legacy, legacy, legacy, legacy); err != nil {
				t.Fatal(err)
			}
			budget := 30 * time.Second
			if failure == "deadline" {
				budget = time.Nanosecond
			} else if _, err := db.Exec(`CREATE TRIGGER block_reports BEFORE UPDATE ON reports BEGIN SELECT RAISE(ABORT,'blocked report repair'); END;
CREATE TRIGGER block_notifications BEFORE UPDATE ON notifications BEGIN SELECT RAISE(ABORT,'blocked notification repair'); END;`); err != nil {
				t.Fatal(err)
			}
			s, err := openWithTimestampOptions(dir, timestampOpenOptions{fenceBudget: budget})
			if err == nil || s != nil {
				if s != nil {
					_ = s.Close()
				}
				t.Fatalf("failed fence exposed scheduling and pagination: store=%v err=%v", s, err)
			}
			if failure == "deadline" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost deadline cause: %v", err)
			}
			var raw string
			if err := db.QueryRow(`SELECT CAST(available_at AS TEXT) FROM reports`).Scan(&raw); err != nil || raw != legacy {
				t.Fatalf("fixture no longer exercises unrepaired report: %q %v", raw, err)
			}
			if _, err := db.Exec(`DROP TRIGGER IF EXISTS block_reports; DROP TRIGGER IF EXISTS block_notifications;`); err != nil {
				t.Fatal(err)
			}
			s, err = Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			reports, err := s.ListPendingReports(context.Background(), time.Date(2026, 10, 21, 4, 30, 0, 0, time.UTC), 10)
			if err != nil || len(reports) != 1 || reports[0].ID != "due" {
				t.Fatalf("reopen omitted due report: %+v %v", reports, err)
			}
		})
	}
}

func TestTimestampRepairWithConcurrentReadSnapshot(t *testing.T) {
	dir := t.TempDir()
	db := openMigratedTestDBIn(t, dir)
	reader, err := sql.Open("ao-sqlite", databaseURI(dir)+pragmas)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	tx, err := reader.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var count int
	if err := tx.QueryRow(`SELECT count(*) FROM sessions`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at) VALUES('snapshot',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,'2026-10-21 09:00:00 +0530 IST')`); err != nil {
		t.Fatal(err)
	}
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatalf("reader snapshot prevented repair: %v", err)
	}
	var raw string
	if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions`).Scan(&raw); err != nil || raw != "2026-10-21 03:30:00 +0000 UTC" {
		t.Fatalf("snapshot repair failed: %q %v", raw, err)
	}
}
