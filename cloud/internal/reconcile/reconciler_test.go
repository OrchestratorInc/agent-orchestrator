package reconcile

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

type lifecycleStore struct {
	acceptedPause bool
	acceptCalls   int
	observations  []string
	lastErrors    []string
	failures      []string
	startupErrors []startupErrorRecord
	repairs       int
}

type startupErrorRecord struct{ code, message string }

func (s *lifecycleStore) ClaimSandboxes(context.Context, string, int, time.Duration) ([]domain.Sandbox, error) {
	return nil, nil
}
func (s *lifecycleStore) RenewSandboxClaim(context.Context, string, string, string, time.Duration) error {
	return nil
}
func (s *lifecycleStore) UpdateSandboxObservation(_ context.Context, _, _, _, _, state, lastError string, _ time.Time) error {
	s.observations = append(s.observations, state)
	s.lastErrors = append(s.lastErrors, lastError)
	return nil
}
func (s *lifecycleStore) AcceptSandboxProviderPause(context.Context, string, string, string, string, time.Time) (bool, error) {
	s.acceptCalls++
	return s.acceptedPause, nil
}
func (s *lifecycleStore) RecordSandboxFailure(_ context.Context, _, _, _, _, lastError string) error {
	s.failures = append(s.failures, lastError)
	return nil
}
func (s *lifecycleStore) ReleaseSandboxClaim(context.Context, string, string, string, time.Time) error {
	return nil
}
func (s *lifecycleStore) IssueAccessTicket(context.Context, string, string, string, []string, time.Duration) (string, error) {
	return "ticket", nil
}
func (s *lifecycleStore) AppendSessionEvent(context.Context, string, string, string, json.RawMessage) (domain.ClientEvent, error) {
	return domain.ClientEvent{}, nil
}
func (s *lifecycleStore) MarkSandboxDeletionRequested(context.Context, string, string, string) error {
	return nil
}
func (s *lifecycleStore) CompleteSandboxDeletion(context.Context, string, string, string) error {
	return nil
}
func (s *lifecycleStore) DisconnectSessionWorkers(context.Context, string, string) error {
	return nil
}
func (s *lifecycleStore) RecordSandboxStartupRepair(context.Context, string, string, string) (int, error) {
	s.repairs++
	return s.repairs, nil
}
func (s *lifecycleStore) RecordSandboxStartupError(_ context.Context, _, _, _, code, message string) error {
	s.startupErrors = append(s.startupErrors, startupErrorRecord{code: code, message: message})
	return nil
}

type lifecycleProvider struct {
	environment sandbox.Environment
	starts      int
	extensions  []time.Time
}

func (p *lifecycleProvider) Create(context.Context, sandbox.Spec) (sandbox.Environment, error) {
	return p.environment, nil
}
func (p *lifecycleProvider) Get(context.Context, sandbox.ID) (sandbox.Environment, error) {
	return p.environment, nil
}
func (p *lifecycleProvider) FindBySession(context.Context, string) (sandbox.Environment, bool, error) {
	return p.environment, true, nil
}
func (p *lifecycleProvider) Start(context.Context, sandbox.ID) error {
	p.starts++
	return nil
}
func (p *lifecycleProvider) Stop(context.Context, sandbox.ID) error   { return nil }
func (p *lifecycleProvider) Pause(context.Context, sandbox.ID) error  { return nil }
func (p *lifecycleProvider) Resume(context.Context, sandbox.ID) error { return nil }
func (p *lifecycleProvider) Delete(context.Context, sandbox.ID) error { return nil }
func (p *lifecycleProvider) ExtendDeadline(_ context.Context, _ sandbox.ID, deadline time.Time) error {
	p.extensions = append(p.extensions, deadline)
	return nil
}

type lifecycleResolver struct{ provider sandbox.Provider }

func (r lifecycleResolver) Resolve(context.Context, domain.Sandbox) (sandbox.Provider, error) {
	return r.provider, nil
}

func testReconciler(store Store, provider sandbox.Provider) *Reconciler {
	return New(store, lifecycleResolver{provider: provider}, Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func runningRecord(keepAlive bool) domain.Sandbox {
	now := time.Now()
	return domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: "coder",
		ProviderEnvironmentID: "workspace-1",
		DesiredState:          domain.SandboxDesiredRunning,
		ObservedState:         domain.SandboxObservedRunning,
		WorkerLastSeenAt:      &now,
		KeepAlive:             keepAlive,
		UpdatedAt:             now,
	}
}

func TestCoderAutostopBecomesPausedWithoutRestart(t *testing.T) {
	store := &lifecycleStore{acceptedPause: true}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateStopped, StopCause: sandbox.StopCauseExternalIdle,
	}}
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), runningRecord(false)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.acceptCalls != 1 || provider.starts != 0 {
		t.Fatalf("accept calls = %d, starts = %d; want 1, 0", store.acceptCalls, provider.starts)
	}
}

