package collaboration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var testNow = time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)

func fixedClock() func() time.Time { return func() time.Time { return testNow } }

const testSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// memoryStore mirrors the real store's contract for the cases the service cares about.
type memoryStore struct {
	messages    map[string]domain.WorkflowMessage
	msgByClient map[string]string
	requests    map[string]domain.WorkflowTaskRequest
	createCalls int
	openCount   int
}

func newMemoryStore() *memoryStore {
	return &memoryStore{
		messages:    make(map[string]domain.WorkflowMessage),
		msgByClient: make(map[string]string),
		requests:    make(map[string]domain.WorkflowTaskRequest),
	}
}

func msgKey(workflowID domain.WorkflowID, clientMessageID string) string {
	return string(workflowID) + "\x00" + clientMessageID
}

func (m *memoryStore) CreateWorkflowMessage(
	_ context.Context,
	message domain.WorkflowMessage,
	now time.Time,
) (domain.WorkflowMessage, bool, error) {
	m.createCalls++
	if m.openCount >= domain.MaxOpenWorkflowMessages {
		return domain.WorkflowMessage{}, false, domain.ErrWorkflowMessageQueueFull
	}
	key := msgKey(message.WorkflowID, message.ClientMessageID)
	if existingID, ok := m.msgByClient[key]; ok {
		existing := m.messages[existingID]
		if existing.PayloadSHA != message.PayloadSHA {
			return domain.WorkflowMessage{}, false, domain.ErrWorkflowMessageConflict
		}
		return existing, false, nil
	}
	message.DeliveryState = domain.WorkflowMessageDeliveryPersisted
	message.Revision = 1
	message.CreatedAt = now
	message.UpdatedAt = now
	m.messages[message.ID] = message
	m.msgByClient[key] = message.ID
	m.openCount++
	return message, true, nil
}

func (m *memoryStore) GetWorkflowMessage(
	_ context.Context,
	workflowID domain.WorkflowID,
	id string,
) (domain.WorkflowMessage, bool, error) {
	message, ok := m.messages[id]
	if !ok || message.WorkflowID != workflowID {
		return domain.WorkflowMessage{}, false, nil
	}
	return message, true, nil
}

func (m *memoryStore) GetWorkflowMessageByClientMessage(
	_ context.Context,
	workflowID domain.WorkflowID,
	clientMessageID string,
) (domain.WorkflowMessage, bool, error) {
	id, ok := m.msgByClient[msgKey(workflowID, clientMessageID)]
	if !ok {
		return domain.WorkflowMessage{}, false, nil
	}
	return m.messages[id], true, nil
}

func (m *memoryStore) ListWorkflowMessages(
	_ context.Context,
	workflowID domain.WorkflowID,
	_, _ int64,
) ([]domain.WorkflowMessage, error) {
	out := make([]domain.WorkflowMessage, 0, len(m.messages))
	for _, message := range m.messages {
		if message.WorkflowID == workflowID {
			out = append(out, message)
		}
	}
	return out, nil
}

func (m *memoryStore) CreateWorkflowTaskRequest(
	_ context.Context,
	request domain.WorkflowTaskRequest,
	now time.Time,
) (domain.WorkflowTaskRequest, bool, error) {
	if existing, ok := m.requests[request.ID]; ok {
		if existing.RequestSHA != request.RequestSHA {
			return domain.WorkflowTaskRequest{}, false, domain.ErrWorkflowTaskRequestConflict
		}
		return existing, false, nil
	}
	request.State = domain.WorkflowTaskRequestStatePending
	request.Revision = 1
	request.CreatedAt = now
	request.UpdatedAt = now
	m.requests[request.ID] = request
	return request, true, nil
}

func (m *memoryStore) GetWorkflowTaskRequest(
	_ context.Context,
	workflowID domain.WorkflowID,
	id string,
) (domain.WorkflowTaskRequest, bool, error) {
	request, ok := m.requests[id]
	if !ok || request.WorkflowID != workflowID {
		return domain.WorkflowTaskRequest{}, false, nil
	}
	return request, true, nil
}

