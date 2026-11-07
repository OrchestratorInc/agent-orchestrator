// Package intervention implements M2's three mid-execution paths: Steer, Queue, and
// Stop & Replan.
//
// It is a coordinator, not a message loop. Everything it does is a guarded composition of
// owners that already exist:
//
//   - the capability Resolver says whether this exact session generation has proven the
//     Steer capability;
//   - the binding store says which session and generation this attempt owns;
//   - the intervention store records the durable receipt;
//   - the ExecutionGateway (chat service behind it) performs the send, steer, or
//     interrupt.
//
// # Receipt states and the repo facts they map onto
//
// The design document proposed REQUESTED/QUEUED/DELIVERED/APPLIED. This repository has no
// such facts, so every receipt state below is pinned to a real one and the mapping is
// enforced rather than documented:
//
//	requested  the intervention row exists; nothing has been sent.
//	           → no chat fact.
//	queued     a chat turn row in state queued (migration 0107), or the chat service
//	           answered Queued=true.
//	           → ChatCapabilityQueue / turn state queued. NOT sent.
//	delivered  a chat turn row in state running, or the chat service reported a
//	           ProviderTurnID for the turn.
//	           → turn state running, or a provider turn id.
//	applied    the chat service reported the guidance landed on a provider turn
//	           (Steer.ProviderTurnID non-empty) AND named the AO turn id.
//	           → Steer.ProviderTurnID + Steer.ChatTurnID.
//	rejected   the chat service refused before contacting the provider.
//	           → a typed chat error (ErrSteerUnsupported, ErrChatNoSteerableTurn,
//	             ErrChatTurnNotSteerable, ErrChatSteerContentUnsupported).
//	cancelled  a queued turn was withdrawn before dispatch.
//	           → the chat queue's own cancel.
//	unknown    the request may have reached the provider and no receipt came back.
//	           → ErrSteerDeliveryUncertain / an unclassified transport failure.
//
// stop_requested / stop_confirmed / stop_unknown are the stop mode and are not message
// states at all: an interrupt either has a receipt or it does not.
//
// domain.InterventionChatFact.Licenses is the enforcement point: a receipt state that
// requires a chat fact cannot be written without one, and the store refuses it with
// domain.ErrInterventionEvidenceMissing. TestNoFabricatedApplied drives that path.
package intervention

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/capability"
)

// Store is the persistence surface the coordinator needs.
type Store interface {
	CreateWorkflowIntervention(
		ctx context.Context,
		intervention domain.WorkflowIntervention,
		now time.Time,
	) (domain.WorkflowIntervention, bool, error)
	// CreateWorkflowInterventionIfNoOpenStop records a stop only when no other stop for the
	// same task is still open, in one transaction. It is the atomic form of the
	// count-then-insert sequence, so two concurrent stops cannot both proceed.
	CreateWorkflowInterventionIfNoOpenStop(
		ctx context.Context,
		intervention domain.WorkflowIntervention,
		now time.Time,
	) (stored domain.WorkflowIntervention, created bool, blocked bool, err error)
	GetWorkflowIntervention(ctx context.Context, id string) (domain.WorkflowIntervention, bool, error)
	GetWorkflowInterventionByClientMessage(
		ctx context.Context,
		workflowID domain.WorkflowID,
		clientMessageID string,
	) (domain.WorkflowIntervention, bool, error)
	ApplyWorkflowInterventionReceipt(
		ctx context.Context,
		id string,
		next domain.WorkflowInterventionState,
		fact domain.InterventionChatFact,
		reasonCode string,
		queueAvailable bool,
		expectedGeneration string,
		expectedRevision int64,
		now time.Time,
	) (domain.WorkflowIntervention, bool, error)
	ListWorkflowInterventionsForAttempt(
		ctx context.Context,
		workflowID domain.WorkflowID,
		taskID domain.WorkflowTaskID,
	) ([]domain.WorkflowIntervention, error)
	CountOpenStopInterventions(
		ctx context.Context,
		workflowID domain.WorkflowID,
		taskID domain.WorkflowTaskID,
	) (int64, error)
}

// BindingReader reads the attempt→session binding that authorizes an intervention.
type BindingReader interface {
	GetWorkflowAttemptBinding(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttemptBinding, bool, error)
	ListWorkflowAttemptRepoBindings(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) ([]domain.WorkflowAttemptRepoBinding, error)
}

// BindingWriter advances the binding when an intervention changes what may happen next.
//
// It is separate from BindingReader so this package can record a settled stop without
// owning the binding state machine. The execution coordinator implements it.
type BindingWriter interface {
	MarkStopOutcome(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
		confirmed bool,
		unknown bool,
	) (domain.WorkflowAttemptBinding, error)
}

// Gateway is the subset of ports.ExecutionGateway the intervention paths need. It is
// narrowed deliberately: this package can send, steer, and interrupt, but it cannot spawn
// or resume, so no code path here can create a session.
type Gateway interface {
	Send(ctx context.Context, request ports.MessageRequest) (ports.MessageReceipt, error)
	// Steer delivers guidance INTO a running turn. It is a distinct method because a send
	// opens a new turn, so expressing a steer as a send would silently change its meaning.
	Steer(ctx context.Context, request ports.MessageRequest) (ports.MessageReceipt, error)
	Interrupt(ctx context.Context, request ports.InterruptRequest) (ports.InterruptReceipt, error)
	// InspectSession is how an intervention learns whether the target is currently live.
	// Consuming the gateway observation directly (rather than a cached readiness) is the
	// point: M2-05 must reach the same conclusion M2-04's send/interrupt gate reaches.
	InspectSession(ctx context.Context, sessionID domain.SessionID) (ports.SessionObservation, error)
}