func TestActiveWorkRestoresStoppedCoderWorkspace(t *testing.T) {
	store := &lifecycleStore{acceptedPause: true}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateStopped, StopCause: sandbox.StopCauseExternalIdle,
	}}
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), runningRecord(true)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.acceptCalls != 0 || provider.starts != 1 {
		t.Fatalf("accept calls = %d, starts = %d; want 0, 1", store.acceptCalls, provider.starts)
	}
	if len(store.observations) != 1 || store.observations[0] != domain.SandboxObservedRestoring {
		t.Fatalf("observations = %v, want restoring", store.observations)
	}
}

func TestStartingUpCoderWorkspaceIgnoresIdleStop(t *testing.T) {
	// A user resume is in flight: the short interaction lease has already lapsed
	// (KeepAlive false), but startup_started_at is recent, so the box is still
	// coming up. A provider idle-stop in this window must be refused and the box
	// restored, not accepted as a pause — otherwise the resume flips back to
	// "resuming" as the terminal is about to appear.
	store := &lifecycleStore{acceptedPause: true}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateStopped, StopCause: sandbox.StopCauseExternalIdle,
	}}
	record := runningRecord(false)
	record.ObservedState = domain.SandboxObservedRestoring
	startedAt := time.Now()
	record.StartupStartedAt = &startedAt
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.acceptCalls != 0 || provider.starts != 1 {
		t.Fatalf("accept calls = %d, starts = %d; want 0, 1", store.acceptCalls, provider.starts)
	}
	if len(store.observations) != 1 || store.observations[0] != domain.SandboxObservedRestoring {
		t.Fatalf("observations = %v, want restoring", store.observations)
	}
}

func TestStaleStartupCoderWorkspaceAcceptsIdleStop(t *testing.T) {
	// A bring-up that never converged has aged past the startup window. The idle
	// guard must no longer hold the box awake: accept the provider pause so
	// compute (and billing) stops instead of looping restores forever.
	store := &lifecycleStore{acceptedPause: true}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateStopped, StopCause: sandbox.StopCauseExternalIdle,
	}}
	record := runningRecord(false)
	stale := time.Now().Add(-2 * DefaultStartupTimeout)
	record.StartupStartedAt = &stale
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.acceptCalls != 1 || provider.starts != 0 {
		t.Fatalf("accept calls = %d, starts = %d; want 1, 0", store.acceptCalls, provider.starts)
	}
}

func TestAmbiguousProviderStopPreservesExistingRestoreBehavior(t *testing.T) {
	store := &lifecycleStore{acceptedPause: true}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateStopped,
	}}
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), runningRecord(false)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.acceptCalls != 0 || provider.starts != 1 {
		t.Fatalf("accept calls = %d, starts = %d; want 0, 1", store.acceptCalls, provider.starts)
	}
}

func TestActiveWorkExtendsNearCoderDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	store := &lifecycleStore{}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateRunning, Deadline: &deadline,
	}}
	started := time.Now()
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), runningRecord(true)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(provider.extensions) != 1 {
		t.Fatalf("extensions = %v, want one", provider.extensions)
	}
	if provider.extensions[0].Before(started.Add(activeDeadlineExtension - time.Second)) {
		t.Fatalf("extended deadline = %s, want about %s from now", provider.extensions[0], activeDeadlineExtension)
	}
	if provider.extensions[0].After(started.Add(activeDeadlineExtension + time.Second)) {
		t.Fatalf("extended deadline = %s, want provider-neutral request about %s from now", provider.extensions[0], activeDeadlineExtension)
	}
}

func TestIdleWorkDoesNotExtendCoderDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	store := &lifecycleStore{}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateRunning, Deadline: &deadline,
	}}
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), runningRecord(false)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(provider.extensions) != 0 {
		t.Fatalf("extensions = %v, want none", provider.extensions)
	}
}