func (m *memoryStore) ListWorkflowTaskRequests(
	_ context.Context,
	workflowID domain.WorkflowID,
	_, _ int64,
) ([]domain.WorkflowTaskRequest, error) {
	out := make([]domain.WorkflowTaskRequest, 0, len(m.requests))
	for _, request := range m.requests {
		if request.WorkflowID == workflowID {
			out = append(out, request)
		}
	}
	return out, nil
}

func (m *memoryStore) DecideWorkflowTaskRequest(
	_ context.Context,
	workflowID domain.WorkflowID,
	id string,
	next domain.WorkflowTaskRequestState,
	decidedBy domain.WorkflowTaskRequestDeciderKind,
	decidedByRef string,
	reason string,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowTaskRequest, bool, error) {
	request, ok := m.requests[id]
	if !ok || request.WorkflowID != workflowID {
		return domain.WorkflowTaskRequest{}, false, domain.ErrWorkflowTaskRequestNotFound
	}
	if request.Revision != expectedRevision || request.State != domain.WorkflowTaskRequestStatePending {
		return domain.WorkflowTaskRequest{}, false, domain.ErrWorkflowTaskRequestConflict
	}
	request.State = next
	kind := decidedBy
	request.DecidedByKind = &kind
	request.DecidedByRef = decidedByRef
	request.DecidedAt = &now
	request.Reason = reason
	request.Revision++
	request.UpdatedAt = now
	m.requests[id] = request
	return request, true, nil
}

func (m *memoryStore) CountPendingWorkflowTaskRequests(
	_ context.Context,
	workflowID domain.WorkflowID,
	requesterTaskID domain.WorkflowTaskID,
) (int64, error) {
	var count int64
	for _, request := range m.requests {
		if request.WorkflowID == workflowID && request.RequesterTaskID == requesterTaskID &&
			request.State == domain.WorkflowTaskRequestStatePending {
			count++
		}
	}
	return count, nil
}

// memoryBindings resolves the caller's attempt.
type memoryBindings struct {
	binding domain.WorkflowAttemptBinding
	found   bool
}

func (m *memoryBindings) GetWorkflowAttemptBinding(
	_ context.Context,
	_ domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, bool, error) {
	return m.binding, m.found, nil
}

// memoryGraph serves the workflow and its tasks.
type memoryGraph struct {
	workflow domain.Workflow
	tasks    map[domain.WorkflowTaskID]domain.WorkflowTask
	found    bool
}

func (m *memoryGraph) GetWorkflow(_ context.Context, _ domain.WorkflowID) (domain.Workflow, bool, error) {
	return m.workflow, m.found, nil
}

func (m *memoryGraph) GetWorkflowTask(
	_ context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) (domain.WorkflowTask, bool, error) {
	task, ok := m.tasks[taskID]
	if !ok || task.WorkflowID != workflowID {
		return domain.WorkflowTask{}, false, nil
	}
	return task, true, nil
}

func (m *memoryGraph) ListWorkflowTasks(
	_ context.Context,
	workflowID domain.WorkflowID,
	_ domain.WorkflowTaskState,
	_, _ int64,
) ([]domain.WorkflowTask, error) {
	out := make([]domain.WorkflowTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		if task.WorkflowID == workflowID {
			out = append(out, task)
		}
	}
	return out, nil
}