// Deps are the coordinator's injected collaborators.
type Deps struct {
	Store    Store
	Bindings BindingReader
	// BindingWriter advances the binding after a settled stop. When nil, a confirmed stop
	// still returns its receipt but the binding is not advanced, and the outcome says so.
	BindingWriter BindingWriter
	Gateway       Gateway
	Resolver      *capability.Resolver
	// Clock defaults to time.Now. Tests must inject it.
	Clock func() time.Time
	// NewID defaults to a deterministic id derived from the client message id.
	NewID func(workflowID domain.WorkflowID, clientMessageID string) string
}

// Coordinator applies the three intervention paths.
type Coordinator struct {
	store    Store
	bindings BindingReader
	writer   BindingWriter
	gateway  Gateway
	resolver *capability.Resolver
	now      func() time.Time
	newID    func(domain.WorkflowID, string) string
}

// New builds a coordinator.
func New(deps Deps) *Coordinator {
	c := &Coordinator{
		store: deps.Store, bindings: deps.Bindings, writer: deps.BindingWriter,
		gateway: deps.Gateway, resolver: deps.Resolver, now: deps.Clock, newID: deps.NewID,
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.resolver == nil {
		c.resolver = capability.NewResolver(capability.WithClock(c.now))
	}
	if c.newID == nil {
		c.newID = func(workflowID domain.WorkflowID, clientMessageID string) string {
			digest := sha256.Sum256([]byte(string(workflowID) + "\x00" + clientMessageID))
			return "intervention-" + hex.EncodeToString(digest[:12])
		}
	}
	return c
}

// OperationSHA fingerprints one intervention request. It never includes message text:
// only the identity and the routing parameters participate, so an edited message body is
// still the same request while a different turn target or mode is not.
func OperationSHA(
	mode domain.WorkflowInterventionMode,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
	attemptID domain.WorkflowAttemptID,
	sessionID domain.SessionID,
	generation string,
	clientMessageID string,
	targetTurnID string,
) string {
	var builder strings.Builder
	for _, part := range []string{
		string(mode), string(workflowID), string(taskID), string(attemptID),
		string(sessionID), generation, clientMessageID, targetTurnID,
	} {
		builder.WriteString(part)
		builder.WriteString("\x00")
	}
	digest := sha256.Sum256([]byte(builder.String()))
	return hex.EncodeToString(digest[:])
}

// Outcome is what one intervention call decided.
type Outcome struct {
	Intervention domain.WorkflowIntervention
	// Performed reports whether the external call was made by this invocation. A replay
	// never sets it.
	Performed bool
	// Frozen means the intervention is in an uncertain state and nothing further may be
	// attempted without reconciliation.
	Frozen bool
	// QueueAvailable accompanies a refused steer: the Queue path is an honest alternative
	// where steering was not proven.
	QueueAvailable bool
	// ReasonCode is a stable code for a client.
	ReasonCode string
}

// SteerRequest asks the provider to absorb guidance into a running turn.
type SteerRequest struct {
	WorkflowID      domain.WorkflowID
	TaskID          domain.WorkflowTaskID
	AttemptID       domain.WorkflowAttemptID
	ClientMessageID string
	Text            string
	// TargetProviderTurnID is the turn the caller believes is running. It is checked
	// against the provider's answer rather than assumed.
	TargetProviderTurnID string
	// ControllerGeneration is the generation the caller observed. An empty value means
	// "use the binding's"; a non-empty value that disagrees with the binding is refused
	// rather than silently replaced, because the caller was looking at a different
	// controller.
	ControllerGeneration string
	// Harness is the harness the owning task declared. The capability Resolver needs it
	// to look up evidence, and an empty value is refused rather than guessed: a wrong
	// harness would authorize the wrong capability set.
	Harness domain.AgentHarness
}

// Steer sends guidance into a turn that is already running.
//
// The preconditions are checked in this order, and each refusal is a typed outcome rather
// than an error, because a client can act on every one of them:
//
//  1. the attempt binding exists, holds a session, and is not stopped;
//  2. the caller's controller generation matches the binding's (generation fencing);
//  3. the capability Resolver proves Steer for this exact session generation — a static
//     profile or a harness-scoped observation is not enough;
//  4. the target turn id is present;
//  5. the request is not a replay.
//
// A failure at step 3 returns QueueAvailable=true and never quietly issues an
// interrupt-and-resend, which has different effects.
func (c *Coordinator) Steer(ctx context.Context, request SteerRequest) (Outcome, error) {
	binding, sessionID, _, refuse, err := c.resolveTarget(ctx, request.AttemptID, false)
	if err != nil || refuse != nil {
		return derefOutcome(refuse), err
	}
	generation, refuseOutcome, err := fenceGeneration(request.ControllerGeneration, binding)
	if refuseOutcome != nil || err != nil {
		return derefOutcome(refuseOutcome), err
	}
	if strings.TrimSpace(request.TargetProviderTurnID) == "" {
		return Outcome{
			ReasonCode:     string(domain.ErrInterventionTurnRequired.Error()),
			QueueAvailable: true,
		}, fmt.Errorf("%w: a steer must name the turn it joins", domain.ErrInterventionTurnRequired)
	}

	// 3. Capability evidence, scoped to this session generation.
	if !request.Harness.IsKnown() && request.Harness != domain.HarnessFake {
		// Without a known harness there is no evidence to consult, and guessing would
		// authorize the wrong capability set.
		return Outcome{
			ReasonCode:     ports.CapabilityReasonNotProbed,
			QueueAvailable: true,
		}, nil
	}
	decision := c.resolver.Gate(ctx, ports.CapabilityProbeRequest{
		Harness:    request.Harness,
		SessionID:  *sessionID,
		Generation: generation,
	}, domain.CapabilityFeatureSteer)
	if !decision.Allowed {
		// Not proven. Offer the queue path explicitly instead of pretending.
		return Outcome{
			ReasonCode:     decision.ReasonCode,
			QueueAvailable: true,
			Frozen:         false,
		}, nil
	}

	return c.performMessage(ctx, messageParams{
		mode:            domain.WorkflowInterventionModeSteer,
		workflowID:      request.WorkflowID,
		taskID:          request.TaskID,
		attemptID:       request.AttemptID,
		sessionID:       *sessionID,
		generation:      generation,
		clientMessageID: request.ClientMessageID,
		text:            request.Text,
		targetTurnID:    request.TargetProviderTurnID,
	})
}

// fenceGeneration resolves the generation a request must act under.
//
// The caller's value wins only when the binding has none of its own; a disagreement is a
// stale-generation refusal, never a silent replacement.
func fenceGeneration(
	requested string,
	binding domain.WorkflowAttemptBinding,
) (string, *Outcome, error) {
	generation := binding.ControllerGeneration
	if strings.TrimSpace(requested) == "" {
		return generation, nil, nil
	}
	if generation != "" && requested != generation {
		return "", &Outcome{
			Frozen:     true,
			ReasonCode: "generation_mismatch",
		}, fmt.Errorf("%w: %w", ports.ErrExecutionStaleGeneration, domain.ErrInterventionStaleGeneration)
	}
	return requested, nil, nil
}

// QueueRequest asks for a durable queued message.
type QueueRequest struct {
	WorkflowID      domain.WorkflowID
	TaskID          domain.WorkflowTaskID
	AttemptID       domain.WorkflowAttemptID
	ClientMessageID string
	Text            string
	// AuthoredByOperator 仅由已验证的 HTTP Principal 设置，不能从请求正文接收。
	AuthoredByOperator bool
	// ControllerGeneration is the generation the caller observed; empty means the
	// binding's. A disagreement is refused, not replaced.
	ControllerGeneration string
}

// Queue durably queues a message for a later turn.
//
// It does NOT require the steer capability, because queueing is AO's own behaviour: the
// message gets a durable chat row whether or not the provider can absorb guidance
// mid-turn. The receipt says queued, and queued is never rendered as sent.
func (c *Coordinator) Queue(ctx context.Context, request QueueRequest) (Outcome, error) {
	binding, sessionID, _, refuse, err := c.resolveTarget(ctx, request.AttemptID, false)
	if err != nil || refuse != nil {
		return derefOutcome(refuse), err
	}
	generation, refuseOutcome, err := fenceGeneration(request.ControllerGeneration, binding)
	if refuseOutcome != nil || err != nil {
		return derefOutcome(refuseOutcome), err
	}
	return c.performMessage(ctx, messageParams{
		mode:            domain.WorkflowInterventionModeQueue,
		human:           request.AuthoredByOperator,
		workflowID:      request.WorkflowID,
		taskID:          request.TaskID,
		attemptID:       request.AttemptID,
		sessionID:       *sessionID,
		generation:      generation,
		clientMessageID: request.ClientMessageID,
		text:            request.Text,
	})
}

// StopRequest asks the located session to stop.
type StopRequest struct {
	WorkflowID      domain.WorkflowID
	TaskID          domain.WorkflowTaskID
	AttemptID       domain.WorkflowAttemptID
	ClientMessageID string
	// ControllerGeneration is the generation the caller observed; empty means the
	// binding's. A disagreement is refused, not replaced.
	ControllerGeneration string
}

// Stop & Replan: interrupt the session, record the receipt, and emit a replan request.
//
// A retried stop with the same client message id must not issue a second interrupt, so the
// store's idempotency is consulted before the gateway is called. An unsettled stop also
// blocks a new one, because issuing a second interrupt while the first is unknown is
// exactly the blind retry the design forbids.
//
// The Interrupt capability gate is asked FIRST and independently of the message paths: a
// harness whose evidence never proved that it can be interrupted gets no interrupt at all,
// not a best-effort one.
//
// On confirmation it returns a ReplanRequest and a nil error. M2 never executes that
// request: planning a new DAG is M3's job, and this package has no ability to spawn.
func (c *Coordinator) Stop(ctx context.Context, request StopRequest) (Outcome, *domain.ReplanRequest, error) {
	binding, sessionID, observation, refuse, err := c.resolveTarget(ctx, request.AttemptID, true)
	if err != nil || refuse != nil {
		return derefOutcome(refuse), nil, err
	}
	generation, refuseOutcome, err := fenceGeneration(request.ControllerGeneration, binding)
	if refuseOutcome != nil || err != nil {
		return derefOutcome(refuseOutcome), nil, err
	}
	now := c.now().UTC()

	// The idempotency lookup happens BEFORE anything external. This is what makes a
	// retried stop free.
	sha := OperationSHA(
		domain.WorkflowInterventionModeStop, request.WorkflowID, request.TaskID,
		request.AttemptID, *sessionID, generation, request.ClientMessageID, "",
	)
	existing, found, err := c.store.GetWorkflowInterventionByClientMessage(
		ctx, request.WorkflowID, request.ClientMessageID,
	)
	if err != nil {
		return Outcome{}, nil, err
	}
	if found {
		if existing.OperationSHA != sha {
			return Outcome{}, nil, domain.ErrInterventionOperationConflict
		}
		// Replay: return the stored receipt. No interrupt is issued. The binding was
		// advanced when the receipt was first applied, so nothing is re-advanced here.
		return Outcome{
			Intervention: existing,
			Frozen:       existing.State == domain.WorkflowInterventionStateStopUnknown,
			ReasonCode:   existing.ReasonCode,
		}, replanForRequest(existing), nil
	}

	// A stop that is not a replay of this exact request has to clear the stop gate. The
	// binding may have settled since the caller decided to stop — an earlier stop is in
	// flight, or it is already settled — and a second interrupt against a session that is
	// already stopping is exactly the blind retry this path exists to prevent.
	if stoppedState(binding.State) {
		reasonCode := "stop_already_pending"
		if binding.State.Terminal() {
			reasonCode = "binding_stopped"
		}
		return Outcome{
			Frozen:     true,
			ReasonCode: reasonCode,
		}, nil, nil
	}

	// The Interrupt gate. Without proven evidence, no interrupt is issued and the
	// capability's own reason code explains why. The harness comes from the observation the
	// binding was just fenced against, so the gate is asked about the same session holder
	// the rest of the path acts on.
	harnessDecision := c.interruptGate(ctx, observation.Harness, *sessionID, generation)
	if !harnessDecision.Allowed {
		return Outcome{
			Frozen:     true,
			ReasonCode: harnessDecision.ReasonCode,
		}, nil, nil
	}

	intervention := domain.WorkflowIntervention{
		ID:                   c.newID(request.WorkflowID, request.ClientMessageID),
		WorkflowID:           request.WorkflowID,
		TaskID:               request.TaskID,
		AttemptID:            &request.AttemptID,
		Mode:                 domain.WorkflowInterventionModeStop,
		SessionID:            sessionID,
		ControllerGeneration: generation,
		ClientMessageID:      request.ClientMessageID,
		OperationSHA:         sha,
	}
	// One transaction: the open-stop check and the insert cannot interleave, so two
	// concurrent stops with different client message ids cannot both insert.
	stored, created, blocked, err := c.store.CreateWorkflowInterventionIfNoOpenStop(
		ctx, intervention, now,
	)
	if err != nil {
		return Outcome{}, nil, err
	}
	if blocked {
		// Another stop for this task is still unsettled. Issuing another interrupt would
		// be the blind retry the design forbids.
		return Outcome{
			Intervention: stored,
			Frozen:       true,
			ReasonCode:   "stop_already_pending",
		}, nil, nil
	}
	if !created {
		// A same-key replay: return the stored receipt, no second interrupt.
		if stored.OperationSHA != sha {
			return Outcome{}, nil, domain.ErrInterventionOperationConflict
		}
		return Outcome{
			Intervention: stored,
			Frozen:       stored.State == domain.WorkflowInterventionStateStopUnknown,
			ReasonCode:   stored.ReasonCode,
		}, replanForRequest(stored), nil
	}
	intervention = stored

	// The insert proves no stop was OPEN; it does not prove the last one had settled,
	// because a winner settles its row before it advances the binding. Without this second
	// fence, a loser that arrives in that window would insert its own row and issue a second
	// interrupt for the same session. The interrupt is the side effect that matters, so the
	// check sits immediately before it.
	if settled, settledErr := c.stoppedSinceOpen(ctx, request); settledErr == nil && settled != "" {
		// This caller's row is unnecessary: another stop already reached a decision. Withdraw
		// it so the durable record shows one stop, not two.
		// `rejected` is the terminal state stop mode allows from requested, and it is the
		// honest one: this caller's stop did nothing because another had already decided.
		if _, _, withdrawErr := c.store.ApplyWorkflowInterventionReceipt(
			ctx, intervention.ID, domain.WorkflowInterventionStateRejected,
			domain.InterventionChatFact{}, "stop_already_settled", false,
			generation, intervention.Revision, now,
		); withdrawErr != nil {
			return Outcome{}, nil, withdrawErr
		}
		return Outcome{Frozen: true, ReasonCode: settled}, nil, nil
	}

	// Mark the request in flight before asking, so a crash mid-call leaves a row that says
	// "a stop was requested and its receipt is unknown" rather than a silent gap.
	inFlight, _, err := c.store.ApplyWorkflowInterventionReceipt(
		ctx, intervention.ID, domain.WorkflowInterventionStateStopRequested,
		domain.InterventionChatFact{}, "", false, generation, intervention.Revision, now,
	)
	if err != nil {
		return Outcome{}, nil, err
	}

	interruptRequest := ports.InterruptRequest{
		SessionID:            *sessionID,
		ControllerGeneration: generation,
		ClientMessageID:      request.ClientMessageID,
	}
	receipt, err := c.gateway.Interrupt(ctx, interruptRequest)

	next, reasonCode, frozen := stopOutcome(interruptRequest, receipt, err)
	updated, applied, applyErr := c.store.ApplyWorkflowInterventionReceipt(
		ctx, intervention.ID, next, domain.InterventionChatFact{}, reasonCode, false,
		generation, inFlight.Revision, now,
	)
	if applyErr != nil {
		return Outcome{}, nil, applyErr
	}
	outcome := Outcome{
		Intervention: updated,
		Performed:    true,
		Frozen:       frozen,
		ReasonCode:   reasonCode,
	}
	if !applied {
		outcome.ReasonCode = "stop_receipt_not_applied"
	}

	// Advance the binding so the stop is a fact everywhere, not just in the intervention
	// receipt. A failure here never turns a confirmed stop into an error: the interrupt
	// already happened, and reporting failure would invite a second one.
	if applied {
		outcome.ReasonCode = c.advanceBindingForStop(ctx, request.AttemptID, next, outcome.ReasonCode)
	}

	var replan *domain.ReplanRequest
	if next == domain.WorkflowInterventionStateStopConfirmed {
		replan = replanForRequest(updated)
		// Preserve the worktree references: a stop must never be read as permission to
		// discard work.
		if refs, err := c.bindings.ListWorkflowAttemptRepoBindings(ctx, request.AttemptID); err == nil {
			for _, ref := range refs {
				if strings.TrimSpace(ref.SessionWorktreeRef) != "" {
					replan.PreservedWorktrees = append(replan.PreservedWorktrees, ref.SessionWorktreeRef)
				}
			}
		}
	}
	return outcome, replan, nil
}

// stopOutcome maps an interrupt receipt (and its error) onto a stop state.
//
// Nothing ambiguous becomes confirmed. A missing answer is stop_unknown, and a transport
// error is unknown too: the interrupt may have reached the controller.
//
// The receipt is also fenced against the request. A receipt that names a different
// generation, a different session, or no generation at all cannot confirm this stop: it
// proves something about a controller the caller is not stopping, and treating it as
// confirmation would advance the binding to a terminal state on evidence about
// unrelated work. That case is stop_unknown with its own reason code rather than a
// rejection, because the interrupt may genuinely have been issued.
func stopOutcome(
	request ports.InterruptRequest,
	receipt ports.InterruptReceipt,
	err error,
) (domain.WorkflowInterventionState, string, bool) {
	switch {
	case errors.Is(err, ports.ErrExecutionUnsupported):
		return domain.WorkflowInterventionStateRejected, "stop_unsupported", false
	case errors.Is(err, ports.ErrExecutionStaleGeneration):
		// The gateway refused before interrupting: the controller is not the one the
		// caller observed, so no stop of that work was attempted. It cannot be recorded
		// as rejected — this row is already stop_requested, and the stop state machine
		// has no edge back to a rejection — so it is frozen as unknown, which is the
		// conservative claim: the caller must re-observe the controller, not retry.
		return domain.WorkflowInterventionStateStopUnknown, "stop_stale_generation", true
	case err != nil:
		return domain.WorkflowInterventionStateStopUnknown, "stop_transport_error", true
	case !receipt.MatchesInterrupt(request):
		return domain.WorkflowInterventionStateStopUnknown, "stop_generation_mismatch", true
	case receipt.Confirmed:
		return domain.WorkflowInterventionStateStopConfirmed, "stop_confirmed", false
	case receipt.NothingToStop:
		// Nothing was in flight: the session is not running a turn, so the stop is
		// trivially satisfied and the task may be replanned.
		return domain.WorkflowInterventionStateStopConfirmed, "stop_nothing_to_stop", false
	default:
		return domain.WorkflowInterventionStateStopUnknown, "stop_receipt_uncertain", true
	}
}

// messageParams is the shared input for the steer and queue paths.
type messageParams struct {
	human           bool
	mode            domain.WorkflowInterventionMode
	workflowID      domain.WorkflowID
	taskID          domain.WorkflowTaskID
	attemptID       domain.WorkflowAttemptID
	sessionID       domain.SessionID
	generation      string
	clientMessageID string
	text            string
	targetTurnID    string
}

// performMessage implements the shared steer/queue sequence: replay lookup, durable
// request, gateway send, receipt from chat facts.
func (c *Coordinator) performMessage(ctx context.Context, params messageParams) (Outcome, error) {
	now := c.now().UTC()
	if strings.TrimSpace(params.clientMessageID) == "" {
		return Outcome{}, fmt.Errorf("%w: clientMessageId is required", domain.ErrInvalidIntervention)
	}

	sha := OperationSHA(
		params.mode, params.workflowID, params.taskID, params.attemptID, params.sessionID,
		params.generation, params.clientMessageID, params.targetTurnID,
	)

	// Replay lookup before any external call, so a retry is free.
	existing, found, err := c.store.GetWorkflowInterventionByClientMessage(
		ctx, params.workflowID, params.clientMessageID,
	)
	if err != nil {
		return Outcome{}, err
	}
	if found {
		if existing.OperationSHA != sha {
			return Outcome{}, domain.ErrInterventionOperationConflict
		}
		return Outcome{
			Intervention:   existing,
			Frozen:         existing.State == domain.WorkflowInterventionStateUnknown,
			ReasonCode:     existing.ReasonCode,
			QueueAvailable: existing.QueueAvailable,
		}, nil
	}

	attemptID := params.attemptID
	intervention := domain.WorkflowIntervention{
		ID:                   c.newID(params.workflowID, params.clientMessageID),
		WorkflowID:           params.workflowID,
		TaskID:               params.taskID,
		AttemptID:            &attemptID,
		Mode:                 params.mode,
		SessionID:            &params.sessionID,
		ControllerGeneration: params.generation,
		ClientMessageID:      params.clientMessageID,
		OperationSHA:         sha,
		TargetProviderTurnID: params.targetTurnID,
	}
	created, newRow, err := c.store.CreateWorkflowIntervention(ctx, intervention, now)
	if err != nil {
		return Outcome{}, err
	}
	if !newRow {
		return Outcome{Intervention: created, ReasonCode: "intervention_replayed"}, nil
	}

	request := ports.MessageRequest{
		SessionID:       params.sessionID,
		ClientMessageID: params.clientMessageID,
		Text:            params.text,
		// 默认保留自动化来源；只有经过 Operator 校验的排队指示可以进入现有人工编辑入口。
		Origin:               domain.MessageOriginAutomation,
		ControllerGeneration: params.generation,
	}
	if params.human {
		request.Origin = domain.MessageOriginHuman
	}
	var receipt ports.MessageReceipt
	var sendErr error
	switch params.mode {
	case domain.WorkflowInterventionModeSteer:
		// A steer must join the turn the caller named. Routing it through Send would open
		// a new turn instead, which is a different operation with a different effect.
		request.Delivery = ports.MessageDeliverySteer
		request.TargetProviderTurnID = params.targetTurnID
		receipt, sendErr = c.gateway.Steer(ctx, request)
	default:
		request.Delivery = ports.MessageDeliverySend
		receipt, sendErr = c.gateway.Send(ctx, request)
	}

	next, fact, reasonCode, queueAvailable := messageOutcome(params.mode, request, receipt, sendErr)

	// A stop can land while this call is in flight. The gateway then answers about a turn
	// that no longer exists, so a successful receipt is not proof the guidance changed
	// anything the caller can still act on. This is checked only for a would-be "applied"
	// steer: every other outcome already refuses to claim success.
	if next == domain.WorkflowInterventionStateApplied && params.mode == domain.WorkflowInterventionModeSteer {
		stopped, checkErr := c.bindingStopped(ctx, params.attemptID)
		switch {
		case checkErr != nil:
			// The binding cannot be re-read, so "applied" cannot be vouched for either.
			next, reasonCode, queueAvailable = domain.WorkflowInterventionStateUnknown,
				"binding_unreadable", false
		case stopped:
			// The stop won the race. The guidance may or may not have landed on the turn
			// before it was interrupted, and the turn is gone either way, so the honest
			// record is unknown: neither applied nor rejected.
			next, reasonCode, queueAvailable = domain.WorkflowInterventionStateUnknown,
				"binding_stopped", false
		}
	}

	updated, applied, applyErr := c.store.ApplyWorkflowInterventionReceipt(
		ctx, intervention.ID, next, fact, reasonCode, queueAvailable,
		params.generation, created.Revision, now,
	)
	if applyErr != nil {
		return Outcome{}, applyErr
	}
	outcome := Outcome{
		Intervention:   updated,
		Performed:      true,
		ReasonCode:     reasonCode,
		QueueAvailable: queueAvailable,
		Frozen:         next == domain.WorkflowInterventionStateUnknown,
	}
	if !applied {
		outcome.ReasonCode = "intervention_receipt_not_applied"
	}
	return outcome, nil
}

// messageOutcome maps a chat receipt (and its error) onto a receipt state plus the chat
// fact that licenses it.
//
// This is where "no fabricated applied" is decided for the message paths. Both paths are
// receipt-verified, because an answer is not evidence until it is an answer about the
// request that was made:
//
//   - a steer must prove it joined exactly the turn and generation the request named
//     (MessageReceipt.MatchesSteer), so a provider that steered some other turn can never
//     be reported as applied; and
//   - a send must prove it recorded the message and client message id on the controller
//     generation the request named (MessageReceipt.MatchesSend), so a receipt minted by a
//     replacement controller cannot settle an intervention aimed at the old one.
//
// A receipt that fails either check is unknown, never applied/delivered/queued: the
// message may have landed somewhere the caller cannot see, so the honest record is one
// that freezes and makes the caller reconcile. Queue is not offered for either mismatch
// for the same reason — the first delivery may already have happened.
func messageOutcome(
	mode domain.WorkflowInterventionMode,
	request ports.MessageRequest,
	receipt ports.MessageReceipt,
	err error,
) (domain.WorkflowInterventionState, domain.InterventionChatFact, string, bool) {
	if err != nil {
		// Every chat refusal is a rejection: the chat service refused before the provider
		// saw anything. An unclassified error is unknown, because it may have been the
		// provider connection that broke.
		switch {
		case errors.Is(err, ports.ErrExecutionOutcomeUnknown):
			// The message may have been delivered. Freeze rather than resend.
			return domain.WorkflowInterventionStateUnknown, domain.InterventionChatFact{},
				"delivery_uncertain", true
		case isChatRefusal(err):
			return domain.WorkflowInterventionStateRejected,
				domain.InterventionChatFact{}, refusalReason(err),
				mode != domain.WorkflowInterventionModeQueue
		default:
			return domain.WorkflowInterventionStateUnknown, domain.InterventionChatFact{},
				"delivery_uncertain", true
		}
	}

	fact := domain.InterventionChatFact{
		ChatTurnID:     receipt.TurnID,
		ProviderTurnID: receipt.ProviderTurnID,
		Queued:         receipt.Queued,
		Duplicate:      receipt.Duplicate,
	}
	if mode == domain.WorkflowInterventionModeSteer {
		if !receipt.MatchesSteer(request) {
			// The gateway answered, but not about the turn the caller named. That is not
			// applied, and it is not a clean delivery either: the guidance went somewhere
			// nobody asked for, so the outcome is unknown and the caller must reconcile.
			// Queue is deliberately NOT offered: the message may already have landed, so
			// queueing it again could deliver it twice.
			return domain.WorkflowInterventionStateUnknown, fact, "steer_target_mismatch", false
		}
		return domain.WorkflowInterventionStateApplied, fact, "applied", false
	}
	if !receipt.MatchesSend(request) {
		// The same rule for the send path. A receipt naming another session, another
		// client message id, another generation, or no turn at all is not a receipt for
		// this request; recording applied/delivered/queued from it would put a fact in
		// the ledger that the gateway never proved.
		return domain.WorkflowInterventionStateUnknown, domain.InterventionChatFact{},
			"send_receipt_mismatch", false
	}
	switch {
	case fact.ProviderTurnID != "" && fact.ChatTurnID != "":
		// The strongest available evidence: the provider named a turn and AO recorded it.
		return domain.WorkflowInterventionStateApplied, fact, "applied", false
	case fact.Queued:
		// A durable chat row and no provider action yet. Queued is never sent.
		return domain.WorkflowInterventionStateQueued, fact, "queued", false
	case fact.ChatTurnID != "" || fact.ProviderTurnID != "":
		return domain.WorkflowInterventionStateDelivered, fact, "delivered", false
	case fact.Duplicate:
		// The chat service already held this client message id, so nothing new was sent.
		// That is not a delivery this call can claim and not a rejection either: the
		// message exists somewhere the caller has to reconcile against. Queue is not
		// offered because it already landed once.
		return domain.WorkflowInterventionStateUnknown, fact, "duplicate_client_message_id", false
	default:
		// MatchesSend already refused this shape, so reaching here means the request and
		// the receipt disagree in a way the check above did not classify.
		return domain.WorkflowInterventionStateUnknown, fact, "no_turn_reported", true
	}
}

// isChatRefusal reports whether the chat service proved it did not contact the provider.
//
// The execution sentinels are included because the gateway narrows chat's own errors into
// them: a stale generation, a terminated session, and a turn mismatch are all refusals the
// chat service decided before any provider action, so none of them may be recorded as
// "unknown" (which would invite a retry).
func isChatRefusal(err error) bool {
	switch {
	case errors.Is(err, ports.ErrExecutionRefused),
		errors.Is(err, ports.ErrExecutionUnsupported),
		errors.Is(err, ports.ErrExecutionStaleGeneration),
		errors.Is(err, ports.ErrExecutionTerminated),
		errors.Is(err, ports.ErrExecutionTargetMismatch):
		return true
	}
	return false
}

// refusalReason maps a chat refusal onto the stable code a client branches on. The steer
// specific sentinels live in internal/service/chat, which this package may not import
// (chat is read-only for M2-05 and owns its own errors), so matching is by code string
// derived from the execution sentinels the gateway returns.
func refusalReason(err error) string {
	switch {
	case errors.Is(err, ports.ErrExecutionUnsupported):
		return "STEER_UNSUPPORTED"
	case errors.Is(err, ports.ErrExecutionStaleGeneration):
		return "STEER_STALE_GENERATION"
	case errors.Is(err, ports.ErrExecutionTerminated):
		return "SESSION_TERMINATED"
	case errors.Is(err, ports.ErrExecutionTargetMismatch):
		return "TURN_MISMATCH"
	default:
		return "chat_refused"
	}
}

// resolveTarget loads the binding and validates that an intervention is even possible.
// A non-nil outcome means the call should return immediately with that refusal.
//
// Two gates are new here and both are refusals rather than degradations:
//
//   - the observation must pass SessionObservation.ValidateFence for the binding's own
//     session and generation, so evidence about a replaced controller cannot authorize an
//     intervention (session_fence_failed); and
//   - a stopped binding refuses everything (binding_stopped). After a confirmed stop there
//     is no running turn to steer, queue into, or stop again.
//
// allowStopped is set only by Stop. A stop that is being retried must still find its own
// stored receipt after the binding has advanced, so the stop gate has to run after the
// replay lookup rather than before it. Every other caller passes false.
func (c *Coordinator) resolveTarget(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	allowStopped bool,
) (domain.WorkflowAttemptBinding, *domain.SessionID, ports.SessionObservation, *Outcome, error) {
	binding, found, err := c.bindings.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		return domain.WorkflowAttemptBinding{}, nil, ports.SessionObservation{}, nil, err
	}
	if !found {
		return domain.WorkflowAttemptBinding{}, nil, ports.SessionObservation{}, &Outcome{
			Frozen:     true,
			ReasonCode: string(domain.ErrWorkflowBindingNotFound.Error()),
		}, domain.ErrWorkflowBindingNotFound
	}
	if binding.SessionID == nil {
		// A binding without a session has nothing to intervene in. This includes the
		// post-deletion reconcile_required state.
		return domain.WorkflowAttemptBinding{}, nil, ports.SessionObservation{}, &Outcome{
			Frozen:     true,
			ReasonCode: "no_session",
		}, nil
	}
	if strings.TrimSpace(binding.ControllerGeneration) == "" {
		return domain.WorkflowAttemptBinding{}, nil, ports.SessionObservation{}, &Outcome{
			Frozen:     true,
			ReasonCode: "no_controller_generation",
		}, nil
	}
	if !allowStopped && stoppedState(binding.State) {
		// The stop gate. Nothing may queue or steer after a stop was requested or settled:
		// the turn those messages would address is the turn that was just stopped.
		return binding, nil, ports.SessionObservation{}, &Outcome{
			Frozen:     true,
			ReasonCode: "binding_stopped",
		}, nil
	}

	// The same live gate M2-04's send and interrupt use. A receipt minted against a dead or
	// unverified session is a false receipt, so an intervention must not proceed.
	if !binding.State.HasSession() {
		return domain.WorkflowAttemptBinding{}, nil, ports.SessionObservation{}, &Outcome{
			Frozen:     true,
			ReasonCode: "binding_not_live",
		}, nil
	}
	observation, err := c.gateway.InspectSession(ctx, *binding.SessionID)
	if err != nil {
		// An unreadable session cannot produce a trustworthy receipt.
		return domain.WorkflowAttemptBinding{}, nil, ports.SessionObservation{}, &Outcome{
			Frozen:     true,
			ReasonCode: "session_not_live",
		}, nil
	}
	if fenceErr := observation.ValidateFence(*binding.SessionID, binding.ControllerGeneration); fenceErr != nil {
		// The evidence describes a different session, a replaced controller, or a
		// terminated session. Acting on it would fence the wrong owner.
		return binding, nil, observation, &Outcome{
			Frozen:     true,
			ReasonCode: "session_fence_failed",
		}, nil
	}
	if !observation.Readiness.HoldsProvider() {
		return domain.WorkflowAttemptBinding{}, nil, observation, &Outcome{
			Frozen:     true,
			ReasonCode: "session_not_live",
		}, nil
	}
	return binding, binding.SessionID, observation, nil, nil
}

