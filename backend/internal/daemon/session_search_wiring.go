package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
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
// Search is optional and never fails daemon startup. A machine with no
// provider transcripts, or a data directory the indexer cannot read, still
// serves search over the sessions it can see. An index that could not be
// opened at all leaves search off: no indexer runs, and the API answers
// "unavailable" while every other feature works as usual.
func startSessionSearch(ctx context.Context, store *sqlite.Store, stateDir string, logger *slog.Logger) *sessionsearch.Service {
	if err := store.SessionSearchUnavailable(); err != nil {
		logger.Warn("session search is off; nothing else is affected", "reason", err)
		return sessionsearch.New(sessionsearch.Options{Logger: logger})
	}
	roots, err := sessionSearchRoots(stateDir)
	if err != nil {
		logger.Warn("session search: transcript roots unavailable; native transcripts will not be indexed", "err", err)
	}
	service := sessionsearch.New(sessionsearch.Options{
		Store:  store,
		Roots:  roots,
		Namer:  store,
		PRs:    store,
		Logger: logger,
	})
	go runSessionSearch(ctx, service, logger)
	return service
}

// sessionSearchRoots resolves the provider-owned transcript directories the
// same way the provider adapters do (nativeconfig.Resolve): the environment
// variable a provider honours, else its default folder under the home directory.
func sessionSearchRoots(stateDir string) (sessionsearch.TranscriptRoots, error) {
	claudeHome, err := nativeconfig.Resolve(nil, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		return sessionsearch.TranscriptRoots{}, err
	}
	codexHome, err := nativeconfig.Resolve(nil, "CODEX_HOME", ".codex")
	if err != nil {
		return sessionsearch.TranscriptRoots{}, err
	}
	// OpenCode keeps its history in its own SQLite store under XDG_DATA_HOME.
	// The v2 store is deliberately separate so OpenCode 1 and 2 never share a
	// database; the indexer reads whichever exists.
	dataHome, err := nativeconfig.Resolve(nil, "XDG_DATA_HOME", filepath.Join(".local", "share"))
	if err != nil {
		return sessionsearch.TranscriptRoots{}, err
	}
	return sessionsearch.TranscriptRoots{
		ClaudeProjects: filepath.Join(claudeHome, "projects"),
		CodexSessions:  filepath.Join(codexHome, "sessions"),
		CodexArchived:  filepath.Join(codexHome, "archived_sessions"),
		OpenCodeData:   dataHome,
		OpenCodeV2Data: filepath.Join(dataHome, "opencode-v2-home"),
		AOStateDir:     stateDir,
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
// never blocks session work. A reindex request (`ao search --reindex`) runs
// here too, between passes, so it never overlaps one.
func runSessionSearch(ctx context.Context, service *sessionsearch.Service, logger *slog.Logger) {
	defer sentryobs.RecoverBackground()
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
		case <-service.ReindexRequests():
			written, err := service.Reindex(ctx)
			switch {
			case err != nil && ctx.Err() == nil:
				logger.Warn("session search reindex failed", "documents", written, "err", err)
			case err == nil:
				logger.Info("session search reindex complete", "documents", written)
			}
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
