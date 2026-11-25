package daemon

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	telemetryadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/telemetry"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
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
// the identity routes is what the exporter stamps and honors, and the local
// SQLite sink (nil when local events are off), which also records every
// exported event for `ao telemetry log`.
func newTelemetrySink(cfg config.Config, store *sqlite.Store, log *slog.Logger) (ports.EventSink, ports.TelemetryIdentityStore, ports.EventSink, bool) {
	if !cfg.Telemetry.Events {
		return telemetryadapter.NoopSink{}, standaloneIdentity(cfg, log), nil, false
	}
	local := telemetryadapter.NewLocalSQLiteSink(store, log, cfg.Telemetry.LocalRetention)
	if cfg.Telemetry.Remote != config.TelemetryRemotePostHog {
		return local, standaloneIdentity(cfg, log), local, false
	}
	remote, err := telemetryadapter.NewPostHogSink(cfg.DataDir, cfg.Telemetry.PostHogKey, cfg.Telemetry.PostHogHost,
		cfg.Telemetry.AppVersion, cfg.Agent, nil, log)
	if err != nil {
		log.Warn("telemetry remote sink disabled", "remote", cfg.Telemetry.Remote, "error", err)
		return local, standaloneIdentity(cfg, log), local, false
	}
	// Every event PostHog accepted is written locally exactly as sent, so
	// `ao telemetry log` shows what left the machine, not the raw local stream.
	remote.OnExported(func(name, distinctID string, props map[string]any) {
		telemetryadapter.RecordExported(context.Background(), local, telemetryadapter.ExportedEvent{Event: name, Surface: "daemon", DistinctID: distinctID, Properties: props})
	})
	// app-state.json and update-settings.json live beside running.json.
	if cfg.RunFilePath != "" {
		remote.SetInstallStateDir(filepath.Dir(cfg.RunFilePath))
	}
	remote.SetLifetimeSessions(store.LifetimeSessions)
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
	rateLimited.SkipMinuteWindow("ao.usage.cli_usage_errors")
	rateLimited.SkipMinuteWindow("ao.usage.messages_sent", "ao.usage.worker_pr_associated", "ao.usage.worker_pr_merged", "ao.prs.closed")
	rateLimited.UseUsageDailyWindow("ao.usage.messages_sent", "ao.usage.worker_pr_associated", "ao.usage.worker_pr_merged", "ao.prs.closed")
	aggregated := telemetryadapter.NewAggregatingSink(rateLimited, aggregatedEventNames, time.Minute)
	// Keep the full local error stream. Remote HTTP 5xx carries only a count
	// per minute; the rate limit is a backstop. CLI usage is one
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
	return telemetryadapter.NewOnboardingSink(telemetryadapter.NewFanoutSink(local, denied), remote.Identity()), remote.Identity(), local, true
}

