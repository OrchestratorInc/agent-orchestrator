package domain

import (
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Workflow collaboration: peer messages between tasks, and task/resource requests that
// require an owner decision.
//
// Two boundaries shape these types, and both are enforced rather than documented.
//
// # Sender authenticity
//
// A collaboration message's sender is derived on the server from the caller's own attempt
// binding, never from the request body. The chat path cannot be reused for this: its
// SendSessionMessageRequest.SenderSessionID is explicitly "cooperative loopback attribution,
// not authentication" and carries a caller-supplied UserAuthored flag. Loopback has no auth
// subject at all (authMiddleware wraps only the LAN listener), so the honest statement is:
// sender_task_id and sender_session_id are UNSPOOFABLE ONLY IN THE WEAK SENSE that they are
// cross-checked against the persisted attempt→session binding, which requires the caller to
// already hold that binding's attempt id. Nothing here authenticates a human operator.
//
// MessageOrigin has human/automation/daemon/provider and deliberately no `agent`, so this
// file declares its own sender enum. A workflow agent is not a chat origin, and forwarding
// one to Chat must map it to automation with AuthoredByUser=false.
//
// # Autonomy boundary
//
// A WorkflowTaskRequest is a REQUEST. Its state machine has no edge that materializes a
// task; approving one records a decision. service/workflow exposes no AddTask/AddEdge, so
// even a decided request cannot create work, and WorkflowTaskRequestDecision therefore
// carries a typed "requires owner approval / not materialized" outcome rather than a
// success that implies anything happened.

// WorkflowMessageSenderKind is who authored a collaboration message.
type WorkflowMessageSenderKind string

const (
	// WorkflowMessageSenderAgent is another task's agent in the same workflow.
	WorkflowMessageSenderAgent WorkflowMessageSenderKind = "agent"
	// WorkflowMessageSenderHuman is a person acting through the daemon.
	WorkflowMessageSenderHuman WorkflowMessageSenderKind = "human"
	// WorkflowMessageSenderDaemon is AO itself (a synthesized notice).
	WorkflowMessageSenderDaemon WorkflowMessageSenderKind = "daemon"
)

// Valid reports whether k is a supported sender kind.
func (k WorkflowMessageSenderKind) Valid() bool {
	switch k {
	case WorkflowMessageSenderAgent, WorkflowMessageSenderHuman, WorkflowMessageSenderDaemon:
		return true
	default:
		return false
	}
}

// IsAgent reports whether the sender is another task's agent, which is the only kind that
// names a sender task and the only kind that must be forwarded to Chat as automation.
func (k WorkflowMessageSenderKind) IsAgent() bool { return k == WorkflowMessageSenderAgent }

// ChatOrigin maps a workflow sender onto the chat vocabulary.
//
// This is the only correct mapping: domain.MessageOrigin has no `agent`, and presenting an
// agent's words as `human` would let a sub-agent impersonate the user in another session's
// transcript.
func (k WorkflowMessageSenderKind) ChatOrigin() MessageOrigin {
	switch k {
	case WorkflowMessageSenderAgent:
		return MessageOriginAutomation
	case WorkflowMessageSenderDaemon:
		return MessageOriginDaemon
	default:
		return MessageOriginHuman
	}
}

// AuthoredByUser reports whether a chat forward may claim human authorship. Only a human
// sender may, and even then the chat layer's own flag decides; an agent never may.
func (k WorkflowMessageSenderKind) AuthoredByUser() bool {
	return k == WorkflowMessageSenderHuman
}

// WorkflowMessageKind classifies the content of a collaboration message.
type WorkflowMessageKind string

const (
	WorkflowMessageKindProgress  WorkflowMessageKind = "progress"
	WorkflowMessageKindQuestion  WorkflowMessageKind = "question"
	WorkflowMessageKindResult    WorkflowMessageKind = "result"
	WorkflowMessageKindHandoff   WorkflowMessageKind = "handoff"
	WorkflowMessageKindReference WorkflowMessageKind = "reference"
)

// Valid reports whether k is a supported message kind.
func (k WorkflowMessageKind) Valid() bool {
	switch k {
	case WorkflowMessageKindProgress, WorkflowMessageKindQuestion,
		WorkflowMessageKindResult, WorkflowMessageKindHandoff,
		WorkflowMessageKindReference:
		return true
	default:
		return false
	}
}

// WorkflowMessageDeliveryState is how far a message got. It says nothing about whether the
// receiver acted on it.
type WorkflowMessageDeliveryState string

const (
	// WorkflowMessageDeliveryPersisted is the durable row and nothing more.
	WorkflowMessageDeliveryPersisted WorkflowMessageDeliveryState = "persisted"
	// WorkflowMessageDeliveryForwarded reached another session's chat.
	WorkflowMessageDeliveryForwarded WorkflowMessageDeliveryState = "forwarded"
	// WorkflowMessageDeliveryRejected was refused (authorization, size, or target state).
	WorkflowMessageDeliveryRejected WorkflowMessageDeliveryState = "rejected"
	// WorkflowMessageDeliveryUnknown may or may not have been forwarded.
	WorkflowMessageDeliveryUnknown WorkflowMessageDeliveryState = "unknown"
)

// Valid reports whether s is a supported delivery state.
func (s WorkflowMessageDeliveryState) Valid() bool {
	switch s {
	case WorkflowMessageDeliveryPersisted, WorkflowMessageDeliveryForwarded,
		WorkflowMessageDeliveryRejected, WorkflowMessageDeliveryUnknown:
		return true
	default:
		return false
	}
}

// Open reports whether the message still counts against the workflow's open-message cap.
func (s WorkflowMessageDeliveryState) Open() bool {
	return s == WorkflowMessageDeliveryPersisted || s == WorkflowMessageDeliveryUnknown
}

// Limits for collaboration content. They mirror the migration's CHECKs; a test asserts
// they agree, so the two cannot drift.
const (
	// MaxWorkflowMessageBodyBytes caps inline message text.
	MaxWorkflowMessageBodyBytes = 4096
	// MaxWorkflowMessagePayloadRefBytes caps the opaque artifact reference.
	MaxWorkflowMessagePayloadRefBytes = 512
	// MaxWorkflowTaskRequestJSONBytes caps the request payload.
	MaxWorkflowTaskRequestJSONBytes = 8192
	// MaxWorkflowTaskRequestReasonBytes caps the decision reason.
	MaxWorkflowTaskRequestReasonBytes = 1024
	// MaxOpenWorkflowMessages caps open messages per workflow. It is a STORE check, not a
	// schema constraint: SQLite cannot express a row-count CHECK, so the store enforces it
	// inside the insert transaction. M2 defines no rate limit.
	MaxOpenWorkflowMessages = 32
)

// WorkflowMessage is one peer collaboration message.
//
// The sender fields are server-derived. CreateWorkflowMessageInput therefore does not
// contain them, so a client cannot even express a forged sender.
type WorkflowMessage struct {
	ID         string     `json:"id"`
	WorkflowID WorkflowID `json:"workflowId"`

	FromTaskID WorkflowTaskID `json:"fromTaskId"`
	// ToTaskID nil means a broadcast to the workflow.
	ToTaskID *WorkflowTaskID `json:"toTaskId,omitempty"`

	SenderSessionID *SessionID `json:"senderSessionId,omitempty"`
	TargetSessionID *SessionID `json:"targetSessionId,omitempty"`

	SenderKind   WorkflowMessageSenderKind `json:"senderKind"`
	SenderTaskID *WorkflowTaskID           `json:"senderTaskId,omitempty"`

	Kind          WorkflowMessageKind          `json:"kind"`
	DeliveryState WorkflowMessageDeliveryState `json:"deliveryState"`

	Body       string `json:"body,omitempty"`
	PayloadRef string `json:"payloadRef,omitempty"`

	ClientMessageID string `json:"clientMessageId"`
	// PayloadSHA is the 64-character fingerprint; it is not exposed as a wire field.
	PayloadSHA string `json:"-"`

	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ValidPayloadRef reports whether a reference is a safe opaque artifact id.
//
// This is a closed character class rather than a denylist. An earlier version rejected only
// '/', '\', ':' and NUL, which let "..", ".", "CON", and every percent-encoded form
// ("a%2Fb", "a%5Cb", "a%252Fb") through — so the comment claimed "not a filesystem path" over a
// check that permitted one. The class below cannot name a path on any platform:
//
//	[A-Za-z0-9._-] only, and the whole value may not be "." or ".."
//
// '%' is excluded so no decoded separator can hide in the id, and the check is anchored
// because "a..b" is a legal flat id while ".." alone is a directory reference. The migration's
// CHECK mirrors this class; a test asserts they agree.
func ValidPayloadRef(ref string) bool {
	if ref == "" || len(ref) > MaxWorkflowMessagePayloadRefBytes {
		return false
	}
	if ref == "." || ref == ".." {
		return false
	}
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '.', r == '_', r == '-':
		default:
			// Everything else — separators, colons, percent, NUL, device names, spaces —
			// is refused, because the class is closed rather than a denylist.
			return false
		}
	}
	return true
}

