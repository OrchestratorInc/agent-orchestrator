package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// Workflow collaboration store (M2-06).
//
// Two properties are enforced here rather than documented:
//
//   - The open-message cap is checked INSIDE the insert transaction. SQLite cannot express a
//     row-count CHECK, so the cap is a store check; evaluating the count in the same
//     transaction as the insert is what stops two concurrent inserts from both passing.
//   - Nothing in this file materializes work. There is no statement that writes
//     workflow_tasks, workflow_edges, or workflow_task_repos, so a task request can be
//     decided but never applied.

// CreateWorkflowMessage stores one collaboration message.
//
// Idempotency is keyed on (workflow_id, clientMessageID): a replay with the same payloadSHA
// returns the existing row with created=false, and a different SHA under the same key is a
// typed conflict. A replay touches nothing, so its child state and revision are untouched.
func (s *Store) CreateWorkflowMessage(
	ctx context.Context,
	message domain.WorkflowMessage,
	now time.Time,
) (domain.WorkflowMessage, bool, error) {
	now = utcTime(now)
	message.DeliveryState = domain.WorkflowMessageDeliveryPersisted
	message.Revision = 1
	message.CreatedAt = now
	message.UpdatedAt = now
	if err := message.Validate(); err != nil {
		return domain.WorkflowMessage{}, false, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowMessage
	created := false
	err := s.inSchedulerTx(ctx, "create workflow message", func(q *gen.Queries) error {
		if message.SenderKind.IsAgent() {
			p, ok := ports.ExecutionPermitFromContext(ctx)
			if !ok || p.WorkflowID != message.WorkflowID || p.TaskID != message.FromTaskID || message.SenderSessionID == nil || p.SessionID != *message.SenderSessionID {
				return domain.ErrAdmissionRequired
			}
			if err := executionPermitFence(ctx, q, p.AttemptID, now, true); err != nil {
				return err
			}
		}
		// Queue depth, evaluated in this transaction so it cannot race with a sibling insert.
		// This is a store check rather than a schema constraint by necessity: SQLite has no
		// row-count CHECK. M2 defines no rate limit — only this depth cap.
		open, err := q.CountOpenWorkflowMessages(ctx, message.WorkflowID)
		if err != nil {
			return err
		}
		if open >= domain.MaxOpenWorkflowMessages {
			return domain.ErrWorkflowMessageQueueFull
		}

		rows, err := q.InsertWorkflowMessage(ctx, gen.InsertWorkflowMessageParams{
			ID: message.ID, WorkflowID: message.WorkflowID,
			FromTaskID: message.FromTaskID, ToTaskID: message.ToTaskID,
			SenderSessionID: message.SenderSessionID, TargetSessionID: message.TargetSessionID,
			SenderKind: message.SenderKind, SenderTaskID: message.SenderTaskID,
			Kind: message.Kind, DeliveryState: domain.WorkflowMessageDeliveryPersisted,
			Body: message.Body, PayloadRef: message.PayloadRef,
			ClientMessageID: message.ClientMessageID, PayloadSha: message.PayloadSHA,
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return mapCollaborationWriteError(err)
		}
		if rows == 0 {
			existing, found, err := s.loadMessageByClientMessage(
				ctx, q, message.WorkflowID, message.ClientMessageID,
			)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrWorkflowMessageConflict
			}
			if existing.PayloadSHA != message.PayloadSHA {
				return domain.ErrWorkflowMessageConflict
			}
			result = existing
			return nil
		}

		stored, found, err := s.loadMessage(ctx, q, message.WorkflowID, message.ID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowMessageNotFound
		}
		result = stored
		created = true
		return nil
	})
	if err != nil {
		return domain.WorkflowMessage{}, false, err
	}
	return result, created, nil
}

// GetWorkflowMessage reads one message.
func (s *Store) GetWorkflowMessage(
	ctx context.Context,
	workflowID domain.WorkflowID,
	id string,
) (domain.WorkflowMessage, bool, error) {
	row, err := s.qr.GetWorkflowMessage(ctx, gen.GetWorkflowMessageParams{WorkflowID: workflowID, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowMessage{}, false, nil
	}
	if err != nil {
		return domain.WorkflowMessage{}, false, fmt.Errorf("get workflow message: %w", err)
	}
	return workflowMessageFromGen(row), true, nil
}

// GetWorkflowMessageByClientMessage is the replay lookup.
func (s *Store) GetWorkflowMessageByClientMessage(
	ctx context.Context,
	workflowID domain.WorkflowID,
	clientMessageID string,
) (domain.WorkflowMessage, bool, error) {
	if strings.TrimSpace(clientMessageID) == "" {
		return domain.WorkflowMessage{}, false, domain.ErrInvalidWorkflowMessage
	}
	row, err := s.qr.GetWorkflowMessageByClientMessage(ctx, gen.GetWorkflowMessageByClientMessageParams{
		WorkflowID: workflowID, ClientMessageID: clientMessageID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowMessage{}, false, nil
	}
	if err != nil {
		return domain.WorkflowMessage{}, false, fmt.Errorf("get workflow message by client id: %w", err)
	}
	return workflowMessageFromGen(row), true, nil
}

// ListWorkflowMessages reads a page of a workflow's messages in creation order.
func (s *Store) ListWorkflowMessages(
	ctx context.Context,
	workflowID domain.WorkflowID,
	limit, offset int64,
) ([]domain.WorkflowMessage, error) {
	rows, err := s.qr.ListWorkflowMessages(ctx, gen.ListWorkflowMessagesParams{
		WorkflowID: workflowID, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow messages: %w", err)
	}
	out := make([]domain.WorkflowMessage, 0, len(rows))
	for _, row := range rows {
		out = append(out, workflowMessageFromGen(row))
	}
	return out, nil
}

// UpdateWorkflowMessageDelivery records a delivery-state change under a revision CAS.
func (s *Store) UpdateWorkflowMessageDelivery(
	ctx context.Context,
	workflowID domain.WorkflowID,
	id string,
	next domain.WorkflowMessageDeliveryState,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowMessage, bool, error) {
	if !next.Valid() {
		return domain.WorkflowMessage{}, false, domain.ErrInvalidWorkflowMessageDelivery
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	applied := false
	err := s.inSchedulerTx(ctx, "update workflow message delivery", func(q *gen.Queries) error {
		message, found, err := s.loadMessage(ctx, q, workflowID, id)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowMessageNotFound
		}
		if message.SenderKind.IsAgent() {
			p, ok := ports.ExecutionPermitFromContext(ctx)
			if !ok || p.WorkflowID != workflowID || p.TaskID != message.FromTaskID {
				return domain.ErrAdmissionRequired
			}
			if err := executionPermitFence(ctx, q, p.AttemptID, now, false); err != nil {
				return err
			}
		}
		rows, err := q.UpdateWorkflowMessageDelivery(ctx, gen.UpdateWorkflowMessageDeliveryParams{
			DeliveryState: next, UpdatedAt: now,
			WorkflowID: workflowID, ID: id, ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return domain.ErrWorkflowMessageConflict
		}
		applied = true
		return nil
	})
	if err != nil {
		return domain.WorkflowMessage{}, false, err
	}
	stored, found, err := s.GetWorkflowMessage(ctx, workflowID, id)
	if err != nil || !found {
		return domain.WorkflowMessage{}, applied, err
	}
	return stored, applied, nil
}

// CreateWorkflowTaskRequest stores one request awaiting a decision.
//
// The state is forced to pending: there is no way to create a request in a decided state, so
// "creator self-approves" is not expressible.
func (s *Store) CreateWorkflowTaskRequest(
	ctx context.Context,
	request domain.WorkflowTaskRequest,
	now time.Time,
) (domain.WorkflowTaskRequest, bool, error) {
	now = utcTime(now)
	request.State = domain.WorkflowTaskRequestStatePending
	request.DecidedByKind = nil
	request.DecidedByRef = ""
	request.DecidedAt = nil
	request.Reason = ""
	request.Revision = 1
	request.CreatedAt = now
	request.UpdatedAt = now
	if err := request.Validate(); err != nil {
		return domain.WorkflowTaskRequest{}, false, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowTaskRequest
	created := false
	err := s.inSchedulerTx(ctx, "create workflow task request", func(q *gen.Queries) error {
		p, ok := ports.ExecutionPermitFromContext(ctx)
		if !ok || p.WorkflowID != request.WorkflowID || p.TaskID != request.RequesterTaskID {
			return domain.ErrAdmissionRequired
		}
		if err := executionPermitFence(ctx, q, p.AttemptID, now, true); err != nil {
			return err
		}
		rows, err := q.InsertWorkflowTaskRequest(ctx, gen.InsertWorkflowTaskRequestParams{
			ID: request.ID, WorkflowID: request.WorkflowID,
			RequesterTaskID: request.RequesterTaskID, RequestKind: request.RequestKind,
			RequestJson: request.RequestJSON, RequestSha: request.RequestSHA,
			State:            domain.WorkflowTaskRequestStatePending,
			WorkflowRevision: request.WorkflowRevision,
			Revision:         1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return mapCollaborationWriteError(err)
		}
		if rows == 0 {
			existing, found, err := s.loadTaskRequest(ctx, q, request.WorkflowID, request.ID)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrWorkflowTaskRequestConflict
			}
			// Same id + same fingerprint is a replay. A different fingerprint under the same
			// id is a conflict, so a request cannot be rewritten after it was raised.
			if existing.RequestSHA != request.RequestSHA {
				return domain.ErrWorkflowTaskRequestConflict
			}
			result = existing
			return nil
		}
		stored, found, err := s.loadTaskRequest(ctx, q, request.WorkflowID, request.ID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowTaskRequestNotFound
		}
		result = stored
		created = true
		return nil
	})
	if err != nil {
		return domain.WorkflowTaskRequest{}, false, err
	}
	return result, created, nil
}

// GetWorkflowTaskRequest reads one request.
func (s *Store) GetWorkflowTaskRequest(
	ctx context.Context,
	workflowID domain.WorkflowID,
	id string,
) (domain.WorkflowTaskRequest, bool, error) {
	row, err := s.qr.GetWorkflowTaskRequest(ctx, gen.GetWorkflowTaskRequestParams{
		WorkflowID: workflowID, ID: id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowTaskRequest{}, false, nil
	}
	if err != nil {
		return domain.WorkflowTaskRequest{}, false, fmt.Errorf("get workflow task request: %w", err)
	}
	return workflowTaskRequestFromGen(row), true, nil
}

// ListWorkflowTaskRequests reads a page of a workflow's requests.
func (s *Store) ListWorkflowTaskRequests(
	ctx context.Context,
	workflowID domain.WorkflowID,
	limit, offset int64,
) ([]domain.WorkflowTaskRequest, error) {
	rows, err := s.qr.ListWorkflowTaskRequests(ctx, gen.ListWorkflowTaskRequestsParams{
		WorkflowID: workflowID, PageLimit: limit, PageOffset: offset,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow task requests: %w", err)
	}
	out := make([]domain.WorkflowTaskRequest, 0, len(rows))
	for _, row := range rows {
		out = append(out, workflowTaskRequestFromGen(row))
	}
	return out, nil
}

// DecideWorkflowTaskRequest records an owner decision under a revision and state CAS.
//
// It writes only the request row. No statement here touches workflow_tasks, workflow_edges,
// or workflow_task_repos, so an approval cannot materialize work even if a caller wanted it
// to.
func (s *Store) DecideWorkflowTaskRequest(
	ctx context.Context,
	workflowID domain.WorkflowID,
	id string,
	next domain.WorkflowTaskRequestState,
	decidedBy domain.WorkflowTaskRequestDeciderKind,
	decidedByRef string,
	reason string,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowTaskRequest, bool, error) {
	if !next.Decided() {
		return domain.WorkflowTaskRequest{}, false, domain.ErrInvalidWorkflowTaskRequestState
	}
	if !decidedBy.Valid() {
		// An agent is not representable as a decider, so this refuses the only forbidden
		// case structurally.
		return domain.WorkflowTaskRequest{}, false, domain.ErrCollaborationDeciderNotPermitted
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	applied := false
	err := s.inTx(ctx, "decide workflow task request", func(q *gen.Queries) error {
		rows, err := q.DecideWorkflowTaskRequest(ctx, gen.DecideWorkflowTaskRequestParams{
			NextState: next, DecidedByKind: &decidedBy, DecidedByRef: decidedByRef,
			DecidedAt: sql.NullTime{Time: now, Valid: true}, Reason: reason, UpdatedAt: now,
			WorkflowID: workflowID, ID: id,
			ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return domain.ErrWorkflowTaskRequestConflict
		}
		applied = true
		return nil
	})
	if err != nil {
		return domain.WorkflowTaskRequest{}, false, err
	}
	stored, found, err := s.GetWorkflowTaskRequest(ctx, workflowID, id)
	if err != nil || !found {
		return domain.WorkflowTaskRequest{}, applied, err
	}
	return stored, applied, nil
}

// CountPendingWorkflowTaskRequests reports a task's undecided request count, so the service
// can bound how many a single agent may accumulate.
func (s *Store) CountPendingWorkflowTaskRequests(
	ctx context.Context,
	workflowID domain.WorkflowID,
	requesterTaskID domain.WorkflowTaskID,
) (int64, error) {
	count, err := s.qr.CountPendingWorkflowTaskRequests(ctx, gen.CountPendingWorkflowTaskRequestsParams{
		WorkflowID: workflowID, RequesterTaskID: requesterTaskID,
	})
	if err != nil {
		return 0, fmt.Errorf("count pending workflow task requests: %w", err)
	}
	return count, nil
}

func (s *Store) loadMessage(
	ctx context.Context,
	q *gen.Queries,
	workflowID domain.WorkflowID,
	id string,
) (domain.WorkflowMessage, bool, error) {
	row, err := q.GetWorkflowMessage(ctx, gen.GetWorkflowMessageParams{WorkflowID: workflowID, ID: id})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowMessage{}, false, nil
	}
	if err != nil {
		return domain.WorkflowMessage{}, false, err
	}
	return workflowMessageFromGen(row), true, nil
}

func (s *Store) loadMessageByClientMessage(
	ctx context.Context,
	q *gen.Queries,
	workflowID domain.WorkflowID,
	clientMessageID string,
) (domain.WorkflowMessage, bool, error) {
	row, err := q.GetWorkflowMessageByClientMessage(ctx, gen.GetWorkflowMessageByClientMessageParams{
		WorkflowID: workflowID, ClientMessageID: clientMessageID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowMessage{}, false, nil
	}
	if err != nil {
		return domain.WorkflowMessage{}, false, err
	}
	return workflowMessageFromGen(row), true, nil
}

func (s *Store) loadTaskRequest(
	ctx context.Context,
	q *gen.Queries,
	workflowID domain.WorkflowID,
	id string,
) (domain.WorkflowTaskRequest, bool, error) {
	row, err := q.GetWorkflowTaskRequest(ctx, gen.GetWorkflowTaskRequestParams{
		WorkflowID: workflowID, ID: id,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowTaskRequest{}, false, nil
	}
	if err != nil {
		return domain.WorkflowTaskRequest{}, false, err
	}
	return workflowTaskRequestFromGen(row), true, nil
}

// mapCollaborationWriteError translates constraint failures with a stable meaning. A caller
// must never match a SQLite message string.
func mapCollaborationWriteError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "workflow_messages.workflow_id, workflow_messages.client_message_id"),
		strings.Contains(message, "UNIQUE constraint failed: workflow_messages"):
		return domain.ErrWorkflowMessageConflict
	case strings.Contains(message, "workflow_task_requests.workflow_id, workflow_task_requests.id"):
		return domain.ErrWorkflowTaskRequestConflict
	case strings.Contains(message, "FOREIGN KEY constraint failed"):
		// Every foreign key in these two tables is a workflow-scoped reference, so a foreign
		// key failure here means the referenced task is not part of this workflow.
		return domain.ErrCollaborationCrossWorkflow
	}
	return err
}

func workflowMessageFromGen(row gen.WorkflowMessage) domain.WorkflowMessage {
	return domain.WorkflowMessage{
		ID: row.ID, WorkflowID: row.WorkflowID,
		FromTaskID: row.FromTaskID, ToTaskID: row.ToTaskID,
		SenderSessionID: row.SenderSessionID, TargetSessionID: row.TargetSessionID,
		SenderKind: row.SenderKind, SenderTaskID: row.SenderTaskID,
		Kind: row.Kind, DeliveryState: row.DeliveryState,
		Body: row.Body, PayloadRef: row.PayloadRef,
		ClientMessageID: row.ClientMessageID, PayloadSHA: row.PayloadSha,
		Revision: row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func workflowTaskRequestFromGen(row gen.WorkflowTaskRequest) domain.WorkflowTaskRequest {
	return domain.WorkflowTaskRequest{
		ID: row.ID, WorkflowID: row.WorkflowID,
		RequesterTaskID: row.RequesterTaskID, RequestKind: row.RequestKind,
		RequestJSON: row.RequestJson, RequestSHA: row.RequestSha, State: row.State,
		DecidedByKind: row.DecidedByKind, DecidedByRef: row.DecidedByRef,
		DecidedAt: nullTimeToTimePtr(row.DecidedAt), Reason: row.Reason,
		WorkflowRevision: row.WorkflowRevision,
		Revision:         row.Revision, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}