// fixture builds a coordinator whose caller holds attempt-1 on task-1 of workflow-1.
func fixture(t *testing.T) (*Coordinator, *memoryStore, *memoryBindings, *memoryGraph) {
	t.Helper()
	session := domain.SessionID("p1-1")
	bindings := &memoryBindings{
		found: true,
		binding: domain.WorkflowAttemptBinding{
			AttemptID: "attempt-1", WorkflowID: "workflow-1", TaskID: "task-1",
			SpawnRequestID: "spawn-1", SpawnHash: testSHA,
			State: domain.WorkflowBindingStateBoundReady, SessionID: &session,
			ControllerGeneration: "gen-1", Revision: 2, CreatedAt: testNow, UpdatedAt: testNow,
		},
	}
	graph := &memoryGraph{
		found: true,
		workflow: domain.Workflow{
			ID: "workflow-1", Title: "T", Goal: "G", State: domain.WorkflowStateActive,
			Revision: 2, CreatedAt: testNow, UpdatedAt: testNow,
		},
		tasks: map[domain.WorkflowTaskID]domain.WorkflowTask{
			"task-1": {ID: "task-1", WorkflowID: "workflow-1", Kind: domain.WorkflowTaskKindDevelopment,
				Title: "A", State: domain.WorkflowTaskStateRunning, Revision: 1, CreatedAt: testNow, UpdatedAt: testNow},
			"task-2": {ID: "task-2", WorkflowID: "workflow-1", Kind: domain.WorkflowTaskKindTesting,
				Title: "B", State: domain.WorkflowTaskStateReady, Revision: 1, CreatedAt: testNow, UpdatedAt: testNow},
		},
	}
	store := newMemoryStore()
	return New(Deps{
		Store: store, Bindings: bindings, Graph: graph, Clock: fixedClock(),
	}), store, bindings, graph
}

// TestCreateMessageDerivesSenderFromTheBinding is the sender-authenticity proof: the task and
// session come from the binding, and a caller cannot supply a different one.
func TestCreateMessageDerivesSenderFromTheBinding(t *testing.T) {
	t.Parallel()
	coordinator, _, bindings, _ := fixture(t)

	outcome, err := coordinator.CreateMessage(context.Background(), domain.CreateWorkflowMessageInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1",
		Kind: domain.WorkflowMessageKindProgress, Body: "halfway", ClientMessageID: "msg-1",
	})
	if err != nil {
		t.Fatalf("CreateMessage: %v", err)
	}
	if outcome.Message.FromTaskID != "task-1" {
		t.Fatalf("from_task = %q, want the binding's task", outcome.Message.FromTaskID)
	}
	if outcome.Message.SenderTaskID == nil || *outcome.Message.SenderTaskID != "task-1" {
		t.Fatalf("sender_task = %v, want the binding's task", outcome.Message.SenderTaskID)
	}
	if outcome.Message.SenderSessionID == nil || *outcome.Message.SenderSessionID != "p1-1" {
		t.Fatalf("sender_session = %v, want the binding's session", outcome.Message.SenderSessionID)
	}
	if outcome.Message.SenderKind != domain.WorkflowMessageSenderAgent {
		t.Fatalf("sender_kind = %q, want agent", outcome.Message.SenderKind)
	}

	// Now change the binding's task: the derived sender follows it, proving the value is read
	// from the binding rather than from the input.
	bindings.binding.TaskID = "task-2"
	second, err := coordinator.CreateMessage(context.Background(), domain.CreateWorkflowMessageInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1",
		Kind: domain.WorkflowMessageKindProgress, Body: "now from task-2", ClientMessageID: "msg-2",
	})
	if err != nil {
		t.Fatalf("second CreateMessage: %v", err)
	}
	if second.Message.FromTaskID != "task-2" {
		t.Fatalf("from_task = %q, want the updated binding's task; the input must not decide it",
			second.Message.FromTaskID)
	}
}

// TestCreateMessageRefusesUnboundAttempt proves an attempt that is not bound cannot speak.
func TestCreateMessageRefusesUnboundAttempt(t *testing.T) {
	t.Parallel()
	coordinator, _, bindings, _ := fixture(t)
	bindings.found = false
	_, err := coordinator.CreateMessage(context.Background(), domain.CreateWorkflowMessageInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-unknown",
		Kind: domain.WorkflowMessageKindProgress, Body: "hi", ClientMessageID: "msg-1",
	})
	if !errors.Is(err, domain.ErrCollaborationNotAuthorized) {
		t.Fatalf("err = %v, want ErrCollaborationNotAuthorized", err)
	}
}

