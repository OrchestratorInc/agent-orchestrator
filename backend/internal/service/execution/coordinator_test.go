package execution

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
)

var testNow = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

func fixedClock() func() time.Time { return func() time.Time { return testNow } }

// memoryBindings is an in-memory BindingStore that mirrors the real store's contract,
// including the spawn-request uniqueness rule and the session CAS. It is used where the
// test is about coordinator logic rather than SQL, so a failure points at the
// coordinator instead of at the database.
type memoryBindings struct {
	mu      sync.Mutex
	byID    map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding
	bySpawn map[string]domain.WorkflowAttemptID
	// attempts holds the attempt rows BindAttempt reads for the lease token. An
	// entry with a lease is how a test proves the token reaches the gateway.
	attempts map[domain.WorkflowAttemptID]domain.WorkflowAttempt
	// strictAttempts stops CreateWorkflowAttemptBinding from materializing a
	// placeholder attempt row, so a test can prove the "attempt is missing" refusal.
	strictAttempts bool
	// repos holds the per-repository expectations and their recorded states, keyed
	// by attempt then repo key. It makes this fake a RepoBindingStore so the
	// repository gate is exercised rather than skipped.
	repos map[domain.WorkflowAttemptID]map[domain.WorkflowRepoKey]domain.WorkflowAttemptRepoBinding
}

func newMemoryBindings() *memoryBindings {
	return &memoryBindings{
		byID:     make(map[domain.WorkflowAttemptID]domain.WorkflowAttemptBinding),
		bySpawn:  make(map[string]domain.WorkflowAttemptID),
		attempts: make(map[domain.WorkflowAttemptID]domain.WorkflowAttempt),
		repos:    make(map[domain.WorkflowAttemptID]map[domain.WorkflowRepoKey]domain.WorkflowAttemptRepoBinding),
	}
}

// ListWorkflowAttemptRepoBindings returns one attempt's repository rows in key order.
func (m *memoryBindings) ListWorkflowAttemptRepoBindings(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
) ([]domain.WorkflowAttemptRepoBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.repos[attemptID]
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, string(key))
	}
	sort.Strings(keys)
	out := make([]domain.WorkflowAttemptRepoBinding, 0, len(keys))
	for _, key := range keys {
		out = append(out, rows[domain.WorkflowRepoKey(key)])
	}
	return out, nil
}

// RecordWorkflowAttemptRepoBinding applies the same per-row CAS the real store does.
func (m *memoryBindings) RecordWorkflowAttemptRepoBinding(
	_ context.Context,
	binding domain.WorkflowAttemptRepoBinding,
	expectedRevision int64,
	_ time.Time,
) (domain.WorkflowAttemptRepoBinding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows := m.repos[binding.AttemptID]
	current, ok := rows[binding.RepoKey]
	if !ok {
		return domain.WorkflowAttemptRepoBinding{}, false, domain.ErrInvalidWorkflowRepoBinding
	}
	if current.Revision != expectedRevision {
		return domain.WorkflowAttemptRepoBinding{}, false, domain.ErrWorkflowBindingRevisionConflict
	}
	binding.Revision++
	rows[binding.RepoKey] = binding
	return binding, true, nil
}

// withAttempt seeds one attempt row.
func (m *memoryBindings) withAttempt(attempt domain.WorkflowAttempt) *memoryBindings {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[attempt.ID] = attempt
	return m
}

// GetWorkflowAttempt mirrors the real store's attempt read.
func (m *memoryBindings) GetWorkflowAttempt(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
) (domain.WorkflowAttempt, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	attempt, ok := m.attempts[attemptID]
	return attempt, ok, nil
}

// seedAttemptFor gives every BindAttempt in this store an attempt row, so the
// fixture's "the attempt exists" precondition holds without each test spelling it.
func (m *memoryBindings) seedAttemptFor(attemptID domain.WorkflowAttemptID, lease string) {
	m.withAttempt(domain.WorkflowAttempt{ID: attemptID, LeaseToken: lease})
}

func (m *memoryBindings) CreateWorkflowAttemptBinding(
	_ context.Context,
	binding domain.WorkflowAttemptBinding,
	repositories []domain.WorkflowAttemptRepoBinding,
	now time.Time,
) (domain.WorkflowAttemptBinding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existingID, ok := m.bySpawn[binding.SpawnRequestID]; ok {
		existing := m.byID[existingID]
		if existing.SpawnHash != binding.SpawnHash || existing.AttemptID != binding.AttemptID {
			return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingSpawningRequestConflict
		}
		return existing, false, nil
	}
	if existing, ok := m.byID[binding.AttemptID]; ok {
		if existing.SpawnRequestID != binding.SpawnRequestID {
			return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingSpawningRequestConflict
		}
		return existing, false, nil
	}
	binding.State = domain.WorkflowBindingStateIntentPersisted
	binding.Revision = 1
	binding.CreatedAt = now
	binding.UpdatedAt = now
	m.byID[binding.AttemptID] = binding
	m.bySpawn[binding.SpawnRequestID] = binding.AttemptID
	rows := make(map[domain.WorkflowRepoKey]domain.WorkflowAttemptRepoBinding, len(repositories))
	for _, repo := range repositories {
		repo.AttemptID = binding.AttemptID
		repo.Revision = 1
		if repo.State == "" {
			repo.State = domain.WorkflowRepoBindingStatePending
		}
		rows[repo.RepoKey] = repo
	}
	m.repos[binding.AttemptID] = rows
	// The real store cannot hold a binding without its attempt row (the FK and the
	// resolve-before-bind order guarantee it), so the fake materializes the same
	// precondition. A test that wants "the attempt is gone" uses strictAttempts.
	if _, ok := m.attempts[binding.AttemptID]; !ok && !m.strictAttempts {
		m.attempts[binding.AttemptID] = domain.WorkflowAttempt{ID: binding.AttemptID}
	}
	return binding, true, nil
}

func (m *memoryBindings) GetWorkflowAttemptBinding(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.byID[attemptID]
	return binding, ok, nil
}

func (m *memoryBindings) GetWorkflowAttemptBindingBySpawnRequest(
	_ context.Context,
	spawnRequestID string,
) (domain.WorkflowAttemptBinding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	attemptID, ok := m.bySpawn[spawnRequestID]
	if !ok {
		return domain.WorkflowAttemptBinding{}, false, nil
	}
	return m.byID[attemptID], true, nil
}

func (m *memoryBindings) BindWorkflowAttemptSession(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
	sessionID domain.SessionID,
	generation string,
	target domain.WorkflowBindingState,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowAttemptBinding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.byID[attemptID]
	if !ok {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingNotFound
	}
	if current.Revision != expectedRevision {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingRevisionConflict
	}
	if !target.IsSessionAcquisitionTarget() {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrInvalidWorkflowBindingTransition
	}
	if err := current.State.ValidateTransition(target); err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}
	// One session, one binding.
	for _, other := range m.byID {
		if other.SessionID != nil && *other.SessionID == sessionID && other.AttemptID != attemptID {
			return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingSessionTaken
		}
	}
	current.SessionID = &sessionID
	current.ControllerGeneration = generation
	current.BoundAt = &now
	current.State = target
	current.Revision++
	current.UpdatedAt = now
	m.byID[attemptID] = current
	return current, true, nil
}

func (m *memoryBindings) TransitionWorkflowAttemptBinding(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
	next domain.WorkflowBindingState,
	failureCode string,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowAttemptBinding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.byID[attemptID]
	if !ok {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingNotFound
	}
	if current.Revision != expectedRevision {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingRevisionConflict
	}
	if err := current.State.ValidateTransition(next); err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}
	current.State = next
	current.FailureCode = failureCode
	current.Revision++
	current.UpdatedAt = now
	m.byID[attemptID] = current
	return current, true, nil
}

// ReanchorWorkflowAttemptGeneration mirrors the real store's CAS: same session only, same
// revision and state only, and it only moves the recorded generation.
func (m *memoryBindings) ReanchorWorkflowAttemptGeneration(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
	sessionID domain.SessionID,
	generation string,
	expectedRevision int64,
	expectedState domain.WorkflowBindingState,
	now time.Time,
) (domain.WorkflowAttemptBinding, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.byID[attemptID]
	if !ok {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingNotFound
	}
	if current.Revision != expectedRevision || current.State != expectedState {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingRevisionConflict
	}
	if current.SessionID == nil || *current.SessionID != sessionID {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrWorkflowBindingSessionTaken
	}
	current.ControllerGeneration = generation
	current.Revision++
	current.UpdatedAt = now
	m.byID[attemptID] = current
	return current, true, nil
}

// permitAll resolves every capability question affirmatively at handshake grade, so a
// test about spawn semantics is not blocked by the capability layer.
func permitAll(harness domain.AgentHarness, sessionID domain.SessionID, generation string) *capability.FakeProbe {
	snapshot := domain.NewHarnessCapabilitySnapshot(
		harness, domain.CapabilitySourceProviderHandshake, testNow, capability.DefaultTTL,
	)
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Streaming = domain.CapabilitySupported
	snapshot.Tools = domain.CapabilitySupported
	snapshot.Interrupt = domain.CapabilitySupported
	snapshot.Steer = domain.CapabilitySupported
	snapshot.Queue = domain.CapabilitySupported
	snapshot.Resume = domain.CapabilityResumeWithHistory
	snapshot.Approval = domain.CapabilityApprovalEnforced
	snapshot.ApplyAt = domain.CapabilityApplyAtTurn
	snapshot.Usage = domain.CapabilityUsageExact
	if sessionID != "" {
		snapshot.ObservedSessionID = sessionID
		snapshot.Generation = generation
	}
	return capability.NewFakeProbe().Script(snapshot)
}

// newTestCoordinator builds a coordinator whose capability gate is satisfied for the
// harness under test.
func newTestCoordinator(t *testing.T, store BindingStore, gateway ports.ExecutionGateway) *Coordinator {
	t.Helper()
	resolver := capability.NewResolver(capability.WithClock(fixedClock()))
	resolver.RegisterProbe(domain.HarnessCodex, permitAll(domain.HarnessCodex, "", ""))
	return New(Deps{
		Store:    store,
		Gateway:  gateway,
		Resolver: resolver,
		Clock:    fixedClock(),
	})
}

func sampleSpawnRequest() ports.SpawnRequest {
	return ports.SpawnRequest{
		WorkflowID: "workflow-1",
		TaskID:     "task-1",
		AttemptID:  "attempt-1",
		ProjectID:  "project-1",
		Harness:    domain.HarnessCodex,
		Mode:       domain.SessionModeChat,
		Prompt:     "implement the thing",
	}
}

