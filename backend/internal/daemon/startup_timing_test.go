package daemon

import (
	"slices"
	"testing"
	"time"
)

func TestStartupStepsAddUpToTheTotal(t *testing.T) {
	start := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	now := start
	steps := newStartupSteps(start, func() time.Time { return now })
	now = now.Add(120 * time.Millisecond)
	steps.done(stepPrepare)
	now = now.Add(4 * time.Second)
	steps.done(stepLockWait)
	now = now.Add(30 * time.Millisecond)
	steps.done(stepStoreOpen)

	got := steps.payload(daemonReadyTotal)
	want := map[string]any{"prepare_ms": int64(120), "lock_wait_ms": int64(4000), "store_open_ms": int64(30), "startup_ms": int64(4150)}
	if len(got) != len(want) {
		t.Fatalf("payload = %v, want %v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("%s = %v, want %v", key, got[key], value)
		}
	}
}

func TestStartupStepsKeepFractionalMillisecondsInTheSteps(t *testing.T) {
	start := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	now := start
	steps := newStartupSteps(start, func() time.Time { return now })
	now = now.Add(1500 * time.Microsecond)
	steps.done(stepPrepare)
	now = now.Add(1500 * time.Microsecond)
	steps.done(stepLockWait)

	got := steps.payload(daemonReadyTotal)
	if got["prepare_ms"] != int64(1) || got["lock_wait_ms"] != int64(2) || got["startup_ms"] != int64(3) {
		t.Fatalf("payload = %v, want prepare_ms 1, lock_wait_ms 2, startup_ms 3", got)
	}
}

// These are the keys the PostHog payload allowlist lets through (see
// TestDaemonStartupPayloadAllowlistCoversEveryStep); a step missing there
// would export stripped.
func TestStartupStepKeysMatchTheTelemetryAllowlist(t *testing.T) {
	keys := func(steps []string, total string) []string {
		out := make([]string, 0, len(steps)+1)
		for _, step := range steps {
			out = append(out, step+"_ms")
		}
		return append(out, total)
	}
	ready := []string{
		"prepare_ms", "lock_wait_ms", "store_open_ms", "lifecycle_setup_ms", "chat_setup_ms", "session_setup_ms",
		"install_recovery_ms", "services_setup_ms", "codex_recovery_ms", "startup_reconcile_ms", "listen_ms", "startup_ms",
	}
	if got := keys(daemonReadySteps, daemonReadyTotal); !slices.Equal(got, ready) {
		t.Errorf("ao.daemon.ready keys = %v, want %v", got, ready)
	}
	reconciled := []string{"chat_hosts_ms", "reviewer_recovery_ms", "sessions_ms", "runtime_ms", "reconcile_ms"}
	if got := keys(daemonReconcileSteps, daemonReconcileTotal); !slices.Equal(got, reconciled) {
		t.Errorf("ao.daemon.startup_reconciled keys = %v, want %v", got, reconciled)
	}
}