// TestCreateMessageCrossWorkflowRefusedBeforeSQL proves the refusal happens before any write.
func TestCreateMessageCrossWorkflowRefusedBeforeSQL(t *testing.T) {
	t.Parallel()
	coordinator, store, bindings, _ := fixture(t)
	// The caller's binding belongs to a different workflow than the one it addresses.
	bindings.binding.WorkflowID = "workflow-2"

	_, err := coordinator.CreateMessage(context.Background(), domain.CreateWorkflowMessageInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1",
		Kind: domain.WorkflowMessageKindProgress, Body: "hi", ClientMessageID: "msg-1",
	})
	if !errors.Is(err, domain.ErrCollaborationCrossWorkflow) {
		t.Fatalf("err = %v, want ErrCollaborationCrossWorkflow", err)
	}
	if store.createCalls != 0 {
		t.Fatalf("store create calls = %d, want 0: the refusal must precede the write", store.createCalls)
	}
}

// TestCreateMessageTargetMustBeInWorkflow proves a directed target is validated before the
// insert, so no partial write happens.
func TestCreateMessageTargetMustBeInWorkflow(t *testing.T) {
	t.Parallel()
	coordinator, store, _, _ := fixture(t)
	foreign := domain.WorkflowTaskID("task-foreign")
	_, err := coordinator.CreateMessage(context.Background(), domain.CreateWorkflowMessageInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1", ToTaskID: &foreign,
		Kind: domain.WorkflowMessageKindQuestion, Body: "hi", ClientMessageID: "msg-1",
	})
	if !errors.Is(err, domain.ErrCollaborationCrossWorkflow) {
		t.Fatalf("err = %v, want ErrCollaborationCrossWorkflow", err)
	}
	if store.createCalls != 0 {
		t.Fatalf("store create calls = %d, want 0", store.createCalls)
	}
}

// TestCreateMessageRejectsSelfTarget proves a message may not address its own sender.
func TestCreateMessageRejectsSelfTarget(t *testing.T) {
	t.Parallel()
	coordinator, _, _, _ := fixture(t)
	self := domain.WorkflowTaskID("task-1")
	_, err := coordinator.CreateMessage(context.Background(), domain.CreateWorkflowMessageInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1", ToTaskID: &self,
		Kind: domain.WorkflowMessageKindQuestion, Body: "hi", ClientMessageID: "msg-1",
	})
	if !errors.Is(err, domain.ErrInvalidWorkflowMessageTarget) {
		t.Fatalf("err = %v, want ErrInvalidWorkflowMessageTarget", err)
	}
}

// TestCreateMessageRefusedInTerminalWorkflow proves collaboration stops once the workflow ends.
func TestCreateMessageRefusedInTerminalWorkflow(t *testing.T) {
	t.Parallel()
	for _, state := range []domain.WorkflowState{
		domain.WorkflowStateCompleted, domain.WorkflowStateFailed, domain.WorkflowStateCancelled,
	} {
		coordinator, _, _, graph := fixture(t)
		graph.workflow.State = state
		_, err := coordinator.CreateMessage(context.Background(), domain.CreateWorkflowMessageInput{
			WorkflowID: "workflow-1", AttemptID: "attempt-1",
			Kind: domain.WorkflowMessageKindProgress, Body: "hi", ClientMessageID: "msg-1",
		})
		if !errors.Is(err, domain.ErrCollaborationNotAuthorized) {
			t.Fatalf("state %q: err = %v, want ErrCollaborationNotAuthorized", state, err)
		}
	}
}

// TestCreateMessageReplayIsFree proves a replay creates nothing and bumps nothing.
func TestCreateMessageReplayIsFree(t *testing.T) {
	t.Parallel()
	coordinator, store, _, _ := fixture(t)
	input := domain.CreateWorkflowMessageInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1",
		Kind: domain.WorkflowMessageKindResult, Body: "done", ClientMessageID: "msg-1",
	}
	first, err := coordinator.CreateMessage(context.Background(), input)
	if err != nil || !first.Created {
		t.Fatalf("first: created=%v err=%v", first.Created, err)
	}
	second, err := coordinator.CreateMessage(context.Background(), input)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if second.Created {
		t.Fatal("the replay reported a new message")
	}
	if second.Message.Revision != first.Message.Revision {
		t.Fatalf("replay bumped the revision: %d vs %d", second.Message.Revision, first.Message.Revision)
	}
	if len(store.messages) != 1 {
		t.Fatalf("messages = %d, want 1", len(store.messages))
	}
}