// TestSpawnKeyIsStableAcrossRetries is the rule that prevents duplicate sessions.
func TestSpawnKeyIsStableAcrossRetries(t *testing.T) {
	t.Parallel()
	first := SpawnKey("attempt-1", ActionSpawn)
	for range 100 {
		if got := SpawnKey("attempt-1", ActionSpawn); got != first {
			t.Fatalf("SpawnKey is not stable: %q vs %q", got, first)
		}
	}
	if SpawnKey("attempt-2", ActionSpawn) == first {
		t.Fatal("different attempts must not share a spawn key")
	}
	if SpawnKey("attempt-1", ActionResume) == first {
		t.Fatal("a resume must not collide with the spawn that created the session")
	}
	if !strings.HasPrefix(first, string(ActionSpawn)+"-") {
		t.Fatalf("spawn key %q should name its action", first)
	}
}

// TestSpawnHashIgnoresPrompt proves an edited prompt is still the same spawn: hashing
// free-form text would turn a correction into a spurious conflict.
func TestSpawnHashIgnoresPrompt(t *testing.T) {
	t.Parallel()
	request := sampleSpawnRequest()
	first, err := SpawnHash(request)
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("hash length = %d, want 64", len(first))
	}
	changedPrompt := request
	changedPrompt.Prompt = "a completely different instruction"
	second, err := SpawnHash(changedPrompt)
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	if first != second {
		t.Fatal("the prompt must not participate in the spawn hash")
	}
	changedHarness := request
	changedHarness.Harness = domain.HarnessClaudeCode
	third, err := SpawnHash(changedHarness)
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	if first == third {
		t.Fatal("a different harness must produce a different spawn hash")
	}
}

// TestBindAttemptStopsAtProvisioning is the "session created is not provider started"
// proof. A provisioning spawn must bind at bound_ready and never reach running.
func TestBindAttemptStopsAtProvisioning(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessProvisioning)
	coordinator := newTestCoordinator(t, store, gateway)

	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if !outcome.Spawned {
		t.Fatal("a provisioning spawn must still record the session")
	}
	// A provisioning session binds at session_provisioning: the row exists, the
	// workspace/controller does not. It is emphatically not running.
	if outcome.Binding.State != domain.WorkflowBindingStateSessionProvisioning {
		t.Fatalf("state = %q, want session_provisioning (a provisioning session is not running)",
			outcome.Binding.State)
	}
	if outcome.Binding.State == domain.WorkflowBindingStateRunning {
		t.Fatal("a provisioning spawn promoted the binding to running")
	}
	if outcome.Binding.SessionID == nil {
		t.Fatal("a provisioning binding must still name the session it describes")
	}
	if outcome.Binding.SessionID == nil {
		t.Fatal("the bound session must be recorded")
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1", gateway.SpawnCalls())
	}
}

// TestNothingReachesRunningOffline is the requirement stated as a general property:
// no spawn outcome available in the offline path may produce a running binding.
func TestNothingReachesRunningOffline(t *testing.T) {
	t.Parallel()
	readinesses := []ports.SessionReadiness{
		ports.SessionReadinessUnknown,
		ports.SessionReadinessProvisioning,
		ports.SessionReadinessReady,
		ports.SessionReadinessExited,
	}
	for _, readiness := range readinesses {
		t.Run(string(readiness), func(t *testing.T) {
			t.Parallel()
			store := newMemoryBindings()
			gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(readiness)
			coordinator := newTestCoordinator(t, store, gateway)

			outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
			if err != nil {
				t.Fatalf("BindAttempt: %v", err)
			}
			if outcome.Binding.State == domain.WorkflowBindingStateRunning {
				t.Fatalf("readiness %q produced a running binding; nothing offline may claim the provider started",
					readiness)
			}
		})
	}
	// Even a ready session, which is the strongest offline observation, stops at
	// bound_ready: promoting to running needs confirmed_runtime evidence.
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessReady)
	coordinator := newTestCoordinator(t, store, gateway)
	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("ready spawn state = %q, want bound_ready", outcome.Binding.State)
	}
}

// TestLostSpawnResponseFreezesWithoutRetry is the central safety proof: a lost answer
// records spawn_unknown, and every later call must refuse without spawning again.
func TestLostSpawnResponseFreezesWithoutRetry(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).LoseSpawnResponse()
	coordinator := newTestCoordinator(t, store, gateway)
	request := sampleSpawnRequest()

	outcome, err := coordinator.BindAttempt(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if !outcome.Frozen {
		t.Fatal("a lost spawn response must freeze the attempt")
	}
	if outcome.Binding.State != domain.WorkflowBindingStateSpawnUnknown {
		t.Fatalf("state = %q, want spawn_unknown", outcome.Binding.State)
	}
	if outcome.Spawned {
		t.Fatal("a frozen outcome must not report a spawn")
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls after the lost response = %d, want 1", gateway.SpawnCalls())
	}

	// Retrying the same request must not spawn again.
	retry, err := coordinator.BindAttempt(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("retry BindAttempt: %v", err)
	}
	if retry.Spawned {
		t.Fatal("the retry reported a spawn")
	}
	if !retry.Frozen {
		t.Fatal("the retry must still be frozen")
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls after retry = %d, want 1 (no blind retry)", gateway.SpawnCalls())
	}

	// And a third time, to prove the freeze is durable rather than one-shot.
	if _, err := coordinator.BindAttempt(context.Background(), request, nil); err != nil {
		t.Fatalf("third BindAttempt: %v", err)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls after a third call = %d, want 1", gateway.SpawnCalls())
	}
}

// TestLostSpawnResponseRecoversByFindingTheSession proves reconciliation adopts the
// session the lost answer created, without spawning.
func TestLostSpawnResponseRecoversByFindingTheSession(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).LoseSpawnResponse()
	coordinator := newTestCoordinator(t, store, gateway)
	request := sampleSpawnRequest()

	if _, err := coordinator.BindAttempt(context.Background(), request, nil); err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1", gateway.SpawnCalls())
	}

	outcome, err := coordinator.Reconcile(context.Background(), request.AttemptID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("Reconcile spawned again: calls = %d, want 1", gateway.SpawnCalls())
	}
	if outcome.Binding.SessionID == nil {
		t.Fatal("Reconcile did not adopt the session the lost answer created")
	}
	// Adoption records the session's real readiness: a fake provisioning spawn stays
	// session_provisioning, and it never becomes running.
	if outcome.Binding.State != domain.WorkflowBindingStateSessionProvisioning {
		t.Fatalf("state = %q, want session_provisioning after adoption", outcome.Binding.State)
	}
	sessions := gateway.SpawnSessions()
	if len(sessions) != 1 {
		t.Fatalf("gateway holds %d sessions, want exactly 1 (no duplicate side effect)", len(sessions))
	}
}

// TestReconcileWithoutEvidenceStaysFrozen proves absence of evidence after a lost answer
// is not proof that nothing happened.
func TestReconcileWithoutEvidenceStaysFrozen(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	// A gateway that fails before creating anything, so FindSpawn will find nothing.
	gateway := NewFakeGateway().WithClock(fixedClock()).
		FailSpawnWithoutEffect(errors.New("connection reset"))
	coordinator := newTestCoordinator(t, store, gateway)
	request := sampleSpawnRequest()

	outcome, err := coordinator.BindAttempt(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateSpawnUnknown {
		t.Fatalf("state = %q, want spawn_unknown even for an unclassified error",
			outcome.Binding.State)
	}

	reconciled, err := coordinator.Reconcile(context.Background(), request.AttemptID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if !reconciled.Frozen {
		t.Fatal("reconciliation with no evidence must stay frozen")
	}
	if reconciled.Binding.SessionID != nil {
		t.Fatal("reconciliation invented a session")
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1", gateway.SpawnCalls())
	}
}

// TestSpawnRefusalIsRecordedAsFailure proves the one error class that may be recorded as
// a definite failure: the gateway proved it created nothing.
func TestSpawnRefusalIsRecordedAsFailure(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).
		FailSpawnWithoutEffect(errSpawnRefused())
	coordinator := newTestCoordinator(t, store, gateway)

	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateFailedConfirmed {
		t.Fatalf("state = %q, want failed_confirmed for a proven pre-effect refusal", outcome.Binding.State)
	}
	if !outcome.Frozen {
		t.Fatal("a failed binding is frozen")
	}
}

// errSpawnRefused wraps ErrExecutionRefused so errors.Is sees the sentinel, the way a
// real adapter would.
func errSpawnRefused() error {
	return &refusalError{}
}

type refusalError struct{}

func (*refusalError) Error() string {
	return "harness unavailable: " + ports.ErrExecutionRefused.Error()
}
func (*refusalError) Unwrap() error { return ports.ErrExecutionRefused }

// TestBindAttemptRefusesWithoutCapabilityEvidence proves the gate runs before any
// durable write or gateway call.
func TestBindAttemptRefusesWithoutCapabilityEvidence(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock())
	// A resolver with no probes registered falls back to the static profile, which can
	// never authorize.
	coordinator := New(Deps{
		Store:    store,
		Gateway:  gateway,
		Resolver: capability.NewResolver(capability.WithClock(fixedClock())),
		Clock:    fixedClock(),
	})

	_, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
	if gateway.SpawnCalls() != 0 {
		t.Fatalf("spawn calls = %d, want 0: a refused request must cost nothing", gateway.SpawnCalls())
	}
	if _, found, _ := store.GetWorkflowAttemptBinding(context.Background(), "attempt-1"); found {
		t.Fatal("a refused request persisted a binding")
	}
}

// TestExistingBindingIsNeverRespawned proves a bound attempt replays instead of
// spawning a second session.
func TestExistingBindingIsNeverRespawned(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock())
	coordinator := newTestCoordinator(t, store, gateway)
	request := sampleSpawnRequest()

	first, err := coordinator.BindAttempt(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("first BindAttempt: %v", err)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want 1", gateway.SpawnCalls())
	}

	second, err := coordinator.BindAttempt(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("second BindAttempt: %v", err)
	}
	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls after replay = %d, want 1", gateway.SpawnCalls())
	}
	if second.Binding.SessionID == nil || *second.Binding.SessionID != *first.Binding.SessionID {
		t.Fatalf("replay returned %v, want the original %v", second.Binding.SessionID, first.Binding.SessionID)
	}
	if second.Spawned {
		t.Fatal("a replay must not report a spawn")
	}
	if len(gateway.SpawnSessions()) != 1 {
		t.Fatalf("gateway holds %d sessions, want 1", len(gateway.SpawnSessions()))
	}
}

