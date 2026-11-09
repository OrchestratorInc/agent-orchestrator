package execution

import (
	"context"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// FakeGateway is a scripted ports.ExecutionGateway for offline tests.
//
// It exists so the coordinator's two hardest rules can be proven without a provider:
//
//   - A spawn whose answer is lost but whose side effect happened. LoseSpawnResponse
//     records the created session in the durable map and then returns
//     ErrSpawnOutcomeUnknown, exactly the shape a dropped connection produces.
//   - A session that never becomes ready. Readiness is scripted per session, so an
//     async provisioning session can be observed forever without ever reaching ready.
//
// It is NOT a production adapter, but there is NO compile-time seal that enforces that. An
// earlier version of this comment claimed an unexported marker method made a mis-wire a
// compile error; that was false. A seal of that kind only works through an interface declared
// in THIS package whose method set includes the unexported method, and the gateway port
// (ports.ExecutionGateway) lives in a different package and knows nothing about it. Any package
// can therefore name *FakeGateway and pass it where a real gateway is expected.
//
// What actually keeps this offline is behavioural, and is what the tests assert:
//
//   - No real ports.ExecutionGateway implementation exists in the repo: the Session-Manager
//     backed gateway is blocked on the M0-05 provider budget.
//   - daemon.newM2Services is the single wiring site, and it is the only place a gateway is
//     constructed for the daemon.
//   - A coordinator built without a gateway refuses work instead of degrading into a no-op.
type FakeGateway struct {
	mu sync.Mutex

	// sessions is the durable state this fake "owns": a spawn writes here before any
	// answer is returned, which is what makes a lost response observable.
	sessions map[domain.SessionID]ports.SessionObservation
	// byRequestID is the ClientRequestID index, the fake's answer to FindSpawn.
	byRequestID map[string]domain.SessionID

	sessionSeq int

	// Scripted spawn behavior.
	spawnErr          error
	loseSpawnResponse bool
	spawnReadiness    ports.SessionReadiness
	spawnGeneration   string
	// spawnWorktrees is the repository evidence every successful spawn reports. It
	// exists so a test can withhold it (the single-repo shape) or supply it (the
	// same-or-different-commit case) without a real Git worktree.
	spawnWorktrees []ports.RepoWorktreeObservation

	// Scripted per-method behavior.
	inspectErr       error
	findErr          error
	sendErr          error
	steerErr         error
	interruptErr     error
	resumeErr        error
	interruptReceipt ports.InterruptReceipt
	// observationOverride, when set, is what InspectSession answers regardless of the id it
	// was asked about. It models the one case a keyed map cannot: an owner that answers about
	// a different session than the caller asked about.
	observationOverride *ports.SessionObservation
	// sendReceipt, when set, is the receipt Send returns. It lets a test script a provider
	// turn id so a steer can prove it joined the turn it named.
	sendReceipt    ports.MessageReceipt
	sendReceiptSet bool
	// steerReceipt, when set, is the receipt Steer returns. It is separate from sendReceipt
	// because the two paths must be independently observable: a test asserting "steer never
	// routes through Send" needs to script one without answering the other.
	steerReceipt    ports.MessageReceipt
	steerReceiptSet bool

	spawnCalls     int
	sendCalls      int
	steerCalls     int
	resumeCalls    int
	interruptCalls int
	findCalls      int
	inspectCalls   int
	lastSpawn      ports.SpawnRequest
	lastSend       ports.MessageRequest
	lastSteer      ports.MessageRequest
	lastInterrupt  ports.InterruptRequest
	lastResume     ports.ResumeRequest

	// now supplies observation timestamps. Tests inject a fixed clock so nothing
	// depends on wall time.
	now func() time.Time

	// materialize, when set, is asked to create a real session row before the fake
	// reports success. The coordinator's integration tests need it because
	// workflow_attempt_bindings.session_id is a real foreign key: a fabricated id would
	// be rejected by the database, and weakening the constraint to make a test pass
	// would remove the very guarantee M2-03 established.
	materialize func(ctx context.Context, sessionID domain.SessionID, request ports.SpawnRequest) (domain.SessionID, error)
}

// WithSessionMaterializer installs a hook that must create a real session row for the
// id the fake is about to report. Returning a different id is allowed so a caller can
// let the store assign one.
func (g *FakeGateway) WithSessionMaterializer(
	fn func(ctx context.Context, sessionID domain.SessionID, request ports.SpawnRequest) (domain.SessionID, error),
) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.materialize = fn
	return g
}

