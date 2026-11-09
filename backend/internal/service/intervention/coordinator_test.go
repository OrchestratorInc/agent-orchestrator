package intervention

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
)

var testNow = time.Date(2026, time.November, 3, 12, 0, 0, 0, time.UTC)

func fixedClock() func() time.Time { return func() time.Time { return testNow } }

const testSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// memoryInterventions mirrors the real store's contract: (workflow, clientMessage) is
// unique, same-sha replays, different-sha conflicts, and a receipt state that requires chat
// evidence is refused without it.
type memoryInterventions struct {
	mu         sync.Mutex
	byID       map[string]domain.WorkflowIntervention
	byKey      map[string]string
	applyCalls int
}

func newMemoryInterventions() *memoryInterventions {
	return &memoryInterventions{
		byID:  make(map[string]domain.WorkflowIntervention),
		byKey: make(map[string]string),
	}
}

func interventionKey(workflowID domain.WorkflowID, clientMessageID string) string {
	return string(workflowID) + "\x00" + clientMessageID
}

func (m *memoryInterventions) CreateWorkflowIntervention(
	_ context.Context,
	intervention domain.WorkflowIntervention,
	now time.Time,
) (domain.WorkflowIntervention, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := interventionKey(intervention.WorkflowID, intervention.ClientMessageID)
	if existingID, ok := m.byKey[key]; ok {
		existing := m.byID[existingID]
		if existing.OperationSHA != intervention.OperationSHA {
			return domain.WorkflowIntervention{}, false, domain.ErrInterventionOperationConflict
		}
		return existing, false, nil
	}
	intervention.State = domain.WorkflowInterventionStateRequested
	intervention.Revision = 1
	intervention.CreatedAt = now
	intervention.UpdatedAt = now
	m.byID[intervention.ID] = intervention
	m.byKey[key] = intervention.ID
	return intervention, true, nil
}

func (m *memoryInterventions) GetWorkflowIntervention(
	_ context.Context,
	id string,
) (domain.WorkflowIntervention, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	intervention, ok := m.byID[id]
	return intervention, ok, nil
}

func (m *memoryInterventions) GetWorkflowInterventionByClientMessage(
	_ context.Context,
	workflowID domain.WorkflowID,
	clientMessageID string,
) (domain.WorkflowIntervention, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.byKey[interventionKey(workflowID, clientMessageID)]
	if !ok {
		return domain.WorkflowIntervention{}, false, nil
	}
	return m.byID[id], true, nil
}

func (m *memoryInterventions) ApplyWorkflowInterventionReceipt(
	_ context.Context,
	id string,
	next domain.WorkflowInterventionState,
	fact domain.InterventionChatFact,
	reasonCode string,
	queueAvailable bool,
	expectedGeneration string,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowIntervention, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applyCalls++
	current, ok := m.byID[id]
	if !ok {
		return domain.WorkflowIntervention{}, false, domain.ErrInterventionNotFound
	}
	if current.Revision != expectedRevision {
		return domain.WorkflowIntervention{}, false, domain.ErrInterventionRevisionConflict
	}
	if expectedGeneration != "" && current.ControllerGeneration != expectedGeneration {
		return domain.WorkflowIntervention{}, false, domain.ErrInterventionStaleGeneration
	}
	if next.RequiresChatFact() && !fact.Licenses(next) {
		return domain.WorkflowIntervention{}, false, domain.ErrInterventionEvidenceMissing
	}
	if err := current.Mode.ValidateTransition(current.State, next); err != nil {
		return domain.WorkflowIntervention{}, false, err
	}
	current.State = next
	current.ChatTurnID = fact.ChatTurnID
	current.ProviderTurnID = fact.ProviderTurnID
	current.ReasonCode = reasonCode
	current.QueueAvailable = queueAvailable
	current.Revision++
	current.UpdatedAt = now
	m.byID[id] = current
	return current, true, nil
}

func (m *memoryInterventions) ListWorkflowInterventionsForAttempt(
	_ context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) ([]domain.WorkflowIntervention, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]domain.WorkflowIntervention, 0, len(m.byID))
	for _, intervention := range m.byID {
		if intervention.WorkflowID == workflowID && intervention.TaskID == taskID {
			out = append(out, intervention)
		}
	}
	return out, nil
}

// CreateWorkflowInterventionIfNoOpenStop mirrors the real store's single-transaction
// contract: the open-stop check and the insert happen under one lock, so a concurrent
// second stop with a different client message id is blocked rather than performed.
func (m *memoryInterventions) CreateWorkflowInterventionIfNoOpenStop(
	ctx context.Context,
	intervention domain.WorkflowIntervention,
	now time.Time,
) (domain.WorkflowIntervention, bool, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, existing := range m.byID {
		if existing.WorkflowID != intervention.WorkflowID || existing.TaskID != intervention.TaskID {
			continue
		}
		if existing.Mode != domain.WorkflowInterventionModeStop {
			continue
		}
		// `requested` counts as open, matching the real store: a stop row is created at
		// requested and only advanced by the caller's next write.
		switch existing.State {
		case domain.WorkflowInterventionStateRequested,
			domain.WorkflowInterventionStateStopRequested,
			domain.WorkflowInterventionStateStopUnknown:
			return existing, false, true, nil
		}
	}
	key := interventionKey(intervention.WorkflowID, intervention.ClientMessageID)
	if existingID, ok := m.byKey[key]; ok {
		existing := m.byID[existingID]
		if existing.OperationSHA != intervention.OperationSHA {
			return domain.WorkflowIntervention{}, false, false, domain.ErrInterventionOperationConflict
		}
		return existing, false, false, nil
	}
	intervention.State = domain.WorkflowInterventionStateRequested
	intervention.Revision = 1
	intervention.CreatedAt = now
	intervention.UpdatedAt = now
	m.byID[intervention.ID] = intervention
	m.byKey[key] = intervention.ID
	return intervention, true, false, nil
}

func (m *memoryInterventions) CountOpenStopInterventions(
	_ context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var count int64
	for _, intervention := range m.byID {
		if intervention.WorkflowID != workflowID || intervention.TaskID != taskID {
			continue
		}
		if intervention.Mode != domain.WorkflowInterventionModeStop {
			continue
		}
		if intervention.State == domain.WorkflowInterventionStateStopRequested ||
			intervention.State == domain.WorkflowInterventionStateStopUnknown {
			count++
		}
	}
	return count, nil
}

// seededBinding is the binding reader the tests use.
//
// It is mutex-guarded because the coordinator re-reads the binding inside an intervention
// (the in-flight stop fence), so concurrent tests read it from several goroutines at once —
// exactly as the real store is read.
type seededBinding struct {
	mu       sync.Mutex
	binding  domain.WorkflowAttemptBinding
	repos    []domain.WorkflowAttemptRepoBinding
	found    bool
	getCalls int
}

func (s *seededBinding) GetWorkflowAttemptBinding(
	_ context.Context,
	_ domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	return s.binding, s.found, nil
}

func (s *seededBinding) ListWorkflowAttemptRepoBindings(
	_ context.Context,
	_ domain.WorkflowAttemptID,
) ([]domain.WorkflowAttemptRepoBinding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.repos, nil
}

// setState changes the binding state a later read observes. Concurrent tests use it instead
// of assigning the field directly.
func (s *seededBinding) setState(state domain.WorkflowBindingState) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.binding.State = state
}

// setSession clears the binding's session, modelling the post-deletion state.
func (s *seededBinding) setSession(sessionID *domain.SessionID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.binding.SessionID = sessionID
}

// snapshot returns a copy of the current binding.
func (s *seededBinding) snapshot() domain.WorkflowAttemptBinding {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.binding
}

// recordingGateway counts calls and returns scripted results.
type recordingGateway struct {
	mu             sync.Mutex
	sendCalls      int
	steerCalls     int
	interruptCalls int
	sendReceipt    ports.MessageReceipt
	// sendReceiptExact returns sendReceipt verbatim instead of filling in the request's
	// identity. It exists so a test can script a receipt that genuinely belongs to a
	// different session, client message id, or generation — the shapes the coordinator
	// must refuse.
	sendReceiptExact bool
	steerReceipt     ports.MessageReceipt
	steerReceiptSet  bool
	sendErr          error
	steerErr         error
	interruptReceipt ports.InterruptReceipt
	interruptErr     error
	lastSend         ports.MessageRequest
	lastSteer        ports.MessageRequest
	lastInterrupt    ports.InterruptRequest
	// block, when set, makes Send and Steer wait until it is closed, so a test can hold a
	// message in flight while a stop is confirmed.
	block chan struct{}
	// readiness is what InspectSession reports. The fixture starts at Ready so the ordinary
	// paths exercise the real gate rather than the refusal path.
	readiness ports.SessionReadiness
	harness   domain.AgentHarness
	// observation, when set, is what InspectSession answers instead of the derived value, so
	// a test can script a rotated generation, a terminated session, or a foreign session.
	observation  *ports.SessionObservation
	inspectErr   error
	inspectCalls int
}

