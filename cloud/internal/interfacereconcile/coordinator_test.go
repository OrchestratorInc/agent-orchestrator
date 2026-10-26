package interfacereconcile

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

func testTransition(phase domain.SessionInterfaceTransitionPhase) postgres.CoordinatedInterfaceTransition {
	return postgres.CoordinatedInterfaceTransition{
		SessionInterfaceTransition: domain.SessionInterfaceTransition{
			ID:                   "transition-1",
			OrgID:                "org-1",
			SessionID:            "session-1",
			SourceInterface:      domain.SessionInterfaceTUI,
			TargetInterface:      domain.SessionInterfaceChat,
			Policy:               domain.SessionInterfaceTransitionDrain,
			Phase:                phase,
			NativeConversationID: "native-1",
		},
		Harness: "claude-code",
	}
}

type fakeStore struct {
	transitions          []postgres.CoordinatedInterfaceTransition
	committed            domain.SessionInterface
	commitCalls          int
	advances             []domain.SessionInterfaceTransitionPhase
	claimErr             error
	renewErr             error
	advanceErr           error
	commitErr            error
	heldMessagesReleased bool
	claimCursor          int
	claimLimits          []int
	terminatedFailed     int
}

func (f *fakeStore) ClaimCoordinatedInterfaceTransitions(_ context.Context, _ string, limit int, _ time.Duration) ([]postgres.CoordinatedInterfaceTransition, error) {
	f.claimLimits = append(f.claimLimits, limit)
	if f.claimErr != nil {
		return nil, f.claimErr
	}
	for offset := range len(f.transitions) {
		index := (f.claimCursor + offset) % len(f.transitions)
		phase := f.transitions[index].Phase
		if phase == domain.SessionInterfaceTransitionCompleted ||
			phase == domain.SessionInterfaceTransitionFailed ||
			phase == domain.SessionInterfaceTransitionCancelled {
			continue
		}
		f.claimCursor = (index + 1) % len(f.transitions)
		return []postgres.CoordinatedInterfaceTransition{f.transitions[index]}, nil
	}
	return nil, nil
}
func (f *fakeStore) RenewCoordinatedInterfaceClaim(ctx context.Context, owner, transitionID string, lease time.Duration) error {
	return f.renewErr
}
func (f *fakeStore) AdvanceCoordinatedInterfaceTransition(ctx context.Context, owner, transitionID string, from, to domain.SessionInterfaceTransitionPhase, nativeConversationID, errorCode, errorDetail string, releaseHeldMessages bool) error {
	if f.advanceErr != nil {
		return f.advanceErr
	}
	if releaseHeldMessages {
		f.heldMessagesReleased = true
	}
	f.advances = append(f.advances, to)
	for index := range f.transitions {
		if f.transitions[index].ID != transitionID {
			continue
		}
		if f.transitions[index].Phase != from {
			return postgres.ErrTransitionStale
		}
		f.transitions[index].Phase = to
		f.transitions[index].ErrorCode = errorCode
		f.transitions[index].ErrorDetail = errorDetail
		if nativeConversationID != "" {
			f.transitions[index].NativeConversationID = nativeConversationID
		}
		break
	}
	return nil
}
func (f *fakeStore) CommitCoordinatedSessionInterface(ctx context.Context, owner, orgID, transitionID string, v domain.SessionInterface) (bool, error) {
	if f.commitErr != nil {
		return false, f.commitErr
	}
	f.committed = v
	f.commitCalls++
	return true, nil
}
func (f *fakeStore) RollbackCoordinatedSessionInterface(context.Context, string, string, string) error {
	f.committed = domain.SessionInterfaceTUI
	return nil
}
func (f *fakeStore) CompleteCoordinatedInterfaceTransition(ctx context.Context, owner, transitionID string) error {
	f.advances = append(f.advances, domain.SessionInterfaceTransitionCompleted)
	f.heldMessagesReleased = true
	for index := range f.transitions {
		if f.transitions[index].ID == transitionID {
			f.transitions[index].Phase = domain.SessionInterfaceTransitionCompleted
		}
	}
	return nil
}
func (f *fakeStore) ReleaseCoordinatedInterfaceClaim(ctx context.Context, owner, transitionID string) error {
	return nil
}
func (f *fakeStore) FailTerminatedInterfaceTransition(_ context.Context, _, transitionID string, from domain.SessionInterfaceTransitionPhase, errorCode, errorDetail string) error {
	f.terminatedFailed++
	for index := range f.transitions {
		if f.transitions[index].ID != transitionID {
			continue
		}
		if f.transitions[index].Phase != from {
			return postgres.ErrTransitionStale
		}
		f.transitions[index].Phase = domain.SessionInterfaceTransitionFailed
		f.transitions[index].ErrorCode = errorCode
		f.transitions[index].ErrorDetail = errorDetail
	}
	return nil
}

type fakeDriver struct {
	Inspection          SourceInspection
	inspectErr          error
	stopErr             error
	stopFailedTargetErr error
	startErr            error
	restoreErr          error
	preflight           error
	interrupt           bool
	nativeID            string
	nativeIDErr         error

	preflightCalls    int
	inspectCalls      int
	interruptCalls    int
	stopCalls         int
	stoppedInterfaces []domain.SessionInterface
	nativeIDCalls     int
	startCalls        int
	startedInterfaces []domain.SessionInterface
	readyCalls        int
	readyErr          error
	readyInterfaces   []domain.SessionInterface

	startedWithNativeID string
}