func testReconcilerKeepWarm(store Store, provider sandbox.Provider) *Reconciler {
	return New(store, lifecycleResolver{provider: provider}, Options{
		KeepWarm: true,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// With keep-warm on, an idle session (no active turn, KeepAlive false) must still
// get its Coder deadline extended so the workspace never auto-stops for idleness —
// the opposite of TestIdleWorkDoesNotExtendCoderDeadline above.
func TestKeepWarmExtendsIdleCoderDeadline(t *testing.T) {
	deadline := time.Now().Add(time.Minute)
	store := &lifecycleStore{}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateRunning, Deadline: &deadline,
	}}
	if err := testReconcilerKeepWarm(store, provider).reconcileSandbox(context.Background(), runningRecord(false)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(provider.extensions) != 1 {
		t.Fatalf("extensions = %v, want one (keep-warm extends even when idle)", provider.extensions)
	}
}

// With keep-warm on, a provider external-idle stop must be refused and the box
// restored — an idle cloud session stays up like a local one — the opposite of
// TestCoderAutostopBecomesPausedWithoutRestart above.
func TestKeepWarmRestoresIdleStoppedCoderWorkspace(t *testing.T) {
	store := &lifecycleStore{acceptedPause: true}
	provider := &lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateStopped, StopCause: sandbox.StopCauseExternalIdle,
	}}
	if err := testReconcilerKeepWarm(store, provider).reconcileSandbox(context.Background(), runningRecord(false)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if store.acceptCalls != 0 || provider.starts != 1 {
		t.Fatalf("accept calls = %d, starts = %d; want 0, 1 (keep-warm restores, never accepts idle stop)", store.acceptCalls, provider.starts)
	}
}

type workerSpecStore struct {
	Store
	issued int
}

func (s *workerSpecStore) IssueAccessTicket(
	context.Context, string, string, string, []string, time.Duration,
) (string, error) {
	s.issued++
	return "bootstrap-ticket", nil
}

func TestWorkerSpecUsesPersistedCoderWorkspaceLayout(t *testing.T) {
	t.Parallel()
	store := &workerSpecStore{}
	reconciler := New(store, nil, Options{PublicURL: "https://cloud.example.com"})
	profile := json.RawMessage(`{"coder":{"baseUrl":"https://coder.example.com","owner":"planned-owner","templateId":"2a2e262c-b31c-4202-946d-a19ad45d1fd2","parameters":{"region":"us-west-2"},"durableRoot":"/customer/persistent"}}`)
	spec, err := reconciler.workerSpec(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderCoder,
		ResourceProfile: profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"AO_WORKSPACE_DIR":  "/customer/persistent/repository",
		"AO_DATA_DIR":       "/customer/persistent/.ao/worker",
		"HOME":              "/customer/persistent/.ao/home",
		"CLAUDE_CONFIG_DIR": "/customer/persistent/.ao/home/.claude",
		"CODEX_HOME":        "/customer/persistent/.ao/home/.codex",
	}
	for key, expected := range want {
		if spec.Environment[key] != expected {
			t.Errorf("%s = %q, want %q", key, spec.Environment[key], expected)
		}
	}
	if spec.DurableRoot != "/customer/persistent" {
		t.Errorf("DurableRoot = %q", spec.DurableRoot)
	}
}

func TestWorkerSpecAdvertisesWorkerBinaryHashes(t *testing.T) {
	t.Parallel()
	workerBin := []byte("fake ao-worker binary")
	helperBin := []byte("fake ao helper binary")
	reconciler := New(&workerSpecStore{}, nil, Options{
		PublicURL:          "https://cloud.example.com",
		WorkerBinary:       workerBin,
		WorkerHelperBinary: helperBin,
	})
	spec, err := reconciler.workerSpec(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := spec.Environment["AO_WORKER_EXPECTED_SHA256"]; got != sha256HexOf(workerBin) {
		t.Fatalf("AO_WORKER_EXPECTED_SHA256 = %q, want %q", got, sha256HexOf(workerBin))
	}
	if got := spec.Environment["AO_WORKER_HELPER_EXPECTED_SHA256"]; got != sha256HexOf(helperBin) {
		t.Fatalf("AO_WORKER_HELPER_EXPECTED_SHA256 = %q, want %q", got, sha256HexOf(helperBin))
	}
	if spec.Environment["AO_WORKER_HELPER_PATH"] == "" {
		t.Fatal("AO_WORKER_HELPER_PATH must be advertised so the helper self-update can shadow the baked copy")
	}
}

func TestWorkerSpecOmitsHashesWithoutBinary(t *testing.T) {
	t.Parallel()
	reconciler := New(&workerSpecStore{}, nil, Options{PublicURL: "https://cloud.example.com"})
	spec, err := reconciler.workerSpec(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := spec.Environment["AO_WORKER_EXPECTED_SHA256"]; ok {
		t.Fatal("no worker binary configured: the self-update env must be absent so self-update stays inert")
	}
}

func TestWorkerSpecPreservesOtherProviderWorkspaceLayout(t *testing.T) {
	t.Parallel()
	store := &workerSpecStore{}
	reconciler := New(store, nil, Options{PublicURL: "https://cloud.example.com"})
	spec, err := reconciler.workerSpec(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Environment["AO_WORKSPACE_DIR"] != "/workspace/repository" ||
		spec.Environment["AO_DATA_DIR"] != "/workspace/.ao/worker" || spec.DurableRoot != "" {
		t.Fatalf("unexpected non-Coder layout: %+v", spec)
	}
}

func TestWorkerSpecRejectsCoderWithoutDurableContractBeforeIssuingTicket(t *testing.T) {
	t.Parallel()
	store := &workerSpecStore{}
	reconciler := New(store, nil, Options{PublicURL: "https://cloud.example.com"})
	_, err := reconciler.workerSpec(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderCoder,
	})
	if err == nil || !strings.Contains(err.Error(), "session resource profile") {
		t.Fatalf("workerSpec error = %v", err)
	}
	if store.issued != 0 {
		t.Fatalf("issued %d bootstrap tickets for an invalid layout", store.issued)
	}
}

func TestCoderRestoreBootstrapRequiresDurableIdentity(t *testing.T) {
	t.Parallel()
	now := time.Now()
	reconciler := New(&workerSpecStore{}, nil, Options{})
	record := domain.Sandbox{
		SessionID: "session-1", Provider: sandbox.ProviderCoder, WorkerLastSeenAt: &now,
	}
	bootstrap := reconciler.workerBootstrap(record, sandbox.Spec{DurableRoot: "/mnt/ao"}, false)
	if !bootstrap.RequireDurableIdentity || bootstrap.DurableIdentity != "session-1" {
		t.Fatalf("unexpected restore bootstrap: %+v", bootstrap)
	}
	first := reconciler.workerBootstrap(domain.Sandbox{
		SessionID: "session-2", Provider: sandbox.ProviderCoder,
	}, sandbox.Spec{DurableRoot: "/mnt/ao"}, false)
	if first.RequireDurableIdentity {
		t.Fatal("first Coder bootstrap unexpectedly required an existing identity")
	}
}

// pausePathStore spies on the two store calls the pause path makes.
type pausePathStore struct {
	Store
	disconnected int
	observed     string
}

func (s *pausePathStore) DisconnectSessionWorkers(context.Context, string, string) error {
	s.disconnected++
	return nil
}

func (s *pausePathStore) UpdateSandboxObservation(
	_ context.Context, _, _, _, _, observedState, _ string, _ time.Time,
) error {
	s.observed = observedState
	return nil
}

// restoreProvider fakes the provider calls the provision path and the
// terminated-park path use, counting them so a test can assert which branch a
// restored sandbox took.
type restoreProvider struct {
	sandbox.Provider
	env       sandbox.Environment
	found     bool
	findCalls int
	getCalls  int
}

func (p *restoreProvider) FindBySession(context.Context, string) (sandbox.Environment, bool, error) {
	p.findCalls++
	return p.env, p.found, nil
}

func (p *restoreProvider) Get(context.Context, sandbox.ID) (sandbox.Environment, error) {
	p.getCalls++
	return p.env, nil
}

// A deleted-then-restored sandbox (observed 'deleted', no provider environment,
// desired 'running') must re-enter provisioning and build a fresh sandbox.
func TestRestoredDeletedSandboxReprovisions(t *testing.T) {
	t.Parallel()
	store := &pausePathStore{}
	provider := &restoreProvider{found: true, env: sandbox.Environment{ID: "env-new"}}
	reconciler := New(store, fixedResolver{provider}, Options{})
	if err := reconciler.reconcileSandbox(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		DesiredState:          domain.SandboxDesiredRunning,
		ObservedState:         domain.SandboxObservedDeleted,
		ProviderEnvironmentID: "",
	}); err != nil {
		t.Fatalf("reconcileSandbox: %v", err)
	}
	if provider.findCalls != 1 {
		t.Fatalf("provision not reached: FindBySession called %d times, want 1", provider.findCalls)
	}
	if store.observed != domain.SandboxObservedProvisioning {
		t.Fatalf("observed = %q, want %q", store.observed, domain.SandboxObservedProvisioning)
	}
}

// A restore that re-asserts a running intent on a sandbox parked as 'terminated'
// (repair-storm ceiling) must un-park it and re-enter provisioning.
func TestRestoredTerminatedSandboxReprovisions(t *testing.T) {
	t.Parallel()
	store := &pausePathStore{}
	provider := &restoreProvider{found: true, env: sandbox.Environment{ID: "env-new"}}
	reconciler := New(store, fixedResolver{provider}, Options{})
	if err := reconciler.reconcileSandbox(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		DesiredState:          domain.SandboxDesiredRunning,
		ObservedState:         domain.SandboxObservedTerminated,
		ProviderEnvironmentID: "",
	}); err != nil {
		t.Fatalf("reconcileSandbox: %v", err)
	}
	if provider.findCalls != 1 {
		t.Fatalf("terminated+running not re-provisioned: FindBySession called %d times, want 1", provider.findCalls)
	}
	if store.observed != domain.SandboxObservedProvisioning {
		t.Fatalf("observed = %q, want %q", store.observed, domain.SandboxObservedProvisioning)
	}
}

// The terminated park is preserved for any non-running desired state: a
// terminated sandbox is not probed or resumed while it stays parked.
func TestTerminatedSandboxStaysParkedWhenNotRunning(t *testing.T) {
	t.Parallel()
	store := &pausePathStore{}
	provider := &restoreProvider{env: sandbox.Environment{ID: "env-1"}}
	reconciler := New(store, fixedResolver{provider}, Options{})
	if err := reconciler.reconcileSandbox(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		DesiredState:          domain.SandboxDesiredPaused,
		ObservedState:         domain.SandboxObservedTerminated,
		ProviderEnvironmentID: "env-1",
	}); err != nil {
		t.Fatalf("reconcileSandbox: %v", err)
	}
	if provider.getCalls != 0 || provider.findCalls != 0 {
		t.Fatalf("a parked terminated sandbox must not touch the provider (get=%d find=%d)",
			provider.getCalls, provider.findCalls)
	}
	if store.observed != domain.SandboxObservedTerminated {
		t.Fatalf("observed = %q, want %q (parked)", store.observed, domain.SandboxObservedTerminated)
	}
}