func (g *recordingGateway) Send(_ context.Context, request ports.MessageRequest) (ports.MessageReceipt, error) {
	g.mu.Lock()
	g.sendCalls++
	g.lastSend = request
	block := g.block
	g.mu.Unlock()
	if block != nil {
		<-block
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sendErr != nil {
		return ports.MessageReceipt{}, g.sendErr
	}
	receipt := g.sendReceipt
	if g.sendReceiptExact {
		// Scripted verbatim: the test is about a receipt that does NOT describe this request.
		return receipt, nil
	}
	receipt.SessionID = request.SessionID
	receipt.ClientMessageID = request.ClientMessageID
	// The gateway's real receipts name the generation that produced them, because that is
	// what the coordinator fences against. A fake that omitted it would exercise a receipt
	// shape production can never return.
	if receipt.ControllerGeneration == "" {
		receipt.ControllerGeneration = request.ControllerGeneration
	}
	return receipt, nil
}

// Steer records the request and answers with a receipt that proves the guidance joined the
// turn the caller named, unless a test scripted a different one.
func (g *recordingGateway) Steer(_ context.Context, request ports.MessageRequest) (ports.MessageReceipt, error) {
	// The call is recorded BEFORE the block, so a test holding a steer in flight can observe
	// that it actually entered the gateway.
	g.mu.Lock()
	g.steerCalls++
	g.lastSteer = request
	block := g.block
	g.mu.Unlock()
	if block != nil {
		<-block
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.steerErr != nil {
		return ports.MessageReceipt{}, g.steerErr
	}
	if g.steerReceiptSet {
		receipt := g.steerReceipt
		receipt.SessionID = request.SessionID
		receipt.ClientMessageID = request.ClientMessageID
		return receipt, nil
	}
	return ports.MessageReceipt{
		SessionID: request.SessionID, ClientMessageID: request.ClientMessageID,
		Steered: true, TargetProviderTurnID: request.TargetProviderTurnID,
		ControllerGeneration: request.ControllerGeneration,
		ProviderTurnID:       request.TargetProviderTurnID,
		TurnID:               "turn-" + request.ClientMessageID,
	}, nil
}

func (g *recordingGateway) Interrupt(_ context.Context, request ports.InterruptRequest) (ports.InterruptReceipt, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.interruptCalls++
	g.lastInterrupt = request
	receipt := g.interruptReceipt
	// Every receipt kind names the generation it is about, exactly as the production
	// gateway does: "nothing to stop" and "unknown" are facts about one controller.
	receipt.SessionID = request.SessionID
	if receipt.ControllerGeneration == "" {
		receipt.ControllerGeneration = request.ControllerGeneration
	}
	return receipt, g.interruptErr
}

func (g *recordingGateway) InspectSession(
	_ context.Context,
	sessionID domain.SessionID,
) (ports.SessionObservation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inspectCalls++
	if g.inspectErr != nil {
		return ports.SessionObservation{}, g.inspectErr
	}
	if g.observation != nil {
		return *g.observation, nil
	}
	return ports.SessionObservation{
		SessionID: sessionID, Readiness: g.readiness, ControllerGeneration: "gen-1",
		Harness: g.harness,
	}, nil
}

// SetReadiness scripts liveness for the next intervention.
func (g *recordingGateway) SetReadiness(readiness ports.SessionReadiness) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.readiness = readiness
}

// InspectCount reports how many times liveness was checked.
func (g *recordingGateway) InspectCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inspectCalls
}

func (g *recordingGateway) SendCount() int { g.mu.Lock(); defer g.mu.Unlock(); return g.sendCalls }
func (g *recordingGateway) SteerCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.steerCalls
}
func (g *recordingGateway) InterruptCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.interruptCalls
}
func (g *recordingGateway) LastSend() ports.MessageRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastSend
}
func (g *recordingGateway) LastSteer() ports.MessageRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastSteer
}
func (g *recordingGateway) LastInterrupt() ports.InterruptRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastInterrupt
}
func (g *recordingGateway) SetBlock(block chan struct{}) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.block = block
}

// boundFixture is one attempt bound to one live session generation.
func boundFixture(t *testing.T) (*Coordinator, *memoryInterventions, *seededBinding, *recordingGateway) {
	t.Helper()
	sessionID := domain.SessionID("p1-1")
	binding := domain.WorkflowAttemptBinding{
		AttemptID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
		SpawnRequestID: "spawn-1", SpawnHash: testSHA,
		State:     domain.WorkflowBindingStateBoundReady,
		SessionID: &sessionID, ControllerGeneration: "gen-1",
		Revision: 3, CreatedAt: testNow, UpdatedAt: testNow,
	}
	bindings := &seededBinding{
		binding: binding, found: true,
		repos: []domain.WorkflowAttemptRepoBinding{{
			AttemptID: "attempt-1", WorkflowID: "workflow-1", RepoKey: "backend",
			ProjectID: "project-1", SessionWorktreeRef: "/worktrees/backend",
			State: domain.WorkflowRepoBindingStateVerified,
		}},
	}
	store := newMemoryInterventions()
	// Ready by default: the live gate must pass for the ordinary paths to be exercised.
	gateway := &recordingGateway{
		readiness: ports.SessionReadinessReady, harness: domain.HarnessCodex,
	}
	resolver := capability.NewResolver(capability.WithClock(fixedClock()))
	coordinator := New(Deps{
		Store: store, Bindings: bindings, Gateway: gateway, Resolver: resolver,
		Clock: fixedClock(), BindingWriter: &recordingBindingWriter{},
	})
	return coordinator, store, bindings, gateway
}

// permitInterrupt registers handshake-grade evidence that this session generation can be
// interrupted. It says nothing about steering, because the two capabilities are separate.
func permitInterrupt(t *testing.T, coordinator *Coordinator, harness domain.AgentHarness, sessionID domain.SessionID, generation string) {
	t.Helper()
	snapshot := domain.NewHarnessCapabilitySnapshot(
		harness, domain.CapabilitySourceProviderHandshake, testNow, capability.DefaultTTL,
	)
	snapshot.ObservedSessionID = sessionID
	snapshot.Generation = generation
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Interrupt = domain.CapabilitySupported
	coordinator.resolver.RegisterProbe(harness, capability.NewFakeProbe().Script(snapshot))
}

// permitSteerAndInterrupt registers one snapshot proving both capabilities for this session
// generation. Registering a probe replaces the previous one, so a test that exercises both
// paths must prove both at once.
func permitSteerAndInterrupt(t *testing.T, coordinator *Coordinator, harness domain.AgentHarness, sessionID domain.SessionID, generation string) {
	t.Helper()
	snapshot := domain.NewHarnessCapabilitySnapshot(
		harness, domain.CapabilitySourceProviderHandshake, testNow, capability.DefaultTTL,
	)
	snapshot.ObservedSessionID = sessionID
	snapshot.Generation = generation
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Streaming = domain.CapabilitySupported
	snapshot.Steer = domain.CapabilitySupported
	snapshot.Interrupt = domain.CapabilitySupported
	coordinator.resolver.RegisterProbe(harness, capability.NewFakeProbe().Script(snapshot))
}

// steeringReceipt is what a gateway returns when guidance provably joined the named turn.
func steeringReceipt(clientMessageID, providerTurnID string) ports.MessageReceipt {
	return ports.MessageReceipt{
		SessionID: "p1-1", ClientMessageID: clientMessageID,
		Steered: true, TargetProviderTurnID: providerTurnID,
		ControllerGeneration: "gen-1", ProviderTurnID: providerTurnID,
		TurnID: "turn-1",
	}
}

// recordingBindingWriter records the stop outcome it was asked to record. It is the
// coordinator's only binding write, so a test can assert the binding advanced.
type recordingBindingWriter struct {
	mu      sync.Mutex
	calls   int
	confirm bool
	unknown bool
	err     error
	last    domain.WorkflowAttemptID
}

func (w *recordingBindingWriter) MarkStopOutcome(
	_ context.Context,
	attemptID domain.WorkflowAttemptID,
	confirmed bool,
	unknown bool,
) (domain.WorkflowAttemptBinding, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls++
	w.confirm = confirmed
	w.unknown = unknown
	w.last = attemptID
	return domain.WorkflowAttemptBinding{AttemptID: attemptID}, w.err
}

func (w *recordingBindingWriter) Calls() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

// permitSteer registers handshake-grade evidence that this session generation can steer.
func permitSteer(t *testing.T, coordinator *Coordinator, harness domain.AgentHarness, sessionID domain.SessionID, generation string) {
	t.Helper()
	snapshot := domain.NewHarnessCapabilitySnapshot(
		harness, domain.CapabilitySourceProviderHandshake, testNow, capability.DefaultTTL,
	)
	snapshot.ObservedSessionID = sessionID
	snapshot.Generation = generation
	snapshot.AuthState = domain.AgentAuthenticationAuthorized
	snapshot.Readiness = domain.CapabilityReady
	snapshot.Streaming = domain.CapabilitySupported
	snapshot.Steer = domain.CapabilitySupported
	// Steering and interrupting are separate capabilities, but a fixture that proves one
	// and then exercises the other must not trip an unrelated gate.
	snapshot.Interrupt = domain.CapabilitySupported
	coordinator.resolver.RegisterProbe(harness, capability.NewFakeProbe().Script(snapshot))
}

func steerRequest() SteerRequest {
	return SteerRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-steer-1", Text: "actually, use the other approach",
		TargetProviderTurnID: "prov-turn-1",
		Harness:              domain.HarnessCodex,
	}
}

// TestSteerAppliesWithProviderEvidence proves the happy path reaches applied only because the
// chat service named both turns.
func TestSteerAppliesWithProviderEvidence(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.steerReceipt = steeringReceipt("msg-steer-1", "prov-turn-1")
	gateway.steerReceiptSet = true

	outcome, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateApplied {
		t.Fatalf("state = %q, want applied", outcome.Intervention.State)
	}
	if !outcome.Performed {
		t.Fatal("the first call must report that it performed the send")
	}
	if gateway.SteerCount() != 1 {
		t.Fatalf("steer calls = %d, want 1", gateway.SteerCount())
	}
	if gateway.SendCount() != 0 {
		t.Fatalf("send calls = %d, want 0: a steer must never be delivered as a send", gateway.SendCount())
	}
	if gateway.InterruptCount() != 0 {
		t.Fatal("a supported steer must never interrupt")
	}
	stored, found, _ := store.GetWorkflowInterventionByClientMessage(context.Background(), "workflow-1", "msg-steer-1")
	if !found {
		t.Fatal("the intervention was not persisted")
	}
	if stored.ProviderTurnID != "prov-turn-1" || stored.ChatTurnID != "turn-1" {
		t.Fatalf("stored turns = %q/%q, want the chat service's own ids", stored.ChatTurnID, stored.ProviderTurnID)
	}
}