// TestDecideRefusesAgentDecider proves an agent-held attempt cannot decide.
func TestDecideRefusesAgentDecider(t *testing.T) {
	t.Parallel()
	coordinator, _, _, _ := fixture(t)
	request, err := coordinator.CreateTaskRequest(context.Background(), CreateTaskRequestInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1", RequestID: "req-1",
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		Payload:     json.RawMessage(`{"title":"add tests"}`),
	})
	if err != nil {
		t.Fatalf("CreateTaskRequest: %v", err)
	}

	// The decider supplies its own agent attempt: refused.
	_, err = coordinator.Decide(context.Background(), DecideInput{
		WorkflowID: "workflow-1", RequestID: request.Request.ID, Approve: true,
		DecidedBy: domain.WorkflowTaskRequestDeciderHuman, DecidedByRef: "operator-1",
		ExpectedRevision: request.Request.Revision, DeciderAttemptID: "attempt-1",
	})
	if !errors.Is(err, domain.ErrCollaborationDeciderNotPermitted) {
		t.Fatalf("err = %v, want ErrCollaborationDeciderNotPermitted", err)
	}

	// And an "agent" decider kind is not representable.
	_, err = coordinator.Decide(context.Background(), DecideInput{
		WorkflowID: "workflow-1", RequestID: request.Request.ID, Approve: true,
		DecidedBy: domain.WorkflowTaskRequestDeciderKind("agent"), DecidedByRef: "task-1",
		ExpectedRevision: request.Request.Revision,
	})
	if !errors.Is(err, domain.ErrCollaborationDeciderNotPermitted) {
		t.Fatalf("err = %v, want ErrCollaborationDeciderNotPermitted", err)
	}
}

// TestDecideDetectsTamperedPayload proves the request_sha check catches an edited payload.
func TestDecideDetectsTamperedPayload(t *testing.T) {
	t.Parallel()
	coordinator, store, _, _ := fixture(t)
	request, err := coordinator.CreateTaskRequest(context.Background(), CreateTaskRequestInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1", RequestID: "req-tamper",
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		Payload:     json.RawMessage(`{"title":"add tests"}`),
	})
	if err != nil {
		t.Fatalf("CreateTaskRequest: %v", err)
	}
	// Rewrite the payload in the store without updating the fingerprint.
	tampered := request.Request
	tampered.RequestJSON = `{"title":"and delete the database"}`
	store.requests[tampered.ID] = tampered

	_, err = coordinator.Decide(context.Background(), DecideInput{
		WorkflowID: "workflow-1", RequestID: tampered.ID, Approve: true,
		DecidedBy: domain.WorkflowTaskRequestDeciderHuman, DecidedByRef: "operator-1",
		ExpectedRevision: tampered.Revision,
	})
	if !errors.Is(err, domain.ErrWorkflowTaskRequestTampered) {
		t.Fatalf("err = %v, want ErrWorkflowTaskRequestTampered", err)
	}
}

// TestDecideDetectsGraphMoved proves a request cannot be approved after the graph changed.
func TestDecideDetectsGraphMoved(t *testing.T) {
	t.Parallel()
	coordinator, _, _, graph := fixture(t)
	request, err := coordinator.CreateTaskRequest(context.Background(), CreateTaskRequestInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1", RequestID: "req-moved",
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		Payload:     json.RawMessage(`{"title":"add tests"}`),
	})
	if err != nil {
		t.Fatalf("CreateTaskRequest: %v", err)
	}
	graph.workflow.Revision = 99

	_, err = coordinator.Decide(context.Background(), DecideInput{
		WorkflowID: "workflow-1", RequestID: request.Request.ID, Approve: true,
		DecidedBy: domain.WorkflowTaskRequestDeciderHuman, DecidedByRef: "operator-1",
		ExpectedRevision: request.Request.Revision,
	})
	if !errors.Is(err, domain.ErrWorkflowTaskRequestGraphMoved) {
		t.Fatalf("err = %v, want ErrWorkflowTaskRequestGraphMoved", err)
	}
}

