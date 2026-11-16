package sqlite

import (
	"context"
	"testing"
	"time"
	_ "time/tzdata"
)

func TestRepairProjectAndSessionNumericOffsetColumns(t *testing.T) {
	db := openMigratedTestDB(t)
	loc, err := time.LoadLocation("Asia/Colombo")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 14, 18, 15, 40, 123456789, loc)
	stored := at.String() + " m=+1.234"
	if _, err := db.Exec(`INSERT INTO projects(id,path,registered_at,archived_at)
VALUES('legacy','/tmp/legacy',?,?);
INSERT INTO sessions(id,num,harness,activity_state,activity_last_at,is_terminated,created_at,updated_at,native_identity_observed_at,first_signal_at)
VALUES('legacy-session',1,'aider','idle',CURRENT_TIMESTAMP,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,?,?);`, stored, stored, stored, stored); err != nil {
		t.Fatal(err)
	}
	queries := []string{
		`SELECT registered_at FROM projects WHERE id='legacy'`,
		`SELECT archived_at FROM projects WHERE id='legacy'`,
		`SELECT native_identity_observed_at FROM sessions WHERE id='legacy-session'`,
		`SELECT first_signal_at FROM sessions WHERE id='legacy-session'`,
	}
	for _, query := range queries {
		if err := canScanTime(db, query); err == nil {
			t.Fatalf("fixture is readable before repair: %s", query)
		}
	}
	for range 2 {
		if err := repairTimestamps(context.Background(), db); err != nil {
			t.Fatal(err)
		}
		for _, query := range queries {
			var got time.Time
			if err := db.QueryRow(query).Scan(&got); err != nil {
				t.Fatalf("%s: %v", query, err)
			}
			if !got.Equal(at) || got.Location() != time.UTC {
				t.Fatalf("%s: got %v, want %v", query, got, at.UTC())
			}
		}
	}
	for _, query := range []string{
		`SELECT CAST(registered_at AS TEXT),CAST(archived_at AS TEXT) FROM projects WHERE id='legacy'`,
		`SELECT CAST(native_identity_observed_at AS TEXT),CAST(first_signal_at AS TEXT) FROM sessions WHERE id='legacy-session'`,
	} {
		var first, second string
		if err := db.QueryRow(query).Scan(&first, &second); err != nil {
			t.Fatal(err)
		}
		if first != at.UTC().String() || second != at.UTC().String() {
			t.Fatalf("repair stored %q, %q; want %q", first, second, at.UTC().String())
		}
	}
}