// stoppedSinceOpen reports whether this attempt's stop has already been decided by another
// caller, returning the stable reason code.
//
// It exists because the atomic insert only proves that no stop was still OPEN. A winner
// settles its own row (stop_confirmed) before it advances the binding, so a loser arriving in
// that window would see "nothing open" and interrupt a session that is already stopped.
// Scoping to the attempt is deliberate: a replanned attempt on the same task must be able to
// stop on its own, so a previous attempt's settled stop is not evidence about this one.
func (c *Coordinator) stoppedSinceOpen(ctx context.Context, request StopRequest) (string, error) {
	rows, err := c.store.ListWorkflowInterventionsForAttempt(ctx, request.WorkflowID, request.TaskID)
	if err != nil {
		return "", err
	}
	for _, row := range rows {
		if row.Mode != domain.WorkflowInterventionModeStop {
			continue
		}
		if row.AttemptID == nil || *row.AttemptID != request.AttemptID {
			continue
		}
		if row.ClientMessageID == request.ClientMessageID {
			continue
		}
		if row.State == domain.WorkflowInterventionStateStopConfirmed {
			return "stop_already_settled", nil
		}
	}
	return "", nil
}

// bindingStopped reports whether the attempt's binding has entered a stop state or a proven
// failure while an intervention was in flight.
//
// It re-reads the binding rather than trusting the one resolveTarget saw, because the point
// is to catch a stop that landed between that read and the gateway's answer.
func (c *Coordinator) bindingStopped(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (bool, error) {
	binding, found, err := c.bindings.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		return false, err
	}
	if !found {
		return false, domain.ErrWorkflowBindingNotFound
	}
	return stoppedState(binding.State), nil
}