// NewFakeGateway builds a fake that creates provisioning sessions by default. A
// provisioning default is deliberate: it is the state that most tempts a caller to
// assume success, so the default exercises the stricter path.
func NewFakeGateway() *FakeGateway {
	return &FakeGateway{
		sessions:       make(map[domain.SessionID]ports.SessionObservation),
		byRequestID:    make(map[string]domain.SessionID),
		spawnReadiness: ports.SessionReadinessProvisioning,
		now:            time.Now,
	}
}

// LoseSpawnResponse makes every spawn create its session and then fail with
// ErrSpawnOutcomeUnknown, simulating a dropped answer after the side effect landed.
func (g *FakeGateway) LoseSpawnResponse() *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.loseSpawnResponse = true
	return g
}

// FailSpawnWithoutEffect makes every spawn fail before creating anything, which is the
// one case a coordinator may record as failed_confirmed.
func (g *FakeGateway) FailSpawnWithoutEffect(err error) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spawnErr = err
	return g
}

// SpawnReadiness scripts the readiness a successful spawn reports.
func (g *FakeGateway) SpawnReadiness(readiness ports.SessionReadiness) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spawnReadiness = readiness
	return g
}

// SpawnWorktrees scripts the repository evidence a successful spawn reports. An
// empty slice is the ordinary single-repo session, which writes no
// session_worktrees rows at all.
func (g *FakeGateway) SpawnWorktrees(worktrees []ports.RepoWorktreeObservation) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spawnWorktrees = append([]ports.RepoWorktreeObservation(nil), worktrees...)
	return g
}

// SpawnGeneration scripts the controller generation a successful spawn reports.
func (g *FakeGateway) SpawnGeneration(generation string) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spawnGeneration = generation
	return g
}

// SetSessionReadiness scripts readiness for one existing session, so a test can observe
// a session that never becomes ready.
func (g *FakeGateway) SetSessionReadiness(sessionID domain.SessionID, readiness ports.SessionReadiness) {
	g.mu.Lock()
	defer g.mu.Unlock()
	observation := g.sessions[sessionID]
	observation.Readiness = readiness
	g.sessions[sessionID] = observation
}

// FailInspect makes InspectSession fail, so a test can prove a failed observation does
// not get read as success or as failure.
func (g *FakeGateway) FailInspect(err error) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inspectErr = err
	return g
}

// FailSend scripts a send error.
func (g *FakeGateway) FailSend(err error) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sendErr = err
	return g
}

// FailSteer scripts a steer error.
func (g *FakeGateway) FailSteer(err error) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.steerErr = err
	return g
}

// FailResume scripts a resume error.
func (g *FakeGateway) FailResume(err error) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resumeErr = err
	return g
}

// SteerReceipt scripts the receipt Steer returns.
func (g *FakeGateway) SteerReceipt(receipt ports.MessageReceipt) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.steerReceipt = receipt
	g.steerReceiptSet = true
	return g
}

// SetSessionObservation replaces the durable observation for one existing session, so a
// test can script a rotated controller generation, a terminated session, or a readiness
// value for the fencing tests.
func (g *FakeGateway) SetSessionObservation(observation ports.SessionObservation) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sessions[observation.SessionID] = observation
}

// SwapObservation makes InspectSession answer with this observation whatever id it is asked
// about, which is how a test scripts an owner that describes a different session.
func (g *FakeGateway) SwapObservation(observation ports.SessionObservation) {
	g.mu.Lock()
	defer g.mu.Unlock()
	overridden := observation
	g.observationOverride = &overridden
}

// InterruptResult scripts the receipt Interrupt returns.
func (g *FakeGateway) InterruptResult(receipt ports.InterruptReceipt) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.interruptReceipt = receipt
	return g
}

// SpawnCalls reports how many times Spawn was invoked. The recovery tests assert this
// stays at 1: it is the proof that an unknown outcome never re-spawns.
func (g *FakeGateway) SpawnCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.spawnCalls
}

// SendCalls reports how many times Send was invoked.
func (g *FakeGateway) SendCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.sendCalls
}

// SteerCalls reports how many times Steer was invoked. A test asserting a steer never
// degrades to a send compares this against SendCalls.
func (g *FakeGateway) SteerCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.steerCalls
}

// ResumeCalls reports how many times Resume was invoked.
func (g *FakeGateway) ResumeCalls() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.resumeCalls
}

// LastSend returns the most recent send request.
func (g *FakeGateway) LastSend() ports.MessageRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastSend
}

// LastSteer returns the most recent steer request, so a test can assert the delivery mode,
// target turn, generation, and origin the caller actually sent.
func (g *FakeGateway) LastSteer() ports.MessageRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastSteer
}

// LastInterrupt returns the most recent interrupt request.
func (g *FakeGateway) LastInterrupt() ports.InterruptRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastInterrupt
}