// newTelemetryLocal backs `ao telemetry log|status|on|off`. The log reads the
// exported rows in the local event store; renderer events forwarded by Electron
// main are recorded there too. The opt-out writes the same marker Settings
// writes, which the desktop, the daemon and a paired phone all honor.
func newTelemetryLocal(cfg config.Config, store *sqlite.Store, local ports.EventSink, ident ports.TelemetryIdentityStore, remoteActive bool, errorReports func(context.Context) bool) *httpd.TelemetryLocal {
	retentionDays := int(telemetryadapter.DefaultRetention / (24 * time.Hour))
	if cfg.Telemetry.LocalRetention >= telemetryadapter.MinRetention {
		retentionDays = int(cfg.Telemetry.LocalRetention / (24 * time.Hour))
	}
	doNotTrack := config.DoNotTrack(os.Getenv)
	status := func(ctx context.Context) httpd.TelemetryStatus {
		st := httpd.TelemetryStatus{
			DoNotTrack:       doNotTrack,
			RemoteConfigured: remoteActive,
			LocalLog:         local != nil && store != nil,
			RetentionDays:    retentionDays,
		}
		if ident != nil {
			st.OptedOut = ident.OptedOut()
			st.ShareIdentity = ident.SharesIdentity()
			if !st.OptedOut {
				st.InstallID = ident.Snapshot().InstallID
			}
		}
		st.AnalyticsSending = remoteActive && !doNotTrack && ident != nil && !ident.ExportSuppressed()
		st.ErrorReports = !doNotTrack && errorReports != nil && errorReports(ctx)
		return st
	}
	return &httpd.TelemetryLocal{
		Status: status,
		List: func(ctx context.Context, since time.Time, limit int) ([]telemetryadapter.ExportedEvent, error) {
			if store == nil {
				return nil, nil
			}
			rows, err := store.ListExportedTelemetryEventsSince(ctx, since, int64(limit))
			if err != nil {
				return nil, err
			}
			return telemetryadapter.DecodeExported(rows), nil
		},
		Record: func(ctx context.Context, ev telemetryadapter.ExportedEvent) {
			if local == nil || (ident != nil && ident.OptedOut()) {
				return
			}
			telemetryadapter.RecordExported(ctx, local, ev)
		},
		SetOptedOut: func(_ context.Context, optedOut bool) error {
			if cfg.DataDir == "" {
				return errors.New("telemetry data dir unknown")
			}
			marker := filepath.Join(cfg.DataDir, telemetryadapter.OptOutFile)
			if !optedOut {
				if err := os.Remove(marker); err != nil && !os.IsNotExist(err) {
					return err
				}
				return nil
			}
			if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
				return err
			}
			if err := os.WriteFile(marker, []byte("1\n"), 0o600); err != nil {
				return err
			}
			// Matches the desktop's opt-out: counts held for the daily CLI
			// summary are dropped, not sent after a later opt-in.
			_ = os.Remove(filepath.Join(cfg.DataDir, "telemetry", "cli-usage-daily.json"))
			return nil
		},
	}
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
// background and stores it on the telemetry identity. The login is exported as
// github_actor only when the user turned on "Include my account identity"
// (off by default); otherwise it only tells onboarding that GitHub sign-in is
// done. No lookup runs while telemetry is opted out. The provider is rebuilt
// per attempt because it reads the token at construction. Only the
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
			if ident.OptedOut() {
				continue
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

// emitInstallLifecycleEvents sends ao.app.installed once, on the first daemon
// start of a brand-new install. hasPriorState reports whether the local store
// already holds projects or sessions: an install ID created on upgrade (or
// after the file was lost) is then not a new install, so the event is spent
// without being sent. The event is reserved durably before it is queued, so a
// restart cannot send it twice.
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

// storeHasPriorState reports whether the store already has projects or
// committed sessions. A read error counts as prior state: a missed install
// event is safer than a false one.
func storeHasPriorState(store *sqlite.Store) func(context.Context) bool {
	return func(ctx context.Context) bool {
		if sessions, err := store.LifetimeSessions(ctx); err != nil || sessions > 0 {
			return true
		}
		projects, err := store.CountProjectsIncludingArchived(ctx)
		return err != nil || projects > 0
	}
}

// daemonLauncher reports who started this daemon. The desktop supervisor
// always sets AO_OWNER (app or persistent); a CLI-started daemon has none.
// The common client property stays "cli" for every daemon event, so this is
// how install events split desktop from CLI-only installs.
func daemonLauncher(getenv func(string) string) string {
	if getenv("AO_OWNER") != "" {
		return "desktop"
	}
	return "cli"
}

// installMilestones backs first_for_install. Returned as a nil interface (not
// a typed nil) when there is no data dir, so services omit the property.
func installMilestones(cfg config.Config) ports.InstallMilestones {
	if m := telemetryadapter.NewMilestones(cfg.DataDir); m != nil {
		return m
	}
	return nil
}
