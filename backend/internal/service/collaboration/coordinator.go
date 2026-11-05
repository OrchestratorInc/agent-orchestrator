// Package collaboration implements M2's tiered-autonomy layer: peer messages between tasks,
// and task/resource requests that only an owner may decide.
//
// # Why a separate package
//
// It owns two things that belong to neither service/workflow (which may not gain graph
// mutation) nor service/execution (which owns session-side action): the SENDER DERIVATION
// rule and the DECISION boundary. Keeping them together means the authorization gate and the
// derivation that depends on it are read as one unit.
//
// # Sender authenticity, stated honestly
//
// A message's sender is derived on the server from the caller's own attempt binding:
//
//	AttemptID → workflow_attempt_bindings(attempt_id)
//	          → attempt's (workflow_id, task_id) and session_id
//
// CreateWorkflowMessageInput therefore has NO sender field. A forged sender is not merely
// rejected — it cannot be expressed.
//
// What that does and does not prove. Loopback has no auth subject at all (authMiddleware
// wraps only the LAN listener), and the chat DTO's SenderSessionID is documented as
// cooperative attribution with a caller-supplied UserAuthored flag. So:
//
//   - Unspoofable in this design: sender_task_id and sender_session_id equal the persisted
//     binding for the supplied attempt. A caller cannot claim another task's identity without
//     also supplying that task's attempt id, and the binding for that attempt is a different
//     row.
//   - NOT proven: that a human operator is who the loopback request says. Nothing in this
//     package authenticates a person, and the code does not claim to.
//
// A workflow agent also cannot decide its own request: the decider type has no `agent`
// member, so the forbidden case is unrepresentable.
//
// # The approval boundary is structural
//
// A pending request has no scheduling effect. Nothing here or in the schema writes
// workflow_tasks, workflow_edges, or workflow_task_repos, and service/workflow exposes no
// AddTask/AddEdge. So approving a request records a decision and returns
// materialization=pending; it cannot create work, and it cannot bypass M1's whole-graph
// validation because there is no path that could.
//
// The M2/M3 split is stated in the value, not in prose the client cannot read: M2 owns
// RECORDING the approval (materialization=pending, reasonCode
// task_request_approved_not_materialized), and M3's planner owns MATERIALIZING it
// (materialization=materialized once a task exists). A rejection is
// materialization=not_applicable — there is nothing to create, ever. An approval must never
// be reported as materialized while no task row exists, and no caller may infer scheduling
// from the word "approved".
package collaboration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Store is the persistence surface this package needs.
type Store interface {
	CreateWorkflowMessage(
		ctx context.Context,
		message domain.WorkflowMessage,
		now time.Time,
	) (domain.WorkflowMessage, bool, error)
	GetWorkflowMessage(
		ctx context.Context,
		workflowID domain.WorkflowID,
		id string,
	) (domain.WorkflowMessage, bool, error)
	GetWorkflowMessageByClientMessage(
		ctx context.Context,
		workflowID domain.WorkflowID,
		clientMessageID string,
	) (domain.WorkflowMessage, bool, error)
	ListWorkflowMessages(
		ctx context.Context,
		workflowID domain.WorkflowID,
		limit, offset int64,
	) ([]domain.WorkflowMessage, error)
	CreateWorkflowTaskRequest(
		ctx context.Context,
		request domain.WorkflowTaskRequest,
		now time.Time,
	) (domain.WorkflowTaskRequest, bool, error)
	GetWorkflowTaskRequest(
		ctx context.Context,
		workflowID domain.WorkflowID,
		id string,
	) (domain.WorkflowTaskRequest, bool, error)
	ListWorkflowTaskRequests(
		ctx context.Context,
		workflowID domain.WorkflowID,
		limit, offset int64,
	) ([]domain.WorkflowTaskRequest, error)
	DecideWorkflowTaskRequest(
		ctx context.Context,
		workflowID domain.WorkflowID,
		id string,
		next domain.WorkflowTaskRequestState,
		decidedBy domain.WorkflowTaskRequestDeciderKind,
		decidedByRef string,
		reason string,
		expectedRevision int64,
		now time.Time,
	) (domain.WorkflowTaskRequest, bool, error)
	CountPendingWorkflowTaskRequests(
		ctx context.Context,
		workflowID domain.WorkflowID,
		requesterTaskID domain.WorkflowTaskID,
	) (int64, error)
}

