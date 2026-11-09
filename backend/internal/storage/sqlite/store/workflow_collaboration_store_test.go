package store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

const collabSHA = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

// collaborationFixture creates a workflow with the given tasks so messages and requests have
// legal parents. Each task gets its own attempt so sender coherence can be exercised.
func collaborationFixture(
	t *testing.T,
	s *sqlite.Store,
	workflowID domain.WorkflowID,
	projectID string,
	taskIDs ...domain.WorkflowTaskID,
) map[domain.WorkflowTaskID]domain.WorkflowAttempt {
	t.Helper()
	now := workflowStoreTime()
	seedProject(t, s, projectID)

	tasks := make([]domain.WorkflowTask, 0, len(taskIDs))
	for _, taskID := range taskIDs {
		tasks = append(tasks, workflowStoreTask(taskID, workflowID, 2, now))
	}
	if _, _, err := s.CreateWorkflow(
		context.Background(), workflowStoreRecord(workflowID, now),
		[]domain.WorkflowRepository{bindingWorkflowRepo(workflowID, projectID)},
		tasks, nil, nil,
		"collab-create-"+string(workflowID), strings.Repeat("a", 64),
	); err != nil {
		t.Fatalf("CreateWorkflow(%s): %v", workflowID, err)
	}
	if _, _, err := s.ActivateWorkflow(context.Background(), workflowID, 1,
		"collab-activate-"+string(workflowID), strings.Repeat("b", 64), now.Add(time.Second)); err != nil {
		t.Fatalf("ActivateWorkflow(%s): %v", workflowID, err)
	}

	attempts := make(map[domain.WorkflowTaskID]domain.WorkflowAttempt, len(taskIDs))
	for _, taskID := range taskIDs {
		ready, found, err := s.GetWorkflowTask(context.Background(), workflowID, taskID)
		if err != nil || !found {
			t.Fatalf("GetWorkflowTask(%s): found=%v err=%v", taskID, found, err)
		}
		claimed, created, err := claimWithAdmission(context.Background(), t, s,
			workflowStoreAttempt(domain.WorkflowAttemptID("attempt-"+string(taskID)), workflowID, taskID,
				"worker-1", "lease-"+string(taskID), now.Add(2*time.Second), now.Add(time.Hour)),
			ready.Revision, "collab-claim-"+string(taskID), strings.Repeat("c", 64),
		)
		if err != nil || !created {
			t.Fatalf("ClaimWorkflowTask(%s): created=%v err=%v", taskID, created, err)
		}
		attempts[taskID] = admitCollaborationSession(t, s, claimed, projectID, now.Add(2*time.Second))
	}
	return attempts
}

func collaborationMessage(
	workflowID domain.WorkflowID,
	attempt domain.WorkflowAttempt,
	to *domain.WorkflowTaskID,
	clientMessageID string,
) domain.WorkflowMessage {
	senderTask := attempt.TaskID
	return domain.WorkflowMessage{
		ID:              "message-" + clientMessageID,
		WorkflowID:      workflowID,
		FromTaskID:      attempt.TaskID,
		SenderSessionID: attempt.SessionID,
		ToTaskID:        to,
		SenderKind:      domain.WorkflowMessageSenderAgent,
		SenderTaskID:    &senderTask,
		Kind:            domain.WorkflowMessageKindProgress,
		Body:            "making progress on the parser",
		ClientMessageID: clientMessageID,
		PayloadSHA:      collabSHA,
	}
}

