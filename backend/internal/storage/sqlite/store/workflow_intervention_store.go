package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// Workflow intervention store (M2-05).
//
// One invariant drives every method here: a caller must never be able to record a receipt
// state that the chat service did not prove. ApplyWorkflowInterventionReceipt therefore
// takes the chat evidence as an argument and refuses a state the evidence does not license,
// rather than trusting the caller's claim.
//
// The same writeMu + inTx discipline as the rest of the store applies: SQLite has one
// writer connection, and the idempotency lookup and the insert must not interleave.

// CreateWorkflowIntervention records a new intervention request.
//
// Idempotency is keyed on (workflow_id, clientMessageID): a replay with the same
// operationSHA returns the existing row with created=false, and a different SHA under the
// same key is a typed conflict. Because a Stop is an intervention, that rule is what
// prevents a retried Stop from issuing a second interrupt.
func (s *Store) CreateWorkflowIntervention(
	ctx context.Context,
	intervention domain.WorkflowIntervention,
	now time.Time,
) (domain.WorkflowIntervention, bool, error) {
	now = utcTime(now)
	intervention.State = domain.WorkflowInterventionStateRequested
	intervention.Revision = 1
	intervention.CreatedAt = now
	intervention.UpdatedAt = now
	intervention.SettledAt = nil
	if err := intervention.Validate(); err != nil {
		return domain.WorkflowIntervention{}, false, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowIntervention
	created := false
	err := s.inTx(ctx, "create workflow intervention", func(q *gen.Queries) error {
		rows, err := q.InsertWorkflowIntervention(ctx, gen.InsertWorkflowInterventionParams{
			ID: intervention.ID, WorkflowID: intervention.WorkflowID, TaskID: intervention.TaskID,
			AttemptID: intervention.AttemptID, Mode: intervention.Mode,
			State:                domain.WorkflowInterventionStateRequested,
			SessionID:            intervention.SessionID,
			ControllerGeneration: intervention.ControllerGeneration,
			ClientMessageID:      intervention.ClientMessageID,
			OperationSha:         intervention.OperationSHA,
			TargetProviderTurnID: intervention.TargetProviderTurnID,
			ChatTurnID:           "", ProviderTurnID: "",
			ReasonCode: "", QueueAvailable: false,
			Revision: 1, CreatedAt: now, UpdatedAt: now, SettledAt: sql.NullTime{},
		})
		if err != nil {
			return mapInterventionWriteError(err)
		}
		if rows == 0 {
			existing, found, err := s.loadInterventionByClientMessage(
				ctx, q, intervention.WorkflowID, intervention.ClientMessageID,
			)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrInterventionOperationConflict
			}
			if existing.OperationSHA != intervention.OperationSHA {
				// Same key, different request. Retrying the operation would be a second
				// side effect against a request the caller changed underneath us.
				return domain.ErrInterventionOperationConflict
			}
			result = existing
			return nil
		}

		stored, found, err := s.loadIntervention(ctx, q, intervention.ID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrInterventionNotFound
		}
		result = stored
		created = true
		return nil
	})
	if err != nil {
		return domain.WorkflowIntervention{}, false, err
	}
	return result, created, nil
}

// GetWorkflowIntervention reads one intervention by id.
func (s *Store) GetWorkflowIntervention(
	ctx context.Context,
	id string,
) (domain.WorkflowIntervention, bool, error) {
	row, err := s.qr.GetWorkflowIntervention(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowIntervention{}, false, nil
	}
	if err != nil {
		return domain.WorkflowIntervention{}, false, fmt.Errorf("get workflow intervention: %w", err)
	}
	return workflowInterventionFromGen(row), true, nil
}

