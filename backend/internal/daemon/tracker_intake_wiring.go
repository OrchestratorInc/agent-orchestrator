package daemon

import (
	"context"
	"log/slog"

	trackerintake "github.com/aoagents/agent-orchestrator/backend/internal/observe/trackerintake"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// startTrackerIntake wires the issue-intake loop behind the daemon-wide
// AO_TRACKER_INTAKE gate, which defaults off. A project's own
// trackerIntake.enabled is deliberately not sufficient on its own: intake
// starts one session per eligible issue with no concurrency bound, and its
// prompt-assembly path still has open hardening work, so turning it on is an
// operator decision rather than a per-project one. Returning a nil channel is
// safe; the shutdown await in lifecycle_wiring.go nil-guards it.
//
// With the gate on, the observer always runs. Poll re-reads each project's
// config on every tick and skips projects with intake disabled, so a project
// enabling intake after daemon boot is picked up on the next tick without a
// restart. The multi-tracker (supporting both GitHub and GitLab) is built once
// in Run and shared between the session service and the intake observer.
func startTrackerIntake(ctx context.Context, enabled bool, store *sqlite.Store, sessions *sessionsvc.Service, tracker ports.Tracker, logger *slog.Logger) <-chan struct{} {
	if !enabled {
		logGatedOffIntake(ctx, store, logger)
		return nil
	}
	// SingleTrackerResolver with an empty Provider matches any provider,
	// letting the multi-tracker dispatch based on each project's configured
	// provider. A nil tracker (no credentials) causes Resolve to return an
	// error for every project, which triggers backoff — correct behavior.
	resolver := trackerintake.SingleTrackerResolver{
		Adapter: tracker,
	}
	observer := trackerintake.New(resolver, store, sessions, trackerintake.Config{Logger: logger})
	return observer.Start(ctx)
}

// logGatedOffIntake reports the gate, escalating to Warn when projects still
// carry trackerIntake.enabled. Intake emits no telemetry, so this line is the
// only signal an operator gets that automation they configured has stopped
// running. The scan is diagnostic and gates nothing: a boot-time scan that
// decided whether to start the loop is the regression covered by
// TestStartTrackerIntake_RunsEvenWithoutEnabledProjects. It over-counts a
// project whose repo would never have resolved, which is the safe direction.
func logGatedOffIntake(ctx context.Context, store *sqlite.Store, logger *slog.Logger) {
	const hint = "tracker intake: gated off, set AO_TRACKER_INTAKE=on to enable"
	projects, err := store.ListProjects(ctx)
	if err != nil {
		logger.Info(hint, "projectScanErr", err)
		return
	}
	stranded := 0
	for _, project := range projects {
		if project.Config.TrackerIntake.Enabled {
			stranded++
		}
	}
	if stranded == 0 {
		logger.Info(hint)
		return
	}
	logger.Warn(hint, "projectsWithIntakeEnabled", stranded)
}