func TestWorkflowMessageCreateIsIdempotentByClientMessage(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	workflowID := domain.WorkflowID("workflow-collab-idem")
	attempt := collaborationFixture(t, s, workflowID, "collab-idem-project", "task-a", "task-b")["task-a"]
	now := workflowStoreTime()
	ctx = sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second))
	target := domain.WorkflowTaskID("task-b")

	message := collaborationMessage(workflowID, attempt, &target, "msg-1")
	first, created, err := s.CreateWorkflowMessage(ctx, message, now.Add(3*time.Second))
	if err != nil || !created {
		t.Fatalf("first create: created=%v err=%v", created, err)
	}
	if first.DeliveryState != domain.WorkflowMessageDeliveryPersisted || first.Revision != 1 {
		t.Fatalf("first = %+v, want persisted at revision 1", first)
	}

	// Replay: same key, same sha. Returns the original, creates nothing, bumps nothing.
	second, created, err := s.CreateWorkflowMessage(ctx, message, now.Add(4*time.Second))
	if err != nil {
		t.Fatalf("replay err = %v", err)
	}
	if created {
		t.Fatal("a replay created a second message")
	}
	if second.ID != first.ID || second.Revision != first.Revision {
		t.Fatalf("replay = %+v, want the original %+v at the same revision", second, first)
	}
	// No child rows were inserted: the messages table has no children, so the proof is that
	// the workflow's task and edge rows are unchanged.
	tasks, err := s.ListWorkflowTasks(ctx, workflowID, "", 100, 0)
	if err != nil || len(tasks) != 2 {
		t.Fatalf("tasks = %d err=%v, want the original 2 (a replay must not insert)", len(tasks), err)
	}

	// Same key, different sha: typed conflict.
	conflicting := message
	conflicting.PayloadSHA = strings.Repeat("f", 64)
	if _, _, err := s.CreateWorkflowMessage(ctx, conflicting, now.Add(5*time.Second)); !errors.Is(err, domain.ErrWorkflowMessageConflict) {
		t.Fatalf("conflicting sha err = %v, want ErrWorkflowMessageConflict", err)
	}
}

// TestWorkflowMessageCrossWorkflowRejected is the required composite-key proof: a task from
// another workflow cannot be referenced, on either side.
func TestWorkflowMessageCrossWorkflowRejected(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowA := domain.WorkflowID("workflow-collab-x-a")
	workflowB := domain.WorkflowID("workflow-collab-x-b")
	attemptA := collaborationFixture(t, s, workflowA, "collab-x-project", "task-a")["task-a"]
	ctx = sqlitetest.ExecutionContext(ctx, t, s, attemptA.ID, now.Add(2*time.Second))
	collaborationFixture(t, s, workflowB, "collab-x-project", "task-foreign")

	// A message in workflow A that names workflow B's task as the target.
	foreign := domain.WorkflowTaskID("task-foreign")
	message := collaborationMessage(workflowA, attemptA, &foreign, "msg-cross")
	_, created, err := s.CreateWorkflowMessage(ctx, message, now.Add(3*time.Second))
	if err == nil {
		t.Fatal("a message targeting another workflow's task was accepted")
	}
	if created {
		t.Fatal("the rejected create reported success")
	}
	if !errors.Is(err, domain.ErrCollaborationCrossWorkflow) {
		t.Fatalf("err = %v, want ErrCollaborationCrossWorkflow (not a raw SQLite string)", err)
	}

	// 人工来源也必须经过 from_task 外键；Agent 的伪造来源会更早被许可门禁拒绝。
	badFrom := collaborationMessage(workflowA, attemptA, nil, "msg-cross-from")
	badFrom.FromTaskID = foreign
	badFrom.SenderKind, badFrom.SenderTaskID = domain.WorkflowMessageSenderHuman, nil
	if _, _, err := s.CreateWorkflowMessage(ctx, badFrom, now.Add(4*time.Second)); !errors.Is(err, domain.ErrCollaborationCrossWorkflow) {
		t.Fatalf("foreign from_task err = %v, want ErrCollaborationCrossWorkflow", err)
	}
}

// TestWorkflowMessageNullTargetStillChecksWorkflow is correction 2's required case: SQLite
// MATCH SIMPLE silently skips a composite key when a part is NULL, so a broadcast (NULL
// to_task_id) with a bogus workflow must still be rejected by the single-column workflow FK.
func TestWorkflowMessageNullTargetStillChecksWorkflow(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-collab-null")
	attempt := collaborationFixture(t, s, workflowID, "collab-null-project", "task-a")["task-a"]
	ctx = sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second))

	// Broadcast: nil target, real workflow. This must succeed.
	broadcast := collaborationMessage(workflowID, attempt, nil, "msg-broadcast")
	if _, created, err := s.CreateWorkflowMessage(ctx, broadcast, now.Add(3*time.Second)); err != nil || !created {
		t.Fatalf("broadcast: created=%v err=%v", created, err)
	}

	// 人工广播没有 Agent 许可前置条件，因此这里仍直接验证单列 workflow_id 外键。
	ghostSender := attempt.TaskID
	ghost := domain.WorkflowMessage{
		ID: "message-ghost", WorkflowID: "workflow-does-not-exist",
		FromTaskID: ghostSender, ToTaskID: nil,
		SenderKind: domain.WorkflowMessageSenderHuman,
		Kind:       domain.WorkflowMessageKindProgress, Body: "ghost",
		ClientMessageID: "msg-ghost", PayloadSHA: collabSHA,
	}
	if _, _, err := s.CreateWorkflowMessage(ctx, ghost, now.Add(4*time.Second)); err == nil {
		t.Fatal("a broadcast with a nonexistent workflow was accepted; the single-column workflow FK is missing")
	}
}

