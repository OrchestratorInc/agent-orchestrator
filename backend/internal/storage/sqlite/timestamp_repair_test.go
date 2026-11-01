package sqlite

import (
	"database/sql"
	"fmt"
	"testing"
	"time"
)

// offsetOnlyCases are timestamps in a zone with no name, as the driver stores
// them. The expected instant is what the offset says, whatever the daemon's own
// zone is.
var offsetOnlyCases = []struct {
	name, stored, wantUTC string
}{
	{"+02:00", "2026-10-21 18:15:40.647754 +0200 +0200", "2026-10-21T16:15:40.647754Z"},
	{"-07:00", "2026-10-21 18:15:40.647754 -0700 -0700", "2026-10-22T01:15:40.647754Z"},
	{"half-hour +05:30", "2026-10-21 18:15:40.647754 +0530 +0530", "2026-10-21T12:45:40.647754Z"},
	{"45-minute +05:45", "2026-10-21 18:15:40.647754 +0545 +0545", "2026-10-21T12:30:40.647754Z"},
	{"negative half-hour -03:30", "2026-10-21 18:15:40.647754 -0330 -0330", "2026-10-21T21:45:40.647754Z"},
	{"widest +14:00", "2026-10-21 18:15:40.647754 +1400 +1400", "2026-10-21T04:15:40.647754Z"},
	{"midnight crossing", "2026-10-22 00:30:00.5 +0100 +0100", "2026-10-21T23:30:00.5Z"},
	{"year boundary", "2027-01-01 00:10:00 +0200 +0200", "2026-12-31T22:10:00Z"},
	{"no fraction", "2026-10-21 18:15:40 +0200 +0200", "2026-10-21T16:15:40Z"},
	{"nanosecond fraction", "2026-10-21 18:15:40.123456789 +0200 +0200", "2026-10-21T16:15:40.123456789Z"},
	{"monotonic suffix", "2026-10-21 18:15:40.647754 +0200 +0200 m=+3.456789", "2026-10-21T16:15:40.647754Z"},
}

// untouched values must come through byte for byte.
var untouchedCases = []struct{ name, stored string }{
	{"UTC text", "2026-10-21 16:15:40.647754 +0000 UTC"},
	{"named zone", "2026-10-21 18:15:40.647754 +0200 CEST"},
	{"named zone with monotonic suffix", "2026-10-22 01:02:08.140566 +0530 IST m=+5.311991126"},
	{"RFC 3339 with offset", "2026-10-21T18:15:40+02:00"},
	{"not a time", "not a time"},
	{"empty", ""},
	// Matches the shape but is not a date: leave it for a human rather than guess.
	{"impossible date", "2026-13-45 99:99:99 +0200 +0200"},
}

func openRepairDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openMigratedTestDB(t)
	if _, err := db.Exec(`CREATE TABLE repair_times (
		id INTEGER PRIMARY KEY, at TIMESTAMP, plain TEXT, day DATE, moment DATETIME)`); err != nil {
		t.Fatal(err)
	}
	// The migration run during open already marked the database repaired.
	if _, err := db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	return db
}

func canScanTime(db *sql.DB, query string, args ...any) error {
	var at sql.NullTime
	return db.QueryRow(query, args...).Scan(&at)
}

func TestRepairRewritesOffsetOnlyTimestampsAsUTC(t *testing.T) {
	db := openRepairDB(t)
	for i, c := range offsetOnlyCases {
		if _, err := db.Exec(`INSERT INTO repair_times (id, at) VALUES (?, ?)`, i+1, c.stored); err != nil {
			t.Fatal(err)
		}
		// The fixture must be the bug: the driver cannot read this back.
		if err := canScanTime(db, `SELECT at FROM repair_times WHERE id = ?`, i+1); err == nil {
			t.Fatalf("%s: %q scans fine before the repair, so this test proves nothing", c.name, c.stored)
		}
	}

	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("repair: %v", err)
	}

	for i, c := range offsetOnlyCases {
		var got sql.NullTime
		if err := db.QueryRow(`SELECT at FROM repair_times WHERE id = ?`, i+1).Scan(&got); err != nil {
			t.Fatalf("%s: still unreadable after the repair: %v", c.name, err)
		}
		want, err := time.Parse(time.RFC3339Nano, c.wantUTC)
		if err != nil {
			t.Fatal(err)
		}
		if !got.Valid || !got.Time.Equal(want) {
			t.Fatalf("%s: repaired to %v, want the instant %v", c.name, got.Time, want)
		}
		if _, offset := got.Time.Zone(); offset != 0 {
			t.Fatalf("%s: repaired value has offset %d, want UTC", c.name, offset)
		}
	}
}