// GetWorkflowInterventionByClientMessage reads the intervention one client message id
// names, which is the replay lookup a retried Steer or Stop performs.
func (s *Store) GetWorkflowInterventionByClientMessage(
	ctx context.Context,
	workflowID domain.WorkflowID,
	clientMessageID string,
) (domain.WorkflowIntervention, bool, error) {
	if strings.TrimSpace(clientMessageID) == "" {
		return domain.WorkflowIntervention{}, false, domain.ErrInvalidIntervention
	}
	row, err := s.qr.GetWorkflowInterventionByClientMessage(ctx, gen.GetWorkflowInterventionByClientMessageParams{
		WorkflowID: workflowID, ClientMessageID: clientMessageID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowIntervention{}, false, nil
	}
	if err != nil {
		return domain.WorkflowIntervention{}, false,
			fmt.Errorf("get workflow intervention by client message: %w", err)
	}
	return workflowInterventionFromGen(row), true, nil
}

// ApplyWorkflowInterventionReceipt records a new state for an intervention.
//
// The chat fact is the licence: `fact.Licenses(next)` must hold before the write, so
// "applied" cannot be written without a provider turn id and "delivered" cannot be written
// without a running turn or a provider turn id. The row's own controller generation is
// checked against expectedGeneration so a receipt from a replaced controller cannot settle
// a newer intervention.
func (s *Store) ApplyWorkflowInterventionReceipt(
	ctx context.Context,
	id string,
	next domain.WorkflowInterventionState,
	fact domain.InterventionChatFact,
	reasonCode string,
	queueAvailable bool,
	expectedGeneration string,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowIntervention, bool, error) {
	if !next.Valid() {
		return domain.WorkflowIntervention{}, false, domain.ErrInvalidInterventionState
	}
	if next.RequiresChatFact() && !fact.Licenses(next) {
		// The runtime half of "no fabricated applied": the state is refused because the
		// evidence for it is absent, not because the transition is illegal.
		return domain.WorkflowIntervention{}, false, domain.ErrInterventionEvidenceMissing
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowIntervention
	applied := false
	err := s.inTx(ctx, "apply workflow intervention receipt", func(q *gen.Queries) error {
		current, found, err := s.loadIntervention(ctx, q, id)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrInterventionNotFound
		}
		if current.Revision != expectedRevision {
			return domain.ErrInterventionRevisionConflict
		}
		if expectedGeneration != "" && current.ControllerGeneration != expectedGeneration {
			return domain.ErrInterventionStaleGeneration
		}
		if err := current.Mode.ValidateTransition(current.State, next); err != nil {
			return err
		}

		settledAt := sql.NullTime{}
		if next.Terminal() {
			settledAt = sql.NullTime{Time: now, Valid: true}
		}
		rows, err := q.UpdateWorkflowInterventionState(ctx, gen.UpdateWorkflowInterventionStateParams{
			NextState: next,
			// Chat identifiers come only from the fact, so the stored row cannot claim a
			// turn the chat service never reported.
			ChatTurnID:       fact.ChatTurnID,
			ProviderTurnID:   fact.ProviderTurnID,
			ReasonCode:       reasonCode,
			QueueAvailable:   queueAvailable,
			SettledAt:        settledAt,
			UpdatedAt:        now,
			ID:               id,
			ExpectedRevision: expectedRevision,
			ExpectedState:    current.State,
		})
		if err != nil {
			return mapInterventionWriteError(err)
		}
		if rows == 0 {
			return domain.ErrInterventionRevisionConflict
		}

		stored, found, err := s.loadIntervention(ctx, q, id)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrInterventionNotFound
		}
		result = stored
		applied = true
		return nil
	})
	if err != nil {
		return domain.WorkflowIntervention{}, false, err
	}
	return result, applied, nil
}