// BindingReader resolves the caller's own attempt binding, which is the only source of
// sender identity.
type BindingReader interface {
	GetWorkflowAttemptBinding(
		ctx context.Context,
		attemptID domain.WorkflowAttemptID,
	) (domain.WorkflowAttemptBinding, bool, error)
}

// Deps are the coordinator's injected collaborators.
type Deps struct {
	Store    Store
	Bindings BindingReader
	Graph    Graph
	Clock    func() time.Time
	NewID    func(prefix string, seed string) string
}

// Graph is the graph read surface. It is separate from BindingReader so a caller can supply
// the narrow store methods it actually has.
type Graph interface {
	GetWorkflow(ctx context.Context, id domain.WorkflowID) (domain.Workflow, bool, error)
	GetWorkflowTask(
		ctx context.Context,
		workflowID domain.WorkflowID,
		taskID domain.WorkflowTaskID,
	) (domain.WorkflowTask, bool, error)
	ListWorkflowTasks(
		ctx context.Context,
		workflowID domain.WorkflowID,
		state domain.WorkflowTaskState,
		limit, offset int64,
	) ([]domain.WorkflowTask, error)
}

// Coordinator applies the collaboration rules.
type Coordinator struct {
	store    Store
	bindings BindingReader
	graph    Graph
	now      func() time.Time
	newID    func(prefix, seed string) string
	// limits are the graph bounds a request is validated against before a decision. They
	// mirror the deployment settings in daemon/workflow_wiring.go.
	limits domain.WorkflowGraphLimits
}