// stopSpyProvider fakes just the two provider calls the pause path uses.
type stopSpyProvider struct {
	sandbox.Provider
	state   string
	stopped int
}

func (p *stopSpyProvider) Get(context.Context, sandbox.ID) (sandbox.Environment, error) {
	return sandbox.Environment{ID: "env-1", State: p.state}, nil
}

func (p *stopSpyProvider) Stop(context.Context, sandbox.ID) error {
	p.stopped++
	return nil
}

type fixedResolver struct{ provider sandbox.Provider }

func (r fixedResolver) Resolve(context.Context, domain.Sandbox) (sandbox.Provider, error) {
	return r.provider, nil
}

// Pausing a running sandbox must stop the provider AND disconnect its worker,
// so a subsequent terminal keystroke sees "no worker" and wakes the box instead
// of enqueuing input to a dead worker that expires unclaimed.
func TestReconcilePauseDisconnectsWorker(t *testing.T) {
	t.Parallel()
	store := &pausePathStore{}
	provider := &stopSpyProvider{state: sandbox.StateRunning}
	reconciler := New(store, fixedResolver{provider}, Options{})
	if err := reconciler.reconcileSandbox(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		DesiredState:          domain.SandboxDesiredPaused,
		ObservedState:         domain.SandboxObservedRunning,
		ProviderEnvironmentID: "env-1",
	}); err != nil {
		t.Fatalf("reconcileSandbox: %v", err)
	}
	if provider.stopped != 1 {
		t.Fatalf("provider.Stop called %d times, want 1", provider.stopped)
	}
	if store.disconnected != 1 {
		t.Fatalf("DisconnectSessionWorkers called %d times, want 1", store.disconnected)
	}
	if store.observed != domain.SandboxObservedStopped {
		t.Fatalf("observed = %q, want %q", store.observed, domain.SandboxObservedStopped)
	}
}

