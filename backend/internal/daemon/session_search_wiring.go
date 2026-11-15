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

const sessionSearchSweepInterval = 30 * time.Second

const sessionSearchStartupDelay = 5 * time.Second

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

func sessionSearchRoots(stateDir string) (sessionsearch.TranscriptRoots, error) {
	claudeHome, err := nativeconfig.Resolve(nil, "CLAUDE_CONFIG_DIR", ".claude")
	if err != nil {
		return sessionsearch.TranscriptRoots{}, err
	}
	codexHome, err := nativeconfig.Resolve(nil, "CODEX_HOME", ".codex")
	if err != nil {
		return sessionsearch.TranscriptRoots{}, err
	}
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