// Validate checks identity, sender coherence, and content limits.
func (m WorkflowMessage) Validate() error {
	if strings.TrimSpace(m.ID) == "" ||
		strings.TrimSpace(string(m.WorkflowID)) == "" ||
		strings.TrimSpace(string(m.FromTaskID)) == "" ||
		strings.TrimSpace(m.ClientMessageID) == "" ||
		len(m.PayloadSHA) != 64 ||
		m.Revision < 1 ||
		m.CreatedAt.IsZero() ||
		m.UpdatedAt.IsZero() ||
		m.UpdatedAt.Before(m.CreatedAt) {
		return ErrInvalidWorkflowMessage
	}
	if !m.SenderKind.Valid() {
		return ErrInvalidWorkflowMessageSender
	}
	if !m.Kind.Valid() {
		return ErrInvalidWorkflowMessageKind
	}
	if !m.DeliveryState.Valid() {
		return ErrInvalidWorkflowMessageDelivery
	}
	// Sender coherence: an agent sender names its task, and that task is the origin. A
	// human or daemon sender must not name one.
	if m.SenderKind.IsAgent() {
		if m.SenderTaskID == nil || *m.SenderTaskID != m.FromTaskID {
			return ErrInvalidWorkflowMessageSender
		}
	} else if m.SenderTaskID != nil {
		return ErrInvalidWorkflowMessageSender
	}
	// A directed message may not target its own sender.
	if m.ToTaskID != nil && *m.ToTaskID == m.FromTaskID {
		return ErrInvalidWorkflowMessageTarget
	}
	// A row is never empty, and neither field may exceed its cap.
	if m.Body == "" && m.PayloadRef == "" {
		return ErrInvalidWorkflowMessageContent
	}
	if len([]byte(m.Body)) > MaxWorkflowMessageBodyBytes {
		return ErrInvalidWorkflowMessageContent
	}
	if m.PayloadRef != "" && !ValidPayloadRef(m.PayloadRef) {
		return ErrInvalidWorkflowMessageContent
	}
	return nil
}