// A sandbox already stopped provider-side must not re-stop or re-disconnect on
// every reconcile tick while it stays paused.
func TestReconcilePauseAlreadyStoppedSkipsDisconnect(t *testing.T) {
	t.Parallel()
	store := &pausePathStore{}
	provider := &stopSpyProvider{state: sandbox.StateStopped}
	reconciler := New(store, fixedResolver{provider}, Options{})
	if err := reconciler.reconcileSandbox(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		DesiredState:          domain.SandboxDesiredPaused,
		ObservedState:         domain.SandboxObservedStopped,
		ProviderEnvironmentID: "env-1",
	}); err != nil {
		t.Fatalf("reconcileSandbox: %v", err)
	}
	if provider.stopped != 0 {
		t.Fatalf("provider.Stop called %d times on an already-stopped env, want 0", provider.stopped)
	}
	if store.disconnected != 0 {
		t.Fatalf("DisconnectSessionWorkers called %d times on an already-stopped env, want 0", store.disconnected)
	}
}

// claimSignalStore reports each reconcile pass (every pass starts by claiming).
type claimSignalStore struct {
	lifecycleStore
	claims chan struct{}
}

func (s *claimSignalStore) ClaimSandboxes(context.Context, string, int, time.Duration) ([]domain.Sandbox, error) {
	s.claims <- struct{}{}
	return nil, nil
}