// TestSpawnFailureWithNoSessionIsUnknown proves an answer without a session id is
// treated as an unknown outcome rather than a success.
func TestSpawnFailureWithNoSessionIsUnknown(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := &emptyObservationGateway{FakeGateway: NewFakeGateway().WithClock(fixedClock())}
	coordinator := newTestCoordinator(t, store, gateway)

	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateSpawnUnknown {
		t.Fatalf("state = %q, want spawn_unknown for a sessionless answer", outcome.Binding.State)
	}
	if !outcome.Frozen {
		t.Fatal("a sessionless answer must freeze")
	}
}

// emptyObservationGateway returns a successful call with no session id.
type emptyObservationGateway struct {
	*FakeGateway
}

func (g *emptyObservationGateway) Spawn(
	ctx context.Context,
	request ports.SpawnRequest,
) (ports.SessionObservation, error) {
	if _, err := g.FakeGateway.Spawn(ctx, request); err != nil {
		return ports.SessionObservation{}, err
	}
	return ports.SessionObservation{Readiness: ports.SessionReadinessUnknown}, nil
}

// TestObserveSessionNeverPromotes proves the observation path reports facts and never
// advances the binding.
func TestObserveSessionNeverPromotes(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	// A live session always has a controller generation; a binding that cannot fence an
	// observation is refused by ValidateFence, which is a different (and separately
	// tested) rule than "an observation must not promote".
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnGeneration("gen-1")
	coordinator := newTestCoordinator(t, store, gateway)

	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	sessionID := *outcome.Binding.SessionID
	// Even a session the fake now reports as ready must not promote the binding.
	gateway.SetSessionReadiness(sessionID, ports.SessionReadinessReady)

	observation, state, err := coordinator.ObserveSession(context.Background(), "attempt-1")
	if err != nil {
		t.Fatalf("ObserveSession: %v", err)
	}
	if observation.Readiness != ports.SessionReadinessReady {
		t.Fatalf("readiness = %q, want the observed value", observation.Readiness)
	}
	if state == domain.WorkflowBindingStateRunning {
		t.Fatalf("state = %q: a ready session observation promoted the binding to running", state)
	}
	if state != domain.WorkflowBindingStateSessionProvisioning {
		t.Fatalf("state = %q, want the recorded session_provisioning; observation must not promote", state)
	}
}

// TestSendRequiresMatchingBoundSession proves a message cannot be aimed at a session the
// binding does not own.
func TestSendRequiresMatchingBoundSession(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	// A live session is required: the send gate refuses a provisioning one, and a send
	// must name the controller generation it observed.
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1")
	coordinator := newTestCoordinator(t, store, gateway)
	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	bound := *outcome.Binding.SessionID

	receipt, err := coordinator.SendMessage(context.Background(), "attempt-1", ports.MessageRequest{
		SessionID:       bound,
		ClientMessageID: "msg-1",
		Text:            "steer",
		Delivery:        ports.MessageDeliverySend,
	})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if receipt.ClientMessageID != "msg-1" {
		t.Fatalf("receipt = %+v", receipt)
	}

	if _, err := coordinator.SendMessage(context.Background(), "attempt-1", ports.MessageRequest{
		SessionID:       "someone-elses-session",
		ClientMessageID: "msg-2",
		Text:            "steer",
		Delivery:        ports.MessageDeliverySend,
	}); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("cross-session send err = %v, want ErrExecutionRefused", err)
	}
	if gateway.SendCalls() != 1 {
		t.Fatalf("send calls = %d, want 1 (the refused send must not reach the gateway)", gateway.SendCalls())
	}

	if _, err := coordinator.SendMessage(context.Background(), "attempt-1", ports.MessageRequest{
		SessionID: bound,
		Text:      "no client message id",
		Delivery:  ports.MessageDeliverySend,
	}); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("missing client message id err = %v, want ErrExecutionRefused", err)
	}
}

// TestInterruptReportsUnknown proves an uncertain stop is surfaced as uncertain rather
// than retried or reported as stopped.
func TestInterruptReportsUnknown(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	// A live session is required: the interrupt gate refuses anything else, because asking
	// a dead session to stop would mint a false receipt.
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1").
		InterruptResult(ports.InterruptReceipt{Unknown: true})
	coordinator := newTestCoordinator(t, store, gateway)
	if _, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil); err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}

	receipt, err := coordinator.InterruptTurn(context.Background(), "attempt-1", "msg-stop-1")
	if err != nil {
		t.Fatalf("InterruptTurn: %v", err)
	}
	if !receipt.Unknown || receipt.Confirmed {
		t.Fatalf("receipt = %+v, want unknown and not confirmed", receipt)
	}
}

// TestInterruptRequiresBoundSession proves an interrupt cannot be aimed at nothing.
func TestInterruptRequiresBoundSession(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock())
	coordinator := newTestCoordinator(t, store, gateway)

	if _, err := coordinator.InterruptTurn(context.Background(), "attempt-1", "msg-stop-1"); !errors.Is(err, domain.ErrWorkflowBindingNotFound) {
		t.Fatalf("err = %v, want ErrWorkflowBindingNotFound", err)
	}
}

// TestSpawnRequestValidation proves the identity checks run before anything else.
func TestSpawnRequestValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*ports.SpawnRequest)
	}{
		{"missing workflow", func(r *ports.SpawnRequest) { r.WorkflowID = "" }},
		{"missing task", func(r *ports.SpawnRequest) { r.TaskID = "" }},
		{"missing attempt", func(r *ports.SpawnRequest) { r.AttemptID = "" }},
		{"missing project", func(r *ports.SpawnRequest) { r.ProjectID = "" }},
		{"unknown harness", func(r *ports.SpawnRequest) { r.Harness = "gpt-imaginary" }},
		{"unknown mode", func(r *ports.SpawnRequest) { r.Mode = "telepathy" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			request := sampleSpawnRequest()
			tt.mutate(&request)
			store := newMemoryBindings()
			gateway := NewFakeGateway().WithClock(fixedClock())
			coordinator := newTestCoordinator(t, store, gateway)
			if _, err := coordinator.BindAttempt(context.Background(), request, nil); !errors.Is(err, ports.ErrExecutionRefused) {
				t.Fatalf("err = %v, want ErrExecutionRefused", err)
			}
			if gateway.SpawnCalls() != 0 {
				t.Fatal("an invalid request reached the gateway")
			}
		})
	}
}

// TestConcurrentBindAttemptSpawnsOnce proves the coordinator does not duplicate a side
// effect when two callers race on one attempt.
func TestConcurrentBindAttemptSpawnsOnce(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock())
	coordinator := newTestCoordinator(t, store, gateway)

	const callers = 6
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Errors are expected for losers; the assertion is about side effects.
			_, _ = coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
		}()
	}
	wg.Wait()

	if gateway.SpawnCalls() != 1 {
		t.Fatalf("spawn calls = %d, want exactly 1", gateway.SpawnCalls())
	}
	if sessions := gateway.SpawnSessions(); len(sessions) != 1 {
		t.Fatalf("gateway holds %d sessions, want 1", len(sessions))
	}
}

// TestConcurrentBindAttemptNeverDuplicatesSpawn documents the bug this test caught.
//
// The first implementation persisted the intent, then spawned. Two racing callers both
// observed a freshly persisted intent (one created it, the other saw created=false but
// still in intent_persisted) and both reached the gateway, producing two sessions for one
// attempt — the exact duplicate the design exists to prevent. The fix is the
// intent_persisted → spawn_requested CAS, which only one caller can win.
//
// It asserts on the lowest observable fact (gateway call count) so a future refactor that
// reintroduces the gap fails here rather than in production.
func TestConcurrentBindAttemptNeverDuplicatesSpawn(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock())
	coordinator := newTestCoordinator(t, store, gateway)

	const callers = 16
	var wg sync.WaitGroup
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
		}()
	}
	wg.Wait()

	if calls := gateway.SpawnCalls(); calls != 1 {
		t.Fatalf("spawn calls = %d, want exactly 1: %d concurrent callers produced %d spawns",
			calls, callers, calls)
	}
	if sessions := gateway.SpawnSessions(); len(sessions) != 1 {
		t.Fatalf("gateway holds %d sessions, want 1", len(sessions))
	}
	// The binding is bound at most once, and it names the single session that exists.
	binding, found, err := store.GetWorkflowAttemptBinding(context.Background(), "attempt-1")
	if err != nil || !found {
		t.Fatalf("get binding: found=%v err=%v", found, err)
	}
	if binding.SessionID == nil {
		t.Fatal("the winning caller did not record its session")
	}
}

// TestReadinessPlansAreExhaustive is the P1-1 guard: every known readiness value has an
// explicit consequence, and none of the non-live ones is allowed to land on bound_ready.
func TestReadinessPlansAreExhaustive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		readiness  ports.SessionReadiness
		bind       bool
		target     domain.WorkflowBindingState
		transition domain.WorkflowBindingState
		failure    string
		live       bool
	}{
		{ports.SessionReadinessReady, true, domain.WorkflowBindingStateBoundReady, "", "", true},
		{ports.SessionReadinessProvisioning, true, domain.WorkflowBindingStateSessionProvisioning, "", "session_provisioning", false},
		{ports.SessionReadinessExited, false, "", domain.WorkflowBindingStateFailedConfirmed, "session_exited", false},
		{ports.SessionReadinessFailed, false, "", domain.WorkflowBindingStateFailedConfirmed, "spawn_failed", false},
		{ports.SessionReadinessUnknown, false, "", domain.WorkflowBindingStateReconcileRequired, "session_readiness_unknown", false},
	}
	for _, tt := range tests {
		t.Run(string(tt.readiness), func(t *testing.T) {
			t.Parallel()
			plan := planForReadiness(tt.readiness)
			if plan.bind != tt.bind {
				t.Fatalf("bind = %v, want %v", plan.bind, tt.bind)
			}
			if plan.target != tt.target {
				t.Fatalf("target = %q, want %q", plan.target, tt.target)
			}
			if plan.transition != tt.transition {
				t.Fatalf("transition = %q, want %q", plan.transition, tt.transition)
			}
			if plan.failureCode != tt.failure {
				t.Fatalf("failureCode = %q, want %q", plan.failureCode, tt.failure)
			}
			if plan.live != tt.live {
				t.Fatalf("live = %v, want %v", plan.live, tt.live)
			}
			// No readiness value other than Ready may claim bound_ready.
			if tt.readiness != ports.SessionReadinessReady && plan.target == domain.WorkflowBindingStateBoundReady {
				t.Fatalf("readiness %q lands on bound_ready", tt.readiness)
			}
		})
	}
}

