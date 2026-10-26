package reconcile

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// launchStore keeps one sandbox row the way Postgres would across reconcile
// passes: a bootstrap ticket is a launch record, a startup repair counts the
// attempt and reopens the startup window, and an observation moves the
// observed state. Time comes from the test's clock.
type launchStore struct {
	lifecycleStore
	now      func() time.Time
	record   domain.Sandbox
	launches []postgres.WorkerLaunch
	// seenFresh makes the latest-launch read report a check-in the claimed
	// record has not seen yet.
	seenFresh bool
	released  int
}

func (s *launchStore) IssueAccessTicket(_ context.Context, _, _, purpose string, _ []string, _ time.Duration) (string, error) {
	if purpose == "worker_bootstrap" {
		s.launches = append(s.launches, postgres.WorkerLaunch{
			Epoch: int64(len(s.launches) + 1), IssuedAt: s.now(),
		})
	}
	return fmt.Sprintf("ticket-%d", len(s.launches)), nil
}

func (s *launchStore) LatestWorkerLaunch(context.Context, string, string) (postgres.WorkerLaunch, error) {
	if len(s.launches) == 0 {
		return postgres.WorkerLaunch{}, postgres.ErrNotFound
	}
	launch := s.launches[len(s.launches)-1]
	launch.WorkerSeen = s.record.WorkerLastSeenAt != nil || s.seenFresh
	return launch, nil
}

func (s *launchStore) RecordSandboxStartupRepair(context.Context, string, string, string) (int, error) {
	s.repairs++
	now := s.now()
	s.record.StartupAttempts++
	s.record.StartupStartedAt = &now
	return s.record.StartupAttempts, nil
}

func (s *launchStore) UpdateSandboxObservation(
	ctx context.Context, owner, orgID, sessionID, providerID, state, lastError string, after time.Time,
) error {
	s.record.ProviderEnvironmentID = providerID
	s.record.ObservedState = state
	return s.lifecycleStore.UpdateSandboxObservation(ctx, owner, orgID, sessionID, providerID, state, lastError, after)
}

func (s *launchStore) RecordSandboxStartupError(ctx context.Context, owner, orgID, sessionID, code, message string) error {
	s.record.StartupErrorCode = code
	return s.lifecycleStore.RecordSandboxStartupError(ctx, owner, orgID, sessionID, code, message)
}

func (s *launchStore) ReleaseSandboxClaim(context.Context, string, string, string, time.Time) error {
	s.released++
	return nil
}

func (s *launchStore) terminated() bool {
	return s.record.ObservedState == domain.SandboxObservedTerminated
}

// launchHarness drives one session through reconcile passes on a fake clock.
type launchHarness struct {
	t        *testing.T
	clock    time.Time
	start    time.Time
	store    *launchStore
	provider *byoProvider
	activity *recordedActivity
	r        *Reconciler
}

func newLaunchHarness(t *testing.T, record domain.Sandbox) *launchHarness {
	t.Helper()
	h := &launchHarness{t: t, clock: time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)}
	h.start = h.clock
	started := h.clock
	record.ObservedState = domain.SandboxObservedProvisioning
	record.StartupStartedAt = &started
	record.UpdatedAt = started
	h.store = &launchStore{now: func() time.Time { return h.clock }, record: record}
	h.provider = &byoProvider{lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateRunning,
	}}}
	h.activity = &recordedActivity{}
	h.r = byoReconciler(h.store, h.provider)
	h.r.now = func() time.Time { return h.clock }
	h.r.SetActivityRecorder(h.activity)
	return h
}

// at runs one reconcile pass at the given offset from the startup's start.
func (h *launchHarness) at(offset time.Duration) {
	h.t.Helper()
	h.clock = h.start.Add(offset)
	if err := h.r.reconcileSandbox(context.Background(), h.store.record); err != nil {
		h.t.Fatalf("reconcile at %s: %v", offset, err)
	}
}

func (h *launchHarness) wantLaunches(offset time.Duration, want int) {
	h.t.Helper()
	if got := len(h.provider.bootstraps); got != want {
		h.t.Fatalf("at %s: %d worker launches, want %d", offset, got, want)
	}
	if h.store.terminated() {
		h.t.Fatalf("at %s: session parked, want it still starting", offset)
	}
}

func (h *launchHarness) relaunches() int {
	count := 0
	for _, kind := range h.activity.kinds() {
		if kind == domain.ActivityWorkerRelaunched {
			count++
		}
	}
	return count
}