func TestRepairLeavesEveryOtherValueAlone(t *testing.T) {
	db := openRepairDB(t)
	for i, c := range untouchedCases {
		if _, err := db.Exec(`INSERT INTO repair_times (id, at) VALUES (?, ?)`, i+1, c.stored); err != nil {
			t.Fatal(err)
		}
	}
	nullID, intID, blobID := len(untouchedCases)+1, len(untouchedCases)+2, len(untouchedCases)+3
	if _, err := db.Exec(`INSERT INTO repair_times (id, at) VALUES (?, NULL), (?, 12345), (?, X'2B30323030')`, nullID, intID, blobID); err != nil {
		t.Fatal(err)
	}

	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("repair: %v", err)
	}

	for i, c := range untouchedCases {
		var got string
		if err := db.QueryRow(`SELECT CAST(at AS TEXT) FROM repair_times WHERE id = ?`, i+1).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != c.stored {
			t.Fatalf("%s: %q was rewritten to %q", c.name, c.stored, got)
		}
	}
	var nullType, intType, blobType string
	if err := db.QueryRow(`SELECT typeof(at) FROM repair_times WHERE id = ?`, nullID).Scan(&nullType); err != nil || nullType != "null" {
		t.Fatalf("NULL changed to %q (err %v)", nullType, err)
	}
	if err := db.QueryRow(`SELECT typeof(at) FROM repair_times WHERE id = ?`, intID).Scan(&intType); err != nil || intType != "integer" {
		t.Fatalf("integer changed to %q (err %v)", intType, err)
	}
	if err := db.QueryRow(`SELECT typeof(at) FROM repair_times WHERE id = ?`, blobID).Scan(&blobType); err != nil || blobType != "blob" {
		t.Fatalf("blob changed to %q (err %v)", blobType, err)
	}
}

// The text column and the three declared time types are told apart by type
// name; a plain TEXT column that happens to hold the same text is not a time.
func TestRepairOnlyTouchesTimestampColumns(t *testing.T) {
	db := openRepairDB(t)
	const stored = "2026-10-21 18:15:40.647754 +0200 +0200"
	if _, err := db.Exec(`INSERT INTO repair_times (id, at, plain, day, moment) VALUES (1, ?, ?, ?, ?)`, stored, stored, stored, stored); err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	var at, plain, day, moment string
	if err := db.QueryRow(`SELECT CAST(at AS TEXT), plain, CAST(day AS TEXT), CAST(moment AS TEXT) FROM repair_times`).Scan(&at, &plain, &day, &moment); err != nil {
		t.Fatal(err)
	}
	const want = "2026-10-21 16:15:40.647754 +0000 UTC"
	if at != want || day != want || moment != want {
		t.Fatalf("TIMESTAMP, DATE and DATETIME columns = %q, %q, %q; each want %q", at, day, moment, want)
	}
	if plain != stored {
		t.Fatalf("TEXT column was rewritten to %q", plain)
	}
}