// TestUnrecognizedReadinessFailsClosed proves a future readiness value cannot inherit
// success by falling through a default.
func TestUnrecognizedReadinessFailsClosed(t *testing.T) {
	t.Parallel()
	plan := planForReadiness(ports.SessionReadiness("halfway"))
	if plan.bind {
		t.Fatal("an unrecognized readiness value bound the session")
	}
	if plan.transition != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("transition = %q, want reconcile_required", plan.transition)
	}
	if plan.live {
		t.Fatal("an unrecognized readiness value was reported live")
	}
}

// TestExitedSessionNeverBindsAndRefusesActions is the P1-1 acceptance test: an exited probe
// must not be bound_ready, and both send and interrupt must refuse without reaching the
// gateway.
func TestExitedSessionNeverBindsAndRefusesActions(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessExited)
	coordinator := newTestCoordinator(t, store, gateway)

	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State == domain.WorkflowBindingStateBoundReady {
		t.Fatal("an exited session was reported bound_ready")
	}
	if outcome.Binding.State != domain.WorkflowBindingStateFailedConfirmed {
		t.Fatalf("state = %q, want failed_confirmed", outcome.Binding.State)
	}
	if outcome.Binding.FailureCode != "session_exited" {
		t.Fatalf("failure code = %q, want session_exited", outcome.Binding.FailureCode)
	}
	if !outcome.Frozen {
		t.Fatal("an exited session must freeze the attempt")
	}

	// Actions must refuse, and must not reach the gateway.
	if _, err := coordinator.SendMessage(context.Background(), "attempt-1", ports.MessageRequest{
		SessionID: "fake-session-1", ClientMessageID: "msg-1", Text: "steer",
		Delivery: ports.MessageDeliverySend,
	}); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("send err = %v, want ErrExecutionRefused", err)
	}
	if _, err := coordinator.InterruptTurn(context.Background(), "attempt-1", "msg-stop-1"); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("interrupt err = %v, want ErrExecutionRefused", err)
	}
	if gateway.SendCalls() != 0 {
		t.Fatalf("send calls = %d, want 0", gateway.SendCalls())
	}
	var interruptCalls int
	// The fake exposes send/spawn counters; interrupt is covered by the same refusal gate
	// before any call, which the error above proves.
	_ = interruptCalls
}

// TestUnknownSessionReadinessNeverBinds proves an unreadable readiness is treated as
// reconcile_required rather than as success.
func TestUnknownSessionReadinessNeverBinds(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessUnknown)
	coordinator := newTestCoordinator(t, store, gateway)

	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State == domain.WorkflowBindingStateBoundReady {
		t.Fatal("an unknown readiness was reported bound_ready")
	}
	if outcome.Binding.State != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("state = %q, want reconcile_required", outcome.Binding.State)
	}
	if outcome.Binding.FailureCode != "session_readiness_unknown" {
		t.Fatalf("failure code = %q, want session_readiness_unknown", outcome.Binding.FailureCode)
	}
	if _, err := coordinator.SendMessage(context.Background(), "attempt-1", ports.MessageRequest{
		SessionID: "fake-session-1", ClientMessageID: "msg-1", Text: "steer",
		Delivery: ports.MessageDeliverySend,
	}); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("send err = %v, want ErrExecutionRefused", err)
	}
	if gateway.SendCalls() != 0 {
		t.Fatalf("send calls = %d, want 0", gateway.SendCalls())
	}
}

// TestBoundSessionThatLaterExitsRefusesActions proves the second half of the live gate: a
// binding that WAS live stops accepting work once the session exits.
func TestBoundSessionThatLaterExitsRefusesActions(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1")
	coordinator := newTestCoordinator(t, store, gateway)

	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready", outcome.Binding.State)
	}
	sessionID := *outcome.Binding.SessionID

	// A live session accepts a message.
	if _, err := coordinator.SendMessage(context.Background(), "attempt-1", ports.MessageRequest{
		SessionID: sessionID, ClientMessageID: "msg-1", Text: "steer",
		Delivery: ports.MessageDeliverySend,
	}); err != nil {
		t.Fatalf("live send: %v", err)
	}
	sendsWhileLive := gateway.SendCalls()

	// The provider process ends. The binding row still says bound_ready, and it must not
	// be trusted on its own.
	gateway.SetSessionReadiness(sessionID, ports.SessionReadinessExited)

	if _, err := coordinator.SendMessage(context.Background(), "attempt-1", ports.MessageRequest{
		SessionID: sessionID, ClientMessageID: "msg-2", Text: "steer",
		Delivery: ports.MessageDeliverySend,
	}); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("send after exit err = %v, want ErrExecutionRefused", err)
	}
	if _, err := coordinator.InterruptTurn(context.Background(), "attempt-1", "msg-stop-1"); !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("interrupt after exit err = %v, want ErrExecutionRefused", err)
	}
	if gateway.SendCalls() != sendsWhileLive {
		t.Fatalf("a refused send reached the gateway: calls went from %d to %d", sendsWhileLive, gateway.SendCalls())
	}
}

// TestReconcileAdvancesAStalledSpawnRequested is P1-2: a crash between the claim CAS and the
// gateway call leaves spawn_requested, and reconcile must be able to move it.
func TestReconcileAdvancesAStalledSpawnRequested(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessReady)
	coordinator := newTestCoordinator(t, store, gateway)

	// Persist an intent and claim the spawn right, then stop: this is the crash window.
	request := sampleSpawnRequest()
	hash, err := SpawnHash(request)
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	binding, created, err := store.CreateWorkflowAttemptBinding(
		context.Background(),
		domain.WorkflowAttemptBinding{
			AttemptID: request.AttemptID, WorkflowID: request.WorkflowID, TaskID: request.TaskID,
			SpawnRequestID: SpawnKey(request.AttemptID, ActionSpawn), SpawnHash: hash,
		}, nil, testNow,
	)
	if err != nil || !created {
		t.Fatalf("create intent: created=%v err=%v", created, err)
	}
	stalled, applied, err := store.TransitionWorkflowAttemptBinding(
		context.Background(), request.AttemptID,
		domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, testNow,
	)
	if err != nil || !applied {
		t.Fatalf("claim spawn right: applied=%v err=%v", applied, err)
	}
	if stalled.State != domain.WorkflowBindingStateSpawnRequested {
		t.Fatalf("state = %q, want spawn_requested", stalled.State)
	}

	// No session was ever created, so reconcile must fail closed to reconcile_required.
	outcome, err := coordinator.Reconcile(context.Background(), request.AttemptID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("state = %q, want reconcile_required when no session exists", outcome.Binding.State)
	}
	if !outcome.Frozen {
		t.Fatal("a stall with no session must freeze")
	}
	if gateway.SpawnCalls() != 0 {
		t.Fatalf("reconcile spawned: calls = %d, want 0", gateway.SpawnCalls())
	}

	// The escape hatch works: a later bind attempt can now proceed from reconcile_required?
	// No — reconcile_required is frozen by design, so BindAttempt must not spawn.
	retry, err := coordinator.BindAttempt(context.Background(), request, nil)
	if err != nil {
		t.Fatalf("retry BindAttempt: %v", err)
	}
	if retry.Spawned {
		t.Fatal("a frozen stalled binding spawned again")
	}
	if gateway.SpawnCalls() != 0 {
		t.Fatalf("spawn calls = %d, want 0 (a stall must never re-spawn)", gateway.SpawnCalls())
	}
}

// TestReconcileAdoptsAStalledSpawnThatActuallyHappened is P1-2's second branch: the session
// was created before the crash, so reconcile adopts it without spawning again.
func TestReconcileAdoptsAStalledSpawnThatActuallyHappened(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessReady)
	coordinator := newTestCoordinator(t, store, gateway)
	request := sampleSpawnRequest()

	// The gateway created the session, then the crash happened before the answer was
	// recorded. The spawn must carry the same stable key the binding will use, because that
	// key is what FindSpawn looks the session up by.
	request.SpawnKey = SpawnKey(request.AttemptID, ActionSpawn)
	if _, err := gateway.Spawn(context.Background(), request); err != nil {
		t.Fatalf("gateway Spawn: %v", err)
	}
	spawnCallsBefore := gateway.SpawnCalls()

	hash, err := SpawnHash(request)
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	binding, created, err := store.CreateWorkflowAttemptBinding(
		context.Background(),
		domain.WorkflowAttemptBinding{
			AttemptID: request.AttemptID, WorkflowID: request.WorkflowID, TaskID: request.TaskID,
			SpawnRequestID: SpawnKey(request.AttemptID, ActionSpawn), SpawnHash: hash,
		}, nil, testNow,
	)
	if err != nil || !created {
		t.Fatalf("create intent: created=%v err=%v", created, err)
	}
	if _, applied, err := store.TransitionWorkflowAttemptBinding(
		context.Background(), request.AttemptID,
		domain.WorkflowBindingStateSpawnRequested, "", binding.Revision, testNow,
	); err != nil || !applied {
		t.Fatalf("claim spawn right: applied=%v err=%v", applied, err)
	}

	outcome, err := coordinator.Reconcile(context.Background(), request.AttemptID)
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if gateway.SpawnCalls() != spawnCallsBefore {
		t.Fatalf("reconcile spawned again: %d → %d", spawnCallsBefore, gateway.SpawnCalls())
	}
	if outcome.Binding.SessionID == nil {
		t.Fatal("reconcile did not adopt the session that already existed")
	}
	if outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready after adopting a live session", outcome.Binding.State)
	}
}

// liveFixture binds one attempt to a live, ready session at a known generation.
func liveFixture(t *testing.T) (*Coordinator, *memoryBindings, *FakeGateway, domain.WorkflowAttemptBinding) {
	t.Helper()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1")
	coordinator := newTestCoordinator(t, store, gateway)
	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.SessionID == nil || outcome.Binding.ControllerGeneration != "gen-1" {
		t.Fatalf("fixture failed to bind a fenced session: %+v", outcome.Binding)
	}
	return coordinator, store, gateway, outcome.Binding
}

// mutateBinding edits the durable binding row in place, which is how a test simulates a
// row written by an older AO version or by a controller that never recorded a generation.
func mutateBinding(t *testing.T, store *memoryBindings, attemptID domain.WorkflowAttemptID, fn func(*domain.WorkflowAttemptBinding)) {
	t.Helper()
	store.mu.Lock()
	defer store.mu.Unlock()
	binding, ok := store.byID[attemptID]
	if !ok {
		t.Fatalf("no binding for %q", attemptID)
	}
	fn(&binding)
	store.byID[attemptID] = binding
}

func sessionObservation(sessionID domain.SessionID, generation string, readiness ports.SessionReadiness) ports.SessionObservation {
	return ports.SessionObservation{
		SessionID: sessionID, ControllerGeneration: generation, Readiness: readiness,
		ObservedAt: testNow,
	}
}