// TestWorkflowMessageQueueDepthAtCap proves correction 9: the cap is 32 and it is enforced by
// the store, inside the insert transaction.
func TestWorkflowMessageQueueDepthAtCap(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-collab-depth")
	attempt := collaborationFixture(t, s, workflowID, "collab-depth-project", "task-a")["task-a"]
	ctx = sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second))

	for i := range domain.MaxOpenWorkflowMessages {
		message := collaborationMessage(workflowID, attempt, nil,
			"msg-depth-"+string(rune('a'+i%26))+string(rune('a'+i/26)))
		if _, created, err := s.CreateWorkflowMessage(
			ctx, message, now.Add(3*time.Second+time.Duration(i)*time.Millisecond),
		); err != nil || !created {
			t.Fatalf("create %d: created=%v err=%v", i, created, err)
		}
	}

	overflow := collaborationMessage(workflowID, attempt, nil, "msg-overflow")
	_, _, err := s.CreateWorkflowMessage(ctx, overflow, now.Add(4*time.Second))
	if !errors.Is(err, domain.ErrWorkflowMessageQueueFull) {
		t.Fatalf("err = %v, want ErrWorkflowMessageQueueFull at the %d cap", err, domain.MaxOpenWorkflowMessages)
	}

	// Settling one message below the cap makes room again, which proves the cap counts OPEN
	// messages rather than all rows.
	stored, found, err := s.GetWorkflowMessageByClientMessage(ctx, workflowID, "msg-depth-aa")
	if err != nil || !found {
		t.Fatalf("get first message: found=%v err=%v", found, err)
	}
	if _, _, err := s.UpdateWorkflowMessageDelivery(
		ctx, workflowID, stored.ID, domain.WorkflowMessageDeliveryForwarded,
		stored.Revision, now.Add(5*time.Second),
	); err != nil {
		t.Fatalf("settle message: %v", err)
	}
	if _, created, err := s.CreateWorkflowMessage(ctx, overflow, now.Add(6*time.Second)); err != nil || !created {
		t.Fatalf("create after settling one: created=%v err=%v", created, err)
	}
}

// TestWorkflowMessageContentLimits proves the body/payload caps and the non-empty rule.
func TestWorkflowMessageContentLimits(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-collab-limits")
	attempt := collaborationFixture(t, s, workflowID, "collab-limits-project", "task-a")["task-a"]
	ctx = sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second))

	tests := []struct {
		name    string
		mutate  func(*domain.WorkflowMessage)
		wantErr bool
	}{
		{"oversized body", func(m *domain.WorkflowMessage) {
			m.Body = strings.Repeat("x", domain.MaxWorkflowMessageBodyBytes+1)
		}, true},
		{"empty body and payload", func(m *domain.WorkflowMessage) {
			m.Body = ""
			m.PayloadRef = ""
		}, true},
		{"payload ref with a path separator", func(m *domain.WorkflowMessage) {
			m.Body = ""
			m.PayloadRef = "../secrets/artifact"
		}, true},
		{"payload ref with a drive colon", func(m *domain.WorkflowMessage) {
			m.Body = ""
			m.PayloadRef = `C:artifact`
		}, true},
		{"oversized payload ref", func(m *domain.WorkflowMessage) {
			m.Body = ""
			m.PayloadRef = strings.Repeat("a", domain.MaxWorkflowMessagePayloadRefBytes+1)
		}, true},
		{"payload only is legal", func(m *domain.WorkflowMessage) {
			m.Body = ""
			m.PayloadRef = "artifact-abc123"
		}, false},
		{"at the body cap is legal", func(m *domain.WorkflowMessage) {
			m.Body = strings.Repeat("x", domain.MaxWorkflowMessageBodyBytes)
		}, false},
	}
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			message := collaborationMessage(workflowID, attempt, nil,
				"msg-limits-"+string(rune('a'+i)))
			tt.mutate(&message)
			_, _, err := s.CreateWorkflowMessage(ctx, message, now.Add(3*time.Second+time.Duration(i)*time.Millisecond))
			if tt.wantErr && err == nil {
				t.Fatal("the constraint accepted content it must reject")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("legal content was rejected: %v", err)
			}
		})
	}
}