func TestWakeRunsAPassWithoutWaitingForTheInterval(t *testing.T) {
	store := &claimSignalStore{claims: make(chan struct{}, 4)}
	reconciler := New(store, nil, Options{
		Interval: time.Hour,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = reconciler.Run(ctx) }()

	waitForClaim := func(what string) {
		t.Helper()
		select {
		case <-store.claims:
		case <-time.After(5 * time.Second):
			t.Fatalf("no reconcile pass %s", what)
		}
	}
	waitForClaim("at startup")
	reconciler.Wake()
	waitForClaim("after Wake")
}

func TestWakeNeverBlocksWhenAPassIsAlreadyPending(t *testing.T) {
	reconciler := New(&lifecycleStore{}, nil, Options{})
	done := make(chan struct{})
	go func() {
		// Nothing drains the channel because Run is not running.
		reconciler.Wake()
		reconciler.Wake()
		reconciler.Wake()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wake blocked")
	}
}

// blockingClaimStore holds the first ticker pass inside its claim so a test can
// prove a woken pass does not queue behind it.
type blockingClaimStore struct {
	lifecycleStore
	calls   chan int
	release chan struct{}
	n       int
	mu      sync.Mutex
}

func (s *blockingClaimStore) ClaimSandboxes(context.Context, string, int, time.Duration) ([]domain.Sandbox, error) {
	s.mu.Lock()
	s.n++
	n := s.n
	s.mu.Unlock()
	s.calls <- n
	if n == 2 { // the first ticker pass
		<-s.release
	}
	return nil, nil
}

func TestWokenPassDoesNotWaitForABusyTickerPass(t *testing.T) {
	store := &blockingClaimStore{calls: make(chan int, 8), release: make(chan struct{})}
	defer close(store.release)
	reconciler := New(store, nil, Options{
		Interval: 10 * time.Millisecond,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = reconciler.Run(ctx) }()

	next := func(what string) int {
		t.Helper()
		select {
		case n := <-store.calls:
			return n
		case <-time.After(5 * time.Second):
			t.Fatalf("no claim %s", what)
			return 0
		}
	}
	next("for the initial pass")
	next("for the ticker pass, which now blocks")
	reconciler.Wake()
	if n := next("for the woken pass while the ticker pass is blocked"); n != 3 {
		t.Fatalf("claim #%d, want the woken pass (#3)", n)
	}
}

// resumingProvider reports running as soon as it is started, as a Freestyle
// VM does once its memory is restored.
type resumingProvider struct{ lifecycleProvider }

func (p *resumingProvider) Start(ctx context.Context, id sandbox.ID) error {
	p.environment.State = sandbox.StateRunning
	return p.lifecycleProvider.Start(ctx, id)
}

func (p *resumingProvider) Resume(ctx context.Context, id sandbox.ID) error {
	return p.Start(ctx, id)
}

func TestFreestyleRestoreRefreshesWorkerInSamePass(t *testing.T) {
	store := &lifecycleStore{}
	provider := &resumingProvider{lifecycleProvider{environment: sandbox.Environment{
		ID: "vm-1", State: sandbox.StatePaused,
	}}}
	record := runningRecord(false)
	record.Provider = sandbox.ProviderFreestyle
	record.ObservedState = domain.SandboxObservedStopped
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if provider.starts != 1 {
		t.Fatalf("starts = %d, want 1", provider.starts)
	}
	// Without the inline wait the pass would end in "restoring" and the worker
	// refresh would wait for a later pass.
	if got := store.observations; len(got) != 1 || got[0] != domain.SandboxObservedBootstrapping {
		t.Fatalf("observations = %v, want [%s]", got, domain.SandboxObservedBootstrapping)
	}
}

func TestCoderRestoreKeepsTickDrivenRefresh(t *testing.T) {
	store := &lifecycleStore{}
	provider := &resumingProvider{lifecycleProvider{environment: sandbox.Environment{
		ID: "workspace-1", State: sandbox.StateStopped,
	}}}
	if err := testReconciler(store, provider).reconcileSandbox(context.Background(), runningRecord(false)); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := store.observations; len(got) != 1 || got[0] != domain.SandboxObservedRestoring {
		t.Fatalf("observations = %v, want [%s]", got, domain.SandboxObservedRestoring)
	}
}

// uniqueProvider rejects a second Create for the same session, as Freestyle
// does for a taken slug, and counts the lookups the reconciler makes.
type uniqueProvider struct {
	lifecycleProvider
	exists  bool
	lookups int
	creates int
}

func (p *uniqueProvider) CreateRejectsDuplicates() {}

func (p *uniqueProvider) Create(context.Context, sandbox.Spec) (sandbox.Environment, error) {
	p.creates++
	if p.exists {
		return sandbox.Environment{}, sandbox.ErrAlreadyExists
	}
	return p.environment, nil
}

func (p *uniqueProvider) FindBySession(context.Context, string) (sandbox.Environment, bool, error) {
	p.lookups++
	return p.environment, p.exists, nil
}

func provisionRecord() domain.Sandbox {
	return domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		DesiredState:  domain.SandboxDesiredRunning,
		ObservedState: domain.SandboxObservedRequested,
	}
}

func TestProvisionSkipsLookupForDuplicateRejectingProvider(t *testing.T) {
	store := &lifecycleStore{}
	provider := &uniqueProvider{lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
		ID: "vm-1", State: sandbox.StateRunning,
	}}}
	_ = testReconciler(store, provider).provision(context.Background(), provisionRecord(), provider)
	if provider.lookups != 0 || provider.creates != 1 {
		t.Fatalf("lookups = %d, creates = %d; want 0, 1", provider.lookups, provider.creates)
	}
}