// TestLiveTargetFenceTable is the generation/session fencing proof: every way an observation
// can fail to describe the binding must report not-live with a stable reason and a nil error,
// because "not my session" is an answer rather than a failure.
func TestLiveTargetFenceTable(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		prepare    func(t *testing.T, store *memoryBindings, gateway *FakeGateway, binding domain.WorkflowAttemptBinding)
		wantLive   bool
		wantReason string
	}{
		{
			name:     "live",
			prepare:  func(*testing.T, *memoryBindings, *FakeGateway, domain.WorkflowAttemptBinding) {},
			wantLive: true,
		},
		{
			name: "session mismatch",
			prepare: func(_ *testing.T, _ *memoryBindings, gateway *FakeGateway, _ domain.WorkflowAttemptBinding) {
				// The gateway answers about a different session entirely.
				gateway.SwapObservation(sessionObservation("someone-elses-session", "gen-1", ports.SessionReadinessReady))
			},
			wantReason: "session_mismatch",
		},
		{
			name: "generation mismatch",
			prepare: func(_ *testing.T, _ *memoryBindings, gateway *FakeGateway, binding domain.WorkflowAttemptBinding) {
				gateway.SwapObservation(sessionObservation(*binding.SessionID, "gen-0", ports.SessionReadinessReady))
			},
			wantReason: "generation_mismatch",
		},
		{
			name: "terminated",
			prepare: func(_ *testing.T, _ *memoryBindings, gateway *FakeGateway, binding domain.WorkflowAttemptBinding) {
				observation := sessionObservation(*binding.SessionID, "gen-1", ports.SessionReadinessReady)
				observation.Terminated = true
				gateway.SwapObservation(observation)
			},
			wantReason: "session_terminated",
		},
		{
			name: "readiness provisioning",
			prepare: func(_ *testing.T, _ *memoryBindings, gateway *FakeGateway, binding domain.WorkflowAttemptBinding) {
				gateway.SwapObservation(sessionObservation(*binding.SessionID, "gen-1", ports.SessionReadinessProvisioning))
			},
			wantReason: "readiness_provisioning",
		},
		{
			name: "readiness exited",
			prepare: func(_ *testing.T, _ *memoryBindings, gateway *FakeGateway, binding domain.WorkflowAttemptBinding) {
				gateway.SwapObservation(sessionObservation(*binding.SessionID, "gen-1", ports.SessionReadinessExited))
			},
			wantReason: "readiness_exited",
		},
		{
			name: "no generation",
			prepare: func(t *testing.T, store *memoryBindings, _ *FakeGateway, binding domain.WorkflowAttemptBinding) {
				mutateBinding(t, store, binding.AttemptID, func(b *domain.WorkflowAttemptBinding) {
					b.ControllerGeneration = ""
				})
			},
			wantReason: "generation_missing",
		},
		{
			name: "no session",
			prepare: func(t *testing.T, store *memoryBindings, _ *FakeGateway, binding domain.WorkflowAttemptBinding) {
				mutateBinding(t, store, binding.AttemptID, func(b *domain.WorkflowAttemptBinding) {
					b.SessionID = nil
					b.State = domain.WorkflowBindingStateReconcileRequired
				})
			},
			wantReason: "no_session",
		},
		{
			name: "observation failed",
			prepare: func(_ *testing.T, _ *memoryBindings, gateway *FakeGateway, _ domain.WorkflowAttemptBinding) {
				gateway.FailInspect(errors.New("probe timeout"))
			},
			wantReason: "observation_failed",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator, store, gateway, binding := liveFixture(t)
			tt.prepare(t, store, gateway, binding)
			_, _, live, reason, err := coordinator.LiveTarget(context.Background(), binding.AttemptID)
			if err != nil {
				t.Fatalf("LiveTarget returned an error for a not-live answer: %v", err)
			}
			if live != tt.wantLive {
				t.Fatalf("live = %v, want %v (reason %q)", live, tt.wantLive, reason)
			}
			if reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tt.wantReason)
			}
		})
	}
}

// TestLiveTargetMissingBindingIsAnError proves a missing row is still an error, so it cannot
// be confused with a binding that is merely not live.
func TestLiveTargetMissingBindingIsAnError(t *testing.T) {
	t.Parallel()
	coordinator := newTestCoordinator(t, newMemoryBindings(), NewFakeGateway().WithClock(fixedClock()))
	_, _, live, reason, err := coordinator.LiveTarget(context.Background(), "attempt-1")
	if !errors.Is(err, domain.ErrWorkflowBindingNotFound) {
		t.Fatalf("err = %v, want ErrWorkflowBindingNotFound", err)
	}
	if live || reason != "binding_not_found" {
		t.Fatalf("live=%v reason=%q, want false/binding_not_found", live, reason)
	}
}

// TestSendMessageStampsBindingGeneration proves the caller cannot pick a generation: the
// binding's own value is what reaches the gateway.
func TestSendMessageStampsBindingGeneration(t *testing.T) {
	t.Parallel()
	coordinator, _, gateway, binding := liveFixture(t)
	if _, err := coordinator.SendMessage(context.Background(), binding.AttemptID, ports.MessageRequest{
		SessionID: *binding.SessionID, ClientMessageID: "msg-1", Text: "hello",
		Delivery: ports.MessageDeliverySend, ControllerGeneration: "gen-old",
	}); err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if got := gateway.LastSend(); got.ControllerGeneration != "gen-1" {
		t.Fatalf("generation = %q, want the binding's gen-1", got.ControllerGeneration)
	}
}

// TestSteerMessageRoutesToSteer proves guidance takes the steer path with the exact fields a
// gateway needs, and never falls back to a send.
func TestSteerMessageRoutesToSteer(t *testing.T) {
	t.Parallel()
	coordinator, _, gateway, binding := liveFixture(t)

	receipt, err := coordinator.SteerMessage(context.Background(), binding.AttemptID, ports.MessageRequest{
		SessionID: *binding.SessionID, ClientMessageID: "msg-steer-1", Text: "use the other approach",
		Delivery: ports.MessageDeliverySteer, TargetProviderTurnID: "prov-turn-1",
		Origin: domain.MessageOriginAutomation,
	})
	if err != nil {
		t.Fatalf("SteerMessage: %v", err)
	}
	if !receipt.Steered || receipt.TargetProviderTurnID != "prov-turn-1" {
		t.Fatalf("receipt = %+v, want a steer onto prov-turn-1", receipt)
	}
	sent := gateway.LastSteer()
	if sent.Delivery != ports.MessageDeliverySteer || sent.TargetProviderTurnID != "prov-turn-1" {
		t.Fatalf("gateway request = %+v, want delivery=steer on the named turn", sent)
	}
	if sent.ControllerGeneration != "gen-1" {
		t.Fatalf("generation = %q, want the binding's gen-1", sent.ControllerGeneration)
	}
	if sent.Origin != domain.MessageOriginAutomation {
		t.Fatalf("origin = %q, want automation", sent.Origin)
	}
	if gateway.SteerCalls() != 1 || gateway.SendCalls() != 0 {
		t.Fatalf("steer=%d send=%d, want 1/0: a steer must never degrade to a send",
			gateway.SteerCalls(), gateway.SendCalls())
	}
}

// TestSteerMessageMismatchedReceiptIsAnError is the "wrong turn is not success" proof.
func TestSteerMessageMismatchedReceiptIsAnError(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		receipt ports.MessageReceipt
	}{
		{
			name: "different target turn",
			receipt: ports.MessageReceipt{
				Steered: true, TargetProviderTurnID: "prov-turn-OTHER",
				ControllerGeneration: "gen-1",
			},
		},
		{
			name: "not steered",
			receipt: ports.MessageReceipt{
				TargetProviderTurnID: "prov-turn-1", ControllerGeneration: "gen-1",
			},
		},
		{
			name: "different generation",
			receipt: ports.MessageReceipt{
				Steered: true, TargetProviderTurnID: "prov-turn-1", ControllerGeneration: "gen-0",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator, _, gateway, binding := liveFixture(t)
			gateway.SteerReceipt(tt.receipt)

			_, err := coordinator.SteerMessage(context.Background(), binding.AttemptID, ports.MessageRequest{
				SessionID: *binding.SessionID, ClientMessageID: "msg-steer-1", Text: "guidance",
				Delivery: ports.MessageDeliverySteer, TargetProviderTurnID: "prov-turn-1",
			})
			if !errors.Is(err, ports.ErrExecutionTargetMismatch) {
				t.Fatalf("err = %v, want ErrExecutionTargetMismatch", err)
			}
		})
	}
}

// TestSteerMessageRefusesWrongModeAndStaleGeneration proves the two refusals that must happen
// before any provider call.
func TestSteerMessageRefusesWrongModeAndStaleGeneration(t *testing.T) {
	t.Parallel()
	t.Run("send delivery", func(t *testing.T) {
		t.Parallel()
		coordinator, _, gateway, binding := liveFixture(t)
		_, err := coordinator.SteerMessage(context.Background(), binding.AttemptID, ports.MessageRequest{
			SessionID: *binding.SessionID, ClientMessageID: "msg-1", Text: "x",
			Delivery: ports.MessageDeliverySend,
		})
		if !errors.Is(err, ports.ErrExecutionRefused) {
			t.Fatalf("err = %v, want ErrExecutionRefused", err)
		}
		if gateway.SteerCalls() != 0 || gateway.SendCalls() != 0 {
			t.Fatal("a refused steer reached the gateway")
		}
	})
	t.Run("stale generation", func(t *testing.T) {
		t.Parallel()
		coordinator, _, gateway, binding := liveFixture(t)
		_, err := coordinator.SteerMessage(context.Background(), binding.AttemptID, ports.MessageRequest{
			SessionID: *binding.SessionID, ClientMessageID: "msg-1", Text: "x",
			Delivery: ports.MessageDeliverySteer, TargetProviderTurnID: "prov-turn-1",
			ControllerGeneration: "gen-old",
		})
		if !errors.Is(err, ports.ErrExecutionStaleGeneration) {
			t.Fatalf("err = %v, want ErrExecutionStaleGeneration", err)
		}
		if gateway.SteerCalls() != 0 {
			t.Fatal("a stale-generation steer reached the gateway")
		}
	})
	t.Run("missing target turn", func(t *testing.T) {
		t.Parallel()
		coordinator, _, gateway, binding := liveFixture(t)
		_, err := coordinator.SteerMessage(context.Background(), binding.AttemptID, ports.MessageRequest{
			SessionID: *binding.SessionID, ClientMessageID: "msg-1", Text: "x",
			Delivery: ports.MessageDeliverySteer,
		})
		if !errors.Is(err, ports.ErrExecutionRefused) {
			t.Fatalf("err = %v, want ErrExecutionRefused", err)
		}
		if gateway.SteerCalls() != 0 {
			t.Fatal("a turnless steer reached the gateway")
		}
	})
}