// New builds a coordinator.
func New(deps Deps) *Coordinator {
	c := &Coordinator{
		store: deps.Store, bindings: deps.Bindings, graph: deps.Graph,
		now: deps.Clock, newID: deps.NewID,
		limits: domain.WorkflowGraphLimits{MaxTasks: 50, MaxDepth: 12, MaxFanOut: 8},
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.newID == nil {
		c.newID = func(prefix, seed string) string {
			digest := sha256.Sum256([]byte(prefix + "\x00" + seed))
			return prefix + "-" + hex.EncodeToString(digest[:12])
		}
	}
	return c
}

// WithLimits overrides the graph bounds used when validating a decision.
func (c *Coordinator) WithLimits(limits domain.WorkflowGraphLimits) *Coordinator {
	c.limits = limits
	return c
}

// CallerIdentity is what the server derived about the caller. It is never built from request
// input.
type CallerIdentity struct {
	WorkflowID domain.WorkflowID
	TaskID     domain.WorkflowTaskID
	AttemptID  domain.WorkflowAttemptID
	SessionID  *domain.SessionID
	Kind       domain.WorkflowMessageSenderKind
}

// ResolveCaller derives the caller's identity from its own attempt binding.
//
// This is the single place sender identity is decided. The fields it refuses to trust from a
// request: the task id, the session id, the sender kind, and any "authored by user" flag.
// Every one of them comes from the persisted binding instead, so a caller can only speak for
// the task whose attempt it actually holds.
//
// requestedKind is the only caller-influenced input, and it is restricted: an agent kind is
// permitted only when the binding exists, because only a real attempt can claim agent
// identity. A human claim never grants extra privilege — it loses the agent fields.
func (c *Coordinator) ResolveCaller(
	ctx context.Context,
	workflowID domain.WorkflowID,
	attemptID domain.WorkflowAttemptID,
	requestedKind domain.WorkflowMessageSenderKind,
) (CallerIdentity, error) {
	if !requestedKind.Valid() {
		return CallerIdentity{}, domain.ErrInvalidWorkflowMessageSender
	}
	binding, found, err := c.bindings.GetWorkflowAttemptBinding(ctx, attemptID)
	if err != nil {
		return CallerIdentity{}, err
	}
	if !found {
		return CallerIdentity{}, fmt.Errorf("%w: no binding for the supplied attempt", domain.ErrCollaborationNotAuthorized)
	}
	// Cross-workflow is refused here, before any SQL touches the collaboration tables, so a
	// mismatched reference produces no partial write.
	if binding.WorkflowID != workflowID {
		return CallerIdentity{}, domain.ErrCollaborationCrossWorkflow
	}
	identity := CallerIdentity{
		WorkflowID: binding.WorkflowID,
		TaskID:     binding.TaskID,
		AttemptID:  binding.AttemptID,
		SessionID:  binding.SessionID,
		Kind:       requestedKind,
	}
	if requestedKind.IsAgent() {
		// An agent must actually hold a session; otherwise it is not executing and has no
		// business speaking as one.
		if identity.SessionID == nil {
			return CallerIdentity{}, fmt.Errorf("%w: an agent sender must hold a session", domain.ErrCollaborationNotAuthorized)
		}
	}
	return identity, nil
}

// requireOperableWorkflow refuses a workflow in a terminal state.
func (c *Coordinator) requireOperableWorkflow(ctx context.Context, workflowID domain.WorkflowID) error {
	workflow, found, err := c.graph.GetWorkflow(ctx, workflowID)
	if err != nil {
		return err
	}
	if !found {
		return domain.ErrCollaborationWorkflowNotFound
	}
	switch workflow.State {
	case domain.WorkflowStateCompleted, domain.WorkflowStateFailed, domain.WorkflowStateCancelled:
		return fmt.Errorf("%w: workflow is %s", domain.ErrCollaborationNotAuthorized, workflow.State)
	}
	return nil
}

// MessageOutcome is the result of CreateMessage.
type MessageOutcome struct {
	Message domain.WorkflowMessage
	// Created reports whether this call stored the row. A replay never sets it.
	Created bool
}

// CreateMessage stores a peer collaboration message.
//
// Order: terminal check, sender derivation, target validation, then the insert. Every refusal
// happens before the write, so a rejected message leaves nothing behind.
func (c *Coordinator) CreateMessage(
	ctx context.Context,
	input domain.CreateWorkflowMessageInput,
) (MessageOutcome, error) {
	if err := c.requireOperableWorkflow(ctx, input.WorkflowID); err != nil {
		return MessageOutcome{}, err
	}
	if !input.Kind.Valid() {
		return MessageOutcome{}, domain.ErrInvalidWorkflowMessageKind
	}
	if strings.TrimSpace(input.ClientMessageID) == "" {
		return MessageOutcome{}, domain.ErrInvalidWorkflowMessage
	}
	identity, err := c.ResolveCaller(ctx, input.WorkflowID, input.AttemptID, domain.WorkflowMessageSenderAgent)
	if err != nil {
		return MessageOutcome{}, err
	}

	// A directed target must belong to this workflow. Checking it here keeps the failure a
	// typed cross-workflow refusal rather than a raw foreign-key string.
	var targetSession *domain.SessionID
	if input.ToTaskID != nil {
		target, found, err := c.graph.GetWorkflowTask(ctx, input.WorkflowID, *input.ToTaskID)
		if err != nil {
			return MessageOutcome{}, err
		}
		if !found {
			return MessageOutcome{}, domain.ErrCollaborationCrossWorkflow
		}
		if target.WorkflowID != input.WorkflowID {
			return MessageOutcome{}, domain.ErrCollaborationCrossWorkflow
		}
		if *input.ToTaskID == identity.TaskID {
			// A message may not target its own sender: that is a loop, not collaboration.
			return MessageOutcome{}, domain.ErrInvalidWorkflowMessageTarget
		}
		targetSession = nil
	}

	now := c.now().UTC()
	senderTaskID := identity.TaskID
	message := domain.WorkflowMessage{
		ID:         c.newID("message", string(input.WorkflowID)+"\x00"+input.ClientMessageID),
		WorkflowID: input.WorkflowID,
		FromTaskID: identity.TaskID,
		ToTaskID:   input.ToTaskID,
		// Both session ids are server-derived. The sender's is the binding's; the target's is
		// unknown until that task's own binding is consulted, so it stays nil here rather than
		// being guessed.
		SenderSessionID: identity.SessionID,
		TargetSessionID: targetSession,
		SenderKind:      identity.Kind,
		SenderTaskID:    &senderTaskID,
		Kind:            input.Kind,
		Body:            input.Body,
		PayloadRef:      input.PayloadRef,
		ClientMessageID: input.ClientMessageID,
		PayloadSHA:      MessagePayloadSHA(input.Kind, input.Body, input.PayloadRef, input.ToTaskID),
	}
	stored, created, err := c.store.CreateWorkflowMessage(ctx, message, now)
	if err != nil {
		return MessageOutcome{}, err
	}
	return MessageOutcome{Message: stored, Created: created}, nil
}

// MessagePayloadSHA fingerprints a message. It covers the kind and the target but not the
// raw body text in a reversible way; the digest is what the idempotency rule compares.
func MessagePayloadSHA(
	kind domain.WorkflowMessageKind,
	body string,
	payloadRef string,
	toTaskID *domain.WorkflowTaskID,
) string {
	target := ""
	if toTaskID != nil {
		target = string(*toTaskID)
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		string(kind), body, payloadRef, target,
	}, "\x00")))
	return hex.EncodeToString(digest[:])
}

