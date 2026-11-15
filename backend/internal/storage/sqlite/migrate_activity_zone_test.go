package sqlite

import (
	"testing"
	"time"
)

func TestMigration0120NormalizesLocalZoneActivityTimestamps(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 94)

	now := time.Now().UTC()
	if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at)
		VALUES ('p1', '/tmp/p1', 'proj', ?)`, now); err != nil {
		t.Fatalf("seed project: %v", err)
	}

	seed := []struct {
		id      string
		written string
		want    string
	}{
		{"ao-1", "2026-06-28 18:45:08.349363 +0800 CST m=+25660.013723251", "2026-06-28 10:45:08.349363 +0000 UTC"},
		{"ao-2", "2026-07-02 18:05:06.42722 +0800 CST m=+3515.018840501", "2026-07-02 10:05:06.42722 +0000 UTC"},
		{"ao-3", "2026-08-12 22:14:57.047745 +0700 +07", "2026-08-12 15:14:57.047745 +0000 UTC"},
		{"ao-4", "2026-07-27 07:03:53.393954 +0800 CST m=+50790.035115335", "2026-07-26 23:03:53.393954 +0000 UTC"},
		{"ao-5", "2026-07-27 07:03:53.393954 -0500 EST m=+50790.035115335", "2026-07-27 12:03:53.393954 +0000 UTC"},
		{"ao-6", "2026-07-27 07:03:53.393954 +0000 UTC", "2026-07-27 07:03:53.393954 +0000 UTC"},
	}
	for n, s := range seed {
		if _, err := db.Exec(`INSERT INTO sessions (id, project_id, num, kind, activity_state, activity_last_at, is_terminated, created_at, updated_at)
			VALUES (?, 'p1', ?, 'worker', 'exited', ?, 0, ?, ?)`, s.id, n+1, s.written, now, now); err != nil {
			t.Fatalf("seed session %s: %v", s.id, err)
		}
	}

	upTo(t, db, 120)

	for _, s := range seed {
		var got string
		if err := db.QueryRow(`SELECT CAST(activity_last_at AS TEXT) FROM sessions WHERE id = ?`, s.id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", s.id, err)
		}
		if got != s.want {
			t.Errorf("%s activity_last_at = %q, want %q", s.id, got, s.want)
		}
	}

	var comparableRows int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM sessions WHERE activity_last_at <= '2026-08-12 16:00:00.000000 +0000 UTC'`,
	).Scan(&comparableRows); err != nil {
		t.Fatal(err)
	}
	if comparableRows != len(seed) {
		t.Errorf("rows comparing as timestamps = %d, want %d", comparableRows, len(seed))
	}
}
