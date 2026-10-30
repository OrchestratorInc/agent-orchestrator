package daemon

import (
	"context"
	"log/slog"
	"path/filepath"
	"time"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// aggregatedEventNames are the event names prone to bursts (crash loops,
// retry storms) that get folded into one rollup event per minute instead of
// one PostHog event per occurrence, and correspondingly get the generous
// daily rate-limit tier since their per-occurrence cost is already gone (see
// RateLimitedSink's eventsPerNamePerDayAggregated). Must match the event
// names as actually emitted (see httpd/router.go and httpd/recover.go /
// httpd/log.go) exactly, including underscores - there is no compile-time
// check tying this list to the emit sites.
//
// Deliberately does NOT include ao.cli.invoked or ao.app.active: those are
// already deduped to at most once per command-path/day (ao.cli.invoked) or
// once per day (ao.app.active) at the httpd layer before they ever reach a
// sink, including for ao hooks/ao pty-host and high-frequency polling
// commands like ao status/ao session ls - adding them here would double up
// on dedup logic that already owns that job and gains nothing.
var aggregatedEventNames = []string{
	"ao.http.5xx",
	"ao.daemon.panic",
}

// newTelemetrySink also returns the live telemetry identity (nil when the data
// dir is unknown), shared with the remote sink so a sign-in or opt-out seen by
// the identity routes is what the exporter stamps and honors.
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
	// Both wrap only the billed remote sink; local storage keeps every event
	// unaggregated and unfiltered for debugging regardless of PostHog volume.
	// Aggregation sits in front: burst-prone event names get folded into one
	// rollup per minute before they ever reach the rate limiter, which then
	// applies the generous tier to those same names as a structural backstop
	// rather than the primary cost control, and still does the real limiting
	// job for every event name that isn't aggregated.
	rateLimited := telemetryadapter.NewRateLimitedSink(remote, aggregatedEventNames)
	// CLI usage is already one event per command path and error kind. The
	// minute cap would drop most of that daily burst. The day cap stays.
	rateLimited.SkipMinuteWindow("ao.cli.usage_errors")
	rateLimited.SkipMinuteWindow("ao.usage.messages_sent", "ao.usage.worker_pr_associated", "ao.usage.worker_pr_merged")
	rateLimited.UseUsageDailyWindow("ao.usage.messages_sent", "ao.usage.worker_pr_associated", "ao.usage.worker_pr_merged")
	aggregated := telemetryadapter.NewAggregatingSink(rateLimited, aggregatedEventNames, time.Minute)
	// Keep the full local error stream. Remote HTTP 5xx is not sampled: the
	// minute rollup and the rate limit already bound it. CLI usage is one
	// event per command path and error kind per completed UTC day.
	daily := telemetryadapter.NewDailySummarySink(aggregated, filepath.Join(cfg.DataDir, "telemetry", "cli-usage-daily.json"))
	remote.Identity().OnInstallRetired(daily.Discard)
	// Opt-out is enforced ahead of aggregation: events folded into a rollup
	// while opted out could flush after an opt back in.
	gated := telemetryadapter.NewOptOutSink(daily, remote.Identity().ExportSuppressed)
	// The kill switch sits outermost on the remote chain so a silenced stream
	// costs nothing downstream: no aggregation window, no rate-limit slot, no
	// export. Local storage is unaffected, so silenced events stay debuggable.
	denied := telemetryadapter.NewDenylistSink(gated, cfg.Telemetry.DisabledEvents)
	return telemetryadapter.NewFanoutSink(local, denied), remote.Identity(), true
}

// Recording is independent of sink startup so a temporary remote setup
// failure does not lose accepted local usage facts.
func configureUsageRecording(cfg config.Config, store *sqlite.Store, identity ports.TelemetryIdentityStore) {
	if !cfg.Telemetry.Events || cfg.Telemetry.Remote != config.TelemetryRemotePostHog || identity == nil {
		return
	}
	store.SetUsageTelemetryAllowed(func() bool { return !identity.OptedOut() })
}

// standaloneIdentity serves the identity routes when nothing is exported, so the
// phone still learns the opt-out state and the install ID.
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

// gitHubLoginRetryDelays space the attempts to resolve the operator's GitHub
// login after daemon start. A fresh install usually has no token yet, so the
// later attempts catch a PAT or `gh auth login` done shortly after launch.
// ponytail: fixed schedule, then give up until the next daemon start.
var gitHubLoginRetryDelays = []time.Duration{0, 30 * time.Second, 2 * time.Minute, 10 * time.Minute}

// startGitHubLoginResolver resolves the authenticated human GitHub login in the
// background and stores it on the telemetry identity, so every exported daemon
// event carries github_actor, not just ao.session.spawned. The provider is
// rebuilt per attempt because it reads the token at construction. Only the
// authenticated GitHub account is used, never git config.
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