// The prod incident: a bring-your-own Coder worker was launched, never checked
// in, and nothing relaunched it before the 20-minute ceiling parked the
// session. It is now relaunched once its check-in window closes, and the
// session parks only after the relaunched worker's own window closes too.
func TestLaunchedWorkerThatNeverChecksInIsRelaunchedOnceThenParked(t *testing.T) {
	h := newLaunchHarness(t, coderRecord(t, "connection-1", byoBudget, 0))

	h.at(time.Minute) // the workspace is ready: the first worker launches
	h.wantLaunches(time.Minute, 1)
	h.at(13*time.Minute - time.Second) // its check-in window is still open
	h.wantLaunches(13*time.Minute-time.Second, 1)

	h.at(13 * time.Minute) // one check-in timeout after the launch
	h.wantLaunches(13*time.Minute, 2)
	if h.store.record.StartupAttempts != 1 || h.relaunches() != 1 {
		t.Fatalf("startup attempts = %d, relaunch events = %d; want 1, 1", h.store.record.StartupAttempts, h.relaunches())
	}
	event, _ := h.activity.find(domain.ActivityWorkerRelaunched)
	if event.Attrs["reason"] != "never_checked_in" || event.Attrs["epoch"] != int64(1) {
		t.Fatalf("relaunch event attrs = %v", event.Attrs)
	}

	h.at(25*time.Minute - time.Second) // the relaunched worker's window is still open
	h.wantLaunches(25*time.Minute-time.Second, 2)

	h.at(25 * time.Minute)
	if !h.store.terminated() || len(h.provider.bootstraps) != 2 {
		t.Fatalf("observed %q after %d launches; want parked after the second window, with no third launch",
			h.store.record.ObservedState, len(h.provider.bootstraps))
	}
	if h.provider.stops != 0 {
		t.Fatalf("stops = %d; AO must never stop a BYO workspace on a startup failure", h.provider.stops)
	}
	last := h.store.startupErrors[len(h.store.startupErrors)-1]
	if last.code != sandbox.StartupErrorWorkerNeverStarted ||
		!strings.Contains(last.message, "even after AO relaunched it") ||
		!strings.Contains(last.message, "12 minutes") {
		t.Fatalf("startup error = %+v, want worker_never_started explaining the relaunch", last)
	}
}

// A worker that checks in inside its window is never relaunched: neither one
// whose heartbeat the claimed record already shows, nor one whose check-in
// landed after the row was claimed, nor one still retrying a bootstrap it
// redeemed late.
func TestWorkerCheckingInDuringItsWindowIsNotRelaunched(t *testing.T) {
	t.Run("checked in after the claim", func(t *testing.T) {
		h := newLaunchHarness(t, coderRecord(t, "connection-1", byoBudget, 0))
		h.at(time.Minute)
		h.store.seenFresh = true
		h.at(13 * time.Minute)
		h.wantLaunches(13*time.Minute, 1)
		if h.store.released != 1 || h.store.record.StartupAttempts != 0 {
			t.Fatalf("released = %d, attempts = %d; want the claim released untouched", h.store.released, h.store.record.StartupAttempts)
		}
	})
	t.Run("still retrying a ticket it redeemed", func(t *testing.T) {
		h := newLaunchHarness(t, coderRecord(t, "connection-1", byoBudget, 0))
		h.at(time.Minute)
		// The worker reached the control plane 5 minutes after its launch,
		// lost the response, and may retry for the redemption retry window.
		redeemed := h.start.Add(6 * time.Minute)
		h.store.launches[0].RedeemedAt = &redeemed
		h.at(13 * time.Minute)
		h.wantLaunches(13*time.Minute, 1)
		h.at(16*time.Minute - time.Second)
		h.wantLaunches(16*time.Minute-time.Second, 1)
		h.at(16 * time.Minute)
		h.wantLaunches(16*time.Minute, 2)
	})
}

// A ready check can hold the worker back for most of a long bring-your-own
// startup budget. The worker launched late still gets its whole check-in
// window and a relaunch before the session parks, instead of the ceiling
// parking it a few minutes after launch.
func TestLongBYOReadinessBudgetStillGetsARelaunchBeforeTheCeiling(t *testing.T) {
	h := newLaunchHarness(t, coderRecord(t, "connection-1", map[string]any{"startupTimeoutSeconds": 1500}, 0))
	h.provider.bootstrapErr = fmt.Errorf("coder: workspace ready check has not passed yet: %w", sandbox.ErrWorkspaceNotReady)
	h.at(time.Minute)
	h.at(10 * time.Minute)
	if h.store.record.ObservedState != domain.SandboxObservedProvisioning {
		t.Fatalf("observed %q while the ready check holds, want provisioning", h.store.record.ObservedState)
	}

	h.provider.bootstrapErr = nil
	h.at(16 * time.Minute) // the ready check passes: the worker launches
	if h.store.record.ObservedState != domain.SandboxObservedBootstrapping {
		t.Fatalf("observed %q after the launch, want bootstrapping", h.store.record.ObservedState)
	}
	launches := len(h.provider.bootstraps)

	h.at(25 * time.Minute) // the session's own 25-minute ceiling
	h.wantLaunches(25*time.Minute, launches)
	h.at(28 * time.Minute) // the late launch's check-in window closes
	h.wantLaunches(28*time.Minute, launches+1)
	if h.relaunches() != 1 {
		t.Fatalf("relaunch events = %d, want 1", h.relaunches())
	}
	h.at(40 * time.Minute)
	if !h.store.terminated() {
		t.Fatalf("observed %q, want parked once the relaunched worker's window closed", h.store.record.ObservedState)
	}
}