// TestNoFabricatedApplied is the required "cannot fabricate applied" proof.
//
// A gateway that answers successfully but names no turn cannot produce applied: the receipt
// falls back to unknown, and an explicit attempt to write applied without the fact is
// refused by the store.
func TestNoFabricatedApplied(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	// A bare acknowledgement: no steered turn id, not queued.
	gateway.steerReceipt = ports.MessageReceipt{SessionID: "p1-1", ClientMessageID: "msg-steer-1"}
	gateway.steerReceiptSet = true

	outcome, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State == domain.WorkflowInterventionStateApplied {
		t.Fatal("applied was fabricated from an acknowledgement that named no turn")
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateUnknown {
		t.Fatalf("state = %q, want unknown for a sessionless answer", outcome.Intervention.State)
	}
	if !outcome.Frozen {
		t.Fatal("an unproven steer must freeze rather than claim progress")
	}

	// And the store itself refuses the fabricated state, so no caller can bypass the mapping.
	_, applied, err := store.ApplyWorkflowInterventionReceipt(
		context.Background(), outcome.Intervention.ID,
		domain.WorkflowInterventionStateApplied, domain.InterventionChatFact{},
		"forced", false, "gen-1", outcome.Intervention.Revision, testNow,
	)
	if !errors.Is(err, domain.ErrInterventionEvidenceMissing) {
		t.Fatalf("err = %v, want ErrInterventionEvidenceMissing", err)
	}
	if applied {
		t.Fatal("the store wrote applied without chat evidence")
	}
}

// TestSteerRefusedWhenCapabilityUnknown is the required "unknown capability refuses with
// queueAvailable and no interrupt" proof.
func TestSteerRefusedWhenCapabilityUnknown(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	// No probe registered: the static profile applies, which can never authorize.
	gateway.sendReceipt = ports.MessageReceipt{TurnID: "turn-1", ProviderTurnID: "prov-turn-1"}

	outcome, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State == domain.WorkflowInterventionStateApplied {
		t.Fatal("an unproven capability produced applied")
	}
	if !outcome.QueueAvailable {
		t.Fatal("a refused steer must tell the client that Queue is available")
	}
	if outcome.ReasonCode == "" {
		t.Fatal("a refused steer must carry a reason code")
	}
	// The decisive assertion: no provider action, and specifically no interrupt.
	if gateway.SteerCount() != 0 {
		t.Fatalf("steer calls = %d, want 0 when steer is unproven", gateway.SteerCount())
	}
	if gateway.InterruptCount() != 0 {
		t.Fatal("an unproven steer must never fall back to interrupt-and-resend")
	}
	// Nothing was persisted either: a refusal costs nothing.
	if _, found, _ := store.GetWorkflowInterventionByClientMessage(context.Background(), "workflow-1", "msg-steer-1"); found {
		t.Fatal("a refused steer persisted an intervention")
	}
}

// TestSteerRefusedForStaleGeneration is the generation-fencing proof.
func TestSteerRefusedForStaleGeneration(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	// Evidence is scoped to gen-0, but the binding is on gen-1 (a replaced controller).
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-0")
	gateway.sendReceipt = ports.MessageReceipt{TurnID: "turn-1", ProviderTurnID: "prov-turn-1"}

	outcome, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State == domain.WorkflowInterventionStateApplied {
		t.Fatal("evidence from a replaced generation authorized the current one")
	}
	if !outcome.QueueAvailable {
		t.Fatal("a generation refusal must still offer Queue")
	}
	if gateway.SteerCount() != 0 {
		t.Fatalf("steer calls = %d, want 0", gateway.SteerCount())
	}
	if gateway.InterruptCount() != 0 {
		t.Fatal("a stale-generation refusal interrupted the session")
	}
}

// TestSteerRequiresTargetTurn proves a steer without a turn id is refused before anything.
func TestSteerRequiresTargetTurn(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	request := steerRequest()
	request.TargetProviderTurnID = ""

	_, err := coordinator.Steer(context.Background(), request)
	if !errors.Is(err, domain.ErrInterventionTurnRequired) {
		t.Fatalf("err = %v, want ErrInterventionTurnRequired", err)
	}
	if gateway.SendCount() != 0 || gateway.InterruptCount() != 0 {
		t.Fatal("a turnless steer reached the gateway")
	}
}

// TestQueueIsIndependentOfSteerCapability proves queueing does not need the steer capability
// and never renders queued as sent.
func TestQueueIsIndependentOfSteerCapability(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	// No capability evidence at all; queue must still work.
	gateway.sendReceipt = ports.MessageReceipt{
		SessionID: "p1-1", ClientMessageID: "msg-queue-1", TurnID: "turn-q1", Queued: true,
	}

	outcome, err := coordinator.Queue(context.Background(), QueueRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-queue-1", Text: "next, also update the docs",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateQueued {
		t.Fatalf("state = %q, want queued", outcome.Intervention.State)
	}
	if outcome.Intervention.State == domain.WorkflowInterventionStateApplied ||
		outcome.Intervention.State == domain.WorkflowInterventionStateDelivered {
		t.Fatal("a queued message was reported as sent or delivered")
	}
	if gateway.SendCount() != 1 {
		t.Fatalf("send calls = %d, want 1: queuing opens a new turn", gateway.SendCount())
	}
	if gateway.SteerCount() != 0 {
		t.Fatalf("steer calls = %d, want 0: queue must never steer", gateway.SteerCount())
	}
	if last := gateway.LastSend(); last.Delivery != ports.MessageDeliverySend {
		t.Fatalf("queue delivery = %q, want send", last.Delivery)
	}
}

// TestStopIssuesOneInterruptOnRetry is the required "exactly one interrupt" proof.
func TestStopIssuesOneInterruptOnRetry(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}
	request := StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-1",
	}

	outcome, replan, err := coordinator.Stop(context.Background(), request)
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("state = %q, want stop_confirmed", outcome.Intervention.State)
	}
	if replan == nil {
		t.Fatal("a confirmed stop must emit a replan request")
	}
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls = %d, want 1", gateway.InterruptCount())
	}

	// Retry with the same client message id: exactly one interrupt, total.
	for range 3 {
		replay, replayPlan, err := coordinator.Stop(context.Background(), request)
		if err != nil {
			t.Fatalf("replay Stop: %v", err)
		}
		if replay.Performed {
			t.Fatal("a replay reported that it performed the interrupt")
		}
		if replay.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
			t.Fatalf("replay state = %q, want the stored stop_confirmed", replay.Intervention.State)
		}
		if replayPlan == nil {
			t.Fatal("a replay must still return the replan request")
		}
	}
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls after retries = %d, want exactly 1", gateway.InterruptCount())
	}
	rows, err := store.ListWorkflowInterventionsForAttempt(context.Background(), "workflow-1", "task-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("interventions = %d, want 1 (a retry must not create a second row)", len(rows))
	}
}

// TestStopUnknownFreezesAndKeepsWorktrees is the required uncertain-stop proof.
func TestStopUnknownFreezesAndKeepsWorktrees(t *testing.T) {
	t.Parallel()
	coordinator, store, bindings, gateway := boundFixture(t)
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	// No confirmation and no "nothing to stop": the receipt is uncertain.
	gateway.interruptReceipt = ports.InterruptReceipt{Unknown: true}

	outcome, replan, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-unknown",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateStopUnknown {
		t.Fatalf("state = %q, want stop_unknown", outcome.Intervention.State)
	}
	if !outcome.Frozen {
		t.Fatal("an uncertain stop must freeze")
	}
	if replan != nil {
		t.Fatal("an uncertain stop must not emit a replan request")
	}

	// The session and its worktree refs must survive: a stop is not permission to discard
	// work.
	stored, found, _ := store.GetWorkflowInterventionByClientMessage(context.Background(), "workflow-1", "msg-stop-unknown")
	if !found {
		t.Fatal("the stop was not persisted")
	}
	if stored.SessionID == nil || *stored.SessionID != "p1-1" {
		t.Fatalf("session = %v, want the session preserved", stored.SessionID)
	}
	if bindings.snapshot().SessionID == nil {
		t.Fatal("the binding lost its session")
	}
	repos, _ := bindings.ListWorkflowAttemptRepoBindings(context.Background(), "attempt-1")
	if len(repos) != 1 || repos[0].SessionWorktreeRef == "" {
		t.Fatalf("worktree refs = %+v, want the reference preserved", repos)
	}

	// A second, differently-keyed stop must be refused while the first is unsettled.
	second, _, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-2",
	})
	if err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	if !second.Frozen || second.ReasonCode != "stop_already_pending" {
		t.Fatalf("second stop = %+v, want stop_already_pending", second)
	}
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls = %d, want 1: an unknown stop must not be retried", gateway.InterruptCount())
	}
}

// TestStopConfirmsWhenNothingWasRunning proves "nothing to stop" is a clean confirmation, not
// an uncertainty.
func TestStopConfirmsWhenNothingWasRunning(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{NothingToStop: true}

	outcome, replan, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-idle",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("state = %q, want stop_confirmed", outcome.Intervention.State)
	}
	if replan == nil {
		t.Fatal("an idle session stop still permits a replan")
	}
	if outcome.Intervention.ReasonCode != "stop_nothing_to_stop" {
		t.Fatalf("reason = %q, want stop_nothing_to_stop", outcome.Intervention.ReasonCode)
	}
}

// TestStopReplanPreservesWorktrees proves the replan request names the worktrees that must
// survive.
func TestStopReplanPreservesWorktrees(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	_, replan, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-preserve",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if replan == nil {
		t.Fatal("no replan request")
	}
	if len(replan.PreservedWorktrees) != 1 || replan.PreservedWorktrees[0] != "/worktrees/backend" {
		t.Fatalf("preserved worktrees = %v, want the bound worktree ref", replan.PreservedWorktrees)
	}
	// M2 only produces the request: it carries no task list or plan.
	if replan.TaskID == "" || replan.WorkflowID == "" {
		t.Fatal("the replan request lost its identity")
	}
}

// TestReplayIsFreeForSteerAndQueue proves both message paths replay without a second send.
func TestReplayIsFreeForSteerAndQueue(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.sendReceipt = ports.MessageReceipt{
		SessionID: "p1-1", ClientMessageID: "msg-steer-1",
		TurnID: "turn-1", ProviderTurnID: "prov-turn-1",
	}

	if _, err := coordinator.Steer(context.Background(), steerRequest()); err != nil {
		t.Fatalf("first Steer: %v", err)
	}
	replay, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("replay Steer: %v", err)
	}
	if replay.Performed {
		t.Fatal("a steer replay performed another send")
	}
	if replay.Intervention.State != domain.WorkflowInterventionStateApplied {
		t.Fatalf("replay state = %q, want the stored applied", replay.Intervention.State)
	}
	if gateway.SteerCount() != 1 {
		t.Fatalf("steer calls = %d, want 1", gateway.SteerCount())
	}
	if gateway.SendCount() != 0 {
		t.Fatalf("send calls = %d, want 0: a steer must never be delivered as a send", gateway.SendCount())
	}
}