// TestWorkflowMessageSenderCoherence proves an agent sender must name its own origin task and a
// non-agent must not name one.
func TestWorkflowMessageSenderCoherence(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-collab-sender")
	attempts := collaborationFixture(t, s, workflowID, "collab-sender-project", "task-a", "task-b")
	attemptA := attempts["task-a"]
	ctx = sqlitetest.ExecutionContext(ctx, t, s, attemptA.ID, now.Add(2*time.Second))

	t.Run("agent naming a different task", func(t *testing.T) {
		message := collaborationMessage(workflowID, attemptA, nil, "msg-sender-1")
		other := domain.WorkflowTaskID("task-b")
		message.SenderTaskID = &other
		if _, _, err := s.CreateWorkflowMessage(ctx, message, now.Add(time.Second)); err == nil {
			t.Fatal("an agent message named a sender task different from its origin")
		}
	})

	t.Run("human naming a task", func(t *testing.T) {
		message := collaborationMessage(workflowID, attemptA, nil, "msg-sender-2")
		message.SenderKind = domain.WorkflowMessageSenderHuman
		senderTask := attemptA.TaskID
		message.SenderTaskID = &senderTask
		if _, _, err := s.CreateWorkflowMessage(ctx, message, now.Add(2*time.Second)); err == nil {
			t.Fatal("a human message named a sender task")
		}
	})

	t.Run("human without a task is legal", func(t *testing.T) {
		message := collaborationMessage(workflowID, attemptA, nil, "msg-sender-3")
		message.SenderKind = domain.WorkflowMessageSenderHuman
		message.SenderTaskID = nil
		if _, created, err := s.CreateWorkflowMessage(ctx, message, now.Add(3*time.Second)); err != nil || !created {
			t.Fatalf("human message: created=%v err=%v", created, err)
		}
	})
}

// TestWorkflowMessageSurvivesSessionDeletion is correction 7 applied.
func TestWorkflowMessageSurvivesSessionDeletion(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	projectID := "collab-session-project"
	workflowID := domain.WorkflowID("workflow-collab-session")
	attempt := collaborationFixture(t, s, workflowID, projectID, "task-a")["task-a"]
	ctx = sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second))

	session := *attempt.SessionID
	rec, found, err := s.GetSession(ctx, session)
	if err != nil || !found {
		t.Fatalf("get session: found=%v err=%v", found, err)
	}
	rec.IsTerminated = true
	if err := s.UpdateSession(ctx, rec); err != nil {
		t.Fatalf("terminate: %v", err)
	}

	message := collaborationMessage(workflowID, attempt, nil, "msg-session")
	message.SenderSessionID = &session
	message.TargetSessionID = &session
	created, _, err := s.CreateWorkflowMessage(ctx, message, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := s.DeleteTerminatedSession(ctx, session); err != nil {
		t.Fatalf("DeleteTerminatedSession was blocked by a message row: %v", err)
	}
	after, found, err := s.GetWorkflowMessage(ctx, workflowID, created.ID)
	if err != nil || !found {
		t.Fatalf("get message after delete: found=%v err=%v", found, err)
	}
	if after.SenderSessionID != nil || after.TargetSessionID != nil {
		t.Fatalf("the message still names a deleted session: sender=%v target=%v",
			after.SenderSessionID, after.TargetSessionID)
	}
	// The body is history and survives.
	if after.Body != message.Body {
		t.Fatalf("body = %q, want the original preserved", after.Body)
	}
}