func TestRepairHandlesTablesNamedWithQuotes(t *testing.T) {
	db := openMigratedTestDB(t)
	if _, err := db.Exec(`CREATE TABLE "odd ""name" ("when" TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO "odd ""name" ("when") VALUES ('2026-10-21 18:15:40 +0200 +0200')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	if err := canScanTime(db, `SELECT "when" FROM "odd ""name"`); err != nil {
		t.Fatalf("quoted table not repaired: %v", err)
	}
}

// Many rows with the same value are rewritten together; distinct values each get their own instant.
func TestRepairRewritesEveryRow(t *testing.T) {
	db := openRepairDB(t)
	for i := 0; i < 200; i++ {
		stored := fmt.Sprintf("2026-10-21 18:%02d:40 +0200 +0200", i%60)
		if _, err := db.Exec(`INSERT INTO repair_times (id, at) VALUES (?, ?)`, i+1, stored); err != nil {
			t.Fatal(err)
		}
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("repair: %v", err)
	}
	var bad int
	if err := db.QueryRow(`SELECT COUNT(*) FROM repair_times WHERE CAST(at AS TEXT) GLOB '*[+-][0-9][0-9][0-9][0-9] [+-][0-9][0-9][0-9][0-9]*'`).Scan(&bad); err != nil || bad != 0 {
		t.Fatalf("%d offset-only rows left (err %v)", bad, err)
	}
	rows, err := db.Query(`SELECT at FROM repair_times ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	n := 0
	for rows.Next() {
		var at sql.NullTime
		if err := rows.Scan(&at); err != nil {
			t.Fatalf("row %d unreadable: %v", n+1, err)
		}
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if n != 200 {
		t.Fatalf("read %d rows, want 200", n)
	}
}

// The pass runs once: it scans every timestamp column, which is too much to do
// on each start. A second run is harmless, and a database marked done is skipped.
func TestRepairRunsOncePerDatabase(t *testing.T) {
	db := openRepairDB(t)
	if _, err := db.Exec(`INSERT INTO repair_times (id, at) VALUES (1, '2026-10-21 18:15:40 +0200 +0200')`); err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("first repair: %v", err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != offsetOnlyTimestampRepairVersion {
		t.Fatalf("user_version = %d (err %v), want %d", version, err, offsetOnlyTimestampRepairVersion)
	}

	var first string
	if err := db.QueryRow(`SELECT CAST(at AS TEXT) FROM repair_times WHERE id = 1`).Scan(&first); err != nil {
		t.Fatal(err)
	}
	// Idempotent when forced to run again over already-repaired data.
	if _, err := db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("second repair: %v", err)
	}
	var second string
	if err := db.QueryRow(`SELECT CAST(at AS TEXT) FROM repair_times WHERE id = 1`).Scan(&second); err != nil || second != first {
		t.Fatalf("second repair changed %q to %q (err %v)", first, second, err)
	}

	// A database already marked repaired is not scanned again.
	if _, err := db.Exec(`INSERT INTO repair_times (id, at) VALUES (2, '2026-10-21 18:15:40 +0200 +0200')`); err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("marked repair: %v", err)
	}
	if err := canScanTime(db, `SELECT at FROM repair_times WHERE id = 2`); err == nil {
		t.Fatal("a database marked repaired was scanned again")
	}
}

// A database whose user_version is already ahead (a newer binary wrote it) is not downgraded.
func TestRepairKeepsAHigherUserVersion(t *testing.T) {
	db := openRepairDB(t)
	if _, err := db.Exec(`PRAGMA user_version = 7`); err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatal(err)
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 7 {
		t.Fatalf("user_version = %d (err %v), want it left at 7", version, err)
	}
}

// The repair rolls back as one unit: a failure leaves the database unmarked so it runs again.
func TestRepairFailureLeavesTheDatabaseUnmarked(t *testing.T) {
	db := openRepairDB(t)
	if _, err := db.Exec(`INSERT INTO repair_times (id, at) VALUES (1, '2026-10-21 18:15:40 +0200 +0200')`); err != nil {
		t.Fatal(err)
	}
	// A trigger that rejects the rewrite makes the UPDATE fail.
	if _, err := db.Exec(`CREATE TRIGGER block_rewrite BEFORE UPDATE ON repair_times BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err == nil {
		t.Fatal("repair succeeded although the rewrite was blocked")
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != 0 {
		t.Fatalf("a failed repair marked the database done: user_version = %d (err %v)", version, err)
	}
	// Unblocked, the next start repairs it.
	if _, err := db.Exec(`DROP TRIGGER block_rewrite`); err != nil {
		t.Fatal(err)
	}
	if err := repairOffsetOnlyTimestamps(db); err != nil {
		t.Fatalf("retry: %v", err)
	}
	if err := canScanTime(db, `SELECT at FROM repair_times WHERE id = 1`); err != nil {
		t.Fatalf("retry did not repair: %v", err)
	}
}

// migrate runs the repair, so a daemon that opens a poisoned database can list its rows again.
func TestMigrateRepairsOffsetOnlyTimestampsInRealTables(t *testing.T) {
	db := openMigratedTestDB(t)
	const stored = "2026-10-21 18:15:40.647754 +0200 +0200"
	if _, err := db.Exec(`INSERT INTO projects (id, path, registered_at) VALUES ('p1', '/tmp/p1', ?)`, stored); err != nil {
		t.Fatalf("seed project: %v", err)
	}
	if err := canScanTime(db, `SELECT registered_at FROM projects WHERE id = 'p1'`); err == nil {
		t.Fatal("fixture is readable before the repair, so this test proves nothing")
	}
	if _, err := db.Exec(`PRAGMA user_version = 0`); err != nil {
		t.Fatal(err)
	}
	if err := migrate(db); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var got sql.NullTime
	if err := db.QueryRow(`SELECT registered_at FROM projects WHERE id = 'p1'`).Scan(&got); err != nil {
		t.Fatalf("project still unreadable after migrate: %v", err)
	}
	if want := time.Date(2026, 10, 21, 16, 15, 40, 647754000, time.UTC); !got.Time.Equal(want) {
		t.Fatalf("registered_at = %v, want %v", got.Time, want)
	}
}