func (f *fakeDriver) PreflightTarget(context.Context, postgres.CoordinatedInterfaceTransition) error {
	f.preflightCalls++
	return f.preflight
}
func (f *fakeDriver) InspectSource(context.Context, postgres.CoordinatedInterfaceTransition) (SourceInspection, error) {
	f.inspectCalls++
	return f.Inspection, f.inspectErr
}
func (f *fakeDriver) InterruptSource(context.Context, postgres.CoordinatedInterfaceTransition) error {
	f.interrupt = true
	f.interruptCalls++
	return nil
}
func (f *fakeDriver) StopSource(_ context.Context, transition postgres.CoordinatedInterfaceTransition) error {
	f.stopCalls++
	f.stoppedInterfaces = append(f.stoppedInterfaces, transition.SourceInterface)
	return f.stopErr
}
func (f *fakeDriver) StopFailedTarget(_ context.Context, transition postgres.CoordinatedInterfaceTransition) error {
	f.stoppedInterfaces = append(f.stoppedInterfaces, transition.TargetInterface)
	return f.stopFailedTargetErr
}
func (f *fakeDriver) ResolveNativeConversationID(context.Context, postgres.CoordinatedInterfaceTransition) (string, error) {
	f.nativeIDCalls++
	if f.nativeIDErr != nil {
		return "", f.nativeIDErr
	}
	return f.nativeID, nil
}
func (f *fakeDriver) StartTarget(_ context.Context, transition postgres.CoordinatedInterfaceTransition, nativeID string) error {
	f.startCalls++
	f.startedInterfaces = append(f.startedInterfaces, transition.TargetInterface)
	f.startedWithNativeID = nativeID
	if transition.TargetInterface == domain.SessionInterfaceChat {
		return f.startErr
	}
	if f.startCalls > 1 {
		return f.restoreErr
	}
	return nil
}
func (f *fakeDriver) VerifyControllerReady(_ context.Context, transition postgres.CoordinatedInterfaceTransition) error {
	f.readyCalls++
	f.readyInterfaces = append(f.readyInterfaces, transition.TargetInterface)
	return f.readyErr
}

func newCoordinator(store *fakeStore, driver *fakeDriver) *Coordinator {
	return New(store, driver, Options{Interval: time.Millisecond, Logger: slog.New(slog.DiscardHandler)})
}

// fakeRequestStore satisfies the TransportDriver's RequestStore for tests that
// never reach a dispatch (preflight fails before any worker request exists).
type fakeRequestStore struct{}

func (fakeRequestStore) CreateCoordinatedInterfaceRequest(context.Context, string, string, string, json.RawMessage) (domain.WorkerRequest, error) {
	return domain.WorkerRequest{}, errors.New("no requests expected")
}
func (fakeRequestStore) GetCoordinatedInterfaceRequestResult(context.Context, string, string, string) (domain.WorkerRequest, error) {
	return domain.WorkerRequest{}, errors.New("no requests expected")
}

type failedInterruptRequestStore struct{}

func (failedInterruptRequestStore) CreateCoordinatedInterfaceRequest(_ context.Context, _, _, kind string, _ json.RawMessage) (domain.WorkerRequest, error) {
	return domain.WorkerRequest{Kind: kind, Status: "failed", ErrorCode: "INTERRUPT_FAILED"}, nil
}
func (failedInterruptRequestStore) GetCoordinatedInterfaceRequestResult(context.Context, string, string, string) (domain.WorkerRequest, error) {
	return domain.WorkerRequest{}, errors.New("unexpected poll")
}

func TestInterruptSourceWaitsForWorkerResult(t *testing.T) {
	transition := testTransition(domain.SessionInterfaceTransitionDraining)
	driver := NewTransportDriver(failedInterruptRequestStore{}, "owner", time.Second, nil)
	if err := driver.InterruptSource(context.Background(), transition); err == nil {
		t.Fatal("interrupt returned success before the worker reported failure")
	}
}

func TestReconcileHappyPath(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)}}
	driver := &fakeDriver{Inspection: SourceInspection{Idle: true}, nativeID: "native-1"}
	err := newCoordinator(store, driver).ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.committed != domain.SessionInterfaceChat {
		t.Fatalf("expected interface committed to chat, got %q", store.committed)
	}
	last := store.advances[len(store.advances)-1]
	if last != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("expected final phase completed, got %q", last)
	}
}

type busyFirstDriver struct {
	*fakeDriver
	busy bool
}

func (d *busyFirstDriver) InspectSource(_ context.Context, transition postgres.CoordinatedInterfaceTransition) (SourceInspection, error) {
	if transition.ID == "transition-1" && d.busy {
		return SourceInspection{Idle: false}, nil
	}
	return SourceInspection{Idle: true}, nil
}

func TestReconcileBusyDrainYieldsToOtherSession(t *testing.T) {
	busy := testTransition(domain.SessionInterfaceTransitionDraining)
	ready := testTransition(domain.SessionInterfaceTransitionRequested)
	ready.ID, ready.SessionID = "transition-2", "session-2"
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{busy, ready}}
	driver := &busyFirstDriver{fakeDriver: &fakeDriver{nativeID: "native-1"}, busy: true}
	coordinator := New(store, driver, Options{
		Interval: time.Millisecond, MaxPendingRetries: 1, Logger: slog.New(slog.DiscardHandler),
	})
	for range 3 {
		if err := coordinator.ReconcileOnce(context.Background()); err != nil {
			t.Fatalf("reconcile busy source: %v", err)
		}
	}
	if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionDraining {
		t.Fatalf("busy transition phase = %q, want draining", got)
	}
	if got := store.transitions[1].Phase; got != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("independent transition phase = %q, want completed", got)
	}
	if len(coordinator.retries) != 0 {
		t.Fatalf("busy drain counted as failed worker command: %v", coordinator.retries)
	}
	for _, limit := range store.claimLimits {
		if limit != 1 {
			t.Fatalf("claimed %d transitions before processing them, want one at a time", limit)
		}
	}
	driver.busy = false
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile idle source: %v", err)
	}
	if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("formerly busy transition phase = %q, want completed", got)
	}
}

