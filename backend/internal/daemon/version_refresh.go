package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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

// runVersionRefreshSweep moves idle Chat sessions, and with
// AO_TUI_VERSION_REFRESH idle terminal sessions, still on a replaced harness
// release to the installed one, and removes Homebrew kegs kept for sessions
// that have all moved. It runs whether or not idle hibernation is on: with it
// off, a moved session is started again right away; with it on, the session
// sleeps as idle hibernation would have left it and wakes on the new release.
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
		if cfg.VersionRefresh && settings.VersionRefreshEnabled(ctx) {
			wake := !settings.ChatHibernationEnabled()
			if err := sessions.RefreshStaleChats(ctx, cfg.VersionRefreshIdle, wake); err != nil && ctx.Err() == nil {
				log.Warn("moving idle chats to the installed harness release failed", "err", err)
			}
			if cfg.TUIVersionRefresh {
				if err := sessions.RefreshStaleTerminals(ctx, cfg.VersionRefreshIdle); err != nil && ctx.Err() == nil {
					log.Warn("restarting idle terminals on the installed harness release failed", "err", err)
				}
			}
		}
		if installs != nil {
			installs.RunDeferredCleanups(ctx)
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
