package domain

import (
	"errors"
	"strings"
	"time"
)

// Workflow interventions: Steer, Queue, and Stop & Replan.
//
// The receipt vocabulary here is deliberately NOT the design document's
// REQUESTED/QUEUED/DELIVERED/APPLIED proposal, because that vocabulary has no
// counterpart in this repository. AO's real facts are:
//
//   - chat turn state: queued/running/completed/recovered/interrupted/failed/cancelled
//     (migrations 0107 and 0118);
//   - SteerOrSend's result: Steered | Sent with Duplicate and Replayed flags.
//
// So an intervention receipt records what AO actually recorded, and every receipt state
// maps onto exactly one of those facts. The mapping is:
//
//	requested  → the row exists; nothing has been sent.      (no chat fact yet)
//	queued     → a chat turn row in state queued.             (queue path only)
//	delivered  → a chat turn row in state running, OR a steer the chat service
//	             acknowledged for a specific provider turn.
//	applied    → the chat service reported the guidance landed on the turn the caller
//	             named (Steer.ProviderTurnID == requested providerTurnId).
//	rejected   → the chat service refused; no provider call was made.
//	cancelled  → a queued turn was withdrawn before dispatch.
//	unknown    → the request may or may not have reached the provider.
//
// Two rules follow from that table and are enforced by construction below:
//
//  1. delivered and applied are unreachable without a chat fact. There is no method
//     that takes "trust me" and produces them; ApplyReceipt requires the chat-side
//     evidence as an argument.
//  2. queued never means sent. A queued row has a durable message and no provider
//     action, and the mapping table above says so in the same sentence.
//
// Stop is its own mode with its own states: an interrupt's outcome is confirmed or
// unknown, and "stop_confirmed" is not a message state.

// WorkflowInterventionMode is what the caller asked for.
type WorkflowInterventionMode string

const (
	// WorkflowInterventionModeSteer asks the provider to absorb guidance into a turn
	// that is already running.
	WorkflowInterventionModeSteer WorkflowInterventionMode = "steer"
	// WorkflowInterventionModeQueue durably queues a message for a later turn.
	WorkflowInterventionModeQueue WorkflowInterventionMode = "queue"
	// WorkflowInterventionModeStop asks the session to stop, then records a replan
	// request.
	WorkflowInterventionModeStop WorkflowInterventionMode = "stop"
)

// Valid reports whether m is a supported intervention mode.
func (m WorkflowInterventionMode) Valid() bool {
	switch m {
	case WorkflowInterventionModeSteer, WorkflowInterventionModeQueue, WorkflowInterventionModeStop:
		return true
	default:
		return false
	}
}

// WorkflowInterventionState is the durable progress of one intervention.
type WorkflowInterventionState string

const (
	WorkflowInterventionStateRequested WorkflowInterventionState = "requested"
	WorkflowInterventionStateQueued    WorkflowInterventionState = "queued"
	WorkflowInterventionStateDelivered WorkflowInterventionState = "delivered"
	WorkflowInterventionStateApplied   WorkflowInterventionState = "applied"
	WorkflowInterventionStateRejected  WorkflowInterventionState = "rejected"
	WorkflowInterventionStateCancelled WorkflowInterventionState = "cancelled"
	WorkflowInterventionStateUnknown   WorkflowInterventionState = "unknown"
	// The stop-specific states. A stop never passes through applied: an interrupt
	// either has a receipt or it does not.
	WorkflowInterventionStateStopRequested WorkflowInterventionState = "stop_requested"
	WorkflowInterventionStateStopConfirmed WorkflowInterventionState = "stop_confirmed"
	WorkflowInterventionStateStopUnknown   WorkflowInterventionState = "stop_unknown"
)

