package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionsearch"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// sessionSearchSweepInterval is how often the indexer looks for new session
// content. It is deliberately coarse: the index is a recall surface, not a live
// feed, and a 30-second lag keeps the daemon's writer connection free for the
// work sessions are actually doing.
const sessionSearchSweepInterval = 30 * time.Second

// sessionSearchStartupDelay lets the daemon finish serving its first requests
// before a backfill starts reading transcripts. On a machine with many
// sessions, a backfill that began during boot would compete with the desktop's
// own startup reads.
const sessionSearchStartupDelay = 5 * time.Second

// startSessionSearch wires the session search service over the store and the
// provider transcript roots, then starts its background passes.
//
// It never fails daemon startup: a machine with no provider transcripts, or a
// data directory the indexer cannot read, still serves search over the sessions
// it can see. Every failure is a logged warning, never a boot error.
func startSessionSearch(ctx context.Context, store *sqlite.Store, logger *slog.Logger) *sessionsearch.Service {
	roots, err := sessionSearchRoots()
	if err != nil {
		logger.Warn("session search: transcript roots unavailable; native transcripts will not be indexed", "err", err)
	}
	service := sessionsearch.New(sessionsearch.Options{
		Store:   store,
		Roots:   roots,
		Namer:   store,
		PRs:     store,
		Machine: hostLabel(),
		Logger:  logger,
	})
	go runSessionSearch(ctx, service, logger)
	return service
}

// hostLabel is this machine's name, recorded on every document so the
// machine: operator has something to match.
func hostLabel() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "local"
	}
	return host
}

// sessionSearchRoots resolves the provider-owned transcript directories. They
// mirror the usage collector's roots so both features read the same files, but
// they are resolved here rather than shared: usage may legitimately be disabled
// while search still wants the transcripts.
func sessionSearchRoots() (sessionsearch.TranscriptRoots, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return sessionsearch.TranscriptRoots{}, err
	}
	claudeHome := strings.TrimSpace(os.Getenv("CLAUDE_CONFIG_DIR"))
	if claudeHome == "" {
		claudeHome = filepath.Join(home, ".claude")
	}
	codexHome := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if codexHome == "" {
		codexHome = filepath.Join(home, ".codex")
	}
	// OpenCode keeps its history in its own SQLite store under XDG_DATA_HOME.
	// The v2 store is deliberately separate so OpenCode 1 and 2 never share a
	// database; the indexer reads whichever exists.
	dataHome := strings.TrimSpace(os.Getenv("XDG_DATA_HOME"))
	if dataHome == "" {
		dataHome = filepath.Join(home, ".local", "share")
	}
	return sessionsearch.TranscriptRoots{
		ClaudeProjects: filepath.Join(claudeHome, "projects"),
		CodexSessions:  filepath.Join(codexHome, "sessions"),
		CodexArchived:  filepath.Join(codexHome, "archived_sessions"),
		OpenCodeData:   dataHome,
		OpenCodeV2Data: filepath.Join(dataHome, "opencode-v2-home"),
	}, nil
}

func backfill(ctx context.Context, service *sessionsearch.Service, logger *slog.Logger) {
	if written, err := service.IndexAll(ctx); err != nil {
		if ctx.Err() == nil {
			logger.Warn("session search backfill failed; retrying on the next sweep", "documents", written, "err", err)
		}
	} else {
		logger.Info("session search backfill complete", "documents", written)
	}
}

// runSessionSearch performs the startup backfill and then sweeps on a timer
// until ctx is cancelled. Each pass is bounded and yields, so a long backfill
// never blocks session work.
func runSessionSearch(ctx context.Context, service *sessionsearch.Service, logger *slog.Logger) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(sessionSearchStartupDelay):
	}
	backfill(ctx, service, logger)

	ticker := time.NewTicker(sessionSearchSweepInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// A sweep only tops up an index a backfill filled. Until one has
			// completed, retry the backfill instead: one that failed (an
			// unreadable row, a locked table) would otherwise leave the index
			// empty for the life of the daemon.
			if !service.Backfilled() {
				backfill(ctx, service, logger)
				continue
			}
			written, err := service.Sweep(ctx)
			if err != nil && ctx.Err() == nil {
				logger.Warn("session search sweep failed", "documents", written, "err", err)
			}
		}
	}
}
