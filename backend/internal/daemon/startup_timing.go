package daemon

import (
	"context"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// ao.daemon.ready is sent once the API listens: how long startup took from
// Run, split into back-to-back steps that add up to startup_ms. A slow launch
// whose sidebar waited on the daemon shows which step held it.
const daemonReadyEvent = "ao.daemon.ready"

// ao.daemon.startup_reconciled is sent when the background restore that starts
// with the listener ends: chat hosts, reviewer chats, sessions and agent
// processes, adding up to reconcile_ms.
const daemonStartupReconciledEvent = "ao.daemon.startup_reconciled"

// Steps of ao.daemon.ready, in order. Each is sent as <step>_ms; the payload
// allowlist in adapters/telemetry lists the same keys.
const (
	stepPrepare          = "prepare"           // config, data directory, lifecycle log, browser runtime tokens and the stale run-file probe
	stepLockWait         = "lock_wait"         // the data directory lock, held by a predecessor still shutting down
	stepStoreOpen        = "store_open"        // SQLite open, migrations and report claim recovery
	stepLifecycleSetup   = "lifecycle_setup"   // skills, telemetry, notifications, CDC and the lifecycle manager
	stepChatSetup        = "chat_setup"        // chat services, side chats, the Codex home and the reviewer resolver
	stepSessionSetup     = "session_setup"     // the session services
	stepInstallRecovery  = "install_recovery"  // harness install jobs left by a previous run
	stepServicesSetup    = "services_setup"    // search, terminals, usage, SCM and the remaining services
	stepCodexRecovery    = "codex_recovery"    // interrupted Codex account switches
	stepStartupReconcile = "startup_reconcile" // the session safety reconcile that gates the API
	stepListen           = "listen"            // the remaining listeners and the HTTP bind
)

// Steps of ao.daemon.startup_reconciled, in order.
const (
	stepChatHosts        = "chat_hosts"
	stepReviewerRecovery = "reviewer_recovery"
	stepSessions         = "sessions"
	stepRuntime          = "runtime"
)

var (
	daemonReadySteps     = []string{stepPrepare, stepLockWait, stepStoreOpen, stepLifecycleSetup, stepChatSetup, stepSessionSetup, stepInstallRecovery, stepServicesSetup, stepCodexRecovery, stepStartupReconcile, stepListen}
	daemonReconcileSteps = []string{stepChatHosts, stepReviewerRecovery, stepSessions, stepRuntime}
	daemonReadyTotal     = "startup_ms"
	daemonReconcileTotal = "reconcile_ms"
)

// startupSteps splits a stretch of startup into back-to-back named steps, so
// the steps always add up to the whole and no time goes unnamed.
type startupSteps struct {
	now   func() time.Time
	start time.Time
	// lastMs is when the previous step ended, in whole milliseconds since start.
	lastMs int64
	ms     map[string]int64
}

func newStartupSteps(start time.Time, now func() time.Time) *startupSteps {
	return &startupSteps{now: now, start: start, ms: map[string]int64{}}
}

// done ends the step that ran since the previous one ended. Steps are the gaps
// between whole-millisecond end times, so rounding never makes them add up to
// less than the total.
func (s *startupSteps) done(step string) {
	endMs := s.now().Sub(s.start).Milliseconds()
	s.ms[step+"_ms"] += endMs - s.lastMs
	s.lastMs = endMs
}

// payload is each step and the total under totalKey, in milliseconds.
func (s *startupSteps) payload(totalKey string) map[string]any {
	out := make(map[string]any, len(s.ms)+1)
	for key, ms := range s.ms {
		out[key] = ms
	}
	out[totalKey] = s.lastMs
	return out
}

func emitStartupSteps(sink ports.EventSink, name string, steps *startupSteps, totalKey string) {
	sink.Emit(context.Background(), ports.TelemetryEvent{
		Name:       name,
		Source:     "daemon",
		OccurredAt: time.Now().UTC(),
		Level:      ports.TelemetryLevelInfo,
		Payload:    steps.payload(totalKey),
	})
}