// CreateWorkflowMessageInput is everything a caller may supply for a message.
//
// It deliberately contains no sender field: the sender is resolved from the caller's own
// attempt binding. Making the absence structural is stronger than validating a supplied
// value, because a forged sender cannot be expressed at all.
type CreateWorkflowMessageInput struct {
	WorkflowID WorkflowID
	// AttemptID identifies the caller. The service resolves from_task, sender session, and
	// sender kind from this attempt's binding.
	AttemptID WorkflowAttemptID
	// ToTaskID nil means a broadcast.
	ToTaskID        *WorkflowTaskID
	Kind            WorkflowMessageKind
	Body            string
	PayloadRef      string
	ClientMessageID string
}

// WorkflowTaskRequestKind is what a task is asking the owner for.
type WorkflowTaskRequestKind string

const (
	WorkflowTaskRequestKindCreateTask          WorkflowTaskRequestKind = "create_task"
	WorkflowTaskRequestKindAddDependency       WorkflowTaskRequestKind = "add_dependency"
	WorkflowTaskRequestKindExpandRepos         WorkflowTaskRequestKind = "expand_repos"
	WorkflowTaskRequestKindIncreaseConcurrency WorkflowTaskRequestKind = "increase_concurrency"
	WorkflowTaskRequestKindIncreaseBudget      WorkflowTaskRequestKind = "increase_budget"
)