// LastResume returns the most recent resume request.
func (g *FakeGateway) LastResume() ports.ResumeRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastResume
}

// ForgetSpawnMapping makes FindSpawn blind, modelling a gateway that lost its own record of a
// spawn it already performed. It exists so a test can prove that "no evidence" freezes rather
// than re-spawns.
func (g *FakeGateway) ForgetSpawnMapping() *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.byRequestID = map[string]domain.SessionID{}
	return g
}

// SendReceipt scripts the receipt Send returns.
func (g *FakeGateway) SendReceipt(receipt ports.MessageReceipt) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sendReceipt = receipt
	g.sendReceiptSet = true
	return g
}

// SendCount is SendCalls under the name the E2E assertions read.
func (g *FakeGateway) SendCount() int { return g.SendCalls() }

// InterruptCount reports how many times Interrupt was invoked. The E2E tests assert this stays
// at 1 across a stop replay and at 0 for a refused steer.
func (g *FakeGateway) InterruptCount() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.interruptCalls
}

// LastSpawn returns the most recent spawn request, so a test can assert the key was
// stable across a retry.
func (g *FakeGateway) LastSpawn() ports.SpawnRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastSpawn
}

// SpawnSessions returns every session the fake has ever created.
func (g *FakeGateway) SpawnSessions() []ports.SessionObservation {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]ports.SessionObservation, 0, len(g.sessions))
	for _, observation := range g.sessions {
		out = append(out, observation)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SessionID < out[j].SessionID })
	return out
}