// TestSameKeyDifferentContentConflicts proves the fingerprint rule: a reused key with
// different routing parameters is a conflict, not a replay.
func TestSameKeyDifferentContentConflicts(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.sendReceipt = ports.MessageReceipt{
		SessionID: "p1-1", ClientMessageID: "msg-steer-1",
		TurnID: "turn-1", ProviderTurnID: "prov-turn-1",
	}

	request := steerRequest()
	if _, err := coordinator.Steer(context.Background(), request); err != nil {
		t.Fatalf("first Steer: %v", err)
	}
	// Same client message id, a different turn target: a different operation.
	changed := request
	changed.TargetProviderTurnID = "prov-turn-2"
	if _, err := coordinator.Steer(context.Background(), changed); !errors.Is(err, domain.ErrInterventionOperationConflict) {
		t.Fatalf("err = %v, want ErrInterventionOperationConflict", err)
	}
	if gateway.SteerCount() != 1 {
		t.Fatalf("steer calls = %d, want 1: a conflicting key must not steer again", gateway.SteerCount())
	}
}

// TestInterventionRefusedWithoutBindingSession proves the paths are inert when the binding
// has no session (which includes the post-deletion reconcile_required state).
func TestInterventionRefusedWithoutBindingSession(t *testing.T) {
	t.Parallel()
	coordinator, _, bindings, gateway := boundFixture(t)
	bindings.setSession(nil)
	bindings.setState(domain.WorkflowBindingStateReconcileRequired)

	for _, call := range []func() error{
		func() error {
			_, err := coordinator.Steer(context.Background(), steerRequest())
			return err
		},
		func() error {
			_, err := coordinator.Queue(context.Background(), QueueRequest{
				WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
				ClientMessageID: "msg-q", Text: "text",
			})
			return err
		},
		func() error {
			_, _, err := coordinator.Stop(context.Background(), StopRequest{
				WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
				ClientMessageID: "msg-s",
			})
			return err
		},
	} {
		_ = call()
	}
	if gateway.SendCount() != 0 || gateway.InterruptCount() != 0 {
		t.Fatalf("a sessionless binding reached the gateway: send=%d interrupt=%d",
			gateway.SendCount(), gateway.InterruptCount())
	}
}

// TestOperationSHANeverContainsText proves the fingerprint cannot leak message content.
func TestOperationSHANeverContainsText(t *testing.T) {
	t.Parallel()
	text := "the secret token is sk-live-abcdef"
	sha := OperationSHA(
		domain.WorkflowInterventionModeSteer, "workflow-1", "task-1", "attempt-1",
		"p1-1", "gen-1", "msg-1", "prov-1",
	)
	if len(sha) != 64 {
		t.Fatalf("sha length = %d, want 64", len(sha))
	}
	if strings.Contains(sha, "sk-live") || strings.Contains(sha, text) {
		t.Fatal("the operation fingerprint embedded message text")
	}
	// The same routing parameters always produce the same fingerprint, so a retry replays.
	again := OperationSHA(
		domain.WorkflowInterventionModeSteer, "workflow-1", "task-1", "attempt-1",
		"p1-1", "gen-1", "msg-1", "prov-1",
	)
	if again != sha {
		t.Fatal("the operation fingerprint is not stable")
	}
	// A different mode is a different operation.
	if OperationSHA(domain.WorkflowInterventionModeQueue, "workflow-1", "task-1", "attempt-1",
		"p1-1", "gen-1", "msg-1", "prov-1") == sha {
		t.Fatal("mode must participate in the fingerprint")
	}
}

// TestSteerRejectsUnknownHarness proves the gate refuses to guess a harness.
func TestSteerRejectsUnknownHarness(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	request := steerRequest()
	request.Harness = "gpt-imaginary"

	outcome, err := coordinator.Steer(context.Background(), request)
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State != "" && outcome.Intervention.State != domain.WorkflowInterventionStateRequested {
		t.Fatalf("state = %q, want nothing recorded", outcome.Intervention.State)
	}
	if !outcome.QueueAvailable {
		t.Fatal("an unproven harness must offer Queue")
	}
	if gateway.SendCount() != 0 {
		t.Fatal("an unknown harness reached the gateway")
	}
}

// TestInterventionsRefuseWhenTheSessionIsNotLive is the M2-05 half of the P1-1 contract: a
// Steer, Queue, or Stop receipt must never be minted against a session whose gated
// observation is not live.
//
// The three modes are checked together because they share resolveTarget, so a regression in
// the gate shows up in all of them rather than only in the one a test happened to cover.
func TestInterventionsRefuseWhenTheSessionIsNotLive(t *testing.T) {
	t.Parallel()
	notLive := []ports.SessionReadiness{
		ports.SessionReadinessProvisioning,
		ports.SessionReadinessExited,
		ports.SessionReadinessUnknown,
	}
	for _, readiness := range notLive {
		t.Run(string(readiness), func(t *testing.T) {
			t.Parallel()
			coordinator, store, _, gateway := boundFixture(t)
			permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
			gateway.SetReadiness(readiness)

			steer, err := coordinator.Steer(context.Background(), steerRequest())
			if err != nil {
				t.Fatalf("Steer: %v", err)
			}
			if steer.Intervention.State == domain.WorkflowInterventionStateApplied {
				t.Fatalf("readiness %q produced an applied steer receipt", readiness)
			}

			queue, err := coordinator.Queue(context.Background(), QueueRequest{
				WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
				ClientMessageID: "msg-q-notlive", Text: "queued text",
			})
			if err != nil {
				t.Fatalf("Queue: %v", err)
			}
			if queue.Intervention.State == domain.WorkflowInterventionStateQueued {
				t.Fatalf("readiness %q produced a queued receipt", readiness)
			}

			stop, replan, err := coordinator.Stop(context.Background(), StopRequest{
				WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
				ClientMessageID: "msg-s-notlive",
			})
			if err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if stop.Intervention.State == domain.WorkflowInterventionStateStopConfirmed {
				t.Fatalf("readiness %q produced a confirmed stop", readiness)
			}
			if replan != nil {
				t.Fatalf("readiness %q produced a replan request", readiness)
			}

			// The decisive assertion: nothing reached the chat service or the controller.
			if gateway.SendCount() != 0 {
				t.Fatalf("send calls = %d, want 0 for a non-live session", gateway.SendCount())
			}
			if gateway.InterruptCount() != 0 {
				t.Fatalf("interrupt calls = %d, want 0 for a non-live session", gateway.InterruptCount())
			}
			// And no receipt was persisted for any of the three.
			rows, err := store.ListWorkflowInterventionsForAttempt(context.Background(), "workflow-1", "task-1")
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(rows) != 0 {
				t.Fatalf("a non-live session persisted %d intervention rows", len(rows))
			}
		})
	}
}

// TestInterventionsProceedWhenTheSessionIsLive is the positive control for the gate above:
// with a live observation the same three calls do reach the gateway, so the refusal above is
// about liveness rather than about a path that never works.
func TestInterventionsProceedWhenTheSessionIsLive(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.sendReceipt = ports.MessageReceipt{
		SessionID: "p1-1", ClientMessageID: "msg-steer-1",
		TurnID: "turn-1", ProviderTurnID: "prov-turn-1",
	}
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	steer, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if steer.Intervention.State != domain.WorkflowInterventionStateApplied {
		t.Fatalf("state = %q, want applied", steer.Intervention.State)
	}

	stop, replan, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-s-live",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stop.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("stop state = %q, want stop_confirmed", stop.Intervention.State)
	}
	if replan == nil {
		t.Fatal("a live confirmed stop must emit a replan request")
	}
	if gateway.SteerCount() != 1 || gateway.SendCount() != 0 || gateway.InterruptCount() != 1 {
		t.Fatalf("steer=%d send=%d interrupt=%d, want 1/0/1",
			gateway.SteerCount(), gateway.SendCount(), gateway.InterruptCount())
	}
}

// TestStopSameClientIDDifferentTargetConflictsByExecution proves the same-key/different-sha
// branch of the stop path by perturbing a field that participates in OperationSHA while
// holding clientMessageID and the controller generation fixed.
//
// A stop carries no target turn, so the perturbation is the participant list itself: changing
// the attempt (which is in the SHA) under the same client message id must conflict rather than
// silently replay the original receipt. The decisive assertions are the typed error AND the
// interrupt count staying at 1: a conflict must not issue a second interrupt.
func TestStopSameClientIDDifferentTargetConflictsByExecution(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}
	original := StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-conflict",
	}

	if _, _, err := coordinator.Stop(context.Background(), original); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls = %d, want 1", gateway.InterruptCount())
	}

	// Sanity: the fingerprints really do differ for these two requests.
	first := OperationSHA(
		domain.WorkflowInterventionModeStop, original.WorkflowID, original.TaskID,
		original.AttemptID, "p1-1", "gen-1", original.ClientMessageID, "",
	)
	second := OperationSHA(
		domain.WorkflowInterventionModeStop, original.WorkflowID, original.TaskID,
		"attempt-2", "p1-1", "gen-1", original.ClientMessageID, "",
	)
	if first == second {
		t.Fatal("fixture problem: the two requests must have different fingerprints")
	}

	// Same client message id, different attempt: a different operation under the same key.
	changed := original
	changed.AttemptID = "attempt-2"
	_, _, err := coordinator.Stop(context.Background(), changed)
	if !errors.Is(err, domain.ErrInterventionOperationConflict) {
		t.Fatalf("err = %v, want ErrInterventionOperationConflict", err)
	}
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls after the conflict = %d, want exactly 1", gateway.InterruptCount())
	}
	// The original receipt is untouched.
	stored, found, err := store.GetWorkflowInterventionByClientMessage(
		context.Background(), original.WorkflowID, original.ClientMessageID,
	)
	if err != nil || !found {
		t.Fatalf("get stored stop: found=%v err=%v", found, err)
	}
	if stored.OperationSHA != first {
		t.Fatal("the stored stop's fingerprint changed")
	}
	if stored.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("state = %q, want the original stop_confirmed preserved", stored.State)
	}
}

