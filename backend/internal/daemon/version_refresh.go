package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
)

// versionRefreshSweepTick paces moves to a newly installed harness release.
// Sessions must already have been quiet for VersionRefreshIdle, so a tick
// only bounds how long after that they wait.
const versionRefreshSweepTick = time.Minute

type versionRefreshSettings interface {
	VersionRefreshEnabled(ctx context.Context) bool
	ChatHibernationEnabled() bool
}

// runVersionRefreshSweep moves idle Chat sessions still on a replaced harness
// release to the installed one, and removes Homebrew kegs kept for sessions
// that have all moved. It runs whether or not idle hibernation is on: with it
// off, a moved session is started again right away; with it on, the session
// sleeps as idle hibernation would have left it and wakes on the new release.
func runVersionRefreshSweep(ctx context.Context, cfg config.Config, settings versionRefreshSettings, sessions sessionLifecycle, installs *systeminstall.Service, log *slog.Logger) {
	ticker := time.NewTicker(versionRefreshSweepTick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		if cfg.VersionRefresh && settings.VersionRefreshEnabled(ctx) {
			wake := !settings.ChatHibernationEnabled()
			if err := sessions.RefreshStaleChats(ctx, cfg.VersionRefreshIdle, wake); err != nil && ctx.Err() == nil {
				log.Warn("moving idle chats to the installed harness release failed", "err", err)
			}
		}
		if installs != nil {
			installs.RunDeferredCleanups(ctx)
		}
	}
}