func TestProvisionAdoptsSandboxOnDuplicateConflict(t *testing.T) {
	store := &lifecycleStore{}
	provider := &uniqueProvider{exists: true, lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
		ID: "vm-1", State: sandbox.StateRunning,
	}}}
	if err := testReconciler(store, provider).provision(context.Background(), provisionRecord(), provider); err != nil {
		t.Fatalf("provision: %v", err)
	}
	if provider.lookups != 1 {
		t.Fatalf("lookups = %d, want 1 after the conflict", provider.lookups)
	}
	if got := store.observations; len(got) != 1 || got[0] != domain.SandboxObservedProvisioning {
		t.Fatalf("observations = %v, want adoption as provisioning", got)
	}
}

// A sandbox last observed paused whose compute is already running (the
// provider reported it running before the restore branch acted) must get a
// fresh worker at once: the pause fenced the old one, and without a relaunch
// the session waits out the whole startup deadline with no worker.
func TestPausedSandboxFoundRunningRelaunchesWorker(t *testing.T) {
	store := &lifecycleStore{}
	provider := &byoProvider{lifecycleProvider: lifecycleProvider{environment: sandbox.Environment{
		ID: "vm-1", State: sandbox.StateRunning,
	}}}
	seen := time.Now().Add(-10 * time.Second)
	record := domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: sandbox.ProviderFreestyle,
		ProviderEnvironmentID: "vm-1",
		DesiredState:          domain.SandboxDesiredRunning,
		ObservedState:         domain.SandboxObservedStopped,
		WorkerLastSeenAt:      &seen,
		ResourceProfile:       json.RawMessage(`{"provider":"freestyle","freestyle":{"snapshot":"snap-1"}}`),
		UpdatedAt:             seen,
	}
	if err := byoReconciler(store, provider).reconcileSandbox(context.Background(), record); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(provider.bootstraps) != 1 {
		t.Fatalf("bootstraps = %d, want a fresh worker launched now", len(provider.bootstraps))
	}
	if got := store.observations; len(got) != 1 || got[0] != domain.SandboxObservedBootstrapping {
		t.Fatalf("observations = %v, want [%s]", got, domain.SandboxObservedBootstrapping)
	}
}