// TestDecideApprovalNeverMaterializes is the approval-boundary proof: an approval records the
// answer, reports materialization=pending (not materialized), and changes no task row.
func TestDecideApprovalNeverMaterializes(t *testing.T) {
	t.Parallel()
	coordinator, store, _, graph := fixture(t)
	tasksBefore := len(graph.tasks)

	request, err := coordinator.CreateTaskRequest(context.Background(), CreateTaskRequestInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1", RequestID: "req-approve",
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		Payload:     json.RawMessage(`{"title":"add tests"}`),
	})
	if err != nil {
		t.Fatalf("CreateTaskRequest: %v", err)
	}
	if request.Request.State != domain.WorkflowTaskRequestStatePending {
		t.Fatalf("a newly created request is %q, want pending", request.Request.State)
	}

	decision, err := coordinator.Decide(context.Background(), DecideInput{
		WorkflowID: "workflow-1", RequestID: request.Request.ID, Approve: true,
		DecidedBy: domain.WorkflowTaskRequestDeciderHuman, DecidedByRef: "operator-1",
		Reason: "reasonable", ExpectedRevision: request.Request.Revision,
	})
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if decision.Materialized {
		t.Fatal("an approval claimed to materialize a task; service/workflow has no AddTask")
	}
	if decision.Materialization != domain.WorkflowTaskRequestMaterializationPending {
		t.Fatalf("materialization = %q, want pending", decision.Materialization)
	}
	if decision.ReasonCode != domain.TaskRequestReasonApprovedNotMaterialized {
		t.Fatalf("reason = %q, want %q", decision.ReasonCode, domain.TaskRequestReasonApprovedNotMaterialized)
	}
	if decision.Request.State != domain.WorkflowTaskRequestStateApproved {
		t.Fatalf("state = %q, want approved (the decision is recorded)", decision.Request.State)
	}
	// No task was created anywhere.
	if len(graph.tasks) != tasksBefore {
		t.Fatalf("graph tasks = %d, want %d unchanged", len(graph.tasks), tasksBefore)
	}
	if len(store.requests) != 1 {
		t.Fatalf("requests = %d, want 1", len(store.requests))
	}
}

// TestDecideRefusesWhenGraphWouldExceedLimit proves the limit check runs before the decision.
func TestDecideRefusesWhenGraphWouldExceedLimit(t *testing.T) {
	t.Parallel()
	coordinator, _, _, graph := fixture(t)
	coordinator.WithLimits(domain.WorkflowGraphLimits{MaxTasks: 2, MaxDepth: 12, MaxFanOut: 8})

	request, err := coordinator.CreateTaskRequest(context.Background(), CreateTaskRequestInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1", RequestID: "req-limit",
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		Payload:     json.RawMessage(`{"title":"one too many"}`),
	})
	if err != nil {
		t.Fatalf("CreateTaskRequest: %v", err)
	}
	// The fixture already has 2 tasks and MaxTasks is 2.
	_, err = coordinator.Decide(context.Background(), DecideInput{
		WorkflowID: "workflow-1", RequestID: request.Request.ID, Approve: true,
		DecidedBy: domain.WorkflowTaskRequestDeciderHuman, DecidedByRef: "operator-1",
		ExpectedRevision: request.Request.Revision,
	})
	if !errors.Is(err, domain.ErrCollaborationNotAuthorized) {
		t.Fatalf("err = %v, want the limit refusal", err)
	}
	_ = graph
}