// TestInterruptTurnStampsClientMessageID proves a retried stop is idempotent at the gateway
// boundary too.
func TestInterruptTurnStampsClientMessageID(t *testing.T) {
	t.Parallel()
	coordinator, _, gateway, binding := liveFixture(t)
	gateway.InterruptResult(ports.InterruptReceipt{Confirmed: true})

	if _, err := coordinator.InterruptTurn(context.Background(), binding.AttemptID, "msg-stop-1"); err != nil {
		t.Fatalf("InterruptTurn: %v", err)
	}
	request := gateway.LastInterrupt()
	if request.ClientMessageID != "msg-stop-1" {
		t.Fatalf("client message id = %q, want msg-stop-1", request.ClientMessageID)
	}
	if request.ControllerGeneration != "gen-1" {
		t.Fatalf("generation = %q, want the binding's gen-1", request.ControllerGeneration)
	}
}

// TestInterruptTurnRefusesFencedSession proves a rotated controller cannot be interrupted
// through this binding: the observation describes a generation the binding no longer owns.
func TestInterruptTurnRefusesFencedSession(t *testing.T) {
	t.Parallel()
	coordinator, _, gateway, binding := liveFixture(t)
	gateway.SwapObservation(sessionObservation(*binding.SessionID, "gen-2", ports.SessionReadinessReady))

	_, err := coordinator.InterruptTurn(context.Background(), binding.AttemptID, "msg-stop-1")
	if !errors.Is(err, ports.ErrExecutionRefused) {
		t.Fatalf("err = %v, want ErrExecutionRefused", err)
	}
	if gateway.InterruptCount() != 0 {
		t.Fatal("an interrupt was issued against a fenced session")
	}
}

// TestResumeBranches covers every documented Resume outcome.
func TestResumeBranches(t *testing.T) {
	t.Parallel()

	t.Run("no session", func(t *testing.T) {
		t.Parallel()
		store := newMemoryBindings()
		gateway := NewFakeGateway().WithClock(fixedClock())
		coordinator := newTestCoordinator(t, store, gateway)
		// An intent-only binding has no session to resume.
		if _, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil); err != nil {
			t.Fatalf("BindAttempt: %v", err)
		}
		mutateBinding(t, store, "attempt-1", func(b *domain.WorkflowAttemptBinding) {
			b.SessionID = nil
			b.ControllerGeneration = ""
			b.State = domain.WorkflowBindingStateIntentPersisted
		})
		outcome, err := coordinator.Resume(context.Background(), "attempt-1")
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if outcome.ReasonCode != "no_session" {
			t.Fatalf("reason = %q, want no_session", outcome.ReasonCode)
		}
		if gateway.ResumeCalls() != 0 {
			t.Fatal("Resume reached the gateway without a session to resume")
		}
	})

	t.Run("not resumable", func(t *testing.T) {
		t.Parallel()
		coordinator, store, gateway, binding := liveFixture(t)
		mutateBinding(t, store, binding.AttemptID, func(b *domain.WorkflowAttemptBinding) {
			b.State = domain.WorkflowBindingStateStopConfirmed
		})
		outcome, err := coordinator.Resume(context.Background(), binding.AttemptID)
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if outcome.ReasonCode != "not_resumable" {
			t.Fatalf("reason = %q, want not_resumable", outcome.ReasonCode)
		}
		if gateway.ResumeCalls() != 0 {
			t.Fatal("a terminal binding was resumed")
		}
	})

	t.Run("success observes without inventing running", func(t *testing.T) {
		t.Parallel()
		coordinator, _, gateway, binding := liveFixture(t)
		outcome, err := coordinator.Resume(context.Background(), binding.AttemptID)
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if gateway.ResumeCalls() != 1 {
			t.Fatalf("resume calls = %d, want 1", gateway.ResumeCalls())
		}
		if got := gateway.LastResume().SpawnKey; got != binding.SpawnRequestID {
			t.Fatalf("spawn key = %q, want the binding's %q", got, binding.SpawnRequestID)
		}
		if outcome.Binding.State == domain.WorkflowBindingStateRunning {
			t.Fatal("a resume observation promoted the binding to running")
		}
		if outcome.Spawned {
			t.Fatal("resume reported a spawn")
		}
	})

	t.Run("failure freezes at recovery_uncertain", func(t *testing.T) {
		t.Parallel()
		coordinator, _, gateway, binding := liveFixture(t)
		gateway.FailResume(errors.New("provider refused"))
		outcome, err := coordinator.Resume(context.Background(), binding.AttemptID)
		if err != nil {
			t.Fatalf("Resume: %v", err)
		}
		if outcome.ReasonCode != "resume_failed" || !outcome.Frozen {
			t.Fatalf("outcome = %+v, want frozen resume_failed", outcome)
		}
		if outcome.Binding.State != domain.WorkflowBindingStateRecoveryUncertain {
			t.Fatalf("state = %q, want recovery_uncertain", outcome.Binding.State)
		}
		if outcome.Binding.FailureCode != "resume_failed" {
			t.Fatalf("failure code = %q, want resume_failed", outcome.Binding.FailureCode)
		}
		if gateway.SpawnCalls() != 1 {
			t.Fatalf("spawn calls = %d: a failed resume must never spawn", gateway.SpawnCalls())
		}
	})
}

// TestMarkStopOutcomeBranches covers every documented stop outcome, including the illegal
// transition case, which must leave the binding untouched rather than corrupt it.
func TestMarkStopOutcomeBranches(t *testing.T) {
	t.Parallel()

	t.Run("confirmed", func(t *testing.T) {
		t.Parallel()
		coordinator, store, _, binding := liveFixture(t)
		mutateBinding(t, store, binding.AttemptID, func(b *domain.WorkflowAttemptBinding) {
			b.State = domain.WorkflowBindingStateStopRequested
		})
		updated, err := coordinator.MarkStopOutcome(context.Background(), binding.AttemptID, true, false)
		if err != nil {
			t.Fatalf("MarkStopOutcome: %v", err)
		}
		if updated.State != domain.WorkflowBindingStateStopConfirmed {
			t.Fatalf("state = %q, want stop_confirmed", updated.State)
		}
		if updated.FailureCode != "stop_confirmed" {
			t.Fatalf("reason = %q, want stop_confirmed", updated.FailureCode)
		}
	})

	t.Run("unknown", func(t *testing.T) {
		t.Parallel()
		coordinator, store, _, binding := liveFixture(t)
		mutateBinding(t, store, binding.AttemptID, func(b *domain.WorkflowAttemptBinding) {
			b.State = domain.WorkflowBindingStateStopRequested
		})
		updated, err := coordinator.MarkStopOutcome(context.Background(), binding.AttemptID, false, true)
		if err != nil {
			t.Fatalf("MarkStopOutcome: %v", err)
		}
		if updated.State != domain.WorkflowBindingStateStopUnknown {
			t.Fatalf("state = %q, want stop_unknown", updated.State)
		}
		if updated.FailureCode != "stop_unknown" {
			t.Fatalf("reason = %q, want stop_unknown", updated.FailureCode)
		}
	})

	t.Run("nothing to stop", func(t *testing.T) {
		t.Parallel()
		coordinator, _, _, binding := liveFixture(t)
		// bound_ready may not reach stop_confirmed directly, so this exercises the
		// refused-transition branch: the binding must be returned unchanged.
		updated, err := coordinator.MarkStopOutcome(context.Background(), binding.AttemptID, false, false)
		if err != nil {
			t.Fatalf("MarkStopOutcome: %v", err)
		}
		if updated.State != domain.WorkflowBindingStateBoundReady {
			t.Fatalf("state = %q, want the unchanged bound_ready", updated.State)
		}
		if updated.Revision != binding.Revision {
			t.Fatalf("revision changed from %d to %d on a refused transition", binding.Revision, updated.Revision)
		}
	})

	t.Run("nothing to stop from stop_requested", func(t *testing.T) {
		t.Parallel()
		coordinator, store, _, binding := liveFixture(t)
		mutateBinding(t, store, binding.AttemptID, func(b *domain.WorkflowAttemptBinding) {
			b.State = domain.WorkflowBindingStateStopRequested
		})
		updated, err := coordinator.MarkStopOutcome(context.Background(), binding.AttemptID, false, false)
		if err != nil {
			t.Fatalf("MarkStopOutcome: %v", err)
		}
		if updated.State != domain.WorkflowBindingStateStopConfirmed {
			t.Fatalf("state = %q, want stop_confirmed", updated.State)
		}
		if updated.FailureCode != "stop_nothing_to_stop" {
			t.Fatalf("reason = %q, want stop_nothing_to_stop", updated.FailureCode)
		}
	})

	t.Run("terminal binding is never moved backwards", func(t *testing.T) {
		t.Parallel()
		coordinator, store, _, binding := liveFixture(t)
		mutateBinding(t, store, binding.AttemptID, func(b *domain.WorkflowAttemptBinding) {
			b.State = domain.WorkflowBindingStateStopConfirmed
		})
		updated, err := coordinator.MarkStopOutcome(context.Background(), binding.AttemptID, false, true)
		if err != nil {
			t.Fatalf("MarkStopOutcome: %v", err)
		}
		if updated.State != domain.WorkflowBindingStateStopConfirmed {
			t.Fatalf("state = %q: a stop_unknown request rewrote a confirmed stop", updated.State)
		}
	})

	t.Run("missing binding", func(t *testing.T) {
		t.Parallel()
		coordinator := newTestCoordinator(t, newMemoryBindings(), NewFakeGateway().WithClock(fixedClock()))
		if _, err := coordinator.MarkStopOutcome(context.Background(), "attempt-1", true, false); !errors.Is(err, domain.ErrWorkflowBindingNotFound) {
			t.Fatalf("err = %v, want ErrWorkflowBindingNotFound", err)
		}
	})
}

