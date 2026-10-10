package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	agentswitchobs "github.com/aoagents/agent-orchestrator/backend/internal/observe/agentswitch"
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
	"ao.cli.usage_errors",
}

func newAgentSwitchFailureDispatcher(
	store ports.AgentSwitchFailureOutboxStore,
	policy agentswitchobs.PolicyCoordinator,
	observer ports.AgentSwitchFailureObserver,
	log *slog.Logger,
) (*agentswitchobs.Dispatcher, error) {
	if observer == nil {
		backlog, err := store.AgentSwitchFailureBacklog(context.Background(), time.Now().UTC())
		if err != nil {
			return nil, fmt.Errorf("inspect agent switch failure backlog: %w", err)
		}
		if policy.Authorization().Enabled || backlog.Pending != 0 || backlog.Leased != 0 {
			return nil, errors.New("agent switch failure observer unavailable with enabled policy or pending payload")
		}
		return nil, nil
	}
	return agentswitchobs.NewDispatcher(agentswitchobs.DispatcherConfig{
		Store: store, Observer: observer, Policy: policy, Logger: log,
	})
}

// newTelemetrySink also returns the live telemetry identity (nil when the data
// dir is unknown), shared with the remote sink so a sign-in or opt-out seen by
// the identity routes is what the exporter stamps and honors.
func newTelemetrySink(cfg config.Config, store *sqlite.Store, log *slog.Logger) (ports.EventSink, ports.TelemetryIdentityStore) {
	if !cfg.Telemetry.Events {
		return telemetryadapter.NoopSink{}, standaloneIdentity(cfg, log)
	}
	local := telemetryadapter.NewLocalSQLiteSink(store, log)
	if cfg.Telemetry.Remote != config.TelemetryRemotePostHog {
		return local, standaloneIdentity(cfg, log)
	}
	remote, err := telemetryadapter.NewPostHogSink(cfg.DataDir, cfg.Telemetry.PostHogKey, cfg.Telemetry.PostHogHost,
		cfg.Telemetry.AppVersion, cfg.Agent, nil, log)
	if err != nil {
		log.Warn("telemetry remote sink disabled", "remote", cfg.Telemetry.Remote, "error", err)
		return local, standaloneIdentity(cfg, log)
	}
	// Both wrap only the billed remote sink; local storage keeps every event
	// unaggregated and unfiltered for debugging regardless of PostHog volume.
	// Aggregation sits in front: burst-prone event names get folded into one
	// rollup per minute before they ever reach the rate limiter, which then
	// applies the generous tier to those same names as a structural backstop
	// rather than the primary cost control, and still does the real limiting
	// job for every event name that isn't aggregated.
	rateLimited := telemetryadapter.NewRateLimitedSink(remote, aggregatedEventNames)
	aggregated := telemetryadapter.NewAggregatingSink(rateLimited, aggregatedEventNames, time.Minute)
	// The kill switch sits outermost on the remote chain so a silenced stream
	// costs nothing downstream: no aggregation window, no rate-limit slot, no
	// export. Local storage is unaffected, so silenced events stay debuggable.
	denied := telemetryadapter.NewDenylistSink(aggregated, cfg.Telemetry.DisabledEvents)
	return telemetryadapter.NewFanoutSink(local, denied), remote.Identity()
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
