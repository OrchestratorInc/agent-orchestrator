package sqlite

import (
	"testing"
	"time"
)

// TestMigration0207NormalizesLocalZoneRegisteredAt is the regression guard for
// issue #6890. projects.registered_at is scanned into a time.Time, but the
// driver binds a time.Time by its String() form and re-parses it with
// "2006-01-02 15:04:05.999999999 -0700 MST". A local-zone clock in a zone whose
// Go name is a numeric offset renders as "2026-10-18 05:03:55.6144103 +0330
// +0330", which cannot match that layout: the column comes back as a string and
// the scan fails, so the project row is unreadable the moment it is written.
//
// Migration 0207 rewrites both registered_at columns to the canonical UTC form
// the rest of the codebase writes. The writers are fixed in the same change, so
// this covers installs a released build already poisoned.
func TestMigration0207NormalizesLocalZoneRegisteredAt(t *testing.T) {
	db := openMigratedDatabaseCopy(t, 206)

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
		// Asia/Tehran: the zone has no alphabetic name, so Go renders the offset
		// twice and the reader's "MST" element can never match it. This is the
		// exact shape that broke the reporter's first launch.
		{"p2", "2026-10-18 05:03:55.6144103 +0330 +0330 m=+185.748497701", "2026-10-18 01:33:55.6144103 +0000 UTC"},
		// Same shape with the monotonic reading already gone: equally unreadable,
		// because the failure is the zone name and not the suffix.
		{"p3", "2026-10-18 05:03:55.6144103 +0330 +0330", "2026-10-18 01:33:55.6144103 +0000 UTC"},
		// An alphabetic zone parses, but only ends up in the column at all when a
		// writer used the local clock, so it is normalized to UTC like the rest.
		{"p4", "2026-06-28 18:45:08.349363 +0800 CST m=+25660.013723251", "2026-06-28 10:45:08.349363 +0000 UTC"},
		// West of UTC shifts forward instead.
		{"p5", "2026-07-27 07:03:53.393954 -0500 EST m=+50790.035115335", "2026-07-27 12:03:53.393954 +0000 UTC"},
		// Already canonical: must be left byte-for-byte alone.
		{"p6", "2026-07-27 07:03:53.393954 +0000 UTC", "2026-07-27 07:03:53.393954 +0000 UTC"},
		// What the legacy import writes (parsed RFC 3339), and the offset form
		// another driver configuration writes: both already read back fine, so the
		// migration must not touch either.
		{"p7", "2026-07-25T00:00:00Z", "2026-07-25T00:00:00Z"},
		{"p8", "2026-07-25 00:00:00.123456+03:30", "2026-07-25 00:00:00.123456+03:30"},
	}
	for _, s := range seed {
		if _, err := db.Exec(`INSERT INTO projects (id, path, display_name, registered_at)
			VALUES (?, '/tmp/' || ?, 'proj', ?)`, s.id, s.id, s.written); err != nil {
			t.Fatalf("seed project %s: %v", s.id, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO workspace_repos (project_id, name, relative_path, registered_at)
		VALUES ('p1', 'child', 'child', ?)`, "2026-10-18 05:03:55.6144103 +0330 +0330 m=+185.748497701"); err != nil {
		t.Fatalf("seed workspace repo: %v", err)
	}

	// archived_at is the other projects timestamp the reader scans, and it is
	// nullable, so the rewrite has to skip NULL rather than leave it alone by
	// accident.
	archived := []struct {
		id      string
		written any
		want    any
	}{
		{"p2", "2026-10-18 05:03:55.6144103 +0330 +0330 m=+185.748497701", "2026-10-18 01:33:55.6144103 +0000 UTC"},
		{"p3", "2026-07-27 07:03:53.393954 -0500 EST m=+50790.035115335", "2026-07-27 12:03:53.393954 +0000 UTC"},
		{"p4", nil, nil},
		{"p5", "2026-07-27 07:03:53.393954 +0000 UTC", "2026-07-27 07:03:53.393954 +0000 UTC"},
	}
	for _, a := range archived {
		if _, err := db.Exec(`UPDATE projects SET archived_at = ? WHERE id = ?`, a.written, a.id); err != nil {
			t.Fatalf("seed archived_at for %s: %v", a.id, err)
		}
	}

	upTo(t, db, 207)

	// CAST to TEXT to see the stored bytes: scanning the column into a string
	// lets the driver re-render it as RFC 3339, which would hide the very
	// difference under test.
	for _, s := range seed {
		var got string
		if err := db.QueryRow(`SELECT CAST(registered_at AS TEXT) FROM projects WHERE id = ?`, s.id).Scan(&got); err != nil {
			t.Fatalf("read %s: %v", s.id, err)
		}
		if got != s.want {
			t.Errorf("%s registered_at = %q, want %q", s.id, got, s.want)
		}
	}

	var repo string
	if err := db.QueryRow(`SELECT CAST(registered_at AS TEXT) FROM workspace_repos WHERE project_id = 'p1' AND name = 'child'`).Scan(&repo); err != nil {
		t.Fatalf("read workspace repo: %v", err)
	}
	if want := "2026-10-18 01:33:55.6144103 +0000 UTC"; repo != want {
		t.Errorf("workspace_repos.registered_at = %q, want %q", repo, want)
	}

	for _, a := range archived {
		var got any
		if err := db.QueryRow(`SELECT CAST(archived_at AS TEXT) FROM projects WHERE id = ?`, a.id).Scan(&got); err != nil {
			t.Fatalf("read archived_at for %s: %v", a.id, err)
		}
		if got != a.want {
			t.Errorf("%s archived_at = %v, want %v", a.id, got, a.want)
		}
	}

	// Every rewritten row has to scan into a time.Time again: that is the read the
	// projects list performs, and the one that produced
	// "unsupported Scan, storing driver.Value type string into type *time.Time".
	rows, err := db.Query(`SELECT id, registered_at FROM projects ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	read := 0
	for rows.Next() {
		var id string
		var at time.Time
		if err := rows.Scan(&id, &at); err != nil {
			t.Fatalf("scan %s into time.Time: %v", id, err)
		}
		if at.IsZero() {
			t.Errorf("%s scanned as the zero time", id)
		}
		read++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if read != len(seed)+1 {
		t.Errorf("scanned %d projects, want %d", read, len(seed)+1)
	}
}
