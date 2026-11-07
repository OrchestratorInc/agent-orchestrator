package sessionsearch_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestSearchBenchmarkRealLatency measures backfill time, index size and query
// latency on a snapshot of a real AO database. Relevance needs graded answers,
// which a person's own history does not have; speed and size do not.
//
//	AO_SEARCH_BENCH_REAL_DB=$HOME/.ao/data/ao.db go test ./internal/service/sessionsearch/ \
//	    -run TestSearchBenchmarkRealLatency -v -timeout 30m
//
// The source is opened read-only and copied with VACUUM INTO, which takes a
// consistent snapshot even while the daemon is writing; every write (the
// migration, the index) goes to the copy in a temporary directory.
func TestSearchBenchmarkRealLatency(t *testing.T) {
	source := os.Getenv("AO_SEARCH_BENCH_REAL_DB")
	if source == "" {
		t.Skip("set AO_SEARCH_BENCH_REAL_DB to an AO database to measure on a read-only snapshot of it")
	}
	// AO_SEARCH_BENCH_KEEP_DIR keeps the indexed snapshot for inspection with
	// the sqlite3 shell; it must be an empty directory the caller owns.
	dir := os.Getenv("AO_SEARCH_BENCH_KEEP_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	snapshot := filepath.Join(dir, "ao.db")
	started := time.Now()
	readOnly, err := sql.Open("sqlite", "file:"+source+"?mode=ro")
	if err != nil {
		t.Fatalf("open source read-only: %v", err)
	}
	if _, err := readOnly.Exec(`VACUUM INTO ?`, snapshot); err != nil {
		_ = readOnly.Close()
		t.Fatalf("snapshot source: %v", err)
	}
	_ = readOnly.Close()
	t.Logf("snapshot: %s", time.Since(started).Round(time.Millisecond))

	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	svc := sessionsearch.New(sessionsearch.Options{Store: store, Namer: store})
	started = time.Now()
	written, err := svc.IndexAll(context.Background())
	if err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	docs, _ := store.CountSessionSearchDocs(context.Background())
	t.Logf("backfill: %d documents written (%d indexed) in %s", written, docs, time.Since(started).Round(time.Millisecond))

	raw, err := sql.Open("sqlite", "file:"+snapshot)
	if err != nil {
		t.Fatalf("open snapshot for size: %v", err)
	}
	defer func() { _ = raw.Close() }()
	var textBytes int64
	_ = raw.QueryRow(`SELECT COALESCE(SUM(LENGTH(title) + LENGTH(body) + LENGTH(paths)), 0) FROM session_search_text`).Scan(&textBytes)
	var ftsBytes int64
	_ = raw.QueryRow(`SELECT COALESCE(SUM(LENGTH(block)), 0) FROM session_search_fts_data`).Scan(&ftsBytes)
	t.Logf("index: %d KB of document text, %d KB of FTS5 postings", textBytes/1024, ftsBytes/1024)

	queries := []string{
		// Words a person types into the palette, one at a time.
		"lo", "log", "login", "test", "fix", "error", "the",
		// Error strings and identifiers.
		"database is locked", "undefined", "TypeError", "panic", "SQLITE_BUSY", "useEffect",
		// Questions an agent asks in its own words.
		"how did we fix the flaky login test",
		"what was the decision about the session migration",
		"where did we change the sidebar layout",
		"why did the build fail on CI",
		// Operators.
		`"command palette"`, "search harness:codex", "test status:killed", "!test sidebar",
	}
	for _, group := range []domain.SessionSearchGroup{domain.SessionSearchGroupSession, domain.SessionSearchGroupMessage} {
		var latencies []time.Duration
		empty := 0
		for _, query := range queries {
			parsed := domain.ParseSessionSearchQuery(query)
			params := domain.SessionSearchParams{Group: group, Limit: 25}
			// One warm-up pass, then the measured pass, so the number is the
			// steady state a person sees while typing rather than a cold cache.
			if _, err := svc.Search(context.Background(), parsed, params); err != nil {
				t.Fatalf("search %q: %v", query, err)
			}
			begin := time.Now()
			response, err := svc.Search(context.Background(), parsed, params)
			if err != nil {
				t.Fatalf("search %q: %v", query, err)
			}
			elapsed := time.Since(begin)
			latencies = append(latencies, elapsed)
			if len(response.Results) == 0 {
				empty++
			}
			if os.Getenv("AO_SEARCH_BENCH_VERBOSE") == "1" {
				t.Logf("%-8s %-52q %3d results of %5d in %s", group, query, len(response.Results), response.Total, elapsed.Round(time.Microsecond))
			}
		}
		sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
		t.Logf("latency %-8s p50 %s p95 %s max %s over %d queries; %d empty",
			group, latencies[len(latencies)/2].Round(time.Microsecond),
			latencies[len(latencies)*95/100].Round(time.Microsecond),
			latencies[len(latencies)-1].Round(time.Microsecond), len(latencies), empty)
	}
}