// stoppedState reports whether a binding is in a stop state that forbids interventions.
// failed_confirmed is included: the attempt provably produced no session to act on.
func stoppedState(state domain.WorkflowBindingState) bool {
	switch state {
	case domain.WorkflowBindingStateStopRequested, domain.WorkflowBindingStateStopConfirmed,
		domain.WorkflowBindingStateStopUnknown, domain.WorkflowBindingStateFailedConfirmed:
		return true
	default:
		return false
	}
}

// interruptGate asks whether this session generation has proven the Interrupt capability.
//
// It is a separate gate from the steer gate on purpose: a harness that can absorb guidance
// mid-turn is not thereby proven able to stop, and the acceptance requires the interrupt
// refusal to be independent. This package does not know the attempt's harness (the binding
// carries no harness field), so the gate asks with the harness the caller declared.
func (c *Coordinator) interruptGate(
	ctx context.Context,
	harness domain.AgentHarness,
	sessionID domain.SessionID,
	generation string,
) capability.Decision {
	if !harness.IsKnown() && harness != domain.HarnessFake {
		// Without a known harness there is no evidence to consult, and guessing would
		// authorize an interrupt nobody proved.
		return capability.Decision{ReasonCode: ports.CapabilityReasonNotProbed}
	}
	return c.resolver.Gate(ctx, ports.CapabilityProbeRequest{
		Harness: harness, SessionID: sessionID, Generation: generation,
	}, domain.CapabilityFeatureInterrupt)
}

