package sqlite

import (
	"strings"
	"testing"
)

func TestReviewRunCurrentHeadLookupIndex(t *testing.T) {
	db := openMigratedTestDB(t)
	assertLookup := func() {
		t.Helper()
		if !indexColumnsMatch(db, "idx_review_run_current_head_lookup", []string{
			"session_id", "pr_url", "target_sha", "harness", "created_at", "id",
		}) {
			t.Fatal("missing current-head review lookup index")
		}
		// Completed runs must be searchable too; the running-only index cannot
		// serve this per-candidate lookup and leaves the batch query quadratic.
		rows, err := db.Query(`EXPLAIN QUERY PLAN SELECT 1 FROM review_run newer
			WHERE newer.session_id = ? AND newer.pr_url = ?
			AND newer.target_sha = ? AND newer.harness = ?
			AND (newer.created_at > ? OR (newer.created_at = ? AND newer.id > ?))`,
			"session", "pr/1", "head", "claude-code", "now", "now", "run")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		indexed := false
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(detail, "SCAN newer") {
				t.Fatalf("review lookup scans history: %s", detail)
			}
			indexed = indexed || strings.Contains(detail, "USING COVERING INDEX idx_review_run_current_head_lookup")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if !indexed {
			t.Fatal("review lookup did not use the covering index")
		}
	}
	assertLookup()
	downTo(t, db, 20261020173311321)
	var remaining int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_review_run_current_head_lookup'`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatal("rollback retained the review lookup index")
	}
	upTo(t, db, 20261022014101630)
	assertLookup()
}