// Valid reports whether k is a supported request kind.
func (k WorkflowTaskRequestKind) Valid() bool {
	switch k {
	case WorkflowTaskRequestKindCreateTask, WorkflowTaskRequestKindAddDependency,
		WorkflowTaskRequestKindExpandRepos, WorkflowTaskRequestKindIncreaseConcurrency,
		WorkflowTaskRequestKindIncreaseBudget:
		return true
	default:
		return false
	}
}

// WorkflowTaskRequestState is the decision status of a request.
type WorkflowTaskRequestState string

const (
	WorkflowTaskRequestStatePending  WorkflowTaskRequestState = "pending"
	WorkflowTaskRequestStateApproved WorkflowTaskRequestState = "approved"
	WorkflowTaskRequestStateRejected WorkflowTaskRequestState = "rejected"
	WorkflowTaskRequestStateExpired  WorkflowTaskRequestState = "expired"
)

// Valid reports whether s is a supported request state.
func (s WorkflowTaskRequestState) Valid() bool {
	switch s {
	case WorkflowTaskRequestStatePending, WorkflowTaskRequestStateApproved,
		WorkflowTaskRequestStateRejected, WorkflowTaskRequestStateExpired:
		return true
	default:
		return false
	}
}

// Decided reports whether a decision has been recorded.
func (s WorkflowTaskRequestState) Decided() bool {
	return s == WorkflowTaskRequestStateApproved || s == WorkflowTaskRequestStateRejected
}

// WorkflowTaskRequestDeciderKind is who decided. There is deliberately no `agent`: a
// workflow agent must not approve its own resource request.
type WorkflowTaskRequestDeciderKind string

const (
	WorkflowTaskRequestDeciderHuman  WorkflowTaskRequestDeciderKind = "human"
	WorkflowTaskRequestDeciderDaemon WorkflowTaskRequestDeciderKind = "daemon"
)

// Valid reports whether k is a supported decider kind. An agent is not representable.
func (k WorkflowTaskRequestDeciderKind) Valid() bool {
	return k == WorkflowTaskRequestDeciderHuman || k == WorkflowTaskRequestDeciderDaemon
}

