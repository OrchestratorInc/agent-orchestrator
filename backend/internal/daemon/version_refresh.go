package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/systeminstall"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// versionRefreshSweepTick paces moves to a newly installed harness release.
// Sessions must already have been quiet for VersionRefreshIdle, so a tick
// only bounds how long after that they wait.
const versionRefreshSweepTick = time.Minute

type versionRefreshSettings interface {
	VersionRefreshEnabled(ctx context.Context) bool
}

// runVersionRefreshSweep moves idle Chat sessions, and with
// AO_TUI_VERSION_REFRESH idle terminal sessions, still on a replaced harness
// release to the installed one, and removes Homebrew kegs kept for sessions
// that have all moved. It runs whether or not idle hibernation is on, and a
// moved chat is always started again right away on the new release.
func runVersionRefreshSweep(ctx context.Context, cfg config.Config, settings versionRefreshSettings, sessions sessionLifecycle, installs *systeminstall.Service, telemetry ports.EventSink, log *slog.Logger) {
	ticker := time.NewTicker(versionRefreshSweepTick)
	defer ticker.Stop()
	gauge := staleSessionsGauge{sink: telemetry}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		gauge.observe(ctx, sessions)
		policy := versionRefreshPolicy(ctx, cfg, settings)
		if policy.Chats {
			if err := sessions.RefreshStaleChats(ctx, policy.MinIdle); err != nil && ctx.Err() == nil {
				log.Warn("moving idle chats to the installed harness release failed", "err", err)
			}
		}
		if policy.Terminals {
			if err := sessions.RefreshStaleTerminals(ctx, policy.MinIdle); err != nil && ctx.Err() == nil {
				log.Warn("restarting idle terminals on the installed harness release failed", "err", err)
			}
		}
		if err := sessions.RetryVersionMoves(ctx); err != nil && ctx.Err() == nil {
			log.Warn("resuming sessions after a failed version move failed", "err", err)
		}
		if installs != nil {
			installs.RunDeferredCleanups(ctx)
			installs.RemoveUnusedManagedReleases(ctx)
		}
	}
}

// EventStaleSessions reports, per harness, how many live sessions still run a
// replaced release. The rollout target is about zero within an hour of an update.
const EventStaleSessions = "ao.harness.stale_sessions"

// staleSessionsGauge reports stale-session counts every staleSessionsEvery,
// and only for harnesses that have some now or had some at the last report.
type staleSessionsGauge struct {
	sink     ports.EventSink
	last     map[domain.AgentHarness]int64
	reported time.Time
}

const staleSessionsEvery = 10 * time.Minute

func (g *staleSessionsGauge) observe(ctx context.Context, sessions sessionLifecycle) {
	if g.sink == nil || time.Since(g.reported) < staleSessionsEvery {
		return
	}
	counts, err := sessions.CountStaleBinarySessions(ctx)
	if err != nil {
		return
	}
	g.reported = time.Now()
	for harness, previous := range g.last {
		if _, ok := counts[harness]; !ok && previous > 0 {
			counts[harness] = 0
		}
	}
	for harness, count := range counts {
		g.sink.Emit(ctx, ports.TelemetryEvent{
			Name: EventStaleSessions, Source: "daemon", OccurredAt: time.Now().UTC(), Level: ports.TelemetryLevelInfo,
			Payload: map[string]any{"harness": string(harness), "sessions": count},
		})
	}
	g.last = counts
}

// versionRefreshPolicy combines the kill switches with the user's setting.
func versionRefreshPolicy(ctx context.Context, cfg config.Config, settings versionRefreshSettings) sessionmanager.VersionRefreshPolicy {
	enabled := cfg.VersionRefresh && settings.VersionRefreshEnabled(ctx)
	return sessionmanager.VersionRefreshPolicy{
		Chats:     enabled,
		Terminals: enabled && cfg.TUIVersionRefresh,
		MinIdle:   cfg.VersionRefreshIdle,
	}
}

// managedHarnessRoot is where AO keeps versioned harness installs: under the
// AO data dir, like every other piece of AO state.
func managedHarnessRoot(cfg config.Config) string {
	if !cfg.ManagedHarnessInstalls || cfg.DataDir == "" {
		return ""
	}
	return filepath.Join(cfg.DataDir, "harnesses")
}