// ListMessages reads a page of a workflow's messages.
func (c *Coordinator) ListMessages(
	ctx context.Context,
	workflowID domain.WorkflowID,
	limit, offset int64,
) ([]domain.WorkflowMessage, error) {
	return c.store.ListWorkflowMessages(ctx, workflowID, normalizePage(limit), offset)
}

// CreateTaskRequestInput is a task asking for work or resources.
type CreateTaskRequestInput struct {
	WorkflowID domain.WorkflowID
	AttemptID  domain.WorkflowAttemptID
	// RequestID is caller-supplied so a retried create replays. It is scoped to the workflow.
	RequestID   string
	RequestKind domain.WorkflowTaskRequestKind
	// Payload is arbitrary JSON. It is stored verbatim (canonicalised) and interpreted only by
	// the decide path.
	Payload json.RawMessage
}

// TaskRequestOutcome is the result of CreateTaskRequest.
type TaskRequestOutcome struct {
	Request domain.WorkflowTaskRequest
	Created bool
}

// CreateTaskRequest stores a request in pending state.
//
// It has no scheduling effect: no task, edge, or repo row is written, and the state cannot be
// chosen by the caller.
func (c *Coordinator) CreateTaskRequest(
	ctx context.Context,
	input CreateTaskRequestInput,
) (TaskRequestOutcome, error) {
	if err := c.requireOperableWorkflow(ctx, input.WorkflowID); err != nil {
		return TaskRequestOutcome{}, err
	}
	if !input.RequestKind.Valid() {
		return TaskRequestOutcome{}, domain.ErrInvalidWorkflowTaskRequestKind
	}
	if strings.TrimSpace(input.RequestID) == "" {
		return TaskRequestOutcome{}, domain.ErrInvalidWorkflowTaskRequest
	}
	if len(input.Payload) == 0 {
		return TaskRequestOutcome{}, domain.ErrInvalidWorkflowTaskRequestPayload
	}
	identity, err := c.ResolveCaller(ctx, input.WorkflowID, input.AttemptID, domain.WorkflowMessageSenderAgent)
	if err != nil {
		return TaskRequestOutcome{}, err
	}
	workflow, found, err := c.graph.GetWorkflow(ctx, input.WorkflowID)
	if err != nil {
		return TaskRequestOutcome{}, err
	}
	if !found {
		return TaskRequestOutcome{}, domain.ErrCollaborationWorkflowNotFound
	}

	canonical, err := CanonicalRequestJSON(input.Payload)
	if err != nil {
		return TaskRequestOutcome{}, domain.ErrInvalidWorkflowTaskRequestPayload
	}

	now := c.now().UTC()
	request := domain.WorkflowTaskRequest{
		ID:               input.RequestID,
		WorkflowID:       input.WorkflowID,
		RequesterTaskID:  identity.TaskID,
		RequestKind:      input.RequestKind,
		RequestJSON:      canonical,
		RequestSHA:       RequestSHA(canonical),
		WorkflowRevision: workflow.Revision,
	}
	stored, created, err := c.store.CreateWorkflowTaskRequest(ctx, request, now)
	if err != nil {
		return TaskRequestOutcome{}, err
	}
	return TaskRequestOutcome{Request: stored, Created: created}, nil
}