// ListWorkflowInterventionsForAttempt reads one attempt's interventions in request order.
func (s *Store) ListWorkflowInterventionsForAttempt(
	ctx context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) ([]domain.WorkflowIntervention, error) {
	rows, err := s.qr.ListWorkflowInterventionsForAttempt(ctx, gen.ListWorkflowInterventionsForAttemptParams{
		WorkflowID: workflowID, TaskID: taskID,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow interventions: %w", err)
	}
	out := make([]domain.WorkflowIntervention, 0, len(rows))
	for _, row := range rows {
		out = append(out, workflowInterventionFromGen(row))
	}
	return out, nil
}

// CountOpenStopInterventions reports how many stops for one task have not settled. The
// intervention service uses it to refuse a second interrupt while the first is unknown.
func (s *Store) CountOpenStopInterventions(
	ctx context.Context,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) (int64, error) {
	count, err := s.qr.CountOpenStopInterventions(ctx, gen.CountOpenStopInterventionsParams{
		WorkflowID: workflowID, TaskID: taskID,
	})
	if err != nil {
		return 0, fmt.Errorf("count open stop interventions: %w", err)
	}
	return count, nil
}

// CreateWorkflowInterventionIfNoOpenStop records a stop intervention only when no other
// stop for the same task is still open, in one transaction.
//
// created reports the insert; blocked reports that an open stop already existed (in which
// case existing is that row and nothing was written). Both can be false only on error,
// which is the same-key replay: the caller already recorded this exact request.
//
// The count and the insert must not interleave, otherwise two concurrent stops with
// different client message ids both observe "no open stop" and both issue an interrupt.
// SQLite has one writer connection guarded by writeMu, so holding it across a single inTx
// is the atomic unit this store already relies on elsewhere. No schema change is needed.
//
// "Open" deliberately includes state `requested`, unlike CountOpenStopInterventions. A stop
// row is created at `requested` and only advanced to `stop_requested` by the caller's next
// write, so counting only the settled-in-flight states would leave exactly the window this
// method exists to close: every racing caller would see an unstarted peer as "no stop".
func (s *Store) CreateWorkflowInterventionIfNoOpenStop(
	ctx context.Context,
	intervention domain.WorkflowIntervention,
	now time.Time,
) (stored domain.WorkflowIntervention, created bool, blocked bool, err error) {
	if intervention.Mode != domain.WorkflowInterventionModeStop {
		return domain.WorkflowIntervention{}, false, false,
			fmt.Errorf("%w: only a stop is guarded by the open-stop rule", domain.ErrInvalidInterventionMode)
	}
	now = utcTime(now)
	intervention.State = domain.WorkflowInterventionStateRequested
	intervention.Revision = 1
	intervention.CreatedAt = now
	intervention.UpdatedAt = now
	intervention.SettledAt = nil
	if err := intervention.Validate(); err != nil {
		return domain.WorkflowIntervention{}, false, false, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	err = s.inTx(ctx, "create workflow intervention if no open stop", func(q *gen.Queries) error {
		// The same-key replay is resolved first: it is not a competing stop, and reporting
		// it as blocked would make a retry look like a refusal.
		replayed, found, err := s.loadInterventionByClientMessage(
			ctx, q, intervention.WorkflowID, intervention.ClientMessageID,
		)
		if err != nil {
			return err
		}
		if found {
			if replayed.OperationSHA != intervention.OperationSHA {
				return domain.ErrInterventionOperationConflict
			}
			stored = replayed
			return nil
		}

		open, found, err := firstOpenStop(ctx, q, intervention.WorkflowID, intervention.TaskID)
		if err != nil {
			return err
		}
		if found {
			stored = open
			blocked = true
			return nil
		}

		rows, err := q.InsertWorkflowIntervention(ctx, gen.InsertWorkflowInterventionParams{
			ID: intervention.ID, WorkflowID: intervention.WorkflowID, TaskID: intervention.TaskID,
			AttemptID: intervention.AttemptID, Mode: intervention.Mode,
			State:                domain.WorkflowInterventionStateRequested,
			SessionID:            intervention.SessionID,
			ControllerGeneration: intervention.ControllerGeneration,
			ClientMessageID:      intervention.ClientMessageID,
			OperationSha:         intervention.OperationSHA,
			TargetProviderTurnID: intervention.TargetProviderTurnID,
			ChatTurnID:           "", ProviderTurnID: "",
			ReasonCode: "", QueueAvailable: false,
			Revision: 1, CreatedAt: now, UpdatedAt: now, SettledAt: sql.NullTime{},
		})
		if err != nil {
			return mapInterventionWriteError(err)
		}
		if rows == 0 {
			// The unique key held a row after all. Because the transaction just proved the
			// key absent, this means the constraint is the only thing that changed: report
			// the typed conflict rather than inventing a state.
			return domain.ErrInterventionOperationConflict
		}

		inserted, found, err := s.loadIntervention(ctx, q, intervention.ID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrInterventionNotFound
		}
		stored = inserted
		created = true
		return nil
	})
	if err != nil {
		return domain.WorkflowIntervention{}, false, false, err
	}
	return stored, created, blocked, nil
}

// firstOpenStop returns the earliest stop for one task that has not settled.
//
// `requested` counts as open on purpose; see CreateWorkflowInterventionIfNoOpenStop. A stop
// in a terminal state (confirmed, rejected, cancelled) is history and blocks nothing.
func firstOpenStop(
	ctx context.Context,
	q *gen.Queries,
	workflowID domain.WorkflowID,
	taskID domain.WorkflowTaskID,
) (domain.WorkflowIntervention, bool, error) {
	rows, err := q.ListWorkflowInterventionsForAttempt(ctx, gen.ListWorkflowInterventionsForAttemptParams{
		WorkflowID: workflowID, TaskID: taskID,
	})
	if err != nil {
		return domain.WorkflowIntervention{}, false, err
	}
	for _, row := range rows {
		if row.Mode != domain.WorkflowInterventionModeStop {
			continue
		}
		switch row.State {
		case domain.WorkflowInterventionStateRequested,
			domain.WorkflowInterventionStateStopRequested,
			domain.WorkflowInterventionStateStopUnknown:
			return workflowInterventionFromGen(row), true, nil
		}
	}
	return domain.WorkflowIntervention{}, false, nil
}

func (s *Store) loadIntervention(
	ctx context.Context,
	q *gen.Queries,
	id string,
) (domain.WorkflowIntervention, bool, error) {
	row, err := q.GetWorkflowIntervention(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowIntervention{}, false, nil
	}
	if err != nil {
		return domain.WorkflowIntervention{}, false, err
	}
	return workflowInterventionFromGen(row), true, nil
}

func (s *Store) loadInterventionByClientMessage(
	ctx context.Context,
	q *gen.Queries,
	workflowID domain.WorkflowID,
	clientMessageID string,
) (domain.WorkflowIntervention, bool, error) {
	row, err := q.GetWorkflowInterventionByClientMessage(ctx, gen.GetWorkflowInterventionByClientMessageParams{
		WorkflowID: workflowID, ClientMessageID: clientMessageID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowIntervention{}, false, nil
	}
	if err != nil {
		return domain.WorkflowIntervention{}, false, err
	}
	return workflowInterventionFromGen(row), true, nil
}

// mapInterventionWriteError translates constraint failures with a stable meaning. A caller
// must never match a SQLite message string.
func mapInterventionWriteError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "workflow_interventions.workflow_id, workflow_interventions.client_message_id"),
		strings.Contains(message, "UNIQUE constraint failed: workflow_interventions"):
		return domain.ErrInterventionOperationConflict
	}
	return err
}

// workflowInterventionFromGen converts one generated row field-explicitly, so adding a
// column is a compile error rather than a silent omission.
func workflowInterventionFromGen(row gen.WorkflowIntervention) domain.WorkflowIntervention {
	return domain.WorkflowIntervention{
		ID:                   row.ID,
		WorkflowID:           row.WorkflowID,
		TaskID:               row.TaskID,
		AttemptID:            row.AttemptID,
		Mode:                 row.Mode,
		State:                row.State,
		SessionID:            row.SessionID,
		ControllerGeneration: row.ControllerGeneration,
		ClientMessageID:      row.ClientMessageID,
		OperationSHA:         row.OperationSha,
		TargetProviderTurnID: row.TargetProviderTurnID,
		ChatTurnID:           row.ChatTurnID,
		ProviderTurnID:       row.ProviderTurnID,
		ReasonCode:           row.ReasonCode,
		QueueAvailable:       row.QueueAvailable,
		Revision:             row.Revision,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
		SettledAt:            nullTimeToTimePtr(row.SettledAt),
	}
}