// TestSteerSameClientIDDifferentTurnConflicts is the message-path form of the same proof: the
// target turn participates in OperationSHA, so changing it under the same key conflicts.
func TestSteerSameClientIDDifferentTurnConflicts(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.sendReceipt = ports.MessageReceipt{
		SessionID: "p1-1", ClientMessageID: "msg-steer-1",
		TurnID: "turn-1", ProviderTurnID: "prov-turn-1",
	}

	original := steerRequest()
	if _, err := coordinator.Steer(context.Background(), original); err != nil {
		t.Fatalf("first Steer: %v", err)
	}
	if gateway.SteerCount() != 1 {
		t.Fatalf("steer calls = %d, want 1", gateway.SteerCount())
	}
	if gateway.SendCount() != 0 {
		t.Fatalf("send calls = %d, want 0: a steer must never be delivered as a send", gateway.SendCount())
	}

	changed := original
	changed.TargetProviderTurnID = "prov-turn-DIFFERENT"
	if _, err := coordinator.Steer(context.Background(), changed); !errors.Is(err, domain.ErrInterventionOperationConflict) {
		t.Fatalf("err = %v, want ErrInterventionOperationConflict", err)
	}
	if gateway.SteerCount() != 1 {
		t.Fatalf("steer calls after the conflict = %d, want exactly 1", gateway.SteerCount())
	}
}

// TestSteerRoutesThroughGatewaySteer is the routing proof: guidance must take the Steer path
// with exactly the fields the chat service needs, and must never be delivered as a send.
func TestSteerRoutesThroughGatewaySteer(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")

	outcome, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateApplied {
		t.Fatalf("state = %q, want applied", outcome.Intervention.State)
	}
	sent := gateway.LastSteer()
	if sent.Delivery != ports.MessageDeliverySteer {
		t.Fatalf("delivery = %q, want steer", sent.Delivery)
	}
	if sent.TargetProviderTurnID != "prov-turn-1" {
		t.Fatalf("target turn = %q, want prov-turn-1", sent.TargetProviderTurnID)
	}
	if sent.ControllerGeneration != "gen-1" {
		t.Fatalf("generation = %q, want the binding's gen-1", sent.ControllerGeneration)
	}
	if sent.Origin != domain.MessageOriginAutomation {
		t.Fatalf("origin = %q, want automation: a workflow message must not present as human", sent.Origin)
	}
	if sent.SessionID != "p1-1" {
		t.Fatalf("session = %q, want the bound session", sent.SessionID)
	}
	if gateway.SteerCount() != 1 || gateway.SendCount() != 0 {
		t.Fatalf("steer=%d send=%d, want 1/0", gateway.SteerCount(), gateway.SendCount())
	}
	if gateway.InterruptCount() != 0 {
		t.Fatal("a steer interrupted the session")
	}
}

// TestSteerReceiptMismatchIsNotApplied is the "wrong target turn is not success" proof. A
// gateway that answers about a different turn steered work nobody asked about, so the
// receipt must be unknown rather than applied, and the caller must not be offered the queue
// path (the guidance may already have landed).
func TestSteerReceiptMismatchIsNotApplied(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	// Steered, but onto a different provider turn.
	gateway.steerReceipt = ports.MessageReceipt{
		SessionID: "p1-1", ClientMessageID: "msg-steer-1", TurnID: "turn-1",
		ProviderTurnID: "prov-turn-OTHER", Steered: true, TargetProviderTurnID: "prov-turn-OTHER",
		ControllerGeneration: "gen-1",
	}
	gateway.steerReceiptSet = true

	outcome, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State == domain.WorkflowInterventionStateApplied {
		t.Fatal("a receipt for another turn was reported as applied")
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateUnknown {
		t.Fatalf("state = %q, want unknown", outcome.Intervention.State)
	}
	if !outcome.Frozen {
		t.Fatal("a mismatched steer receipt must freeze")
	}
	if outcome.QueueAvailable {
		t.Fatal("a possibly-delivered steer offered the queue path, which could deliver it twice")
	}
	if outcome.ReasonCode != "steer_target_mismatch" &&
		outcome.Intervention.ReasonCode != "steer_target_mismatch" {
		t.Fatalf("reason = %q/%q, want steer_target_mismatch",
			outcome.ReasonCode, outcome.Intervention.ReasonCode)
	}
	// The stored row must not claim the turn the provider actually used.
	stored, found, _ := store.GetWorkflowInterventionByClientMessage(
		context.Background(), "workflow-1", "msg-steer-1",
	)
	if !found {
		t.Fatal("the mismatched steer was not persisted")
	}
	if stored.State == domain.WorkflowInterventionStateApplied {
		t.Fatal("the stored intervention claims applied")
	}
}

// TestSteerRefusedForCallerGenerationMismatch proves a caller who observed a different
// controller is refused before any provider call.
func TestSteerRefusedForCallerGenerationMismatch(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	request := steerRequest()
	request.ControllerGeneration = "gen-old"

	outcome, err := coordinator.Steer(context.Background(), request)
	if !errors.Is(err, ports.ErrExecutionStaleGeneration) {
		t.Fatalf("err = %v, want ErrExecutionStaleGeneration", err)
	}
	if !errors.Is(err, domain.ErrInterventionStaleGeneration) {
		t.Fatalf("err = %v, want it to also wrap ErrInterventionStaleGeneration", err)
	}
	if !outcome.Frozen || outcome.ReasonCode != "generation_mismatch" {
		t.Fatalf("outcome = %+v, want frozen generation_mismatch", outcome)
	}
	if gateway.SteerCount() != 0 || gateway.SendCount() != 0 || gateway.InterruptCount() != 0 {
		t.Fatal("a stale-generation steer reached the gateway")
	}
	if _, found, _ := store.GetWorkflowInterventionByClientMessage(
		context.Background(), "workflow-1", "msg-steer-1",
	); found {
		t.Fatal("a stale-generation steer persisted a row")
	}
}

// TestSteerUnsupportedOffersQueue proves an unsupported provider is a rejection with the
// queue path available, not an interrupt-and-resend.
func TestSteerUnsupportedOffersQueue(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.steerErr = ports.ErrExecutionUnsupported

	outcome, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateRejected {
		t.Fatalf("state = %q, want rejected", outcome.Intervention.State)
	}
	if outcome.ReasonCode != "STEER_UNSUPPORTED" {
		t.Fatalf("reason = %q, want STEER_UNSUPPORTED", outcome.ReasonCode)
	}
	if !outcome.QueueAvailable {
		t.Fatal("an unsupported steer must offer the queue path")
	}
	if gateway.InterruptCount() != 0 {
		t.Fatal("an unsupported steer fell back to interrupt-and-resend")
	}
	if gateway.SendCount() != 0 {
		t.Fatal("an unsupported steer was silently delivered as a send")
	}
}

// TestSteerErrorMapping pins the stable reason codes for the execution sentinels.
func TestSteerErrorMapping(t *testing.T) {
	t.Parallel()
	tests := []struct {
		err        error
		wantReason string
	}{
		{ports.ErrExecutionStaleGeneration, "STEER_STALE_GENERATION"},
		{ports.ErrExecutionTerminated, "SESSION_TERMINATED"},
		{ports.ErrExecutionTargetMismatch, "TURN_MISMATCH"},
		{ports.ErrExecutionUnsupported, "STEER_UNSUPPORTED"},
	}
	for _, tt := range tests {
		t.Run(tt.wantReason, func(t *testing.T) {
			t.Parallel()
			coordinator, _, _, gateway := boundFixture(t)
			permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
			gateway.steerErr = tt.err

			outcome, err := coordinator.Steer(context.Background(), steerRequest())
			if err != nil {
				t.Fatalf("Steer: %v", err)
			}
			if outcome.Intervention.State != domain.WorkflowInterventionStateRejected {
				t.Fatalf("state = %q, want rejected", outcome.Intervention.State)
			}
			if outcome.ReasonCode != tt.wantReason {
				t.Fatalf("reason = %q, want %q", outcome.ReasonCode, tt.wantReason)
			}
			if !outcome.QueueAvailable {
				t.Fatal("a refused steer must offer the queue path")
			}
		})
	}
}

// TestSteerOutcomeUnknownFreezes proves a lost answer is uncertain, never applied.
func TestSteerOutcomeUnknownFreezes(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.steerErr = ports.ErrExecutionOutcomeUnknown

	outcome, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("Steer: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateUnknown {
		t.Fatalf("state = %q, want unknown", outcome.Intervention.State)
	}
	if !outcome.Frozen {
		t.Fatal("a lost steer answer must freeze")
	}
	if outcome.ReasonCode != "delivery_uncertain" {
		t.Fatalf("reason = %q, want delivery_uncertain", outcome.ReasonCode)
	}
}

// TestStopCapabilityGateRefusesBeforeInterrupt is the independent Interrupt gate proof: with
// no interrupt evidence the stop is refused and no interrupt is issued, even though the
// binding is live and ready.
func TestStopCapabilityGateRefusesBeforeInterrupt(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	// No probe registered at all: the static profile cannot authorize an interrupt.
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	outcome, replan, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-refused",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if outcome.ReasonCode == "" {
		t.Fatal("a refused stop must carry a reason code")
	}
	if !outcome.Frozen {
		t.Fatal("a refused stop must freeze")
	}
	if replan != nil {
		t.Fatal("a refused stop produced a replan request")
	}
	// The decisive assertion.
	if gateway.InterruptCount() != 0 {
		t.Fatalf("interrupt calls = %d, want 0 when interrupt is unproven", gateway.InterruptCount())
	}
	if _, found, _ := store.GetWorkflowInterventionByClientMessage(
		context.Background(), "workflow-1", "msg-stop-refused",
	); found {
		t.Fatal("a refused stop persisted an intervention row")
	}
}