func TestWorkflowTaskRequestCreateStaysPending(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-collab-request")
	attempt := collaborationFixture(t, s, workflowID, "collab-request-project", "task-a")["task-a"]

	request := domain.WorkflowTaskRequest{
		ID: "request-1", WorkflowID: workflowID, RequesterTaskID: attempt.TaskID,
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		RequestJSON: `{"title":"add tests"}`, RequestSHA: collabSHA, WorkflowRevision: 2,
	}
	// Deliberately try to create it already approved: the store must force pending.
	request.State = domain.WorkflowTaskRequestStateApproved
	created, isNew, err := s.CreateWorkflowTaskRequest(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second)), request, now.Add(3*time.Second))
	if err != nil || !isNew {
		t.Fatalf("create: created=%v err=%v", isNew, err)
	}
	if created.State != domain.WorkflowTaskRequestStatePending {
		t.Fatalf("state = %q, want pending (the creator may not self-approve)", created.State)
	}
	if created.DecidedByKind != nil || created.DecidedAt != nil {
		t.Fatal("a pending request carries a decision")
	}

	// Replay with the same sha returns the original.
	replayed, isNew, err := s.CreateWorkflowTaskRequest(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second)), request, now.Add(4*time.Second))
	if err != nil || isNew {
		t.Fatalf("replay: created=%v err=%v", isNew, err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replay = %+v, want the original", replayed)
	}

	// Different sha under the same id is a conflict.
	changed := request
	changed.RequestSHA = strings.Repeat("e", 64)
	if _, _, err := s.CreateWorkflowTaskRequest(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second)), changed, now.Add(5*time.Second)); !errors.Is(err, domain.ErrWorkflowTaskRequestConflict) {
		t.Fatalf("err = %v, want ErrWorkflowTaskRequestConflict", err)
	}
}

// TestWorkflowTaskRequestDecideIsCASAndPendingOnly proves a decided row cannot be re-decided
// and a stale revision loses.
func TestWorkflowTaskRequestDecideIsCASAndPendingOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-collab-decide")
	attempt := collaborationFixture(t, s, workflowID, "collab-decide-project", "task-a")["task-a"]

	request := domain.WorkflowTaskRequest{
		ID: "request-decide", WorkflowID: workflowID, RequesterTaskID: attempt.TaskID,
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		RequestJSON: `{"title":"add tests"}`, RequestSHA: collabSHA, WorkflowRevision: 2,
	}
	created, _, err := s.CreateWorkflowTaskRequest(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second)), request, now.Add(3*time.Second))
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	decided, applied, err := s.DecideWorkflowTaskRequest(
		ctx, workflowID, created.ID, domain.WorkflowTaskRequestStateApproved,
		domain.WorkflowTaskRequestDeciderHuman, "operator-1", "looks right",
		created.Revision, now.Add(4*time.Second),
	)
	if err != nil || !applied {
		t.Fatalf("decide: applied=%v err=%v", applied, err)
	}
	if decided.State != domain.WorkflowTaskRequestStateApproved {
		t.Fatalf("state = %q, want approved", decided.State)
	}
	if decided.DecidedByKind == nil || *decided.DecidedByKind != domain.WorkflowTaskRequestDeciderHuman {
		t.Fatalf("decided_by_kind = %v, want human", decided.DecidedByKind)
	}
	if decided.DecidedAt == nil {
		t.Fatal("decided_at was not recorded")
	}

	// Re-deciding must fail: the row is no longer pending.
	if _, _, err := s.DecideWorkflowTaskRequest(
		ctx, workflowID, created.ID, domain.WorkflowTaskRequestStateRejected,
		domain.WorkflowTaskRequestDeciderHuman, "operator-2", "changed my mind",
		decided.Revision, now.Add(5*time.Second),
	); err == nil {
		t.Fatal("an already-decided request was re-decided")
	}

	// An agent cannot decide: the decider enum has no agent member, so it is rejected before
	// any SQL.
	other := domain.WorkflowTaskRequest{
		ID: "request-agent", WorkflowID: workflowID, RequesterTaskID: attempt.TaskID,
		RequestKind: domain.WorkflowTaskRequestKindCreateTask,
		RequestJSON: `{}`, RequestSHA: collabSHA, WorkflowRevision: 2,
	}
	otherCreated, _, err := s.CreateWorkflowTaskRequest(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second)), other, now.Add(6*time.Second))
	if err != nil {
		t.Fatalf("create other: %v", err)
	}
	if _, _, err := s.DecideWorkflowTaskRequest(
		ctx, workflowID, otherCreated.ID, domain.WorkflowTaskRequestStateApproved,
		domain.WorkflowTaskRequestDeciderKind("agent"), "task-a", "",
		otherCreated.Revision, now.Add(7*time.Second),
	); !errors.Is(err, domain.ErrCollaborationDeciderNotPermitted) {
		t.Fatalf("agent decider err = %v, want ErrCollaborationDeciderNotPermitted", err)
	}
}