// TestDecideRequiresHumanRefForHumanDecider proves an unattributed human approval is refused.
func TestDecideRequiresHumanRefForHumanDecider(t *testing.T) {
	t.Parallel()
	coordinator, _, _, _ := fixture(t)
	request, err := coordinator.CreateTaskRequest(context.Background(), CreateTaskRequestInput{
		WorkflowID: "workflow-1", AttemptID: "attempt-1", RequestID: "req-ref",
		RequestKind: domain.WorkflowTaskRequestKindIncreaseBudget,
		Payload:     json.RawMessage(`{"tokens":1000}`),
	})
	if err != nil {
		t.Fatalf("CreateTaskRequest: %v", err)
	}
	_, err = coordinator.Decide(context.Background(), DecideInput{
		WorkflowID: "workflow-1", RequestID: request.Request.ID, Approve: true,
		DecidedBy: domain.WorkflowTaskRequestDeciderHuman, DecidedByRef: "  ",
		ExpectedRevision: request.Request.Revision,
	})
	if !errors.Is(err, domain.ErrInvalidWorkflowTaskRequestDecision) {
		t.Fatalf("err = %v, want ErrInvalidWorkflowTaskRequestDecision", err)
	}
}

// TestCanonicalRequestJSONIsStable proves the fingerprint does not depend on key order.
func TestCanonicalRequestJSONIsStable(t *testing.T) {
	t.Parallel()
	first, err := CanonicalRequestJSON(json.RawMessage(`{"b":2,"a":1}`))
	if err != nil {
		t.Fatalf("CanonicalRequestJSON: %v", err)
	}
	second, err := CanonicalRequestJSON(json.RawMessage(`{"a":1,"b":2}`))
	if err != nil {
		t.Fatalf("CanonicalRequestJSON: %v", err)
	}
	if first != second {
		t.Fatalf("canonical forms differ: %q vs %q", first, second)
	}
	if RequestSHA(first) != RequestSHA(second) {
		t.Fatal("the fingerprint differs for equivalent payloads")
	}
	if RequestSHA(first) == RequestSHA(`{"a":2}`) {
		t.Fatal("different payloads produced the same fingerprint")
	}
}

// TestChatOriginNeverClaimsHumanForAgent proves the forwarding mapping.
func TestChatOriginNeverClaimsHumanForAgent(t *testing.T) {
	t.Parallel()
	if domain.WorkflowMessageSenderAgent.ChatOrigin() != domain.MessageOriginAutomation {
		t.Fatal("an agent message must map to automation, never human")
	}
	if domain.WorkflowMessageSenderAgent.AuthoredByUser() {
		t.Fatal("an agent message must never be marked user-authored")
	}
	if !domain.WorkflowMessageSenderHuman.AuthoredByUser() {
		t.Fatal("a human message may be user-authored")
	}
	// The chat vocabulary has no agent origin, which is why this enum exists.
	for _, origin := range []domain.MessageOrigin{
		domain.MessageOriginHuman, domain.MessageOriginAutomation,
		domain.MessageOriginDaemon, domain.MessageOriginProvider,
	} {
		if origin == domain.MessageOrigin("agent") {
			t.Fatal("unreachable")
		}
	}
	if strings.EqualFold(string(domain.MessageOrigin("agent")), string(domain.MessageOriginAutomation)) {
		t.Fatal("unreachable")
	}
}

// TestAIAgentKindRequiresASession proves an agent sender must actually hold a session.
func TestAgentKindRequiresASession(t *testing.T) {
	t.Parallel()
	coordinator, _, bindings, _ := fixture(t)
	bindings.binding.SessionID = nil
	_, err := coordinator.ResolveCaller(context.Background(), "workflow-1", "attempt-1",
		domain.WorkflowMessageSenderAgent)
	if !errors.Is(err, domain.ErrCollaborationNotAuthorized) {
		t.Fatalf("err = %v, want ErrCollaborationNotAuthorized", err)
	}
	// A human caller needs no session.
	if _, err := coordinator.ResolveCaller(context.Background(), "workflow-1", "attempt-1",
		domain.WorkflowMessageSenderHuman); err != nil {
		t.Fatalf("human caller: %v", err)
	}
}