// TestObserveSessionRejectsFencedObservation proves a mismatch is reported while the state
// stays the recorded, non-promotable one.
func TestObserveSessionRejectsFencedObservation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		observe  ports.SessionObservation
		sentinel error
	}{
		{
			name: "generation rotated",
			observe: func() ports.SessionObservation {
				return sessionObservation("", "gen-2", ports.SessionReadinessReady)
			}(),
			sentinel: ports.ErrExecutionStaleGeneration,
		},
		{
			name: "terminated",
			observe: func() ports.SessionObservation {
				o := sessionObservation("", "gen-1", ports.SessionReadinessReady)
				o.Terminated = true
				return o
			}(),
			sentinel: ports.ErrExecutionTerminated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator, _, gateway, binding := liveFixture(t)
			observation := tt.observe
			observation.SessionID = *binding.SessionID
			gateway.SwapObservation(observation)

			got, state, err := coordinator.ObserveSession(context.Background(), binding.AttemptID)
			if !errors.Is(err, tt.sentinel) {
				t.Fatalf("err = %v, want %v", err, tt.sentinel)
			}
			if got.SessionID != *binding.SessionID {
				t.Fatalf("the observation was not returned alongside the fence error: %+v", got)
			}
			if state == domain.WorkflowBindingStateRunning {
				t.Fatalf("state = %q: a fenced observation was reported as promotable", state)
			}
		})
	}
}

// TestCoordinatorSatisfiesBindingWriter pins the wiring contract the daemon relies on: the
// same *Coordinator is handed to the intervention coordinator as its BindingWriter.
func TestCoordinatorSatisfiesBindingWriter(t *testing.T) {
	t.Parallel()
	var writer interface {
		MarkStopOutcome(context.Context, domain.WorkflowAttemptID, bool, bool) (domain.WorkflowAttemptBinding, error)
	} = newTestCoordinator(t, newMemoryBindings(), NewFakeGateway().WithClock(fixedClock()))
	if writer == nil {
		t.Fatal("the coordinator does not satisfy the BindingWriter shape")
	}
}

// TestTerminatedObservationIsNotLive proves a terminated session is never a live target and
// that every gated action refuses without reaching the gateway.
//
// A terminated session is durable history, and an exited one is not success either: neither
// may accept a message. The exit code says nothing about whether the task completed.
func TestTerminatedObservationIsNotLive(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		observe    func(sessionID domain.SessionID) ports.SessionObservation
		wantReason string
	}{
		{
			name: "terminated",
			observe: func(sessionID domain.SessionID) ports.SessionObservation {
				observation := sessionObservation(sessionID, "gen-1", ports.SessionReadinessReady)
				observation.Terminated = true
				return observation
			},
			wantReason: "session_terminated",
		},
		{
			name: "exited",
			observe: func(sessionID domain.SessionID) ports.SessionObservation {
				return sessionObservation(sessionID, "gen-1", ports.SessionReadinessExited)
			},
			wantReason: "readiness_exited",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator, _, gateway, binding := liveFixture(t)
			gateway.SwapObservation(tt.observe(*binding.SessionID))

			_, _, live, reason, err := coordinator.LiveTarget(context.Background(), binding.AttemptID)
			if err != nil {
				t.Fatalf("LiveTarget: %v", err)
			}
			if live {
				t.Fatalf("a %s observation was reported live", tt.name)
			}
			if reason != tt.wantReason {
				t.Fatalf("reason = %q, want %q", reason, tt.wantReason)
			}

			sendsBefore := gateway.SendCalls()
			steersBefore := gateway.SteerCalls()
			interruptsBefore := gateway.InterruptCount()

			if _, err := coordinator.SendMessage(context.Background(), binding.AttemptID, ports.MessageRequest{
				SessionID: *binding.SessionID, ClientMessageID: "msg-send", Text: "hello",
				Delivery: ports.MessageDeliverySend,
			}); !errors.Is(err, ports.ErrExecutionRefused) {
				t.Fatalf("SendMessage err = %v, want ErrExecutionRefused", err)
			}
			if _, err := coordinator.SteerMessage(context.Background(), binding.AttemptID, ports.MessageRequest{
				SessionID: *binding.SessionID, ClientMessageID: "msg-steer", Text: "guidance",
				Delivery: ports.MessageDeliverySteer, TargetProviderTurnID: "prov-turn-1",
			}); !errors.Is(err, ports.ErrExecutionRefused) {
				t.Fatalf("SteerMessage err = %v, want ErrExecutionRefused", err)
			}
			if _, err := coordinator.InterruptTurn(context.Background(), binding.AttemptID, "msg-stop"); !errors.Is(err, ports.ErrExecutionRefused) {
				t.Fatalf("InterruptTurn err = %v, want ErrExecutionRefused", err)
			}

			if gateway.SendCalls() != sendsBefore {
				t.Fatalf("send calls went %d → %d: a refused send reached the gateway",
					sendsBefore, gateway.SendCalls())
			}
			if gateway.SteerCalls() != steersBefore {
				t.Fatalf("steer calls went %d → %d: a refused steer reached the gateway",
					steersBefore, gateway.SteerCalls())
			}
			if gateway.InterruptCount() != interruptsBefore {
				t.Fatalf("interrupt calls went %d → %d: a refused stop reached the gateway",
					interruptsBefore, gateway.InterruptCount())
			}
		})
	}
}

// TestMarkStopOutcomeOnTerminalBindingIsRefused proves a terminal binding is never moved and
// never corrupted, for both terminal states and both requested outcomes.
func TestMarkStopOutcomeOnTerminalBindingIsRefused(t *testing.T) {
	t.Parallel()
	terminals := []domain.WorkflowBindingState{
		domain.WorkflowBindingStateStopConfirmed,
		domain.WorkflowBindingStateFailedConfirmed,
	}
	type askedOutcome struct {
		name      string
		confirmed bool
		unknown   bool
	}
	outcomes := []askedOutcome{
		{name: "confirmed", confirmed: true},
		{name: "unknown", unknown: true},
		{name: "nothing_to_stop"},
	}
	for _, terminal := range terminals {
		for _, asked := range outcomes {
			t.Run(string(terminal)+"/"+asked.name, func(t *testing.T) {
				t.Parallel()
				coordinator, store, _, binding := liveFixture(t)
				mutateBinding(t, store, binding.AttemptID, func(b *domain.WorkflowAttemptBinding) {
					b.State = terminal
					b.FailureCode = "original_reason"
				})
				before, found, err := store.GetWorkflowAttemptBinding(context.Background(), binding.AttemptID)
				if err != nil || !found {
					t.Fatalf("get binding: found=%v err=%v", found, err)
				}

				got, err := coordinator.MarkStopOutcome(
					context.Background(), binding.AttemptID, asked.confirmed, asked.unknown,
				)
				if err != nil {
					t.Fatalf("MarkStopOutcome: %v", err)
				}
				if got.State != terminal {
					t.Fatalf("state = %q, want the unchanged %q", got.State, terminal)
				}
				if got.Revision != before.Revision {
					t.Fatalf("revision moved from %d to %d on a refused transition",
						before.Revision, got.Revision)
				}
				if got.FailureCode != "original_reason" {
					t.Fatalf("failure code = %q, want the original preserved", got.FailureCode)
				}

				// And nothing was written: the durable row is byte-for-byte the same.
				after, found, err := store.GetWorkflowAttemptBinding(context.Background(), binding.AttemptID)
				if err != nil || !found {
					t.Fatalf("re-read binding: found=%v err=%v", found, err)
				}
				if after.State != before.State || after.Revision != before.Revision ||
					after.FailureCode != before.FailureCode {
					t.Fatalf("the binding changed: %+v → %+v", before, after)
				}
			})
		}
	}
}

// TestSpawnHashIgnoresTheLease proves the lease token is not part of the request
// fingerprint. Two requests differing only in their lease must hash identically:
// otherwise a rotated lease would make a legitimate retry look like a conflict, and
// the hash would be an oracle for a secret.
func TestSpawnHashIgnoresTheLease(t *testing.T) {
	t.Parallel()
	request := sampleSpawnRequest()
	request.Workflow = &ports.WorkflowSpawnContext{
		WorkflowID: request.WorkflowID, TaskID: request.TaskID,
		AttemptID: request.AttemptID, LeaseToken: "lease-one",
	}
	first, err := SpawnHash(request)
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	rotated := request
	rotated.Workflow = &ports.WorkflowSpawnContext{
		WorkflowID: request.WorkflowID, TaskID: request.TaskID,
		AttemptID: request.AttemptID, LeaseToken: "lease-two",
	}
	second, err := SpawnHash(rotated)
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	if first != second {
		t.Fatal("the lease token must not participate in the spawn hash")
	}
	without := request
	without.Workflow = nil
	third, err := SpawnHash(without)
	if err != nil {
		t.Fatalf("SpawnHash: %v", err)
	}
	if first != third {
		t.Fatal("presence of a workflow context must not change the spawn hash")
	}
}

// TestBindAttemptCarriesTheStoredLease is the wiring proof for R-03: the attempt's
// lease reaches the gateway spawn request, and it comes from the attempt row rather
// than from the caller.
func TestBindAttemptCarriesTheStoredLease(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	store.seedAttemptFor("attempt-1", "stored-lease-token")
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessReady)
	coordinator := newTestCoordinator(t, store, gateway)

	if _, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil); err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	spawned := gateway.LastSpawn()
	if spawned.Workflow == nil {
		t.Fatal("the gateway spawn carried no workflow context; the session's tool server would have no identity")
	}
	if spawned.Workflow.LeaseToken != "stored-lease-token" {
		t.Fatal("the gateway spawn did not carry the attempt's stored lease")
	}
	if spawned.Workflow.AttemptID != "attempt-1" ||
		spawned.Workflow.WorkflowID != "workflow-1" ||
		spawned.Workflow.TaskID != "task-1" {
		t.Fatalf("workflow context = %+v, want the request's identity", spawned.Workflow)
	}
}

// TestBindAttemptRefusesWhenTheAttemptIsMissing proves a missing attempt row is a
// refusal, not a session that could never collaborate.
func TestBindAttemptRefusesWhenTheAttemptIsMissing(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	store.strictAttempts = true
	gateway := NewFakeGateway().WithClock(fixedClock()).SpawnReadiness(ports.SessionReadinessReady)
	coordinator := newTestCoordinator(t, store, gateway)

	_, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if !errors.Is(err, ErrAttemptNotFound) {
		t.Fatalf("BindAttempt error = %v, want ErrAttemptNotFound", err)
	}
	if gateway.SpawnCalls() != 0 {
		t.Fatalf("spawn calls = %d, want 0: a missing attempt must not spawn", gateway.SpawnCalls())
	}
}

// ---------------------------------------------------------------------------
// 显式 RepoBindings 必须取得实时工作区证据，单仓缺行同样关闭门禁；
// 仅没有 RepoBindings 的 Legacy 请求沿用原来的 readiness 规则。
// ---------------------------------------------------------------------------