type claimLossDriver struct {
	*fakeDriver
	preflightStarted chan struct{}
}

func (d *claimLossDriver) PreflightTarget(ctx context.Context, _ postgres.CoordinatedInterfaceTransition) error {
	close(d.preflightStarted)
	<-ctx.Done()
	return ctx.Err()
}

func TestReconcileCancelsWorkerStepWhenClaimRenewalFails(t *testing.T) {
	store := &fakeStore{
		transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)},
		renewErr:    postgres.ErrTransitionStale,
	}
	driver := &claimLossDriver{fakeDriver: &fakeDriver{}, preflightStarted: make(chan struct{})}
	coordinator := New(store, driver, Options{Interval: time.Millisecond, Logger: slog.New(slog.DiscardHandler)})
	coordinator.lease = 30 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- coordinator.ReconcileOnce(ctx) }()
	select {
	case <-driver.preflightStarted:
	case <-ctx.Done():
		t.Fatal("preflight did not start")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("lost claim did not cancel the worker step")
	}
	if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionPreflighting {
		t.Fatalf("phase after lost claim = %q, want preflighting", got)
	}
	if driver.stopCalls != 0 || driver.startCalls != 0 {
		t.Fatalf("worker steps continued after lost claim: stops=%d starts=%d", driver.stopCalls, driver.startCalls)
	}
}

func TestReconcileTreatsStaleCommitAsCoordinationLoss(t *testing.T) {
	store := &fakeStore{
		transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionSourceStopped)},
		commitErr:   postgres.ErrTransitionStale,
	}
	driver := &fakeDriver{nativeID: "native-1"}
	if err := newCoordinator(store, driver).ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("stale coordinator should stop without failing the transition: %v", err)
	}
	if store.commitCalls != 0 {
		t.Fatalf("stale commit should not be recorded as committed: %d calls", store.commitCalls)
	}
	if len(store.advances) != 0 {
		t.Fatalf("stale coordinator must not advance the transition: %v", store.advances)
	}
}

// Both handoff directions and every supported harness must converge through
// the same durable phase machine, commit the requested interface, and hand the
// shared native conversation identity to the target controller.
func TestReconcileEverySupportedHarnessBothDirections(t *testing.T) {
	for _, harness := range []string{"codex", "claude-code", "cursor"} {
		for _, direction := range []struct {
			name   string
			source domain.SessionInterface
			target domain.SessionInterface
		}{
			{name: "tui-to-chat", source: domain.SessionInterfaceTUI, target: domain.SessionInterfaceChat},
			{name: "chat-to-tui", source: domain.SessionInterfaceChat, target: domain.SessionInterfaceTUI},
		} {
			t.Run(harness+"/"+direction.name, func(t *testing.T) {
				transition := testTransition(domain.SessionInterfaceTransitionRequested)
				transition.Harness = harness
				transition.SourceInterface = direction.source
				transition.TargetInterface = direction.target
				store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
				driver := &fakeDriver{Inspection: SourceInspection{Idle: true}, nativeID: "native-" + harness}
				err := newCoordinator(store, driver).ReconcileOnce(context.Background())
				if err != nil {
					t.Fatalf("reconcile: %v", err)
				}
				if store.committed != direction.target {
					t.Fatalf("expected interface committed to %s, got %q", direction.target, store.committed)
				}
				if driver.startedWithNativeID != "native-"+harness {
					t.Fatalf("target started with native id %q, want native-%s", driver.startedWithNativeID, harness)
				}
				if last := store.advances[len(store.advances)-1]; last != domain.SessionInterfaceTransitionCompleted {
					t.Fatalf("expected final phase completed, got %q", last)
				}
			})
		}
	}
}

func TestTransportDriverPreflightRejectsUnsupportedHarness(t *testing.T) {
	driver := NewTransportDriver(&fakeRequestStore{}, "owner", time.Millisecond, slog.New(slog.DiscardHandler))
	transition := testTransition(domain.SessionInterfaceTransitionRequested)
	for _, harness := range []string{"codex", "claude-code", "cursor"} {
		transition.Harness = harness
		if err := driver.PreflightTarget(context.Background(), transition); err != nil {
			t.Fatalf("preflight %s: %v", harness, err)
		}
	}
	transition.Harness = "unknown-harness"
	if err := driver.PreflightTarget(context.Background(), transition); err == nil {
		t.Fatal("expected an unsupported harness to fail preflight")
	}
}

func TestReconcileInterruptPolicy(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)}}
	store.transitions[0].Policy = domain.SessionInterfaceTransitionInterrupt
	driver := &fakeDriver{Inspection: SourceInspection{Idle: true}}
	err := newCoordinator(store, driver).ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if !driver.interrupt {
		t.Fatal("expected interrupt to be issued for interrupt policy")
	}
	if store.committed != domain.SessionInterfaceChat {
		t.Fatalf("expected interface committed to chat, got %q", store.committed)
	}
}