// advanceBindingForStop advances the binding to agree with a settled stop.
//
// The intervention receipt is already durable at this point, so a failure here is reported
// as a reason code rather than an error: turning a confirmed stop into an error would invite
// the caller to issue a second interrupt.
func (c *Coordinator) advanceBindingForStop(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	next domain.WorkflowInterventionState,
	reasonCode string,
) string {
	if next != domain.WorkflowInterventionStateStopConfirmed &&
		next != domain.WorkflowInterventionStateStopUnknown {
		return reasonCode
	}
	if c.writer == nil {
		// No writer is wired. The receipt stands; the binding was not advanced, and saying
		// so is more honest than implying it was.
		return reasonCode
	}
	unknown := next == domain.WorkflowInterventionStateStopUnknown
	confirmed := next == domain.WorkflowInterventionStateStopConfirmed
	if _, err := c.writer.MarkStopOutcome(ctx, attemptID, confirmed, unknown); err != nil {
		return "binding_advance_failed"
	}
	return reasonCode
}

// replanForRequest builds the replan request a settled stop produces. It carries no plan:
// producing a new DAG is M3's job.
func replanForRequest(intervention domain.WorkflowIntervention) *domain.ReplanRequest {
	if intervention.Mode != domain.WorkflowInterventionModeStop {
		return nil
	}
	if intervention.State != domain.WorkflowInterventionStateStopConfirmed {
		return nil
	}
	return &domain.ReplanRequest{
		InterventionID: intervention.ID,
		WorkflowID:     intervention.WorkflowID,
		TaskID:         intervention.TaskID,
		AttemptID:      intervention.AttemptID,
		SessionID:      intervention.SessionID,
		ReasonCode:     intervention.ReasonCode,
		RequestedAt:    intervention.UpdatedAt,
	}
}

func derefOutcome(outcome *Outcome) Outcome {
	if outcome == nil {
		return Outcome{}
	}
	return *outcome
}