// retiredStore records the store calls a retired-provider row may make.
type retiredStore struct {
	pausePathStore
	completed    int
	startupCode  string
	startupError string
	lastError    string
}

func (s *retiredStore) CompleteSandboxDeletion(context.Context, string, string, string) error {
	s.completed++
	return nil
}

func (s *retiredStore) RecordSandboxStartupError(_ context.Context, _, _, _, code, message string) error {
	s.startupCode, s.startupError = code, message
	return nil
}

func (s *retiredStore) UpdateSandboxObservation(
	_ context.Context, _, _, _, _, observedState, lastError string, _ time.Time,
) error {
	s.observed, s.lastError = observedState, lastError
	return nil
}

// failingResolver fails the test if the reconciler asks it for a provider.
type failingResolver struct{ t *testing.T }

func (r failingResolver) Resolve(context.Context, domain.Sandbox) (sandbox.Provider, error) {
	r.t.Error("Resolve called for a retired provider")
	return nil, errors.New("unexpected resolve")
}

// Deleting a session on a retired provider completes without any provider call,
// so the row stops retrying and its quota is released.
func TestRetiredProviderDeletionCompletesWithoutProvider(t *testing.T) {
	t.Parallel()
	for _, provider := range []string{"nodeops", "ecs", "lambda-microvms"} {
		store := &retiredStore{}
		reconciler := New(store, failingResolver{t}, Options{})
		if err := reconciler.reconcileSandbox(context.Background(), domain.Sandbox{
			SessionID: "session-1", OrgID: "org-1", Provider: provider,
			DesiredState:          domain.SandboxDesiredDeleted,
			ObservedState:         domain.SandboxObservedRunning,
			ProviderEnvironmentID: "env-1",
		}); err != nil {
			t.Fatalf("%s: reconcileSandbox: %v", provider, err)
		}
		if store.completed != 1 {
			t.Fatalf("%s: CompleteSandboxDeletion called %d times, want 1", provider, store.completed)
		}
	}
}

// A live session on a retired provider is parked as terminated with a reason
// the session surfaces, instead of failing Resolve every tick forever.
func TestRetiredProviderSessionIsParkedTerminated(t *testing.T) {
	t.Parallel()
	store := &retiredStore{}
	reconciler := New(store, failingResolver{t}, Options{})
	if err := reconciler.reconcileSandbox(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: "nodeops",
		DesiredState:          domain.SandboxDesiredRunning,
		ObservedState:         domain.SandboxObservedRunning,
		ProviderEnvironmentID: "env-1",
	}); err != nil {
		t.Fatalf("reconcileSandbox: %v", err)
	}
	if store.observed != domain.SandboxObservedTerminated || store.lastError == "" {
		t.Fatalf("observed = %q lastError = %q, want terminated with a reason", store.observed, store.lastError)
	}
	if store.startupCode != sandbox.StartupErrorProviderRetired || store.startupError == "" {
		t.Fatalf("startup error = %q %q, want %q", store.startupCode, store.startupError, sandbox.StartupErrorProviderRetired)
	}
	if store.disconnected != 1 || store.completed != 0 {
		t.Fatalf("disconnected = %d completed = %d, want 1 and 0", store.disconnected, store.completed)
	}
}

// An already parked retired session is not re-disconnected or re-recorded.
func TestRetiredProviderParkedSessionStaysQuiet(t *testing.T) {
	t.Parallel()
	store := &retiredStore{}
	reconciler := New(store, failingResolver{t}, Options{})
	if err := reconciler.reconcileSandbox(context.Background(), domain.Sandbox{
		SessionID: "session-1", OrgID: "org-1", Provider: "nodeops",
		DesiredState:     domain.SandboxDesiredRunning,
		ObservedState:    domain.SandboxObservedTerminated,
		StartupErrorCode: sandbox.StartupErrorProviderRetired,
	}); err != nil {
		t.Fatalf("reconcileSandbox: %v", err)
	}
	if store.disconnected != 0 || store.startupCode != "" {
		t.Fatalf("disconnected = %d startup code = %q, want no repeat", store.disconnected, store.startupCode)
	}
	if store.observed != domain.SandboxObservedTerminated {
		t.Fatalf("observed = %q, want terminated", store.observed)
	}
}
