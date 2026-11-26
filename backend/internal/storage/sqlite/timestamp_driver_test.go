package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"
)

func TestLegacyTimestampReadsPreserveTypesAndInstants(t *testing.T) {
	db, err := sql.Open("ao-sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	const bad = "2026-10-21 18:15:40.123456789 +0200 +0200 m=+5.31"
	_, err = db.Exec(`CREATE TABLE sample(at TIMESTAMP,plain TEXT,blob TIMESTAMP,missing TIMESTAMP); INSERT INTO sample VALUES (?,?,X'2B30323030',NULL)`, bad, bad)
	if err != nil {
		t.Fatal(err)
	}
	check := func(row *sql.Row) {
		t.Helper()
		var at time.Time
		var plain string
		var blob []byte
		var missing sql.NullTime
		if err := row.Scan(&at, &plain, &blob, &missing); err != nil {
			t.Fatal(err)
		}
		if want := time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC); !at.Equal(want) {
			t.Fatalf("got %v, want %v", at, want)
		}
		if plain != bad || string(blob) != "+0200" || missing.Valid {
			t.Fatalf("changed non-time values: %q %q %v", plain, blob, missing)
		}
	}
	const query = `SELECT at,plain,blob,missing FROM sample`
	check(db.QueryRow(query))
	stmt, err := db.PrepareContext(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	check(stmt.QueryRowContext(context.Background()))
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	types, err := rows.ColumnTypes()
	defer rows.Close()
	if err != nil || types[0].DatabaseTypeName() != "TIMESTAMP" || types[1].DatabaseTypeName() != "TEXT" {
		t.Fatalf("metadata: %v %v", types, err)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var cancelled time.Time
	if err := db.QueryRowContext(ctx, `SELECT at FROM sample`).Scan(&cancelled); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled query: %v", err)
	}
}

func TestLegacyTimestampReadsMatchDriverLayouts(t *testing.T) {
	cases := []struct {
		value string
		want  time.Time
	}{
		{"2026-10-21 18:15:40.123456789+02:00", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21T18:15:40.123456789+02:00", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21 10:45:40.123456789-05:30", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21 16:15:40.123456789", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21T16:15:40.123456789", time.Date(2026, 10, 21, 16, 15, 40, 123456789, time.UTC)},
		{"2026-10-21 16:15:40Z", time.Date(2026, 10, 21, 16, 15, 40, 0, time.UTC)},
		{"2026-10-21 16:15", time.Date(2026, 10, 21, 16, 15, 0, 0, time.UTC)},
		{"2026-10-21T16:15", time.Date(2026, 10, 21, 16, 15, 0, 0, time.UTC)},
		{"2026-10-21 16:15Z", time.Date(2026, 10, 21, 16, 15, 0, 0, time.UTC)},
		{"2026-10-21", time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)},
	}
	for _, driverName := range []string{"sqlite", "ao-sqlite"} {
		t.Run(driverName, func(t *testing.T) {
			db, err := sql.Open(driverName, ":memory:")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			if _, err := db.Exec(`CREATE TABLE layouts(at TIMESTAMP, plain TEXT)`); err != nil {
				t.Fatal(err)
			}
			for _, tc := range cases {
				if _, err := db.Exec(`INSERT INTO layouts(at,plain) VALUES(?,?)`, tc.value, tc.value); err != nil {
					t.Fatal(err)
				}
				var at time.Time
				var plain string
				if err := db.QueryRow(`SELECT at,plain FROM layouts WHERE plain=?`, tc.value).Scan(&at, &plain); err != nil {
					t.Fatalf("scan %q: %v", tc.value, err)
				}
				if !at.Equal(tc.want) || plain != tc.value {
					t.Errorf("%q: got %v and %q; want %v and unchanged text", tc.value, at, plain, tc.want)
				}
			}
		})
	}
}

func TestRepairNormalizesHistoricalLocalEncodings(t *testing.T) {
	db := openRepairDB(t)
	for i, value := range []string{"2026-10-21 18:15:40.123456789 +0200 CEST", "2026-10-21 21:45:40.123456789 +0530 IST m=+5.31", "2026-10-21T18:15:40.123456789+02:00"} {
		if _, err := db.Exec(`INSERT INTO repair_times(id,at) VALUES (?,?)`, i, value); err != nil {
			t.Fatal(err)
		}
	}
	if err := repairTimestamps(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Query(`SELECT CAST(at AS TEXT) FROM repair_times`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var got string
		if err := rows.Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != "2026-10-21 16:15:40.123456789 +0000 UTC" {
			t.Fatalf("historical time: %q", got)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}