// Spawn implements ports.ExecutionGateway.
func (g *FakeGateway) Spawn(ctx context.Context, request ports.SpawnRequest) (ports.SessionObservation, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionObservation{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.spawnCalls++
	g.lastSpawn = request

	if g.spawnErr != nil {
		// A scripted pre-effect failure: nothing was created.
		return ports.SessionObservation{}, g.spawnErr
	}

	// The side effect happens before the answer is decided, which is the whole point:
	// a lost response must leave a session behind.
	clientRequestID := request.SpawnKey
	if existingID, ok := g.byRequestID[clientRequestID]; ok {
		observation := g.sessions[existingID]
		if g.loseSpawnResponse {
			return ports.SessionObservation{}, ports.ErrSpawnOutcomeUnknown
		}
		return observation, nil
	}

	g.sessionSeq++
	sessionID := domain.SessionID("fake-session-" + strconv.Itoa(g.sessionSeq))
	// The materializer runs while the fake's lock is held, so a nested callback cannot
	// re-enter Spawn. It is called before the session is recorded, so a failure leaves
	// the fake consistent with the owner: nothing was created.
	if g.materialize != nil {
		materialized, err := g.materialize(ctx, sessionID, request)
		if err != nil {
			return ports.SessionObservation{}, err
		}
		if materialized != "" {
			sessionID = materialized
		}
	}
	observation := ports.SessionObservation{
		SessionID:              sessionID,
		ProjectID:              request.ProjectID,
		Kind:                   domain.KindWorker,
		Harness:                request.Harness,
		Mode:                   request.Mode,
		Readiness:              g.spawnReadiness,
		ControllerGeneration:   g.spawnGeneration,
		ClientRequestID:        clientRequestID,
		ClientRequestCommitted: true,
		Worktrees:              append([]ports.RepoWorktreeObservation(nil), g.spawnWorktrees...),
		ObservedAt:             g.nowLocked(),
	}
	g.sessions[sessionID] = observation
	g.byRequestID[clientRequestID] = sessionID

	if g.loseSpawnResponse {
		// The session exists; the caller will never learn its id from this call.
		return ports.SessionObservation{}, ports.ErrSpawnOutcomeUnknown
	}
	return observation, nil
}

// InspectSession implements ports.ExecutionGateway.
func (g *FakeGateway) InspectSession(
	ctx context.Context,
	sessionID domain.SessionID,
) (ports.SessionObservation, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionObservation{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.inspectCalls++
	if g.inspectErr != nil {
		return ports.SessionObservation{}, g.inspectErr
	}
	if g.observationOverride != nil {
		return *g.observationOverride, nil
	}
	observation, ok := g.sessions[sessionID]
	if !ok {
		return ports.SessionObservation{}, ports.ErrExecutionSessionNotFound
	}
	return observation, nil
}

// FindSpawn implements ports.ExecutionGateway.
func (g *FakeGateway) FindSpawn(
	ctx context.Context,
	clientRequestID string,
) (ports.SessionObservation, bool, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionObservation{}, false, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.findCalls++
	if g.findErr != nil {
		return ports.SessionObservation{}, false, g.findErr
	}
	sessionID, ok := g.byRequestID[clientRequestID]
	if !ok {
		return ports.SessionObservation{}, false, nil
	}
	return g.sessions[sessionID], true, nil
}

// Send implements ports.ExecutionGateway.
func (g *FakeGateway) Send(
	ctx context.Context,
	request ports.MessageRequest,
) (ports.MessageReceipt, error) {
	if err := ctx.Err(); err != nil {
		return ports.MessageReceipt{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.sendCalls++
	g.lastSend = request
	if g.sendErr != nil {
		return ports.MessageReceipt{}, g.sendErr
	}
	if _, ok := g.sessions[request.SessionID]; !ok {
		return ports.MessageReceipt{}, ports.ErrExecutionSessionNotFound
	}
	if g.sendReceiptSet {
		receipt := g.sendReceipt
		receipt.SessionID = request.SessionID
		receipt.ClientMessageID = request.ClientMessageID
		if receipt.ControllerGeneration == "" {
			// Every real receipt names the generation that produced it, because the
			// intervention layer fences a send receipt against the request's generation.
			receipt.ControllerGeneration = request.ControllerGeneration
		}
		return receipt, nil
	}
	return ports.MessageReceipt{
		SessionID:       request.SessionID,
		ClientMessageID: request.ClientMessageID,
		Queued:          true,
		// The fake stands in for a gateway that observed this controller generation.
		ControllerGeneration: request.ControllerGeneration,
	}, nil
}

// Steer implements ports.ExecutionGateway.
//
// The default answer proves the guidance joined exactly the turn and generation the caller
// named, because that is what a successful steer means. A test that wants a mismatching
// receipt scripts SteerReceipt explicitly.
func (g *FakeGateway) Steer(
	ctx context.Context,
	request ports.MessageRequest,
) (ports.MessageReceipt, error) {
	if err := ctx.Err(); err != nil {
		return ports.MessageReceipt{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.steerCalls++
	g.lastSteer = request
	if g.steerErr != nil {
		return ports.MessageReceipt{}, g.steerErr
	}
	if _, ok := g.sessions[request.SessionID]; !ok {
		return ports.MessageReceipt{}, ports.ErrExecutionSessionNotFound
	}
	if g.steerReceiptSet {
		receipt := g.steerReceipt
		receipt.SessionID = request.SessionID
		receipt.ClientMessageID = request.ClientMessageID
		return receipt, nil
	}
	return ports.MessageReceipt{
		SessionID:            request.SessionID,
		ClientMessageID:      request.ClientMessageID,
		Steered:              true,
		TargetProviderTurnID: request.TargetProviderTurnID,
		ControllerGeneration: request.ControllerGeneration,
		ProviderTurnID:       request.TargetProviderTurnID,
		// A steer receipt must name the AO turn as well as the provider turn: only both
		// ids together prove which durable row absorbed the guidance.
		TurnID: "turn-" + request.ClientMessageID,
	}, nil
}

// Interrupt implements ports.ExecutionGateway.
func (g *FakeGateway) Interrupt(
	ctx context.Context,
	request ports.InterruptRequest,
) (ports.InterruptReceipt, error) {
	if err := ctx.Err(); err != nil {
		return ports.InterruptReceipt{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.interruptCalls++
	g.lastInterrupt = request
	if g.interruptErr != nil {
		return ports.InterruptReceipt{}, g.interruptErr
	}
	receipt := g.interruptReceipt
	receipt.SessionID = request.SessionID
	if receipt.ControllerGeneration == "" {
		// A stop receipt always names the generation it is about, including the
		// nothing-to-stop and unknown kinds.
		receipt.ControllerGeneration = request.ControllerGeneration
	}
	return receipt, nil
}

// Resume implements ports.ExecutionGateway.
func (g *FakeGateway) Resume(
	ctx context.Context,
	request ports.ResumeRequest,
) (ports.SessionObservation, error) {
	if err := ctx.Err(); err != nil {
		return ports.SessionObservation{}, err
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.resumeCalls++
	g.lastResume = request
	if g.resumeErr != nil {
		return ports.SessionObservation{}, g.resumeErr
	}
	observation, ok := g.sessions[request.SessionID]
	if !ok {
		return ports.SessionObservation{}, ports.ErrExecutionSessionNotFound
	}
	return observation, nil
}

// WithClock replaces the fake's timestamp source.
func (g *FakeGateway) WithClock(now func() time.Time) *FakeGateway {
	g.mu.Lock()
	defer g.mu.Unlock()
	if now != nil {
		g.now = now
	}
	return g
}

// nowLocked reports the fake's current time. Callers must hold g.mu.
func (g *FakeGateway) nowLocked() time.Time {
	if g.now == nil {
		return time.Now().UTC()
	}
	return g.now().UTC()
}