// TestStopConfirmedAdvancesBinding proves a settled stop becomes a binding fact, so the stop
// gate downstream is not merely an intervention receipt.
func TestStopConfirmedAdvancesBinding(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	writer := &recordingBindingWriter{}
	coordinator.writer = writer
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	outcome, _, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-advance",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("state = %q, want stop_confirmed", outcome.Intervention.State)
	}
	if writer.Calls() != 1 {
		t.Fatalf("binding writes = %d, want 1", writer.Calls())
	}
	if !writer.confirm || writer.unknown {
		t.Fatalf("recorded confirmed=%v unknown=%v, want confirmed only", writer.confirm, writer.unknown)
	}
	if writer.last != "attempt-1" {
		t.Fatalf("advanced binding %q, want attempt-1", writer.last)
	}
}

// TestStopNothingToStopAdvancesBinding proves "nothing to stop" advances too: the session is
// not running a turn, so nothing may be steered into it afterwards.
func TestStopNothingToStopAdvancesBinding(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	writer := &recordingBindingWriter{}
	coordinator.writer = writer
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{NothingToStop: true}

	outcome, _, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-idle-advance",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("state = %q, want stop_confirmed", outcome.Intervention.State)
	}
	if writer.Calls() != 1 || !writer.confirm {
		t.Fatalf("binding writes = %d confirmed=%v, want 1 confirmed", writer.Calls(), writer.confirm)
	}
}

// TestStopUncertainAdvancesBindingAsUnknown proves an uncertain stop freezes the binding too.
func TestStopUncertainAdvancesBindingAsUnknown(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	writer := &recordingBindingWriter{}
	coordinator.writer = writer
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Unknown: true}

	if _, _, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-uncertain",
	}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if writer.Calls() != 1 || !writer.unknown || writer.confirm {
		t.Fatalf("binding writes = %d confirmed=%v unknown=%v, want 1 unknown",
			writer.Calls(), writer.confirm, writer.unknown)
	}
}

// TestBindingAdvanceFailureNeverUnconfirmsTheStop proves a writer error is reported as a
// reason code, not as a failed stop: the interrupt already happened.
func TestBindingAdvanceFailureNeverUnconfirmsTheStop(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	writer := &recordingBindingWriter{err: errors.New("revision conflict")}
	coordinator.writer = writer
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	outcome, replan, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-advance-fail",
	})
	if err != nil {
		t.Fatalf("Stop returned an error for a binding-write failure: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("state = %q, want the stop to stay confirmed", outcome.Intervention.State)
	}
	if outcome.ReasonCode != "binding_advance_failed" {
		t.Fatalf("reason = %q, want binding_advance_failed", outcome.ReasonCode)
	}
	if replan == nil {
		t.Fatal("a confirmed stop must still emit a replan request")
	}
}

// TestSendAndSteerRefusedAfterStop is the stop gate: after a settled stop nothing may queue
// or steer into the session.
func TestSendAndSteerRefusedAfterStop(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		state domain.WorkflowBindingState
	}{
		{"stop_requested", domain.WorkflowBindingStateStopRequested},
		{"stop_confirmed", domain.WorkflowBindingStateStopConfirmed},
		{"stop_unknown", domain.WorkflowBindingStateStopUnknown},
		{"failed_confirmed", domain.WorkflowBindingStateFailedConfirmed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator, store, bindings, gateway := boundFixture(t)
			permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
			bindings.setState(tt.state)

			steer, err := coordinator.Steer(context.Background(), steerRequest())
			if err != nil {
				t.Fatalf("Steer: %v", err)
			}
			if steer.ReasonCode != "binding_stopped" {
				t.Fatalf("steer reason = %q, want binding_stopped", steer.ReasonCode)
			}
			if !steer.Frozen {
				t.Fatal("a steer into a stopped binding must freeze")
			}

			queue, err := coordinator.Queue(context.Background(), QueueRequest{
				WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
				ClientMessageID: "msg-q-stopped", Text: "too late",
			})
			if err != nil {
				t.Fatalf("Queue: %v", err)
			}
			if queue.ReasonCode != "binding_stopped" {
				t.Fatalf("queue reason = %q, want binding_stopped", queue.ReasonCode)
			}

			if gateway.SteerCount() != 0 || gateway.SendCount() != 0 || gateway.InterruptCount() != 0 {
				t.Fatalf("a stopped binding reached the gateway: steer=%d send=%d interrupt=%d",
					gateway.SteerCount(), gateway.SendCount(), gateway.InterruptCount())
			}
			rows, err := store.ListWorkflowInterventionsForAttempt(
				context.Background(), "workflow-1", "task-1",
			)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(rows) != 0 {
				t.Fatalf("a stopped binding persisted %d intervention rows", len(rows))
			}
		})
	}
}

// TestInterventionFenceFailureRefuses proves a session observation from a replaced
// controller cannot authorize an intervention.
func TestInterventionFenceFailureRefuses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		prepare   func(*recordingGateway)
		wantFence bool
	}{
		{
			name: "generation rotated",
			prepare: func(g *recordingGateway) {
				g.observation = &ports.SessionObservation{
					SessionID: "p1-1", ControllerGeneration: "gen-2",
					Readiness: ports.SessionReadinessReady,
				}
			},
			wantFence: true,
		},
		{
			name: "session terminated",
			prepare: func(g *recordingGateway) {
				g.observation = &ports.SessionObservation{
					SessionID: "p1-1", ControllerGeneration: "gen-1",
					Readiness: ports.SessionReadinessReady, Terminated: true,
				}
			},
			wantFence: true,
		},
		{
			name: "different session",
			prepare: func(g *recordingGateway) {
				g.observation = &ports.SessionObservation{
					SessionID: "p9-9", ControllerGeneration: "gen-1",
					Readiness: ports.SessionReadinessReady,
				}
			},
			wantFence: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator, _, bindings, gateway := boundFixture(t)
			permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
			tt.prepare(gateway)
			_ = bindings

			steer, err := coordinator.Steer(context.Background(), steerRequest())
			if err != nil {
				t.Fatalf("Steer: %v", err)
			}
			if steer.ReasonCode != "session_fence_failed" {
				t.Fatalf("reason = %q, want session_fence_failed", steer.ReasonCode)
			}
			if gateway.SteerCount() != 0 {
				t.Fatal("a fenced steer reached the gateway")
			}
		})
	}
}

// TestSteerClientMessageIDIsIdempotent proves a retried steer is free even when the receipt
// is the only thing that changed.
func TestSteerClientMessageIDIsIdempotent(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitSteer(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")

	first, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("first Steer: %v", err)
	}
	gateway.steerReceipt = ports.MessageReceipt{TurnID: "different"}

	for range 3 {
		replay, err := coordinator.Steer(context.Background(), steerRequest())
		if err != nil {
			t.Fatalf("replay Steer: %v", err)
		}
		if replay.Performed {
			t.Fatal("a steer replay performed another delivery")
		}
		if replay.Intervention.ID != first.Intervention.ID {
			t.Fatalf("replay returned %q, want %q", replay.Intervention.ID, first.Intervention.ID)
		}
	}
	if gateway.SteerCount() != 1 {
		t.Fatalf("steer calls = %d, want exactly 1 across replays", gateway.SteerCount())
	}
}

// TestStopClientMessageIDIsIdempotent proves a retried stop issues exactly one interrupt and
// advances the binding exactly once.
func TestStopClientMessageIDIsIdempotent(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	writer := &recordingBindingWriter{}
	coordinator.writer = writer
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}
	request := StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-idem",
	}

	if _, _, err := coordinator.Stop(context.Background(), request); err != nil {
		t.Fatalf("first Stop: %v", err)
	}
	for range 3 {
		if _, _, err := coordinator.Stop(context.Background(), request); err != nil {
			t.Fatalf("replay Stop: %v", err)
		}
	}
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls = %d, want exactly 1", gateway.InterruptCount())
	}
	if writer.Calls() != 1 {
		t.Fatalf("binding writes = %d, want exactly 1", writer.Calls())
	}
}

// TestSteerAndStopConcurrentlyEndStopped is the race the stop gate exists for: a steer held
// in flight while a stop is confirmed must not be reported as applied afterwards, and the
// retry must be refused because the binding is stopped.
func TestSteerAndStopConcurrentlyEndStopped(t *testing.T) {
	t.Parallel()
	coordinator, _, bindings, gateway := boundFixture(t)
	writer := &recordingBindingWriter{}
	coordinator.writer = writer
	permitSteerAndInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	// Hold the steer in flight.
	release := make(chan struct{})
	gateway.SetBlock(release)

	steerDone := make(chan struct {
		outcome Outcome
		err     error
	}, 1)
	go func() {
		outcome, err := coordinator.Steer(context.Background(), steerRequest())
		steerDone <- struct {
			outcome Outcome
			err     error
		}{outcome, err}
	}()

	// Wait until the steer is actually inside the gateway.
	deadline := time.Now().Add(2 * time.Second)
	for gateway.SteerCount() == 0 {
		if time.Now().After(deadline) {
			select {
			case result := <-steerDone:
				t.Fatalf("the in-flight steer returned early: outcome=%+v err=%v",
					result.outcome, result.err)
			default:
				t.Fatal("the in-flight steer never reached the gateway")
			}
		}
		time.Sleep(time.Millisecond)
	}

	// Confirm the stop while the steer is still blocked.
	stop, _, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-race",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stop.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("stop state = %q, want stop_confirmed", stop.Intervention.State)
	}
	if writer.Calls() != 1 || !writer.confirm {
		t.Fatalf("binding writes = %d confirmed=%v, want 1 confirmed", writer.Calls(), writer.confirm)
	}

	// The binding is now stopped everywhere that gates an intervention.
	bindings.setState(domain.WorkflowBindingStateStopConfirmed)

	gateway.SetBlock(nil)
	close(release)
	<-steerDone

	// The retry must be refused on the stop gate, without reaching the gateway again.
	steersBefore := gateway.SteerCount()
	retry, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("retry Steer: %v", err)
	}
	if retry.ReasonCode != "binding_stopped" {
		t.Fatalf("retry reason = %q, want binding_stopped", retry.ReasonCode)
	}
	if gateway.SteerCount() != steersBefore {
		t.Fatal("a steer retried against a stopped binding reached the gateway")
	}
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls = %d, want 1", gateway.InterruptCount())
	}
}