func TestReconcileResumesFromDurablePhaseWithoutReplayingCompletedWork(t *testing.T) {
	for _, test := range []struct {
		phase     domain.SessionInterfaceTransitionPhase
		preflight int
		inspect   int
		interrupt int
		stop      int
		nativeID  int
		commit    int
		start     int
	}{
		{phase: domain.SessionInterfaceTransitionRequested, preflight: 1, interrupt: 1, stop: 1, nativeID: 1, commit: 1, start: 1},
		{phase: domain.SessionInterfaceTransitionPreflighting, preflight: 1, interrupt: 1, stop: 1, nativeID: 1, commit: 1, start: 1},
		{phase: domain.SessionInterfaceTransitionDraining, interrupt: 1, stop: 1, nativeID: 1, commit: 1, start: 1},
		{phase: domain.SessionInterfaceTransitionSourceStopping, stop: 1, nativeID: 1, commit: 1, start: 1},
		{phase: domain.SessionInterfaceTransitionSourceStopped, nativeID: 1, commit: 1, start: 1},
		{phase: domain.SessionInterfaceTransitionTargetStarting, start: 1},
		{phase: domain.SessionInterfaceTransitionActivating},
	} {
		t.Run(string(test.phase), func(t *testing.T) {
			transition := testTransition(test.phase)
			transition.Policy = domain.SessionInterfaceTransitionInterrupt
			store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
			driver := &fakeDriver{Inspection: SourceInspection{Idle: true}, nativeID: "native-resumed"}

			if err := newCoordinator(store, driver).ReconcileOnce(context.Background()); err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionCompleted {
				t.Fatalf("phase = %q, want completed", got)
			}
			if driver.preflightCalls != test.preflight || driver.inspectCalls != test.inspect ||
				driver.interruptCalls != test.interrupt || driver.stopCalls != test.stop ||
				driver.nativeIDCalls != test.nativeID || store.commitCalls != test.commit ||
				driver.startCalls != test.start {
				t.Fatalf("calls = preflight:%d inspect:%d interrupt:%d stop:%d native:%d commit:%d start:%d",
					driver.preflightCalls, driver.inspectCalls, driver.interruptCalls,
					driver.stopCalls, driver.nativeIDCalls, store.commitCalls, driver.startCalls)
			}
		})
	}
}

func TestReconcilePendingStopResumesAtSourceStopping(t *testing.T) {
	transition := testTransition(domain.SessionInterfaceTransitionSourceStopping)
	transition.Policy = domain.SessionInterfaceTransitionInterrupt
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
	driver := &fakeDriver{
		Inspection: SourceInspection{Idle: true},
		nativeID:   "native-resumed",
		stopErr:    errPendingWorkerCommand,
	}
	coordinator := newCoordinator(store, driver)
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionSourceStopping {
		t.Fatalf("phase after pending stop = %q, want source_stopping", got)
	}
	driver.stopErr = nil
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if driver.inspectCalls != 0 || driver.interruptCalls != 0 {
		t.Fatalf("completed drain was replayed: inspect=%d interrupt=%d", driver.inspectCalls, driver.interruptCalls)
	}
	if driver.stopCalls != 2 || driver.nativeIDCalls != 1 || store.commitCalls != 1 || driver.startCalls != 1 {
		t.Fatalf("calls = stop:%d native:%d commit:%d start:%d", driver.stopCalls, driver.nativeIDCalls, store.commitCalls, driver.startCalls)
	}
}

func TestReconcileDrainWaitsForSourceToBecomeIdle(t *testing.T) {
	for name, inspection := range map[string]SourceInspection{
		"decision pending":      {DecisionPending: true},
		"draft present":         {DraftPresent: true},
		"quiescence unverified": {QuiescenceUnverified: true},
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)}}
			driver := &fakeDriver{Inspection: inspection}
			coordinator := newCoordinator(store, driver)
			if err := coordinator.ReconcileOnce(context.Background()); err != nil {
				t.Fatalf("reconcile while source is active: %v", err)
			}
			if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionDraining {
				t.Fatalf("phase while source is active = %q, want draining", got)
			}
			if store.committed != "" || store.heldMessagesReleased {
				t.Fatalf("source was changed while waiting: committed=%q released=%t", store.committed, store.heldMessagesReleased)
			}
			driver.Inspection = SourceInspection{Idle: true}
			if err := coordinator.ReconcileOnce(context.Background()); err != nil {
				t.Fatalf("reconcile after source becomes idle: %v", err)
			}
			if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionCompleted {
				t.Fatalf("phase after source becomes idle = %q, want completed", got)
			}
		})
	}
}

