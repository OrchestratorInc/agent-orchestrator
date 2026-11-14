package daemon

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

var aggregatedEventNames = []string{
	"ao.http.5xx",
	"ao.daemon.panic",
}

func newTelemetrySink(cfg config.Config, store *sqlite.Store, log *slog.Logger) (ports.EventSink, ports.TelemetryIdentityStore, bool) {
	if !cfg.Telemetry.Events {
		return telemetryadapter.NoopSink{}, standaloneIdentity(cfg, log), false
	}
	local := telemetryadapter.NewLocalSQLiteSink(store, log, cfg.Telemetry.LocalRetention)
	if cfg.Telemetry.Remote != config.TelemetryRemotePostHog {
		return local, standaloneIdentity(cfg, log), false
	}
	remote, err := telemetryadapter.NewPostHogSink(cfg.DataDir, cfg.Telemetry.PostHogKey, cfg.Telemetry.PostHogHost,
		cfg.Telemetry.AppVersion, cfg.Agent, nil, log)
	if err != nil {
		log.Warn("telemetry remote sink disabled", "remote", cfg.Telemetry.Remote, "error", err)
		return local, standaloneIdentity(cfg, log), false
	}
	if cfg.RunFilePath != "" {
		remote.SetInstallStateDir(filepath.Dir(cfg.RunFilePath))
	}
	remote.SetLifetimeSessions(store.LifetimeSessions)
	rateLimited := telemetryadapter.NewRateLimitedSink(remote, aggregatedEventNames)
	rateLimited.SkipMinuteWindow("ao.usage.cli_usage_errors")
	rateLimited.SkipMinuteWindow("ao.usage.messages_sent", "ao.usage.worker_pr_associated", "ao.usage.worker_pr_merged", "ao.prs.closed")
	rateLimited.UseUsageDailyWindow("ao.usage.messages_sent", "ao.usage.worker_pr_associated", "ao.usage.worker_pr_merged", "ao.prs.closed")
	aggregated := telemetryadapter.NewAggregatingSink(rateLimited, aggregatedEventNames, time.Minute)
	daily := telemetryadapter.NewDailySummarySink(aggregated, filepath.Join(cfg.DataDir, "telemetry", "cli-usage-daily.json"))
	remote.Identity().OnInstallRetired(daily.Discard)
	gated := telemetryadapter.NewOptOutSink(daily, remote.Identity().ExportSuppressed)
	denied := telemetryadapter.NewDenylistSink(gated, cfg.Telemetry.DisabledEvents)
	return telemetryadapter.NewOnboardingSink(telemetryadapter.NewFanoutSink(local, denied), remote.Identity()), remote.Identity(), true
}

const writeLockHeldEvent = "ao.daemon.write_lock_held"

func recordSlowWriteLocks(store *sqlite.Store, sink ports.EventSink) {
	store.SetWriteLockObserver(func(caller string, held time.Duration) {
		sink.Emit(context.Background(), ports.TelemetryEvent{
			Name:       writeLockHeldEvent,
			Source:     "daemon",
			OccurredAt: time.Now().UTC(),
			Level:      ports.TelemetryLevelWarn,
			Payload:    map[string]any{"caller": caller, "duration_ms": held.Milliseconds()},
		})
	})
}

func configureUsageRecording(cfg config.Config, store *sqlite.Store, identity ports.TelemetryIdentityStore) {
	if !cfg.Telemetry.Events || cfg.Telemetry.Remote != config.TelemetryRemotePostHog || identity == nil {
		return
	}
	store.SetUsageTelemetryAllowed(func() bool { return !identity.OptedOut() })
}

func standaloneIdentity(cfg config.Config, log *slog.Logger) ports.TelemetryIdentityStore {
	if cfg.DataDir == "" {
		return nil
	}
	ident, err := telemetryadapter.NewIdentity(cfg.DataDir)
	if err != nil {
		log.Warn("telemetry identity unavailable", "error", err)
		return nil
	}
	return ident
}

var gitHubLoginRetryDelays = []time.Duration{0, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

func startGitHubLoginResolver(ctx context.Context, cfg config.Config, ident ports.TelemetryIdentityStore, log *slog.Logger) {
	if ident == nil || !cfg.Telemetry.Events || cfg.Telemetry.Remote != config.TelemetryRemotePostHog {
		return
	}
	go func() {
		defer sentryobs.RecoverBackground()
		for _, delay := range gitHubLoginRetryDelays {
			select {
			case <-ctx.Done():
				return
			case <-time.After(delay):
			}
			scm := newMultiSCMProvider(cfg.GitLab, log)
			if scm == nil {
				continue
			}
			attempt, cancel := context.WithTimeout(ctx, 15*time.Second)
			identity, err := scm.AuthenticatedIdentityForProvider(attempt, "github", "")
			cancel()
			if err == nil && identity.Human && identity.Login != "" {
				ident.SetGitHubLogin(identity.Login)
				return
			}
		}
	}()
}

func emitInstallLifecycleEvents(cfg config.Config, ident ports.TelemetryIdentityStore, sink ports.EventSink, hasPriorState func(context.Context) bool, log *slog.Logger) {
	if ident == nil || !cfg.Telemetry.Events {
		return
	}
	ctx := context.Background()
	prior := hasPriorState != nil && hasPriorState(ctx)
	marked, err := ident.MarkInstalledEvent(ctx)
	if err != nil {
		log.Warn("record telemetry install event", "error", err)
		return
	}
	if !marked || prior {
		return
	}
	sink.Emit(ctx, ports.TelemetryEvent{
		Name:       "ao.app.installed",
		Source:     "daemon",
		OccurredAt: time.Now().UTC(),
		Level:      ports.TelemetryLevelInfo,
		Payload:    map[string]any{"launcher": daemonLauncher(os.Getenv)},
	})
}

func storeHasPriorState(store *sqlite.Store) func(context.Context) bool {
	return func(ctx context.Context) bool {
		if sessions, err := store.LifetimeSessions(ctx); err != nil || sessions > 0 {
			return true
		}
		projects, err := store.CountProjectsIncludingArchived(ctx)
		return err != nil || projects > 0
	}
}

func daemonLauncher(getenv func(string) string) string {
	if getenv("AO_OWNER") != "" {
		return "desktop"
	}
	return "cli"
}

func installMilestones(cfg config.Config) ports.InstallMilestones {
	if m := telemetryadapter.NewMilestones(cfg.DataDir); m != nil {
		return m
	}
	return nil
}
