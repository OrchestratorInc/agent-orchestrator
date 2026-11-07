package controllers_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// TestServeSessionSearchOverSnapshot serves the daemon's HTTP API, with only
// session search wired, over a COPY of a real AO database, so search can be
// tested end to end against real sessions without starting a daemon (whose
// startup reconciles sessions, chat hosts, reviewers and automations).
//
// It is a manual QA fixture, skipped unless AO_SEARCH_SNAPSHOT_DIR names a
// directory holding a copied ao.db, for example:
//
//	sqlite3 "file:$HOME/.ao/data/ao.db?mode=ro" "VACUUM INTO '/tmp/search-e2e/ao.db'"
//
// The copy is migrated and indexed in place. Transcript roots are this
// machine's, read-only, as in the daemon. It then listens on
// 127.0.0.1:$AO_SEARCH_SNAPSHOT_PORT (default 3097) and writes
// $AO_SEARCH_SNAPSHOT_DIR/running.json, so `AO_RUN_FILE=<that file> ao search`
// talks to it like a daemon. It serves until a file named "stop" appears in the
// directory or AO_SEARCH_SNAPSHOT_SERVE (a duration, default 30m) elapses.
func TestServeSessionSearchOverSnapshot(t *testing.T) {
	dir := os.Getenv("AO_SEARCH_SNAPSHOT_DIR")
	if dir == "" {
		t.Skip("set AO_SEARCH_SNAPSHOT_DIR to a directory holding a copied ao.db")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	refuseLiveDataDir(t, dir)
	if _, err := os.Stat(filepath.Join(dir, "ao.db")); err != nil {
		t.Fatalf("no ao.db in %s: %v", dir, err)
	}
	port := 3097
	if raw := os.Getenv("AO_SEARCH_SNAPSHOT_PORT"); raw != "" {
		if port, err = strconv.Atoi(raw); err != nil {
			t.Fatalf("AO_SEARCH_SNAPSHOT_PORT: %v", err)
		}
	}
	serveFor := 30 * time.Minute
	if raw := os.Getenv("AO_SEARCH_SNAPSHOT_SERVE"); raw != "" {
		if serveFor, err = time.ParseDuration(raw); err != nil {
			t.Fatalf("AO_SEARCH_SNAPSHOT_SERVE: %v", err)
		}
	}

	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	defer func() { _ = store.Close() }()
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	host, _ := os.Hostname()
	// AO_SEARCH_SNAPSHOT_TAIL_DEPTH tries a session tail depth end to end
	// (negative: every match, the old unbounded tail).
	var ranking domain.SessionSearchRanking
	if raw := os.Getenv("AO_SEARCH_SNAPSHOT_TAIL_DEPTH"); raw != "" {
		if ranking.SessionTailDepth, err = strconv.Atoi(raw); err != nil {
			t.Fatalf("AO_SEARCH_SNAPSHOT_TAIL_DEPTH: %v", err)
		}
		t.Logf("session tail depth %d", ranking.TailDepth())
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc := sessionsearch.New(sessionsearch.Options{
		Store: store,
		Roots: sessionsearch.TranscriptRoots{
			ClaudeProjects: filepath.Join(home, ".claude", "projects"),
			CodexSessions:  filepath.Join(home, ".codex", "sessions"),
			CodexArchived:  filepath.Join(home, ".codex", "archived_sessions"),
			OpenCodeData:   filepath.Join(home, ".local", "share"),
			OpenCodeV2Data: filepath.Join(home, ".local", "share", "opencode-v2-home"),
		},
		Namer:   store,
		PRs:     store,
		Machine: host,
		Logger:  log,
		Ranking: ranking,
	})
	ctx := context.Background()
	started := time.Now()
	written, err := svc.IndexAll(ctx)
	if err != nil {
		t.Fatalf("IndexAll: %v", err)
	}
	t.Logf("indexed %d documents in %s", written, time.Since(started).Round(time.Millisecond))
	if coverage, err := svc.Coverage(ctx); err == nil {
		t.Logf("coverage: %+v", coverage)
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", config.LoopbackHost, port))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{
		Handler:           httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{SessionSearch: svc}, httpd.ControlDeps{}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("serve: %v", err)
		}
	}()
	runFile := filepath.Join(dir, "running.json")
	if err := runfile.Write(runFile, runfile.Info{PID: os.Getpid(), Port: port, StartedAt: time.Now().UTC()}); err != nil {
		t.Fatalf("write run file: %v", err)
	}
	defer func() { _ = os.Remove(runFile) }()
	t.Logf("serving session search on http://%s:%d; AO_RUN_FILE=%s", config.LoopbackHost, port, runFile)
	fmt.Fprintf(os.Stderr, "session-search-snapshot ready on port %d\n", port)

	stop := filepath.Join(dir, "stop")
	_ = os.Remove(stop)
	deadline := time.Now().Add(serveFor)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(stop); err == nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	shutdown, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdown)
}

// refuseLiveDataDir stops the fixture from migrating or indexing a database a
// daemon owns: opening a store applies migrations.
func refuseLiveDataDir(t *testing.T, dir string) {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("resolve %s: %v", dir, err)
	}
	live := []string{os.Getenv("AO_DATA_DIR")}
	if home, err := os.UserHomeDir(); err == nil {
		live = append(live, filepath.Join(home, ".ao", "data"))
	}
	for _, candidate := range live {
		if candidate == "" {
			continue
		}
		if other, err := filepath.EvalSymlinks(candidate); err == nil && other == resolved {
			t.Fatalf("%s is a live AO data directory; copy its ao.db elsewhere first", dir)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "daemon.lock")); err == nil {
		t.Fatalf("%s holds a daemon.lock; copy its ao.db elsewhere first", dir)
	}
}