func TestReconcileTargetStartFailureRestoresSourceTerminal(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)}}
	driver := &fakeDriver{Inspection: SourceInspection{Idle: true}, startErr: errors.New("harness unavailable")}
	coordinator := newCoordinator(store, driver)
	err := coordinator.ReconcileOnce(context.Background())
	if err != nil && !errors.Is(err, errCoordinationLost) {
		t.Fatalf("unexpected reconcile error: %v", err)
	}
	if store.committed != domain.SessionInterfaceChat || store.transitions[0].Phase != domain.SessionInterfaceTransitionRecovery {
		t.Fatalf("target-start failure did not fence recovery: interface=%q phase=%q", store.committed, store.transitions[0].Phase)
	}
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("restore source: %v", err)
	}
	if store.committed != domain.SessionInterfaceTUI {
		t.Fatalf("source terminal was not restored after target failure: %q", store.committed)
	}
	last := store.advances[len(store.advances)-1]
	if last != domain.SessionInterfaceTransitionFailed {
		t.Fatalf("expected failed transition after source restoration, got %q", last)
	}
	if !store.heldMessagesReleased {
		t.Fatal("held messages were not released to the restored terminal")
	}
	if len(driver.stoppedInterfaces) != 2 || driver.stoppedInterfaces[0] != domain.SessionInterfaceTUI || driver.stoppedInterfaces[1] != domain.SessionInterfaceChat {
		t.Fatalf("controller stop order = %v, want tui then chat", driver.stoppedInterfaces)
	}
	if len(driver.startedInterfaces) != 2 || driver.startedInterfaces[0] != domain.SessionInterfaceChat || driver.startedInterfaces[1] != domain.SessionInterfaceTUI {
		t.Fatalf("controller start order = %v, want chat then tui", driver.startedInterfaces)
	}
}

func TestReconcileTargetStartFailureKeepsWorkFencedUntilTargetStops(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)}}
	driver := &fakeDriver{
		Inspection:          SourceInspection{Idle: true},
		startErr:            errors.New("chat failed to start"),
		stopFailedTargetErr: errors.New("target stop unconfirmed"),
	}
	coordinator := newCoordinator(store, driver)
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.committed != domain.SessionInterfaceChat || store.transitions[0].Phase != domain.SessionInterfaceTransitionRecovery {
		t.Fatalf("unsafe rollback despite uncertain target stop: interface=%q phase=%q", store.committed, store.transitions[0].Phase)
	}
	if store.heldMessagesReleased {
		t.Fatal("released held messages before the failed target stopped")
	}
}

func TestReconcileTargetStartFailureKeepsWorkFencedUntilSourceRestarts(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)}}
	driver := &fakeDriver{
		Inspection: SourceInspection{Idle: true},
		startErr:   errors.New("chat failed to start"),
		restoreErr: errors.New("terminal failed to restart"),
	}
	coordinator := newCoordinator(store, driver)
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.transitions[0].Phase != domain.SessionInterfaceTransitionRecovery || store.heldMessagesReleased {
		t.Fatalf("source restart failure released work: phase=%q released=%t", store.transitions[0].Phase, store.heldMessagesReleased)
	}
	driver.restoreErr = nil
	// The failed restart backs off before the next recovery attempt.
	coordinator.now = func() time.Time { return time.Now().Add(time.Minute) }
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.transitions[0].Phase != domain.SessionInterfaceTransitionFailed || !store.heldMessagesReleased {
		t.Fatalf("source recovery did not complete: phase=%q released=%t", store.transitions[0].Phase, store.heldMessagesReleased)
	}
}

func TestReconcilePreflightFailure(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)}}
	driver := &fakeDriver{preflight: errors.New("chat unsupported")}
	err := newCoordinator(store, driver).ReconcileOnce(context.Background())
	if err != nil {
		t.Fatalf("expected preflight failure surfaced as recovered failure, got err: %v", err)
	}
	if store.committed != "" {
		t.Fatalf("no session interface should be committed on preflight failure, got %q", store.committed)
	}
	if !store.heldMessagesReleased {
		t.Fatal("expected prompts held during preflight failure to be released atomically")
	}
}

func TestReconcileSourceStopFailureRestoresUncommittedSource(t *testing.T) {
	transition := testTransition(domain.SessionInterfaceTransitionRecovery)
	transition.SourceInterface = domain.SessionInterfaceChat
	transition.TargetInterface = domain.SessionInterfaceTUI
	transition.ErrorCode = "SOURCE_STOP_FAILED"
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}, committed: domain.SessionInterfaceChat}
	driver := &fakeDriver{}
	if err := newCoordinator(store, driver).ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if store.committed != domain.SessionInterfaceChat || store.transitions[0].Phase != domain.SessionInterfaceTransitionFailed {
		t.Fatalf("source recovery did not fail the handoff: interface=%q phase=%q", store.committed, store.transitions[0].Phase)
	}
	if !store.heldMessagesReleased || store.transitions[0].ErrorCode != "SOURCE_STOP_FAILED" {
		t.Fatalf("held messages or failure reason lost: released=%t code=%q", store.heldMessagesReleased, store.transitions[0].ErrorCode)
	}
	if len(driver.startedInterfaces) != 0 || driver.readyCalls != 1 {
		t.Fatalf("ready source was restarted or not verified: started=%v ready=%d", driver.startedInterfaces, driver.readyCalls)
	}
}

func TestReconcileRecoveryReleasesMessagesOnlyAfterControllerReady(t *testing.T) {
	transition := testTransition(domain.SessionInterfaceTransitionRecovery)
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
	driver := &fakeDriver{readyErr: errors.New("replacement controller is still starting")}
	if err := newCoordinator(store, driver).ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile should leave recovery pending: %v", err)
	}
	if store.transitions[0].Phase != domain.SessionInterfaceTransitionRecovery {
		t.Fatalf("phase = %q, want recovery_required while controller is not ready", store.transitions[0].Phase)
	}
	if store.heldMessagesReleased {
		t.Fatal("must not release prompts before controller readiness proof")
	}

	driver.readyErr = nil
	if err := newCoordinator(store, driver).ReconcileOnce(context.Background()); err != nil {
		t.Fatalf("reconcile ready recovery: %v", err)
	}
	if store.transitions[0].Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %q, want completed after readiness proof", store.transitions[0].Phase)
	}
	if !store.heldMessagesReleased {
		t.Fatal("expected recovery to release held prompts atomically")
	}
	if driver.readyCalls != 2 {
		t.Fatalf("readiness calls = %d, want 2", driver.readyCalls)
	}
}

