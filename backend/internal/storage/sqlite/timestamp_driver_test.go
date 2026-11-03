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
