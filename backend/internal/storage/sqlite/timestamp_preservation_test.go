package sqlite

import (
	"context"
	"strings"
	"testing"
)

func TestRepairPreservesSessionRevisionAndChangeLog(t *testing.T) {
	db := openMigratedTestDB(t)
	const legacy = "2026-10-21 09:00:00.123456789 +0530 IST"
	_, err := db.Exec(`INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at,latest_user_prompt_at,latest_interaction_at)
VALUES('preserved',1,'aider','idle',?,0,?,?,?,?);`, legacy, legacy, legacy, legacy, legacy)
	if err != nil {
		t.Fatal(err)
	}
	state := func() [3]int64 {
		t.Helper()
		var values [3]int64
		if err := db.QueryRow(`SELECT revision,(SELECT count(*) FROM change_log),COALESCE((SELECT seq FROM sqlite_sequence WHERE name='change_log'),-1) FROM sessions WHERE id='preserved'`).Scan(&values[0], &values[1], &values[2]); err != nil {
			t.Fatal(err)
		}
		return values
	}
	for _, name := range []string{"normal", "missing sequence", "validation error", "cleanup error"} {
		t.Run(name, func(t *testing.T) {
			if _, err := db.Exec(`UPDATE sessions SET activity_last_at=?,created_at=?,updated_at=?,latest_user_prompt_at=?,latest_interaction_at=? WHERE id='preserved'`, legacy, legacy, legacy, legacy, legacy); err != nil {
				t.Fatal(err)
			}
			if name == "missing sequence" {
				if _, err := db.Exec(`DELETE FROM sqlite_sequence WHERE name='change_log'`); err != nil {
					t.Fatal(err)
				}
			}
			if name == "validation error" {
				if _, err := db.Exec(`CREATE TRIGGER keep_validation BEFORE UPDATE ON sessions BEGIN SELECT RAISE(ABORT,'validation retained'); END`); err != nil {
					t.Fatal(err)
				}
				defer func() {
					if _, err := db.Exec(`DROP TRIGGER keep_validation`); err != nil {
						t.Fatal(err)
					}
				}()
			}
			if name == "cleanup error" {
				if _, err := db.Exec(`CREATE TRIGGER fail_cleanup BEFORE DELETE ON change_log BEGIN SELECT RAISE(ABORT,'cleanup retained'); END`); err != nil {
					t.Fatal(err)
				}
			}
			before := state()
			var beforeTime string
			if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions WHERE id='preserved'`).Scan(&beforeTime); err != nil {
				t.Fatal(err)
			}
			err := repairTimestamps(context.Background(), db)
			if strings.HasSuffix(name, "error") {
				want := strings.TrimSuffix(name, " error") + " retained"
				if err == nil || !strings.Contains(err.Error(), want) {
					t.Fatalf("%s was not retained: %v", name, err)
				}
				var afterTime string
				if err := db.QueryRow(`SELECT CAST(updated_at AS TEXT) FROM sessions WHERE id='preserved'`).Scan(&afterTime); err != nil || afterTime != beforeTime {
					t.Fatalf("failed repair changed timestamp from %q to %q: %v", beforeTime, afterTime, err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if after := state(); after != before {
				t.Fatalf("representation-only repair changed [session revision, change-log rows, change-log sequence] from %v to %v", before, after)
			}
		})
	}
}