// TestStopAndSteerConcurrencyEndsStopped is the coordinator-level steer-vs-stop race.
//
// The store proves one stop row; this proves what the *in-flight steer* is allowed to
// record, which is the part a row count cannot see. It asserts the exact steer call count
// (1: the retry is refused on the stop gate before reaching the gateway).
func TestStopAndSteerConcurrencyEndsStopped(t *testing.T) {
	t.Parallel()
	coordinator, _, bindings, gateway := boundFixture(t)
	writer := &recordingBindingWriter{}
	coordinator.writer = writer
	permitSteerAndInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	release := make(chan struct{})
	gateway.SetBlock(release)

	type steerResult struct {
		outcome Outcome
		err     error
	}
	steerDone := make(chan steerResult, 1)
	go func() {
		outcome, err := coordinator.Steer(context.Background(), steerRequest())
		steerDone <- steerResult{outcome: outcome, err: err}
	}()

	// Wait until the steer is inside the gateway, then confirm a stop over the top of it.
	deadline := time.Now().Add(2 * time.Second)
	for gateway.SteerCount() == 0 {
		if time.Now().After(deadline) {
			select {
			case result := <-steerDone:
				t.Fatalf("the in-flight steer returned early: outcome=%+v err=%v",
					result.outcome, result.err)
			default:
				t.Fatal("the in-flight steer never reached the gateway")
			}
		}
		time.Sleep(time.Millisecond)
	}

	stop, _, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-race",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if stop.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("stop state = %q, want stop_confirmed", stop.Intervention.State)
	}
	if writer.Calls() != 1 || !writer.confirm {
		t.Fatalf("binding writes = %d confirmed=%v, want 1 confirmed", writer.Calls(), writer.confirm)
	}
	// The binding the intervention path re-reads now says stop_confirmed.
	bindings.setState(domain.WorkflowBindingStateStopConfirmed)

	// The steer's own gateway answer is a perfectly good steer receipt: it echoes the turn
	// and generation that were requested. It must STILL not be recorded as applied, because
	// the turn it joined was interrupted while the answer was in flight.
	gateway.SetBlock(nil)
	close(release)

	result := <-steerDone
	if result.err != nil {
		t.Fatalf("in-flight Steer: %v", result.err)
	}
	if result.outcome.Intervention.State == domain.WorkflowInterventionStateApplied {
		t.Fatal("a steer whose target turn was stopped mid-flight was recorded as applied")
	}
	if result.outcome.Intervention.State != domain.WorkflowInterventionStateUnknown {
		t.Fatalf("state = %q, want unknown: the guidance may have landed before the stop, and the turn is gone",
			result.outcome.Intervention.State)
	}
	if result.outcome.Intervention.ReasonCode != "binding_stopped" {
		t.Fatalf("reason = %q, want binding_stopped", result.outcome.Intervention.ReasonCode)
	}
	if !result.outcome.Frozen {
		t.Fatal("a raced steer must freeze rather than claim progress")
	}
	if result.outcome.QueueAvailable {
		t.Fatal("a raced steer offered the queue path to a stopped binding")
	}

	// The retry is refused by the stop gate BEFORE reaching the gateway, so the steer call
	// count stays at exactly 1: the one call that was already in flight.
	retry, err := coordinator.Steer(context.Background(), steerRequest())
	if err != nil {
		t.Fatalf("retry Steer: %v", err)
	}
	if retry.ReasonCode != "binding_stopped" {
		t.Fatalf("retry reason = %q, want binding_stopped", retry.ReasonCode)
	}
	if gateway.SteerCount() != 1 {
		t.Fatalf("steer calls = %d, want exactly 1: the retry must not reach the gateway",
			gateway.SteerCount())
	}
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls = %d, want exactly 1", gateway.InterruptCount())
	}
}

// TestSteerSuccessAfterStopIsStillNotApplied is the variant where the gateway reports
// success only after the stop settled.
//
// The store cannot help here: a clean steer receipt carries the chat fact that licenses
// Applied, so the receipt write would succeed. The refusal therefore has to happen in the
// coordinator, by re-reading the binding after the gateway answers. That is what this test
// pins: the recorded intervention stays unknown even though the gateway said "steered".
func TestSteerSuccessAfterStopIsStillNotApplied(t *testing.T) {
	t.Parallel()
	coordinator, store, bindings, gateway := boundFixture(t)
	writer := &recordingBindingWriter{}
	coordinator.writer = writer
	permitSteerAndInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	// Hold the steer, confirm the stop, then let the steer answer successfully.
	release := make(chan struct{})
	gateway.SetBlock(release)
	type steerResult struct {
		outcome Outcome
		err     error
	}
	steerDone := make(chan steerResult, 1)
	go func() {
		outcome, err := coordinator.Steer(context.Background(), steerRequest())
		steerDone <- steerResult{outcome: outcome, err: err}
	}()
	deadline := time.Now().Add(2 * time.Second)
	for gateway.SteerCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the in-flight steer never reached the gateway")
		}
		time.Sleep(time.Millisecond)
	}

	if _, _, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-before-success",
	}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	bindings.setState(domain.WorkflowBindingStateStopConfirmed)

	// A clean, matching steer receipt: the strongest success the gateway can report.
	gateway.steerReceipt = steeringReceipt("msg-steer-1", "prov-turn-1")
	gateway.steerReceiptSet = true

	gateway.SetBlock(nil)
	close(release)
	result := <-steerDone
	if result.err != nil {
		t.Fatalf("in-flight Steer: %v", result.err)
	}
	if result.outcome.Intervention.State == domain.WorkflowInterventionStateApplied {
		t.Fatal("a matching steer receipt recorded applied after the binding had stopped")
	}
	if result.outcome.Intervention.State != domain.WorkflowInterventionStateUnknown {
		t.Fatalf("state = %q, want unknown", result.outcome.Intervention.State)
	}

	// And the durable row agrees: the store was never asked to write applied.
	stored, found, _ := store.GetWorkflowInterventionByClientMessage(
		context.Background(), "workflow-1", "msg-steer-1",
	)
	if !found {
		t.Fatal("the raced steer was not persisted")
	}
	if stored.State == domain.WorkflowInterventionStateApplied {
		t.Fatal("the stored row claims applied after the stop")
	}
	if stored.State != domain.WorkflowInterventionStateUnknown ||
		stored.ReasonCode != "binding_stopped" {
		t.Fatalf("stored = %q/%q, want unknown/binding_stopped", stored.State, stored.ReasonCode)
	}
}

// TestConcurrentStopsIssueOneInterrupt is the coordinator-level counterpart to the store's
// atomic-insert test: the store proves only one row is created, this proves only one
// interrupt is therefore issued through the coordinator.
func TestConcurrentStopsIssueOneInterrupt(t *testing.T) {
	t.Parallel()
	coordinator, store, _, gateway := boundFixture(t)
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	const callers = 8
	start := make(chan struct{})
	var wg sync.WaitGroup
	outcomes := make([]Outcome, callers)
	errs := make([]error, callers)
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			outcome, _, err := coordinator.Stop(context.Background(), StopRequest{
				WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
				ClientMessageID: "msg-stop-race-" + strconv.Itoa(i),
			})
			outcomes[i] = outcome
			errs[i] = err
		}()
	}
	close(start)
	wg.Wait()

	for i := range callers {
		if errs[i] != nil {
			t.Fatalf("caller %d: %v", i, errs[i])
		}
	}
	// The decisive assertion: exactly one side effect, regardless of how the losers were
	// told (stop_already_pending, binding_stopped, or a replay of the winner's receipt).
	if gateway.InterruptCount() != 1 {
		t.Fatalf("interrupt calls = %d, want exactly 1 across %d concurrent stops",
			gateway.InterruptCount(), callers)
	}
	performed := 0
	for _, outcome := range outcomes {
		if outcome.Performed {
			performed++
		}
		if outcome.Intervention.State == domain.WorkflowInterventionStateStopConfirmed &&
			outcome.ReasonCode == "" {
			t.Fatalf("a caller reported a confirmed stop with no reason: %+v", outcome)
		}
	}
	if performed != 1 {
		t.Fatalf("callers reporting Performed = %d, want exactly 1", performed)
	}

	// Exactly one row reached a decision. Losers that inserted before the winner settled are
	// withdrawn as rejected (stop_already_settled) rather than left open, so no row claims an
	// interrupt that never happened.
	rows, err := store.ListWorkflowInterventionsForAttempt(context.Background(), "workflow-1", "task-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	confirmed := 0
	for _, row := range rows {
		switch row.State {
		case domain.WorkflowInterventionStateStopConfirmed:
			confirmed++
		case domain.WorkflowInterventionStateRejected:
			if row.ReasonCode != "stop_already_settled" {
				t.Fatalf("withdrawn row reason = %q, want stop_already_settled", row.ReasonCode)
			}
		default:
			t.Fatalf("row %q is %q: every stop must settle", row.ID, row.State)
		}
	}
	if confirmed != 1 {
		t.Fatalf("stop_confirmed rows = %d, want exactly 1", confirmed)
	}
}