// WorkflowTaskRequest is a task's request for new work or more resources.
type WorkflowTaskRequest struct {
	ID         string     `json:"id"`
	WorkflowID WorkflowID `json:"workflowId"`

	RequesterTaskID WorkflowTaskID          `json:"requesterTaskId"`
	RequestKind     WorkflowTaskRequestKind `json:"requestKind"`

	// RequestJSON is canonicalised JSON. It is returned to a reader, but only the decide
	// path interprets it, and nothing materializes work from it.
	RequestJSON string                   `json:"requestJson"`
	RequestSHA  string                   `json:"-"`
	State       WorkflowTaskRequestState `json:"state"`

	DecidedByKind *WorkflowTaskRequestDeciderKind `json:"decidedByKind,omitempty"`
	DecidedByRef  string                          `json:"decidedByRef,omitempty"`
	DecidedAt     *time.Time                      `json:"decidedAt,omitempty"`
	Reason        string                          `json:"reason,omitempty"`

	// WorkflowRevision is the graph revision the request was raised against.
	WorkflowRevision int64 `json:"workflowRevision"`

	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Validate checks identity, state coherence, and limits.
func (r WorkflowTaskRequest) Validate() error {
	if strings.TrimSpace(r.ID) == "" ||
		strings.TrimSpace(string(r.WorkflowID)) == "" ||
		strings.TrimSpace(string(r.RequesterTaskID)) == "" ||
		len(r.RequestSHA) != 64 ||
		r.WorkflowRevision < 1 ||
		r.Revision < 1 ||
		r.CreatedAt.IsZero() ||
		r.UpdatedAt.IsZero() ||
		r.UpdatedAt.Before(r.CreatedAt) {
		return ErrInvalidWorkflowTaskRequest
	}
	if !r.RequestKind.Valid() {
		return ErrInvalidWorkflowTaskRequestKind
	}
	if !r.State.Valid() {
		return ErrInvalidWorkflowTaskRequestState
	}
	if !json.Valid([]byte(r.RequestJSON)) {
		return ErrInvalidWorkflowTaskRequestPayload
	}
	if len([]byte(r.RequestJSON)) > MaxWorkflowTaskRequestJSONBytes {
		return ErrInvalidWorkflowTaskRequestPayload
	}
	if len([]byte(r.Reason)) > MaxWorkflowTaskRequestReasonBytes {
		return ErrInvalidWorkflowTaskRequestPayload
	}
	if r.State == WorkflowTaskRequestStatePending {
		if r.DecidedByKind != nil || r.DecidedAt != nil {
			return ErrInvalidWorkflowTaskRequestDecision
		}
		return nil
	}
	if r.DecidedAt == nil {
		return ErrInvalidWorkflowTaskRequestDecision
	}
	if r.State.Decided() && (r.DecidedByKind == nil || !r.DecidedByKind.Valid()) {
		// An agent decision is unrepresentable here, which is what makes "a sub-agent
		// cannot approve its own request" a type-level fact rather than a check.
		return ErrInvalidWorkflowTaskRequestDecision
	}
	return nil
}

// WorkflowTaskRequestMaterialization is what happened to the requested graph change.
//
// It is a TRI-STATE because "approved" and "the task exists" are different facts, and M2 can
// only ever produce the first. Collapsing them into a bool is what let an approval read as a
// success: `materialized:false` is equally true of a rejection and of an approval that no
// scheduler has picked up yet, so a client could not tell "nothing will happen" from
// "something will happen later".
//
// The three values are exhaustive:
//
//   - not_applicable — the request was REJECTED. There is nothing to materialize, ever.
//   - pending        — the request was APPROVED and no task exists yet. M2 always stops here:
//     service/workflow exposes no AddTask/AddEdge, so recording the owner's answer is the whole
//     of what an approval does. M3's planner owns turning it into work.
//   - materialized   — a task exists for the request. Unreachable in M2; the constant exists so
//     M3 adds a transition rather than a new field, and so the HTTP layer's 200-on-approval rule
//     is already written against the right predicate.
type WorkflowTaskRequestMaterialization string

const (
	// WorkflowTaskRequestMaterializationNotApplicable is a rejection: nothing to create.
	WorkflowTaskRequestMaterializationNotApplicable WorkflowTaskRequestMaterialization = "not_applicable"
	// WorkflowTaskRequestMaterializationPending is an approval with no task created yet.
	WorkflowTaskRequestMaterializationPending WorkflowTaskRequestMaterialization = "pending"
	// WorkflowTaskRequestMaterializationMaterialized is an approval whose task exists.
	WorkflowTaskRequestMaterializationMaterialized WorkflowTaskRequestMaterialization = "materialized"
)

// Valid reports whether m is one of the three defined states.
//
// The empty string is NOT valid: a decision must say what it did, and "" is exactly the
// "unknown" a client would have to guess about.
func (m WorkflowTaskRequestMaterialization) Valid() bool {
	switch m {
	case WorkflowTaskRequestMaterializationNotApplicable,
		WorkflowTaskRequestMaterializationPending,
		WorkflowTaskRequestMaterializationMaterialized:
		return true
	default:
		return false
	}
}

// Materialized reports whether a task exists for the decision.
//
// This is the ONLY derivation of the wire-compatible bool, so the bool and the tri-state can
// never disagree. Everything else (domain, service, HTTP) reads this method rather than
// comparing the status itself.
func (m WorkflowTaskRequestMaterialization) Materialized() bool {
	return m == WorkflowTaskRequestMaterializationMaterialized
}

// WorkflowTaskRequestDecision is the outcome of a decide call.
//
// Materialization carries the tri-state; Materialized is the derived bool kept for wire
// compatibility with clients that only know the older field. It is populated by the service
// (and re-derived by the HTTP layer) from Materialization, never set independently.
type WorkflowTaskRequestDecision struct {
	Request WorkflowTaskRequest `json:"request"`
	// Materialization is not_applicable (rejected), pending (approved, no task yet), or
	// materialized (a task exists).
	Materialization WorkflowTaskRequestMaterialization `json:"materialization"`
	// Materialized reports whether the requested change was applied to the graph. Derived
	// from Materialization — see WorkflowTaskRequestMaterialization.Materialized.
	Materialized bool `json:"materialized"`
	// ReasonCode is a stable code. TaskRequestReasonApprovedNotMaterialized accompanies every
	// approved-but-unmaterialized decision.
	ReasonCode string `json:"reasonCode"`
}

// Task request reason codes.
//
// They are constants rather than literals because they are a wire contract: a client
// branches on them to decide whether to say "approved" or "approved and scheduled".
const (
	// TaskRequestReasonApprovedNotMaterialized means the owner approved and no task was
	// created. M2 always returns this for an approval; M3 will stop returning it once a task
	// exists.
	TaskRequestReasonApprovedNotMaterialized = "task_request_approved_not_materialized"
	// TaskRequestReasonRejected means the owner rejected the request.
	TaskRequestReasonRejected = "task_request_rejected"
	// TaskRequestReasonAlreadyDecided means a retry replayed a decision already recorded.
	TaskRequestReasonAlreadyDecided = "task_request_already_decided"
)

// Collaboration errors.
var (
	ErrInvalidWorkflowMessage             = errors.New("invalid workflow message")
	ErrInvalidWorkflowMessageSender       = errors.New("invalid workflow message sender")
	ErrInvalidWorkflowMessageKind         = errors.New("invalid workflow message kind")
	ErrInvalidWorkflowMessageDelivery     = errors.New("invalid workflow message delivery state")
	ErrInvalidWorkflowMessageTarget       = errors.New("workflow message may not target its own sender")
	ErrInvalidWorkflowMessageContent      = errors.New("workflow message content is empty or too large")
	ErrWorkflowMessageNotFound            = errors.New("workflow message not found")
	ErrWorkflowMessageConflict            = errors.New("workflow message key conflicts with another message")
	ErrWorkflowMessageQueueFull           = errors.New("workflow message queue is full")
	ErrInvalidWorkflowTaskRequest         = errors.New("invalid workflow task request")
	ErrInvalidWorkflowTaskRequestKind     = errors.New("invalid workflow task request kind")
	ErrInvalidWorkflowTaskRequestState    = errors.New("invalid workflow task request state")
	ErrInvalidWorkflowTaskRequestPayload  = errors.New("invalid workflow task request payload")
	ErrInvalidWorkflowTaskRequestDecision = errors.New("invalid workflow task request decision")
	ErrWorkflowTaskRequestNotFound        = errors.New("workflow task request not found")
	ErrWorkflowTaskRequestConflict        = errors.New("workflow task request key conflicts with another request")
	// ErrWorkflowTaskRequestNotMaterialized means the request was decided but cannot be
	// applied, because the workflow service has no graph-mutation entry point. It is not a
	// failure of the decision; it is the honest description of what M2 can do.
	ErrWorkflowTaskRequestNotMaterialized = errors.New("workflow task request cannot be materialized in this milestone")
	// ErrWorkflowTaskRequestTampered means the stored request JSON no longer matches its
	// fingerprint.
	ErrWorkflowTaskRequestTampered = errors.New("workflow task request payload does not match its fingerprint")
	// ErrWorkflowTaskRequestGraphMoved means the workflow revision changed since the request
	// was raised, so approving it would apply a decision to a different graph.
	ErrWorkflowTaskRequestGraphMoved = errors.New("workflow graph changed since the request was raised")
	// ErrCollaborationNotAuthorized means the caller does not own the task it claims to
	// speak for, or the workflow is in a terminal state.
	ErrCollaborationNotAuthorized = errors.New("workflow collaboration is not authorized")
	// ErrCollaborationCrossWorkflow means a reference named a task from another workflow.
	ErrCollaborationCrossWorkflow = errors.New("workflow collaboration reference crosses workflows")
	// ErrCollaborationDeciderNotPermitted means the decider is not a human or the daemon.
	ErrCollaborationDeciderNotPermitted = errors.New("workflow collaboration decision requires an owner")
	// ErrCollaborationWorkflowNotFound means the workflow does not exist.
	ErrCollaborationWorkflowNotFound = errors.New("workflow not found")
)