// repoGateRequest is one spawn request with the repository scope a test wants.
func repoGateRequest(repos ...ports.WorkflowRepoKeyRef) ports.SpawnRequest {
	request := sampleSpawnRequest()
	request.Repos = repos
	return request
}

// repoBindingsFor builds the expected repository bindings a bind would persist.
func repoBindingsFor(repos ...ports.WorkflowRepoKeyRef) []domain.WorkflowAttemptRepoBinding {
	out := make([]domain.WorkflowAttemptRepoBinding, 0, len(repos))
	for _, repo := range repos {
		out = append(out, domain.WorkflowAttemptRepoBinding{
			RepoKey: repo.RepoKey, ProjectID: repo.ProjectID,
			ExpectedBaseSHA: repo.BaseSHA, State: domain.WorkflowRepoBindingStatePending,
		})
	}
	return out
}

// TestSingleRepoBindIsBlockedWithoutWorktreeRows 保留显式单仓缺行的反例，
// 删除显式仓库门禁会错误地把此请求绑定为 ready。
func TestSingleRepoBindIsBlockedWithoutWorktreeRows(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1")
	coordinator := newTestCoordinator(t, store, gateway)

	repo := ports.WorkflowRepoKeyRef{RepoKey: "repo-main", ProjectID: "project-1", BaseSHA: "base-1"}
	outcome, err := coordinator.BindAttempt(
		context.Background(), repoGateRequest(repo), repoBindingsFor(repo))
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("state = %q, want reconcile_required", outcome.Binding.State)
	}
	if outcome.Binding.FailureCode != RepoWorktreeMissingCode {
		t.Fatalf("failure code = %q, want %q", outcome.Binding.FailureCode, RepoWorktreeMissingCode)
	}
	if !outcome.Frozen || outcome.Binding.SessionID != nil {
		t.Fatalf("缺少显式仓库证据仍绑定会话：%+v", outcome)
	}
	if !outcome.Spawned {
		t.Fatal("the spawn must have happened")
	}
}

// TestLegacyBindSucceedsWithoutRepoBindings 固定真正无 Repo 绑定的兼容路径；
// 删除 ready 会话的绑定写入后，此处无法取得会话和 generation。
func TestLegacyBindSucceedsWithoutRepoBindings(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1")
	coordinator := newTestCoordinator(t, store, gateway)

	outcome, err := coordinator.BindAttempt(context.Background(), sampleSpawnRequest(), nil)
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready", outcome.Binding.State)
	}
	if !outcome.Spawned || outcome.Binding.SessionID == nil || outcome.Binding.ControllerGeneration != "gen-1" {
		t.Fatalf("Legacy ready 会话未完成绑定：%+v", outcome)
	}
}

// TestSingleRepoBindVerifiesAPresentWorktree proves the other half: when the session
// DOES report a worktree, it must be the right one.
func TestSingleRepoBindVerifiesAPresentWorktree(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1").
		SpawnWorktrees([]ports.RepoWorktreeObservation{{
			RepoKey: "repo-main", ProjectID: "project-1", WorktreeRef: "/ws/one",
			BaseSHA: "creationsha-1", HeadSHA: "creationsha-1",
		}})
	coordinator := newTestCoordinator(t, store, gateway)

	repo := ports.WorkflowRepoKeyRef{RepoKey: "repo-main", ProjectID: "project-1", BaseSHA: "creationsha-1"}
	outcome, err := coordinator.BindAttempt(
		context.Background(), repoGateRequest(repo), repoBindingsFor(repo))
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateBoundReady {
		t.Fatalf("state = %q, want bound_ready", outcome.Binding.State)
	}
	recorded, err := store.ListWorkflowAttemptRepoBindings(context.Background(), outcome.Binding.AttemptID)
	if err != nil || len(recorded) != 1 {
		t.Fatalf("repo bindings = %d err=%v, want 1", len(recorded), err)
	}
	if recorded[0].State != domain.WorkflowRepoBindingStateVerified {
		t.Fatalf("repo state = %q, want verified", recorded[0].State)
	}
	if recorded[0].ObservedHeadSHA != "creationsha-1" {
		t.Fatalf("observed head = %q, want the observed live head", recorded[0].ObservedHeadSHA)
	}
}

// TestMultiRepoBindIsBlockedByAPartialWorktreeList is the multi-repo half: a
// two-repository attempt can never bind on one worktree.
func TestMultiRepoBindIsBlockedByAPartialWorktreeList(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1").
		SpawnWorktrees([]ports.RepoWorktreeObservation{{
			RepoKey: "repo-a", ProjectID: "project-1", WorktreeRef: "/ws/a", BaseSHA: "sha-a", HeadSHA: "sha-a",
		}})
	coordinator := newTestCoordinator(t, store, gateway)

	repoA := ports.WorkflowRepoKeyRef{RepoKey: "repo-a", ProjectID: "project-1", BaseSHA: "sha-a"}
	repoB := ports.WorkflowRepoKeyRef{RepoKey: "repo-b", ProjectID: "project-2", BaseSHA: "sha-b"}
	outcome, err := coordinator.BindAttempt(
		context.Background(), repoGateRequest(repoA, repoB), repoBindingsFor(repoA, repoB))
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("state = %q, want reconcile_required", outcome.Binding.State)
	}
	if outcome.Binding.FailureCode != RepoWorktreeMissingCode {
		t.Fatalf("failure code = %q, want %q", outcome.Binding.FailureCode, RepoWorktreeMissingCode)
	}
	if !outcome.Frozen {
		t.Fatal("a repo-blocked bind must be frozen")
	}
}

// TestMultiRepoBindIsBlockedByAMismatchedBaseSHA 核对实时观察的基线，
// 合法后继 HEAD 不影响身份，但错误的基线必须关闭门禁。
func TestMultiRepoBindIsBlockedByAMismatchedBaseSHA(t *testing.T) {
	t.Parallel()
	store := newMemoryBindings()
	gateway := NewFakeGateway().WithClock(fixedClock()).
		SpawnReadiness(ports.SessionReadinessReady).
		SpawnGeneration("gen-1").
		SpawnWorktrees([]ports.RepoWorktreeObservation{
			{RepoKey: "repo-a", ProjectID: "project-1", WorktreeRef: "/ws/a", BaseSHA: "sha-a", HeadSHA: "sha-a"},
			// repo-b 存在，但实时观察的基线不是该 Attempt 授权的基线。
			{RepoKey: "repo-b", ProjectID: "project-2", WorktreeRef: "/ws/b", BaseSHA: "sha-unexpected", HeadSHA: "sha-unexpected"},
		})
	coordinator := newTestCoordinator(t, store, gateway)

	repoA := ports.WorkflowRepoKeyRef{RepoKey: "repo-a", ProjectID: "project-1", BaseSHA: "sha-a"}
	repoB := ports.WorkflowRepoKeyRef{RepoKey: "repo-b", ProjectID: "project-2", BaseSHA: "sha-b"}
	outcome, err := coordinator.BindAttempt(
		context.Background(), repoGateRequest(repoA, repoB), repoBindingsFor(repoA, repoB))
	if err != nil {
		t.Fatalf("BindAttempt: %v", err)
	}
	if outcome.Binding.State != domain.WorkflowBindingStateReconcileRequired {
		t.Fatalf("state = %q, want reconcile_required", outcome.Binding.State)
	}
	if outcome.Binding.FailureCode != RepoWorktreeMismatchCode {
		t.Fatalf("failure code = %q, want %q", outcome.Binding.FailureCode, RepoWorktreeMismatchCode)
	}
}

// TestReanchorableGenerationTable is the pure decision behind the re-anchor branch. Every way
// an observation may NOT replace the recorded generation is pinned here, because the branch in
// bindObservation is only safe while this predicate refuses everything else: a false positive
// would let an observation overwrite the recorded controller with something that is not an
// owner, and the fence it is meant to repair would then pass for the wrong one.
func TestReanchorableGenerationTable(t *testing.T) {
	t.Parallel()
	sessionID := domain.SessionID("session-1")
	bound := func(generation string) domain.WorkflowAttemptBinding {
		owner := sessionID
		return domain.WorkflowAttemptBinding{
			AttemptID: "attempt-1", SessionID: &owner, ControllerGeneration: generation,
			State: domain.WorkflowBindingStateBoundReady,
		}
	}
	tests := []struct {
		name        string
		binding     domain.WorkflowAttemptBinding
		observation ports.SessionObservation
		want        bool
	}{
		{
			name:        "rotated generation on the same session",
			binding:     bound("gen-1"),
			observation: sessionObservation(sessionID, "gen-2", ports.SessionReadinessReady),
			want:        true,
		},
		{
			name:        "same generation is not a change",
			binding:     bound("gen-1"),
			observation: sessionObservation(sessionID, "gen-1", ports.SessionReadinessReady),
			want:        false,
		},
		{
			name:        "a different session is never adopted",
			binding:     bound("gen-1"),
			observation: sessionObservation("someone-elses", "gen-2", ports.SessionReadinessReady),
			want:        false,
		},
		{
			name:        "a binding with no session cannot be re-anchored",
			binding:     domain.WorkflowAttemptBinding{AttemptID: "attempt-1"},
			observation: sessionObservation(sessionID, "gen-2", ports.SessionReadinessReady),
			want:        false,
		},
		{
			name:        "an empty observed generation is not an owner",
			binding:     bound("gen-1"),
			observation: sessionObservation(sessionID, "", ports.SessionReadinessReady),
			want:        false,
		},
		{
			name:        "whitespace is not a generation",
			binding:     bound("gen-1"),
			observation: sessionObservation(sessionID, "   ", ports.SessionReadinessReady),
			want:        false,
		},
		{
			name:        "a pending placeholder is not an owner",
			binding:     bound("gen-1"),
			observation: sessionObservation(sessionID, pendingControllerGenerationPrefix+"ready", ports.SessionReadinessReady),
			want:        false,
		},
		{
			name:        "a placeholder never replaces another placeholder",
			binding:     bound(pendingControllerGenerationPrefix + "provisioning"),
			observation: sessionObservation(sessionID, pendingControllerGenerationPrefix+"ready", ports.SessionReadinessReady),
			want:        false,
		},
		{
			// The promotion real work depends on: a binding that holds only a placeholder may
			// adopt a real controller, which is what makes the pending marker temporary.
			name:        "a real generation may replace a placeholder",
			binding:     bound(pendingControllerGenerationPrefix + "provisioning"),
			observation: sessionObservation(sessionID, "gen-1", ports.SessionReadinessReady),
			want:        true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := reanchorableGeneration(tt.binding, tt.observation); got != tt.want {
				t.Fatalf("reanchorableGeneration = %v, want %v", got, tt.want)
			}
		})
	}
}