func TestReconcilePendingWorkerCommandRecovers(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRequested)}}
	driver := &fakeDriver{
		Inspection: SourceInspection{Idle: true},
		// The worker never completes the native-conversation-id resolver, so
		// every run reports a pending retryable command before commit.
		nativeIDErr: errPendingWorkerCommand,
	}
	coordinator := newCoordinator(store, driver)
	for attempt := 0; attempt < defaultMaxRetries+2; attempt++ {
		_ = coordinator.ReconcileOnce(context.Background())
	}
	// The pending command is resolved after the source has stopped but before the
	// session interface is committed. The shared failure policy therefore keeps
	// the transition fenced for recovery rather than claiming the source failed
	// cleanly.
	if store.committed != "" {
		t.Fatalf("expected no commit while native id is pending, got %q", store.committed)
	}
	for _, phase := range store.advances {
		if phase == domain.SessionInterfaceTransitionRecovery {
			return
		}
	}
	t.Fatalf("expected terminal phase recovery_required after pending retries, got %v", store.advances)
}

func TestReconcileDrainFailsWhenIdleSourceNeverProvesQuiescence(t *testing.T) {
	for name, transition := range map[string]postgres.CoordinatedInterfaceTransition{
		"provider idle": func() postgres.CoordinatedInterfaceTransition {
			transition := testTransition(domain.SessionInterfaceTransitionRequested)
			transition.SessionActivityState = "idle"
			return transition
		}(),
		// AO typed a message (an API send) that no provider hook confirmed,
		// for example because a terminal dialog swallowed it.
		"unconfirmed typed message": func() postgres.CoordinatedInterfaceTransition {
			transition := testTransition(domain.SessionInterfaceTransitionRequested)
			transition.SessionActivityState = "active"
			transition.SessionActivityUnconfirmed = true
			return transition
		}(),
	} {
		t.Run(name, func(t *testing.T) {
			store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
			driver := &fakeDriver{Inspection: SourceInspection{QuiescenceUnverified: true}}
			coordinator := newCoordinator(store, driver)
			clock := time.Date(2026, 10, 18, 18, 24, 54, 0, time.UTC)
			coordinator.now = func() time.Time { return clock }

			if err := coordinator.ReconcileOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			clock = clock.Add(defaultUnverifiedIdleLimit - time.Second)
			if err := coordinator.ReconcileOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionDraining {
				t.Fatalf("phase inside the proof window = %q, want draining", got)
			}
			clock = clock.Add(time.Second)
			if err := coordinator.ReconcileOnce(context.Background()); err != nil {
				t.Fatal(err)
			}
			got := store.transitions[0]
			if got.Phase != domain.SessionInterfaceTransitionFailed || got.ErrorCode != "SOURCE_DRAIN_FAILED" {
				t.Fatalf("transition = %q/%q, want failed/SOURCE_DRAIN_FAILED", got.Phase, got.ErrorCode)
			}
			if !strings.HasPrefix(got.ErrorDetail, "source controller activity cannot be verified;") {
				t.Fatalf("error detail = %q, want the unverified-source prefix the desktop recognizes", got.ErrorDetail)
			}
			if driver.stopCalls != 0 || driver.interruptCalls != 0 || store.committed != "" {
				t.Fatalf("source was touched: stop=%d interrupt=%d committed=%q", driver.stopCalls, driver.interruptCalls, store.committed)
			}
			if !store.heldMessagesReleased {
				t.Fatal("held messages stay fenced after the source was left running")
			}
		})
	}
}

func TestReconcileDrainWaitsWithoutLimitWhileProviderReportsWork(t *testing.T) {
	for _, state := range []string{"active", "waiting_input", "blocked", ""} {
		t.Run(state, func(t *testing.T) {
			transition := testTransition(domain.SessionInterfaceTransitionRequested)
			transition.SessionActivityState = state
			store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
			driver := &fakeDriver{Inspection: SourceInspection{QuiescenceUnverified: true}}
			coordinator := newCoordinator(store, driver)
			clock := time.Date(2026, 10, 18, 18, 0, 0, 0, time.UTC)
			coordinator.now = func() time.Time { return clock }
			for range 5 {
				if err := coordinator.ReconcileOnce(context.Background()); err != nil {
					t.Fatal(err)
				}
				clock = clock.Add(time.Hour)
			}
			if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionDraining {
				t.Fatalf("phase while the provider reports work = %q, want draining", got)
			}
		})
	}
}

func TestReconcileDrainRestartsProofWindowWhenATurnStarts(t *testing.T) {
	transition := testTransition(domain.SessionInterfaceTransitionRequested)
	transition.SessionActivityState = "idle"
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
	driver := &fakeDriver{Inspection: SourceInspection{QuiescenceUnverified: true}}
	coordinator := newCoordinator(store, driver)
	clock := time.Date(2026, 10, 18, 18, 0, 0, 0, time.UTC)
	coordinator.now = func() time.Time { return clock }
	step := func(state string, advance time.Duration) {
		t.Helper()
		store.transitions[0].SessionActivityState = state
		clock = clock.Add(advance)
		if err := coordinator.ReconcileOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	step("idle", 0)
	step("active", 20*time.Second) // a prompt was submitted
	step("idle", 20*time.Second)   // its Stop has not reached the worker yet
	step("idle", defaultUnverifiedIdleLimit-time.Second)
	if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionDraining {
		t.Fatalf("phase after the turn restarted the window = %q, want draining", got)
	}
	driver.Inspection = SourceInspection{Idle: true}
	step("idle", time.Second)
	if got := store.transitions[0].Phase; got != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase once the source proves idle = %q, want completed", got)
	}
}

