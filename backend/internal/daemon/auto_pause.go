package daemon

import (
	"context"
	"log/slog"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	settingssvc "github.com/aoagents/agent-orchestrator/backend/internal/service/settings"
)

// autoPauseInterval is how often the idle policy is evaluated. A minute is
// coarse enough to cost nothing and fine enough that "pause after 15 min"
// lands within a minute of the threshold.
const autoPauseInterval = time.Minute

// autoPauseSettings is the slice of the settings service the loop reads.
type autoPauseSettings interface {
	Get(ctx context.Context) (settingssvc.Snapshot, error)
}

// autoPauser is the slice of the session service the loop drives.
type autoPauser interface {
	PauseIdle(ctx context.Context, in sessionsvc.PauseIdleInput) (sessionsvc.PauseIdleOutcome, error)
}

// runAutoPause pauses worker agents that have sat idle longer than the user's
// threshold. It re-reads the setting every tick so a change in Settings takes
// effect without a restart, and it does nothing while the setting is zero.
func runAutoPause(ctx context.Context, settings autoPauseSettings, sessions autoPauser, interval time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		autoPauseTick(ctx, settings, sessions, log)
	}
}

func autoPauseTick(ctx context.Context, settings autoPauseSettings, sessions autoPauser, log *slog.Logger) {
	snapshot, err := settings.Get(ctx)
	if err != nil {
		log.Warn("auto-pause: read settings", "err", err)
		return
	}
	if snapshot.AutoPauseIdleMinutes <= 0 {
		return
	}
	out, err := sessions.PauseIdle(ctx, sessionsvc.PauseIdleInput{
		IdleFor: time.Duration(snapshot.AutoPauseIdleMinutes) * time.Minute,
		Reason:  domain.SessionPauseIdle,
	})
	if err != nil {
		log.Warn("auto-pause: sweep failed", "err", err)
		return
	}
	if len(out.Paused) > 0 {
		log.Info("auto-pause: paused idle agents", "sessions", out.Paused, "idleMinutes", snapshot.AutoPauseIdleMinutes)
	}
	for id, reason := range out.Failed {
		log.Warn("auto-pause: could not pause", "session", id, "reason", reason)
	}
}