// CanonicalRequestJSON re-encodes arbitrary JSON with sorted object keys so the fingerprint is
// stable across formatting differences.
func CanonicalRequestJSON(raw json.RawMessage) (string, error) {
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(sortKeys(value))
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

// sortKeys recursively sorts map keys so encoding/json emits a deterministic order.
func sortKeys(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		ordered := make(map[string]any, len(typed))
		for _, key := range keys {
			ordered[key] = sortKeys(typed[key])
		}
		return ordered
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = sortKeys(typed[i])
		}
		return out
	default:
		return value
	}
}

// RequestSHA fingerprints canonical request JSON.
func RequestSHA(canonicalJSON string) string {
	digest := sha256.Sum256([]byte(canonicalJSON))
	return hex.EncodeToString(digest[:])
}

// DecideInput is an owner's decision on a request.
type DecideInput struct {
	WorkflowID   domain.WorkflowID
	RequestID    string
	Approve      bool
	DecidedBy    domain.WorkflowTaskRequestDeciderKind
	DecidedByRef string
	Reason       string
	// ExpectedRevision fences the decision against a concurrent one.
	ExpectedRevision int64
	// DeciderAttemptID, when set, is checked: an agent-held attempt may not decide.
	DeciderAttemptID domain.WorkflowAttemptID
}