// A handoff on a deleted session can never get a worker again. It used to
// stay recovery_required and ask for one on every tick forever (prod: seven
// such sessions logged "dispatch interface.ready: worker unavailable" about
// eight times a second for two weeks). It now ends as failed without touching
// the worker, and its held prompts end as failed turns that say they were not
// sent, never queued ones.
func TestReconcileEndsHandoffOfDeletedSession(t *testing.T) {
	for _, phase := range []domain.SessionInterfaceTransitionPhase{
		domain.SessionInterfaceTransitionRecovery,
		domain.SessionInterfaceTransitionDraining,
	} {
		transition := testTransition(phase)
		transition.Policy = domain.SessionInterfaceTransitionInterrupt
		transition.ErrorCode = "SOURCE_DRAIN_FAILED"
		transition.ErrorDetail = "worker interface command interface.interrupt failed: boom"
		transition.SessionTerminated = true
		store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
		driver := &fakeDriver{readyErr: errors.New("dispatch interface.ready: worker unavailable")}
		coordinator := newCoordinator(store, driver)
		if err := coordinator.ReconcileOnce(context.Background()); err != nil {
			t.Fatalf("%s: %v", phase, err)
		}
		got := store.transitions[0]
		if got.Phase != domain.SessionInterfaceTransitionFailed || got.ErrorCode != SessionTerminatedCode {
			t.Fatalf("%s: phase=%q code=%q, want failed/%s", phase, got.Phase, got.ErrorCode, SessionTerminatedCode)
		}
		if !strings.Contains(got.ErrorDetail, "deleted") || !strings.Contains(got.ErrorDetail, "SOURCE_DRAIN_FAILED") {
			t.Fatalf("%s: detail %q does not explain the deletion and keep the earlier failure", phase, got.ErrorDetail)
		}
		if store.heldMessagesReleased || store.terminatedFailed != 1 {
			t.Fatalf("%s: held prompts must end as not sent (released=%t, failed=%d)", phase, store.heldMessagesReleased, store.terminatedFailed)
		}
		if driver.readyCalls+driver.interruptCalls+driver.inspectCalls+driver.startCalls != 0 {
			t.Fatalf("%s: a deleted session's worker was asked to act: %+v", phase, driver)
		}
		claims := len(store.claimLimits)
		if err := coordinator.ReconcileOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if driver.readyCalls != 0 || len(store.claimLimits) != claims+1 {
			t.Fatalf("%s: ended handoff was claimed again", phase)
		}
	}
}

// A recovery whose worker cannot prove a controller (no worker connected, as
// for a paused sandbox) stays fenced but is retried with a doubling backoff,
// not on every tick, and completes once the controller is ready.
func TestReconcileRecoveryBacksOffWhileControllerUnavailable(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRecovery)}}
	driver := &fakeDriver{readyErr: errors.New("dispatch interface.ready: worker unavailable")}
	coordinator := New(store, driver, Options{
		Interval: time.Millisecond, Logger: slog.New(slog.DiscardHandler),
		RecoveryRetryBase: 2 * time.Second, RecoveryRetryMax: 8 * time.Second,
	})
	now := time.Unix(1_800_000_000, 0)
	coordinator.now = func() time.Time { return now }
	pass := func(advance time.Duration) {
		t.Helper()
		now = now.Add(advance)
		if err := coordinator.ReconcileOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	// Attempts at 0s, 2s, 6s (+4s), 14s (+8s), 22s (+8s, capped).
	for _, step := range []struct {
		advance time.Duration
		calls   int
	}{
		{0, 1}, {time.Second, 1}, {time.Second, 2}, {3 * time.Second, 2}, {time.Second, 3},
		{7 * time.Second, 3}, {time.Second, 4}, {7 * time.Second, 4}, {time.Second, 5},
	} {
		pass(step.advance)
		if driver.readyCalls != step.calls {
			t.Fatalf("after %s: readiness calls = %d, want %d", now.Sub(time.Unix(1_800_000_000, 0)), driver.readyCalls, step.calls)
		}
	}
	if store.transitions[0].Phase != domain.SessionInterfaceTransitionRecovery || store.heldMessagesReleased {
		t.Fatalf("recovery left its fence without proof: phase=%q released=%t", store.transitions[0].Phase, store.heldMessagesReleased)
	}
	driver.readyErr = nil
	pass(8 * time.Second)
	if store.transitions[0].Phase != domain.SessionInterfaceTransitionCompleted || !store.heldMessagesReleased {
		t.Fatalf("ready recovery did not complete: phase=%q released=%t", store.transitions[0].Phase, store.heldMessagesReleased)
	}
	if len(coordinator.recoveryRetries) != 0 {
		t.Fatalf("backoff kept after the recovery completed: %v", coordinator.recoveryRetries)
	}
}