// Pausing or deleting the session while a worker's check-in window is open
// cancels the relaunch: nothing is relaunched into a session the user is
// stopping, and a paused session is not parked as a startup failure either.
func TestPauseOrDeleteDuringTheWindowCancelsRelaunch(t *testing.T) {
	t.Run("pause", func(t *testing.T) {
		h := newLaunchHarness(t, coderRecord(t, "", nil, 0))
		h.at(time.Minute)
		h.at(13 * time.Minute) // relaunched: the relaunch budget is spent
		h.wantLaunches(13*time.Minute, 2)

		h.store.record.DesiredState = domain.SandboxDesiredPaused
		h.at(26 * time.Minute) // past the relaunched worker's window
		h.wantLaunches(26*time.Minute, 2)
		if h.store.record.ObservedState != domain.SandboxObservedStopped || h.provider.stops != 1 {
			t.Fatalf("observed %q, stops = %d; want the paused session stopped, not parked",
				h.store.record.ObservedState, h.provider.stops)
		}
	})
	t.Run("delete", func(t *testing.T) {
		h := newLaunchHarness(t, coderRecord(t, "", nil, 0))
		h.at(time.Minute)
		h.store.record.DesiredState = domain.SandboxDesiredDeleted
		h.at(13 * time.Minute)
		if len(h.provider.bootstraps) != 1 || h.relaunches() != 0 {
			t.Fatalf("launches = %d, relaunch events = %d; want no relaunch into a deleted session",
				len(h.provider.bootstraps), h.relaunches())
		}
	})
}

// The rule is not specific to bring-your-own Coder. On AO's own Coder and on
// Freestyle, whose startup budget is shorter than the worker's bootstrap
// retries, a launched worker is no longer replaced mid-retry at the startup
// deadline: it gets its check-in window, then one relaunch, then the session
// parks and its compute is stopped.
func TestManagedProvidersRelaunchAfterTheCheckInWindow(t *testing.T) {
	freestyle := domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		ProviderEnvironmentID: "vm-1", DesiredState: domain.SandboxDesiredRunning,
	}
	for name, record := range map[string]domain.Sandbox{
		"managed coder": coderRecord(t, "", nil, 0),
		"freestyle":     freestyle,
	} {
		t.Run(name, func(t *testing.T) {
			h := newLaunchHarness(t, record)
			h.at(time.Minute)
			h.wantLaunches(time.Minute, 1)
			h.at(4 * time.Minute) // past the 3-minute startup budget
			h.wantLaunches(4*time.Minute, 1)
			h.at(13 * time.Minute)
			h.wantLaunches(13*time.Minute, 2)
			if h.relaunches() != 1 {
				t.Fatalf("relaunch events = %d, want 1", h.relaunches())
			}
			h.at(25 * time.Minute)
			if !h.store.terminated() || len(h.provider.bootstraps) != 2 || h.provider.stops != 1 {
				t.Fatalf("observed %q, launches = %d, stops = %d; want parked and stopped after one relaunch",
					h.store.record.ObservedState, len(h.provider.bootstraps), h.provider.stops)
			}
		})
	}
}

// A "workspace not ready" reason recorded while the compute was coming up is
// out of date once a worker was launched into it. Parking after the relaunch
// explains what actually failed: the worker never checked in.
func TestParkAfterRelaunchReplacesStaleNotReadyReason(t *testing.T) {
	h := newLaunchHarness(t, coderRecord(t, "connection-1", byoBudget, 0))
	h.store.record.StartupErrorCode = sandbox.StartupErrorWorkspaceNotReady
	h.at(time.Minute)
	h.at(13 * time.Minute)
	h.at(25 * time.Minute)
	if !h.store.terminated() || len(h.store.startupErrors) == 0 {
		t.Fatalf("observed %q, startup errors = %+v; want parked with a reason", h.store.record.ObservedState, h.store.startupErrors)
	}
	if last := h.store.startupErrors[len(h.store.startupErrors)-1]; last.code != sandbox.StartupErrorWorkerNeverStarted {
		t.Fatalf("startup error = %+v, want worker_never_started to replace workspace_not_ready", last)
	}
}

// A worker that checked in and later went quiet belongs to the heartbeat
// repair, not to the relaunch rule: it is never counted as a startup relaunch.
func TestQuietConnectedWorkerIsLeftToHeartbeatRepair(t *testing.T) {
	h := newLaunchHarness(t, coderRecord(t, "connection-1", byoBudget, 0))
	h.at(time.Minute)
	seen := h.start.Add(2 * time.Minute)
	h.store.record.WorkerLastSeenAt = &seen
	h.store.record.ObservedState = domain.SandboxObservedRunning
	h.at(30 * time.Minute)
	if h.relaunches() != 0 || h.store.record.StartupAttempts != 0 || h.store.terminated() {
		t.Fatalf("relaunch events = %d, attempts = %d, observed %q; want a plain heartbeat repair",
			h.relaunches(), h.store.record.StartupAttempts, h.store.record.ObservedState)
	}
	if _, ok := h.activity.find(domain.ActivityWorkerDisconnected); !ok {
		t.Fatalf("activity = %v, want the heartbeat repair's worker.disconnected", h.activity.kinds())
	}
}