// TestWorkflowTaskRequestStateCoherence proves the pending/decided CHECK both directions.
func TestWorkflowTaskRequestStateCoherence(t *testing.T) {
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-collab-coherence")
	attempt := collaborationFixture(t, s, workflowID, "collab-coherence-project", "task-a")["task-a"]

	// A pending row with a decision attached must fail the CHECK; a decided row without one
	// must fail too. Both are exercised directly through Validate, which mirrors the CHECK.
	pendingWithDecision := domain.WorkflowTaskRequest{
		ID: "r1", WorkflowID: workflowID, RequesterTaskID: attempt.TaskID,
		RequestKind: domain.WorkflowTaskRequestKindCreateTask, RequestJSON: `{}`,
		RequestSHA: collabSHA, State: domain.WorkflowTaskRequestStatePending,
		WorkflowRevision: 2, Revision: 1,
		CreatedAt: now, UpdatedAt: now,
	}
	decider := domain.WorkflowTaskRequestDeciderHuman
	decidedAt := now
	pendingWithDecision.DecidedByKind = &decider
	pendingWithDecision.DecidedAt = &decidedAt
	if err := pendingWithDecision.Validate(); !errors.Is(err, domain.ErrInvalidWorkflowTaskRequestDecision) {
		t.Fatalf("pending-with-decision err = %v, want the decision error", err)
	}

	approvedWithoutDecider := domain.WorkflowTaskRequest{
		ID: "r2", WorkflowID: workflowID, RequesterTaskID: attempt.TaskID,
		RequestKind: domain.WorkflowTaskRequestKindCreateTask, RequestJSON: `{}`,
		RequestSHA: collabSHA, State: domain.WorkflowTaskRequestStateApproved,
		WorkflowRevision: 2, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	if err := approvedWithoutDecider.Validate(); !errors.Is(err, domain.ErrInvalidWorkflowTaskRequestDecision) {
		t.Fatalf("approved-without-decider err = %v, want the decision error", err)
	}
}

// TestWorkflowCollaborationRollbackEmitsNoEvents proves a rolled-back transaction produces zero
// change_log rows for the new tables.
//
// CDC is trigger-based, so an event is written by the same statement as the fact. That means a
// rolled-back transaction must leave nothing behind — if a trigger fired outside the
// transaction, or if any path emitted an event manually, a client would see a message or request
// that does not exist.
func TestWorkflowCollaborationRollbackEmitsNoEvents(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)
	now := workflowStoreTime()
	workflowID := domain.WorkflowID("workflow-collab-rollback")
	attempt := collaborationFixture(t, s, workflowID, "collab-rollback-project", "task-a")["task-a"]
	ctx = sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second))

	baseline := latestSeq(t, s)

	// A message whose insert must fail: the target task belongs to another workflow, so the
	// composite foreign key rejects it and the whole insert — including its trigger — rolls back.
	foreign := domain.WorkflowTaskID("task-foreign")
	message := collaborationMessage(workflowID, attempt, &foreign, "msg-rollback")
	if _, _, err := s.CreateWorkflowMessage(ctx, message, now.Add(time.Second)); err == nil {
		t.Fatal("the cross-workflow message was accepted; the rollback path was not exercised")
	}

	// A task request whose insert must fail for the same reason (a bogus requester task).
	badRequest := domain.WorkflowTaskRequest{
		ID: "request-rollback", WorkflowID: workflowID,
		RequesterTaskID: "task-not-in-this-workflow",
		RequestKind:     domain.WorkflowTaskRequestKindCreateTask,
		RequestJSON:     `{"a":1}`, RequestSHA: collabSHA, WorkflowRevision: 2,
	}
	if _, _, err := s.CreateWorkflowTaskRequest(sqlitetest.ExecutionContext(ctx, t, s, attempt.ID, now.Add(2*time.Second)), badRequest, now.Add(2*time.Second)); err == nil {
		t.Fatal("the cross-workflow request was accepted")
	}

	events := allEventsAfter(t, s, baseline)
	for _, event := range events {
		switch event.Type {
		case "workflow_message_created", "workflow_message_updated", "workflow_task_request_updated":
			t.Fatalf("a rolled-back transaction emitted %q: %s", event.Type, event.Payload)
		}
	}

	// And nothing persisted: a reader must not find a message or request that does not exist.
	if _, found, err := s.GetWorkflowMessageByClientMessage(ctx, workflowID, "msg-rollback"); err != nil || found {
		t.Fatalf("message survived a rolled-back insert: found=%v err=%v", found, err)
	}
	if _, found, err := s.GetWorkflowTaskRequest(ctx, workflowID, "request-rollback"); err != nil || found {
		t.Fatalf("request survived a rolled-back insert: found=%v err=%v", found, err)
	}
}