// Valid reports whether s is a supported intervention state.
func (s WorkflowInterventionState) Valid() bool {
	switch s {
	case WorkflowInterventionStateRequested, WorkflowInterventionStateQueued,
		WorkflowInterventionStateDelivered, WorkflowInterventionStateApplied,
		WorkflowInterventionStateRejected, WorkflowInterventionStateCancelled,
		WorkflowInterventionStateUnknown, WorkflowInterventionStateStopRequested,
		WorkflowInterventionStateStopConfirmed, WorkflowInterventionStateStopUnknown:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further transition may touch this intervention.
func (s WorkflowInterventionState) Terminal() bool {
	switch s {
	case WorkflowInterventionStateApplied, WorkflowInterventionStateRejected,
		WorkflowInterventionStateCancelled, WorkflowInterventionStateStopConfirmed:
		return true
	default:
		return false
	}
}

// RequiresChatFact reports whether this state may only be written when the chat service
// supplied evidence for it. It is the enforcement point for "no fabricated applied".
func (s WorkflowInterventionState) RequiresChatFact() bool {
	switch s {
	case WorkflowInterventionStateQueued, WorkflowInterventionStateDelivered,
		WorkflowInterventionStateApplied:
		return true
	default:
		return false
	}
}

// StopState reports whether s belongs to the stop mode.
func (s WorkflowInterventionState) StopState() bool {
	switch s {
	case WorkflowInterventionStateStopRequested, WorkflowInterventionStateStopConfirmed,
		WorkflowInterventionStateStopUnknown:
		return true
	default:
		return false
	}
}

// messageStates is the set a steer or queue intervention may occupy. Stop states are
// deliberately excluded: a steer can never claim a stop state, and the reverse holds too.
var messageStates = map[WorkflowInterventionState]bool{
	WorkflowInterventionStateRequested: true,
	WorkflowInterventionStateQueued:    true,
	WorkflowInterventionStateDelivered: true,
	WorkflowInterventionStateApplied:   true,
	WorkflowInterventionStateRejected:  true,
	WorkflowInterventionStateCancelled: true,
	WorkflowInterventionStateUnknown:   true,
}

// stopStates is the set a stop intervention may occupy.
var stopStates = map[WorkflowInterventionState]bool{
	WorkflowInterventionStateRequested:     true,
	WorkflowInterventionStateStopRequested: true,
	WorkflowInterventionStateStopConfirmed: true,
	WorkflowInterventionStateStopUnknown:   true,
	WorkflowInterventionStateRejected:      true,
}

// AllowsState reports whether this mode may occupy that state.
//
// This is the mode↔state compatibility rule the migration CHECK mirrors. It exists so a
// steer cannot be recorded as stop_confirmed (which would freeze a task that was never
// stopped) and a stop cannot be recorded as applied (which would claim a message landed).
func (m WorkflowInterventionMode) AllowsState(s WorkflowInterventionState) bool {
	switch m {
	case WorkflowInterventionModeSteer, WorkflowInterventionModeQueue:
		return messageStates[s]
	case WorkflowInterventionModeStop:
		return stopStates[s]
	default:
		return false
	}
}

// AllowedStatesFor lists the states a mode may occupy, in a stable order. The migration's
// mode↔state CHECK is equivalent to this function; a test asserts they agree.
func AllowedStatesFor(m WorkflowInterventionMode) []WorkflowInterventionState {
	ordered := []WorkflowInterventionState{
		WorkflowInterventionStateRequested,
		WorkflowInterventionStateQueued,
		WorkflowInterventionStateDelivered,
		WorkflowInterventionStateApplied,
		WorkflowInterventionStateRejected,
		WorkflowInterventionStateCancelled,
		WorkflowInterventionStateUnknown,
		WorkflowInterventionStateStopRequested,
		WorkflowInterventionStateStopConfirmed,
		WorkflowInterventionStateStopUnknown,
	}
	out := make([]WorkflowInterventionState, 0, len(ordered))
	for _, state := range ordered {
		if m.AllowsState(state) {
			out = append(out, state)
		}
	}
	return out
}

// interventionTransitions is the legal graph per mode. An edge not listed here is a typed
// error, so a receipt can never walk backwards into a weaker claim.
var interventionTransitions = map[WorkflowInterventionState][]WorkflowInterventionState{
	WorkflowInterventionStateRequested: {
		WorkflowInterventionStateQueued,
		WorkflowInterventionStateDelivered,
		WorkflowInterventionStateApplied,
		WorkflowInterventionStateRejected,
		WorkflowInterventionStateCancelled,
		WorkflowInterventionStateUnknown,
		WorkflowInterventionStateStopRequested,
	},
	WorkflowInterventionStateQueued: {
		// A queued turn may be delivered when the chat service promotes it, or
		// withdrawn before dispatch.
		WorkflowInterventionStateDelivered,
		WorkflowInterventionStateApplied,
		WorkflowInterventionStateCancelled,
		WorkflowInterventionStateUnknown,
	},
	WorkflowInterventionStateDelivered: {
		WorkflowInterventionStateApplied,
		WorkflowInterventionStateUnknown,
	},
	WorkflowInterventionStateStopRequested: {
		WorkflowInterventionStateStopConfirmed,
		WorkflowInterventionStateStopUnknown,
	},
	WorkflowInterventionStateStopUnknown: {
		// Only a later receipt may settle it. It never re-requests, so there is no edge
		// back to stop_requested.
		WorkflowInterventionStateStopConfirmed,
	},
	// Terminal states have no exits: a settled receipt is evidence.
	WorkflowInterventionStateApplied:       {},
	WorkflowInterventionStateRejected:      {},
	WorkflowInterventionStateCancelled:     {},
	WorkflowInterventionStateUnknown:       {},
	WorkflowInterventionStateStopConfirmed: {},
}

// CanTransitionTo reports whether an intervention may move between these states. The mode
// compatibility is checked separately by ValidateTransition, because the same state pair
// can be legal for one mode and meaningless for another.
func (s WorkflowInterventionState) CanTransitionTo(next WorkflowInterventionState) bool {
	if !s.Valid() || !next.Valid() {
		return false
	}
	for _, allowed := range interventionTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// ValidateTransition checks the edge and the mode↔state pairing together.
func (m WorkflowInterventionMode) ValidateTransition(
	from WorkflowInterventionState,
	to WorkflowInterventionState,
) error {
	if !m.Valid() {
		return ErrInvalidInterventionMode
	}
	if !from.Valid() || !to.Valid() {
		return ErrInvalidInterventionState
	}
	if !m.AllowsState(to) {
		return ErrInterventionModeStateMismatch
	}
	if !from.CanTransitionTo(to) {
		return ErrInvalidInterventionTransition
	}
	return nil
}

// InterventionChatFact is the chat-side evidence that licenses a receipt state.
//
// A zero value licenses nothing. Callers must copy the real values from the chat
// service's own result, so a receipt cannot be invented out of a successful return.
type InterventionChatFact struct {
	// ChatTurnID is the durable AO turn row the intervention produced or targeted.
	ChatTurnID string
	// ChatTurnState is that row's state at the moment it was read.
	ChatTurnState TurnState
	// ProviderTurnID is the provider's own turn id, when the chat service reported one.
	ProviderTurnID string
	// Queued reports the chat service answered with a queued (not dispatched) turn.
	Queued bool
	// Duplicate reports the chat service recognised an already-recorded client message
	// id and answered from its stored receipt.
	Duplicate bool
}

// Licenses reports whether this evidence is sufficient for the requested state.
//
// This is the "no fabricated applied" gate. It is deliberately not a boolean the caller
// can set: the fields come from the chat service, and each state demands a different one.
//
// The delivered/applied asymmetry is intentional. A bare ProviderTurnID licenses delivered:
// the provider named a turn, so the message reached somewhere. Applied additionally requires
// ChatTurnID because only both ids together prove WHICH AO turn absorbed the guidance — a
// provider turn id alone cannot be matched back to a durable AO row, so treating it as
// "applied" would claim the guidance changed the turn the caller named without evidence for
// it. Weakening this to one id would make applied indistinguishable from delivered.
func (f InterventionChatFact) Licenses(state WorkflowInterventionState) bool {
	switch state {
	case WorkflowInterventionStateQueued:
		// A durable turn row in queued state, or an explicit queued answer.
		return f.Queued || f.ChatTurnState == TurnStateQueued
	case WorkflowInterventionStateDelivered:
		// The turn is running, or a provider turn id was reported for it.
		return f.ChatTurnState == TurnStateRunning || strings.TrimSpace(f.ProviderTurnID) != ""
	case WorkflowInterventionStateApplied:
		// Only a provider-confirmed turn id proves the guidance landed.
		return strings.TrimSpace(f.ProviderTurnID) != "" && strings.TrimSpace(f.ChatTurnID) != ""
	default:
		// Rejected, cancelled, unknown and the stop states are decisions or absences, not
		// chat facts, so they need no chat evidence.
		return true
	}
}

// WorkflowIntervention is one durable Steer, Queue, or Stop request.
//
// It never stores message text: only a payload reference, and the identifiers needed to
// correlate with the chat service's own rows. That is what keeps prompt content out of
// change_log payloads and out of the store.
type WorkflowIntervention struct {
	ID         string                    `json:"id"`
	WorkflowID WorkflowID                `json:"workflowId"`
	TaskID     WorkflowTaskID            `json:"taskId"`
	AttemptID  *WorkflowAttemptID        `json:"attemptId,omitempty"`
	Mode       WorkflowInterventionMode  `json:"mode"`
	State      WorkflowInterventionState `json:"state"`

	// SessionID is nullable in every state on purpose. A session may be deleted while an
	// intervention row still exists, and the row must survive that as history; the loss is
	// recorded by the BEFORE DELETE trigger rather than by forbidding a NULL.
	SessionID *SessionID `json:"sessionId,omitempty"`

	// ControllerGeneration fences this row against a replaced controller. A receipt from
	// an older generation must not settle a newer one.
	ControllerGeneration string `json:"controllerGeneration"`

	ClientMessageID string `json:"clientMessageId"`
	// OperationSHA is the 64-character fingerprint of the request. The same key with a
	// different SHA is a conflict, so a retried Stop with edited content cannot issue a
	// second interrupt.
	OperationSHA string `json:"-"`

	TargetProviderTurnID string `json:"targetProviderTurnId,omitempty"`
	// ChatTurnID and ProviderTurnID are filled from the chat service's own facts.
	ChatTurnID     string `json:"chatTurnId,omitempty"`
	ProviderTurnID string `json:"providerTurnId,omitempty"`

	// ReasonCode is a stable code explaining the state, including refusals.
	ReasonCode string `json:"reasonCode,omitempty"`
	// QueueAvailable accompanies a refused steer so a client can offer the Queue path
	// instead of silently doing something with different effects.
	QueueAvailable bool `json:"queueAvailable"`

	Revision  int64      `json:"revision"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	SettledAt *time.Time `json:"settledAt,omitempty"`
}

// Validate checks identity, fingerprint shape, and mode/state coherence.
func (i WorkflowIntervention) Validate() error {
	if strings.TrimSpace(i.ID) == "" ||
		strings.TrimSpace(string(i.WorkflowID)) == "" ||
		strings.TrimSpace(string(i.TaskID)) == "" ||
		strings.TrimSpace(i.ClientMessageID) == "" ||
		len(i.OperationSHA) != 64 ||
		i.Revision < 1 ||
		i.CreatedAt.IsZero() ||
		i.UpdatedAt.IsZero() ||
		i.UpdatedAt.Before(i.CreatedAt) {
		return ErrInvalidIntervention
	}
	if !i.Mode.Valid() {
		return ErrInvalidInterventionMode
	}
	if !i.State.Valid() {
		return ErrInvalidInterventionState
	}
	if !i.Mode.AllowsState(i.State) {
		return ErrInterventionModeStateMismatch
	}
	if strings.TrimSpace(i.ControllerGeneration) == "" {
		return ErrInterventionGenerationRequired
	}
	// A steer names the turn it intends to join; without one it is a plain send.
	if i.Mode == WorkflowInterventionModeSteer && strings.TrimSpace(i.TargetProviderTurnID) == "" {
		return ErrInterventionTurnRequired
	}
	return nil
}

// ReplanRequest is what a confirmed stop produces. M2 only emits it; executing a new DAG
// is M3's job, so this type carries no plan and no task list.
type ReplanRequest struct {
	InterventionID string             `json:"interventionId"`
	WorkflowID     WorkflowID         `json:"workflowId"`
	TaskID         WorkflowTaskID     `json:"taskId"`
	AttemptID      *WorkflowAttemptID `json:"attemptId,omitempty"`
	SessionID      *SessionID         `json:"sessionId,omitempty"`
	ReasonCode     string             `json:"reasonCode"`
	// PreservedWorktrees records the worktree refs that MUST survive the stop. M2 never
	// deletes or resets them.
	PreservedWorktrees []string  `json:"preservedWorktrees,omitempty"`
	RequestedAt        time.Time `json:"requestedAt"`
}

// Intervention errors.
var (
	ErrInvalidIntervention            = errors.New("invalid workflow intervention")
	ErrInvalidInterventionMode        = errors.New("invalid workflow intervention mode")
	ErrInvalidInterventionState       = errors.New("invalid workflow intervention state")
	ErrInvalidInterventionTransition  = errors.New("invalid workflow intervention state transition")
	ErrInterventionModeStateMismatch  = errors.New("workflow intervention state does not belong to its mode")
	ErrInterventionGenerationRequired = errors.New("workflow intervention requires a controller generation")
	ErrInterventionTurnRequired       = errors.New("steer requires the provider turn it joins")
	ErrInterventionNotFound           = errors.New("workflow intervention not found")
	ErrInterventionRevisionConflict   = errors.New("workflow intervention revision conflict")
	ErrInterventionOperationConflict  = errors.New("workflow intervention key conflicts with another request")
	// ErrInterventionReplayed reports a same-key same-fingerprint replay. The caller must
	// return the stored receipt instead of performing the operation again.
	ErrInterventionReplayed = errors.New("workflow intervention already recorded")
	// ErrInterventionEvidenceMissing means a receipt state was requested without the chat
	// fact that licenses it. It is the runtime guard behind "no fabricated applied".
	ErrInterventionEvidenceMissing = errors.New("workflow intervention state requires chat evidence")
	// ErrInterventionStaleGeneration means the receipt or request names a controller
	// generation that is no longer current.
	ErrInterventionStaleGeneration = errors.New("workflow intervention controller generation is stale")
)