// Decide records an owner decision.
//
// The checks, in order, are the ones a wrong decision would have to defeat:
//
//  1. the decider is a human or the daemon (an agent attempt is refused);
//  2. the request is still pending and at the expected revision (CAS);
//  3. the stored JSON still matches its fingerprint (tamper);
//  4. the workflow revision still matches the snapshot the request was raised against;
//  5. the resulting graph would stay within the deployment limits.
//
// The decision reports a TRI-STATE materialization, not a bool:
//
//	rejection → not_applicable  (nothing will ever be created)
//	approval  → pending         (recorded; no task exists) + TaskRequestReasonApprovedNotMaterialized
//
// An approval can never report `materialized` here: service/workflow has no AddTask/AddEdge,
// so recording the answer is the whole of what this method does, and claiming otherwise would
// be a lie about scheduling that has not happened. M3's planner owns turning a pending approval
// into a task; this package records the decision and says so.
func (c *Coordinator) Decide(ctx context.Context, input DecideInput) (domain.WorkflowTaskRequestDecision, error) {
	if !input.DecidedBy.Valid() {
		return domain.WorkflowTaskRequestDecision{}, domain.ErrCollaborationDeciderNotPermitted
	}
	if input.DecidedBy == domain.WorkflowTaskRequestDeciderHuman && strings.TrimSpace(input.DecidedByRef) == "" {
		return domain.WorkflowTaskRequestDecision{}, domain.ErrInvalidWorkflowTaskRequestDecision
	}
	// An agent must not decide, even if it asks nicely: if the caller supplies its attempt, and
	// that attempt belongs to a task, the decision is refused.
	if input.DeciderAttemptID != "" {
		if _, found, err := c.bindings.GetWorkflowAttemptBinding(ctx, input.DeciderAttemptID); err != nil {
			return domain.WorkflowTaskRequestDecision{}, err
		} else if found {
			return domain.WorkflowTaskRequestDecision{}, fmt.Errorf(
				"%w: an agent attempt may not decide a task request", domain.ErrCollaborationDeciderNotPermitted)
		}
	}

	stored, found, err := c.store.GetWorkflowTaskRequest(ctx, input.WorkflowID, input.RequestID)
	if err != nil {
		return domain.WorkflowTaskRequestDecision{}, err
	}
	if !found {
		return domain.WorkflowTaskRequestDecision{}, domain.ErrWorkflowTaskRequestNotFound
	}
	if stored.State != domain.WorkflowTaskRequestStatePending {
		return domain.WorkflowTaskRequestDecision{}, fmt.Errorf(
			"%w: request is already %s", domain.ErrWorkflowTaskRequestConflict, stored.State)
	}
	if stored.Revision != input.ExpectedRevision {
		return domain.WorkflowTaskRequestDecision{}, domain.ErrWorkflowTaskRequestConflict
	}
	// Tamper check: the stored JSON must still hash to the stored fingerprint.
	if RequestSHA(stored.RequestJSON) != stored.RequestSHA {
		return domain.WorkflowTaskRequestDecision{}, domain.ErrWorkflowTaskRequestTampered
	}
	// Graph-moved check: a request raised against revision N may not be approved against N+1.
	workflow, found, err := c.graph.GetWorkflow(ctx, input.WorkflowID)
	if err != nil {
		return domain.WorkflowTaskRequestDecision{}, err
	}
	if !found {
		return domain.WorkflowTaskRequestDecision{}, domain.ErrCollaborationWorkflowNotFound
	}
	if workflow.Revision != stored.WorkflowRevision {
		return domain.WorkflowTaskRequestDecision{}, domain.ErrWorkflowTaskRequestGraphMoved
	}
	// Limit check: the resulting graph must stay within the deployment bounds. Only
	// create_task can move the count, and only by one, but the check is written against the
	// limits rather than the delta so a future kind inherits it.
	if input.Approve && stored.RequestKind == domain.WorkflowTaskRequestKindCreateTask {
		tasks, err := c.graph.ListWorkflowTasks(ctx, input.WorkflowID, "", normalizePage(1000), 0)
		if err != nil {
			return domain.WorkflowTaskRequestDecision{}, err
		}
		if len(tasks)+1 > c.limits.MaxTasks {
			return domain.WorkflowTaskRequestDecision{}, fmt.Errorf(
				"%w: adding a task would exceed the %d-task limit",
				domain.ErrCollaborationNotAuthorized, c.limits.MaxTasks)
		}
	}

	now := c.now().UTC()
	next := domain.WorkflowTaskRequestStateRejected
	if input.Approve {
		next = domain.WorkflowTaskRequestStateApproved
	}
	decided, _, err := c.store.DecideWorkflowTaskRequest(
		ctx, input.WorkflowID, input.RequestID, next, input.DecidedBy, input.DecidedByRef,
		input.Reason, input.ExpectedRevision, now,
	)
	if err != nil {
		return domain.WorkflowTaskRequestDecision{}, err
	}
	// The two decision directions have different materialization facts, and neither is a
	// failure. A rejection is not_applicable — there is nothing to create. An approval is
	// pending — the answer is recorded and no task exists, because this milestone has no
	// graph-mutation entry point. The reason code names that state for the client.
	reasonCode := domain.TaskRequestReasonRejected
	materialization := domain.WorkflowTaskRequestMaterializationNotApplicable
	if input.Approve {
		reasonCode = domain.TaskRequestReasonApprovedNotMaterialized
		materialization = domain.WorkflowTaskRequestMaterializationPending
	}
	return domain.WorkflowTaskRequestDecision{
		Request:         decided,
		Materialization: materialization,
		Materialized:    materialization.Materialized(),
		ReasonCode:      reasonCode,
	}, nil
}

// ListTaskRequests reads a page of a workflow's requests.
func (c *Coordinator) ListTaskRequests(
	ctx context.Context,
	workflowID domain.WorkflowID,
	limit, offset int64,
) ([]domain.WorkflowTaskRequest, error) {
	return c.store.ListWorkflowTaskRequests(ctx, workflowID, normalizePage(limit), offset)
}

// GetTaskRequest reads one request by id inside a workflow.
//
// The workflow id is part of the lookup rather than a check afterwards, so a request id from
// another workflow is simply not found — the caller learns nothing about whether it exists
// elsewhere.
func (c *Coordinator) GetTaskRequest(
	ctx context.Context,
	workflowID domain.WorkflowID,
	requestID string,
) (domain.WorkflowTaskRequest, error) {
	request, found, err := c.store.GetWorkflowTaskRequest(ctx, workflowID, requestID)
	if err != nil {
		return domain.WorkflowTaskRequest{}, err
	}
	if !found {
		return domain.WorkflowTaskRequest{}, domain.ErrWorkflowTaskRequestNotFound
	}
	return request, nil
}

// normalizePage clamps a page size into a sane range.
func normalizePage(limit int64) int64 {
	if limit <= 0 {
		return 50
	}
	if limit > 1000 {
		return 1000
	}
	return limit
}