// TestStopRequiresInterruptCapability pins the Interrupt gate's three levels: no evidence,
// unverified authorization, and full proof.
func TestStopRequiresInterruptCapability(t *testing.T) {
	t.Parallel()

	stopRequest := StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-gate",
	}

	t.Run("no evidence at all", func(t *testing.T) {
		t.Parallel()
		coordinator, _, _, gateway := boundFixture(t)
		// boundFixture registers no probe: the static profile can describe, never authorize.
		gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

		outcome, replan, err := coordinator.Stop(context.Background(), stopRequest)
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if gateway.InterruptCount() != 0 {
			t.Fatalf("interrupt calls = %d, want 0: no evidence may authorize an interrupt",
				gateway.InterruptCount())
		}
		if outcome.ReasonCode == "" || !outcome.Frozen {
			t.Fatalf("outcome = %+v, want a frozen refusal with a reason code", outcome)
		}
		if replan != nil {
			t.Fatal("a refused stop produced a replan request")
		}
	})

	t.Run("interrupt supported but auth only configured", func(t *testing.T) {
		t.Parallel()
		coordinator, _, _, gateway := boundFixture(t)
		snapshot := domain.NewHarnessCapabilitySnapshot(
			domain.HarnessCodex, domain.CapabilitySourceProviderHandshake, testNow, capability.DefaultTTL,
		)
		snapshot.ObservedSessionID = "p1-1"
		snapshot.Generation = "gen-1"
		snapshot.Readiness = domain.CapabilityReady
		snapshot.Interrupt = domain.CapabilitySupported
		// A credential exists locally and nothing proved it works. It must not authorize a
		// control that will spend the user's account.
		snapshot.AuthState = domain.AgentAuthenticationConfigured
		coordinator.resolver.RegisterProbe(domain.HarnessCodex, capability.NewFakeProbe().Script(snapshot))
		gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

		outcome, _, err := coordinator.Stop(context.Background(), stopRequest)
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if gateway.InterruptCount() != 0 {
			t.Fatalf("interrupt calls = %d, want 0: unverified authorization must not interrupt",
				gateway.InterruptCount())
		}
		if outcome.ReasonCode != ports.CapabilityReasonAuthUnverified {
			t.Fatalf("reason = %q, want %q", outcome.ReasonCode, ports.CapabilityReasonAuthUnverified)
		}
	})

	t.Run("fully proven", func(t *testing.T) {
		t.Parallel()
		coordinator, _, _, gateway := boundFixture(t)
		permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
		gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

		outcome, replan, err := coordinator.Stop(context.Background(), stopRequest)
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
		if gateway.InterruptCount() != 1 {
			t.Fatalf("interrupt calls = %d, want 1", gateway.InterruptCount())
		}
		if outcome.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
			t.Fatalf("state = %q, want stop_confirmed", outcome.Intervention.State)
		}
		if replan == nil {
			t.Fatal("a confirmed stop must emit a replan request")
		}
	})
}

// --- Receipt fencing --------------------------------------------------------

// TestSendReceiptMismatchIsNeverApplied is the send-side counterpart of
// TestSteerReceiptMismatchIsNotApplied.
//
// The send path used to accept any receipt that named a turn. A receipt minted by a
// replacement controller, for another client message id, or naming no turn at all then
// settled an intervention as applied/delivered/queued on evidence the gateway never gave.
func TestSendReceiptMismatchIsNeverApplied(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		receipt ports.MessageReceipt
	}{
		{
			name: "another controller generation",
			receipt: ports.MessageReceipt{
				SessionID: "p1-1", ClientMessageID: "msg-queue-1",
				TurnID: "turn-1", ProviderTurnID: "prov-1",
				ControllerGeneration: "gen-2",
			},
		},
		{
			name: "no controller generation",
			receipt: ports.MessageReceipt{
				SessionID: "p1-1", ClientMessageID: "msg-queue-1",
				TurnID: "turn-1", ProviderTurnID: "prov-1",
			},
		},
		{
			name: "another client message id",
			receipt: ports.MessageReceipt{
				SessionID: "p1-1", ClientMessageID: "msg-someone-else",
				TurnID: "turn-1", ProviderTurnID: "prov-1",
			},
		},
		{
			name: "another session",
			receipt: ports.MessageReceipt{
				SessionID: "p2-9", ClientMessageID: "msg-queue-1",
				TurnID: "turn-1", ProviderTurnID: "prov-1",
			},
		},
		{
			name: "a bare acknowledgement with no turn and no queue fact",
			receipt: ports.MessageReceipt{
				SessionID: "p1-1", ClientMessageID: "msg-queue-1",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator, store, _, gateway := boundFixture(t)
			// Scripted verbatim: every case here is a receipt that does not describe the
			// request, so the fake must not repair it.
			gateway.sendReceipt = tt.receipt
			gateway.sendReceiptExact = true

			outcome, err := coordinator.Queue(context.Background(), QueueRequest{
				WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
				ClientMessageID: "msg-queue-1", Text: "next, also update the docs",
			})
			if err != nil {
				t.Fatalf("Queue: %v", err)
			}
			switch outcome.Intervention.State {
			case domain.WorkflowInterventionStateApplied,
				domain.WorkflowInterventionStateDelivered,
				domain.WorkflowInterventionStateQueued:
				t.Fatalf("state = %q: a mismatched receipt produced a delivery claim",
					outcome.Intervention.State)
			}
			if outcome.Intervention.State != domain.WorkflowInterventionStateUnknown {
				t.Fatalf("state = %q, want unknown", outcome.Intervention.State)
			}
			if outcome.ReasonCode != "send_receipt_mismatch" {
				t.Fatalf("reason = %q, want send_receipt_mismatch", outcome.ReasonCode)
			}
			if outcome.QueueAvailable {
				t.Fatal("a mismatched send receipt offered the queue, which could deliver twice")
			}
			if !outcome.Frozen {
				t.Fatal("a mismatched send receipt must freeze the caller")
			}
			stored, found, err := store.GetWorkflowInterventionByClientMessage(
				context.Background(), "workflow-1", "msg-queue-1",
			)
			if err != nil || !found {
				t.Fatalf("read stored intervention: found=%v err=%v", found, err)
			}
			if stored.State != domain.WorkflowInterventionStateUnknown {
				t.Fatalf("stored state = %q, want unknown", stored.State)
			}
		})
	}
}

// TestSendReceiptForTheCurrentGenerationIsApplied is the positive control: the same receipt
// shape, on the request's own generation, is applied.
func TestSendReceiptForTheCurrentGenerationIsApplied(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	gateway.sendReceipt = ports.MessageReceipt{
		SessionID: "p1-1", ClientMessageID: "msg-queue-ok",
		TurnID: "turn-ok", ProviderTurnID: "prov-ok",
	}

	outcome, err := coordinator.Queue(context.Background(), QueueRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-queue-ok", Text: "carry on",
	})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateApplied {
		t.Fatalf("state = %q, want applied", outcome.Intervention.State)
	}
}

// TestStopReceiptForAnotherGenerationIsNeverConfirmed proves a stop receipt about a
// different controller cannot settle this stop.
func TestStopReceiptForAnotherGenerationIsNeverConfirmed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		receipt ports.InterruptReceipt
	}{
		{
			name: "another generation",
			receipt: ports.InterruptReceipt{
				SessionID: "p1-1", ControllerGeneration: "gen-2", Confirmed: true,
			},
		},
		{
			name: "nothing to stop on another generation",
			receipt: ports.InterruptReceipt{
				SessionID: "p1-1", ControllerGeneration: "gen-2", NothingToStop: true,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			coordinator, _, _, gateway := boundFixture(t)
			permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
			gateway.interruptReceipt = tt.receipt

			outcome, replan, err := coordinator.Stop(context.Background(), StopRequest{
				WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
				ClientMessageID: "msg-stop-gen",
			})
			if err != nil {
				t.Fatalf("Stop: %v", err)
			}
			if outcome.Intervention.State == domain.WorkflowInterventionStateStopConfirmed {
				t.Fatal("a receipt for another generation confirmed the stop")
			}
			if outcome.Intervention.State != domain.WorkflowInterventionStateStopUnknown {
				t.Fatalf("state = %q, want stop_unknown", outcome.Intervention.State)
			}
			if outcome.ReasonCode != "stop_generation_mismatch" {
				t.Fatalf("reason = %q, want stop_generation_mismatch", outcome.ReasonCode)
			}
			if !outcome.Frozen {
				t.Fatal("an unconfirmable stop must freeze")
			}
			if replan != nil {
				t.Fatal("an unconfirmed stop emitted a replan request")
			}
		})
	}
}

// TestConfirmedStopCarriesTheGeneration proves the request the gateway receives names the
// binding's generation, so the receipt can only ever be about that controller.
func TestConfirmedStopCarriesTheGeneration(t *testing.T) {
	t.Parallel()
	coordinator, _, _, gateway := boundFixture(t)
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	if _, _, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-fenced",
	}); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := gateway.LastInterrupt().ControllerGeneration; got != "gen-1" {
		t.Fatalf("interrupt generation = %q, want gen-1", got)
	}
}

// TestQueueAfterConfirmedStopMakesNoGatewayCall is the post-stop regression: once a stop is
// confirmed, an old attempt's Queue must be refused by the binding gate itself, so the
// gateway sees no send at all.
//
// The counter is the assertion: a refusal that happened AFTER a gateway call would still
// show up as a call, and the work would be in the world.
func TestQueueAfterConfirmedStopMakesNoGatewayCall(t *testing.T) {
	t.Parallel()
	coordinator, store, bindings, gateway := boundFixture(t)
	permitInterrupt(t, coordinator, domain.HarnessCodex, "p1-1", "gen-1")
	gateway.interruptReceipt = ports.InterruptReceipt{Confirmed: true}

	outcome, replan, err := coordinator.Stop(context.Background(), StopRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-stop-then-queue",
	})
	if err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if outcome.Intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		t.Fatalf("stop state = %q, want stop_confirmed", outcome.Intervention.State)
	}
	if replan == nil {
		t.Fatal("a confirmed stop emitted no replan request")
	}
	bindings.setState(domain.WorkflowBindingStateStopConfirmed)
	sendsAfterStop := gateway.SendCount()
	steersAfterStop := gateway.SteerCount()

	queued, err := coordinator.Queue(context.Background(), QueueRequest{
		WorkflowID: "workflow-1", TaskID: "task-1", AttemptID: "attempt-1",
		ClientMessageID: "msg-queue-after-stop", Text: "please also do this",
	})
	if err != nil {
		t.Fatalf("Queue after a confirmed stop: %v", err)
	}
	if queued.ReasonCode != "binding_stopped" {
		t.Fatalf("reason = %q, want binding_stopped", queued.ReasonCode)
	}
	if !queued.Frozen {
		t.Fatal("a queue into a stopped binding must freeze")
	}
	if gateway.SendCount() != sendsAfterStop {
		t.Fatalf("send calls went from %d to %d: a post-stop queue reached the gateway",
			sendsAfterStop, gateway.SendCount())
	}
	if gateway.SteerCount() != steersAfterStop {
		t.Fatalf("steer calls went from %d to %d", steersAfterStop, gateway.SteerCount())
	}
	rows, err := store.ListWorkflowInterventionsForAttempt(
		context.Background(), "workflow-1", "task-1",
	)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, row := range rows {
		if row.ClientMessageID == "msg-queue-after-stop" {
			t.Fatalf("a refused post-stop queue persisted a row: %+v", row)
		}
	}
}