// Recoveries waiting out their backoff do not take MaxConcurrent slots, so a
// new switch on another session still runs in the next pass.
func TestReconcileBackedOffRecoveriesDoNotDelayOtherHandoffs(t *testing.T) {
	var transitions []postgres.CoordinatedInterfaceTransition
	for index := range defaultMaxConcurrent {
		transition := testTransition(domain.SessionInterfaceTransitionRecovery)
		transition.ID = "recovery-" + string(rune('a'+index))
		transition.SessionID = "recovery-session-" + string(rune('a'+index))
		transitions = append(transitions, transition)
	}
	fresh := testTransition(domain.SessionInterfaceTransitionRequested)
	fresh.ID, fresh.SessionID = "fresh", "fresh-session"
	store := &fakeStore{transitions: append(transitions, fresh)}
	driver := &fakeDriver{Inspection: SourceInspection{Idle: true}, readyErr: errors.New("dispatch interface.ready: worker unavailable")}
	coordinator := newCoordinator(store, driver)
	// The first pass spends every slot on the recoveries, which back off.
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if driver.readyCalls != defaultMaxConcurrent || store.transitions[len(transitions)].Phase != domain.SessionInterfaceTransitionRequested {
		t.Fatalf("first pass: ready=%d fresh=%q", driver.readyCalls, store.transitions[len(transitions)].Phase)
	}
	if err := coordinator.ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := store.transitions[len(transitions)].Phase; got != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("fresh handoff phase = %q, want completed while the recoveries back off", got)
	}
	if driver.readyCalls != defaultMaxConcurrent {
		t.Fatalf("backed-off recoveries were retried early: ready=%d", driver.readyCalls)
	}
}

// A drain failure is recovered before the session row is committed to the
// target, so recovery restores and verifies the source controller (the one
// the session row still names), never the target that was never started.
func TestReconcileDrainFailureRecoveryVerifiesUncommittedSource(t *testing.T) {
	transition := testTransition(domain.SessionInterfaceTransitionRecovery)
	transition.Policy = domain.SessionInterfaceTransitionInterrupt
	transition.ErrorCode = "SOURCE_DRAIN_FAILED"
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{transition}}
	driver := &fakeDriver{}
	if err := newCoordinator(store, driver).ReconcileOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(driver.readyInterfaces) != 1 || driver.readyInterfaces[0] != domain.SessionInterfaceTUI {
		t.Fatalf("verified interfaces = %v, want the source (tui)", driver.readyInterfaces)
	}
	got := store.transitions[0]
	if got.Phase != domain.SessionInterfaceTransitionFailed || got.ErrorCode != "SOURCE_DRAIN_FAILED" || !store.heldMessagesReleased {
		t.Fatalf("drain recovery: phase=%q code=%q released=%t, want failed/SOURCE_DRAIN_FAILED/released",
			got.Phase, got.ErrorCode, store.heldMessagesReleased)
	}
	if store.committed != "" {
		t.Fatalf("drain recovery committed an interface: %q", store.committed)
	}
}

// The backoff is capped at about 30 seconds by default: a recovery holds the
// session's prompts, so a worker back with the same epoch waits at most that.
func TestRecoveryBackoffDefaultCapIsShort(t *testing.T) {
	coordinator := New(&fakeStore{}, &fakeDriver{}, Options{})
	if got := coordinator.options.RecoveryRetryMax; got > 30*time.Second || got < coordinator.options.RecoveryRetryBase {
		t.Fatalf("default recovery backoff cap = %s, want at most 30s", got)
	}
}

// A recovery backing off tries again at once when the session's live worker
// epoch changes (a new worker connected), and a slow but answering worker
// (a pending command) is not backed off at all.
func TestReconcileRecoveryRetriesAtOnceForANewWorkerAndNeverBacksOffAPendingOne(t *testing.T) {
	store := &fakeStore{transitions: []postgres.CoordinatedInterfaceTransition{testTransition(domain.SessionInterfaceTransitionRecovery)}}
	driver := &fakeDriver{readyErr: errors.New("dispatch interface.ready: worker unavailable")}
	coordinator := New(store, driver, Options{
		Interval: time.Millisecond, Logger: slog.New(slog.DiscardHandler),
		RecoveryRetryBase: 10 * time.Second, RecoveryRetryMax: 30 * time.Second,
	})
	now := time.Unix(1_800_000_000, 0)
	coordinator.now = func() time.Time { return now }
	pass := func(advance time.Duration, calls int, what string) {
		t.Helper()
		now = now.Add(advance)
		if err := coordinator.ReconcileOnce(context.Background()); err != nil {
			t.Fatal(err)
		}
		if driver.readyCalls != calls {
			t.Fatalf("%s: readiness calls = %d, want %d", what, driver.readyCalls, calls)
		}
	}
	pass(0, 1, "first attempt, no worker")
	pass(time.Second, 1, "backing off")
	store.transitions[0].LiveWorkerEpoch = 42
	pass(time.Second, 2, "a new worker connected")
	// Its failure starts the backoff over from the base.
	pass(9*time.Second, 2, "backing off again from the base")
	pass(time.Second, 3, "base backoff over")

	driver.readyErr = errPendingWorkerCommand
	pass(25*time.Second, 4, "attempt after the doubled backoff")
	pass(500*time.Millisecond, 5, "pending command retried on the next tick")
	pass(500*time.Millisecond, 6, "still pending, still not backed off")
	if _, backedOff := coordinator.recoveryRetries[store.transitions[0].ID]; backedOff {
		t.Fatal("a pending worker command left a backoff")
	}
	driver.readyErr = nil
	pass(500*time.Millisecond, 7, "controller ready")
	if store.transitions[0].Phase != domain.SessionInterfaceTransitionCompleted {
		t.Fatalf("phase = %q, want completed", store.transitions[0].Phase)
	}
}
