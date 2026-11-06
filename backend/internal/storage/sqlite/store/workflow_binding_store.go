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

// Attempt→session binding store (M2).
//
// Every write here takes s.writeMu and runs inside s.inTx, the same discipline as
// workflow_lifecycle_store.go: SQLite has one writer connection, and the binding
// store additionally interleaves with workflow_attempts in the same transaction, so
// an unsynchronized read-modify-write could split the two session_id columns.
//
// No method in this file reads or writes workflow_attempts.lease_token. A binding is
// safe to read for any authorized caller; the lease capability stays where exactly
// one response may return it.

// CreateWorkflowAttemptBinding persists the intent to bind an attempt to a session.
//
// Idempotency is keyed on spawnRequestID: a replay returns the existing binding with
// created=false, and a different spawnHash under the same request id is a typed
// conflict rather than a second spawn. This mirrors the workflow idempotency ledger's
// rule that the fingerprint, not the key alone, decides whether two requests are the
// same request.
//
// The repository expectations are written in the same transaction as the intent, so a
// binding can never exist without the base SHAs it was created against.
func (s *Store) CreateWorkflowAttemptBinding(
	ctx context.Context,
	binding domain.WorkflowAttemptBinding,
	repositories []domain.WorkflowAttemptRepoBinding,
	now time.Time,
) (domain.WorkflowAttemptBinding, bool, error) {
	now = utcTime(now)
	binding.State = domain.WorkflowBindingStateIntentPersisted
	binding.Revision = 1
	binding.CreatedAt = now
	binding.UpdatedAt = now
	binding.SessionID = nil
	binding.BoundAt = nil
	if err := binding.Validate(); err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowAttemptBinding
	created := false
	err := s.inSchedulerTx(ctx, "create workflow attempt binding", func(q *gen.Queries) error {
		if err := executionPermitFence(ctx, q, binding.AttemptID, now, true); err != nil {
			return err
		}
		rows, err := q.InsertWorkflowAttemptBinding(ctx, gen.InsertWorkflowAttemptBindingParams{
			AttemptID: binding.AttemptID, WorkflowID: binding.WorkflowID, TaskID: binding.TaskID,
			SpawnRequestID: binding.SpawnRequestID, SpawnHash: binding.SpawnHash,
			State: domain.WorkflowBindingStateIntentPersisted, SessionID: nil,
			ControllerGeneration: "", BoundAt: sql.NullTime{}, FailureCode: "",
			Revision: 1, CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			// Either this request id is already recorded, or the attempt already has
			// a binding. Distinguish the two so a caller can tell a replay from a
			// genuine conflict.
			existing, found, err := s.loadBindingBySpawnRequest(ctx, q, binding.SpawnRequestID)
			if err != nil {
				return err
			}
			if !found {
				return domain.ErrWorkflowBindingSpawningRequestConflict
			}
			if existing.SpawnHash != binding.SpawnHash || existing.AttemptID != binding.AttemptID {
				return domain.ErrWorkflowBindingSpawningRequestConflict
			}
			result = existing
			return nil
		}

		for i := range repositories {
			repo := repositories[i]
			repo.AttemptID = binding.AttemptID
			repo.WorkflowID = binding.WorkflowID
			repo.Revision = 1
			repo.UpdatedAt = now
			if repo.State == "" {
				repo.State = domain.WorkflowRepoBindingStatePending
			}
			if err := repo.Validate(); err != nil {
				return err
			}
			if _, err := q.InsertWorkflowAttemptRepoBinding(ctx, gen.InsertWorkflowAttemptRepoBindingParams{
				AttemptID: repo.AttemptID, WorkflowID: repo.WorkflowID, RepoKey: repo.RepoKey,
				ProjectID: repo.ProjectID, ExpectedBaseSha: repo.ExpectedBaseSHA,
				SessionWorktreeRef: repo.SessionWorktreeRef, ObservedHeadSha: repo.ObservedHeadSHA,
				State: repo.State, Revision: 1, UpdatedAt: now,
			}); err != nil {
				return mapRepoBindingWriteError(err)
			}
		}

		stored, found, err := s.loadBinding(ctx, q, binding.AttemptID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowBindingNotFound
		}
		result = stored
		created = true
		return nil
	})
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}
	return result, created, nil
}

// GetWorkflowAttemptBinding reads one binding by attempt id.
func (s *Store) GetWorkflowAttemptBinding(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, bool, error) {
	row, err := s.qr.GetWorkflowAttemptBinding(ctx, attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowAttemptBinding{}, false, nil
	}
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false, fmt.Errorf("get workflow attempt binding: %w", err)
	}
	return workflowAttemptBindingFromGen(row), true, nil
}

// GetWorkflowAttemptBindingBySpawnRequest reads one binding by its stable spawn
// request id, which is how a retried Bind finds the original without spawning again.
func (s *Store) GetWorkflowAttemptBindingBySpawnRequest(
	ctx context.Context,
	spawnRequestID string,
) (domain.WorkflowAttemptBinding, bool, error) {
	if strings.TrimSpace(spawnRequestID) == "" {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrInvalidWorkflowBinding
	}
	row, err := s.qr.GetWorkflowAttemptBindingBySpawnRequest(ctx, spawnRequestID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowAttemptBinding{}, false, nil
	}
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false,
			fmt.Errorf("get workflow attempt binding by spawn request: %w", err)
	}
	return workflowAttemptBindingFromGen(row), true, nil
}

// HasWorkflowSessionOwnership 包括已接受但尚未 Bind 的启动，供旧 PR 入口闭锁 Workflow 写入。
func (s *Store) HasWorkflowSessionOwnership(ctx context.Context, sessionID domain.SessionID) (bool, error) {
	bound, err := s.qr.HasWorkflowSessionOwnership(ctx, &sessionID)
	return !bound.Valid || bound.Bool, err
}

// GetWorkflowAttemptBindingBySession reads one binding by the session it bound.
//
// It is the session-side lookup a RESUMED session uses to recover which attempt it
// belongs to: a resume has no spawn request id in hand, and the session row does not
// carry the attempt. A session is bound to at most one attempt, so this cannot fan
// out; an absent binding (an ordinary, non-workflow session) is found=false and no
// error.
func (s *Store) GetWorkflowAttemptBindingBySession(
	ctx context.Context,
	sessionID domain.SessionID,
) (domain.WorkflowAttemptBinding, bool, error) {
	if strings.TrimSpace(string(sessionID)) == "" {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrInvalidWorkflowBinding
	}
	key := sessionID
	row, err := s.qr.GetWorkflowAttemptBindingBySession(ctx, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowAttemptBinding{}, false, nil
	}
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false,
			fmt.Errorf("get workflow attempt binding by session: %w", err)
	}
	return workflowAttemptBindingFromGen(row), true, nil
}

// BindWorkflowAttemptSession binds a session to an attempt and mirrors the session
// onto workflow_attempts in the same transaction, so the two columns can never
// disagree.
//
// It refuses to bind when:
//   - the binding is not in a state that may acquire a session,
//   - the expected revision does not match (another binder won),
//   - the session already belongs to another binding, or
//   - workflow_attempts.session_id is already set.
//
// The transition is validated against the domain graph before any SQL runs, so an
// illegal move is a typed domain error rather than a silently accepted write.
func (s *Store) BindWorkflowAttemptSession(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	sessionID domain.SessionID,
	controllerGeneration string,
	target domain.WorkflowBindingState,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowAttemptBinding, bool, error) {
	if strings.TrimSpace(string(sessionID)) == "" || strings.TrimSpace(controllerGeneration) == "" {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrInvalidWorkflowBindingSession
	}
	if !target.IsSessionAcquisitionTarget() {
		// Only provisioning or ready may be the landing state. Anything else — running
		// above all — must come from evidence about a live provider, never from a bind.
		return domain.WorkflowAttemptBinding{}, false, domain.ErrInvalidWorkflowBindingTransition
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowAttemptBinding
	applied := false
	err := s.inSchedulerTx(ctx, "bind workflow attempt session", func(q *gen.Queries) error {
		if err := executionPermitFence(ctx, q, attemptID, now, false); err != nil {
			return err
		}
		current, found, err := s.loadBinding(ctx, q, attemptID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowBindingNotFound
		}
		if current.Revision != expectedRevision {
			return domain.ErrWorkflowBindingRevisionConflict
		}
		// The bind may land on session_provisioning or bound_ready, never running:
		// provisioning is not proof that the provider started, and M2 must not promote
		// an attempt on the strength of a session row alone.
		if err := current.State.ValidateTransition(target); err != nil {
			return err
		}
		if current.SessionID != nil && *current.SessionID != sessionID {
			return domain.ErrWorkflowBindingSessionTaken
		}

		// One session, one binding. The UNIQUE constraint enforces this too; the
		// explicit count produces the stable typed error instead of a raw SQLite
		// message, and it runs inside the transaction so the check cannot race.
		taken, err := q.CountWorkflowAttemptBindingsForSession(ctx, &sessionID)
		if err != nil {
			return err
		}
		if taken > 0 {
			return domain.ErrWorkflowBindingSessionTaken
		}

		rows, err := q.BindWorkflowAttemptSession(ctx, gen.BindWorkflowAttemptSessionParams{
			SessionID: &sessionID, ControllerGeneration: controllerGeneration,
			BoundAt:          sql.NullTime{Time: now, Valid: true},
			NextState:        target,
			UpdatedAt:        now,
			AttemptID:        attemptID,
			ExpectedRevision: expectedRevision,
			ExpectedState:    current.State,
		})
		if err != nil {
			return mapBindingWriteError(err)
		}
		if rows == 0 {
			return domain.ErrWorkflowBindingRevisionConflict
		}

		// The M1 attempt row must agree. Only a null session_id may be written, so a
		// second attempt at the same bind cannot overwrite an existing truth.
		attemptRows, err := q.SetWorkflowAttemptSession(ctx, gen.SetWorkflowAttemptSessionParams{
			SessionID: &sessionID, UpdatedAt: now,
			ID: attemptID, WorkflowID: current.WorkflowID, TaskID: current.TaskID,
		})
		if err != nil {
			return mapBindingWriteError(err)
		}
		if attemptRows == 0 {
			// Either the attempt is not the one this binding names, or it already has
			// a session. Both must abort the whole transaction: a binding that names a
			// session the attempt does not share is exactly the divergence this design
			// forbids.
			return domain.ErrWorkflowBindingAttemptAlreadyBound
		}
		permit, err := loadExecutionPermit(ctx, q, attemptID)
		if err != nil {
			return err
		}
		if err := bindPathReservations(ctx, q, attemptID, sessionID, controllerGeneration, permit.Fence); err != nil {
			return err
		}

		stored, found, err := s.loadBinding(ctx, q, attemptID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowBindingNotFound
		}
		result = stored
		applied = true
		return nil
	})
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}
	return result, applied, nil
}

// ReanchorWorkflowAttemptGeneration records that the controller for an already-bound
// session has been REPLACED, and moves the binding's controller_generation onto the
// replacement.
//
// It exists because generation is otherwise written exactly once, by
// BindWorkflowAttemptSession, whose CAS is guarded `session_id IS NULL`. A chat
// controller that stops and starts again over the same session rotates its generation,
// and a binding that kept the old one fences the live controller out of every
// intervention path forever. Re-anchoring repairs the recorded identity; it does not
// weaken the fence, which still refuses the OLD generation.
//
// The session cannot change: sessionID is a predicate on the row, not an assignment, so
// a binding can never be re-pointed at another session by this statement, and the
// caller-supplied session prevents "re-anchor onto whatever is there now". The
// revision and state CAS means a binding that moved on (stopped, failed, reconciling)
// is left alone.
//
// The state must be one that legitimately holds a live session; the coordinator only
// calls this for states it observed the binding in, and the session predicate makes any
// other state a no-op rather than a write.
func (s *Store) ReanchorWorkflowAttemptGeneration(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	sessionID domain.SessionID,
	generation string,
	expectedRevision int64,
	expectedState domain.WorkflowBindingState,
	now time.Time,
) (domain.WorkflowAttemptBinding, bool, error) {
	if strings.TrimSpace(string(attemptID)) == "" ||
		strings.TrimSpace(string(sessionID)) == "" ||
		strings.TrimSpace(generation) == "" {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrInvalidWorkflowBindingSession
	}
	if !expectedState.Valid() || !expectedState.HasSession() {
		// A state that cannot hold a session has nothing to re-anchor, so the caller is
		// asking about a binding that does not exist in the sense it means.
		return domain.WorkflowAttemptBinding{}, false, domain.ErrInvalidWorkflowBindingState
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowAttemptBinding
	applied := false
	err := s.inSchedulerTx(ctx, "reanchor workflow attempt generation", func(q *gen.Queries) error {
		if err := executionPermitFence(ctx, q, attemptID, now, false); err != nil {
			return err
		}
		current, found, err := s.loadBinding(ctx, q, attemptID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowBindingNotFound
		}
		if current.Revision != expectedRevision || current.State != expectedState {
			return domain.ErrWorkflowBindingRevisionConflict
		}
		if current.SessionID == nil || *current.SessionID != sessionID {
			// Never re-point. A binding whose session is not the one named is a different
			// fact, and repairing the generation must not become a way to adopt a session.
			return domain.ErrWorkflowBindingSessionTaken
		}

		rows, err := q.ReanchorWorkflowAttemptGeneration(
			ctx, gen.ReanchorWorkflowAttemptGenerationParams{
				ControllerGeneration: generation,
				UpdatedAt:            now,
				AttemptID:            attemptID,
				ExpectedRevision:     expectedRevision,
				ExpectedState:        expectedState,
				SessionID:            &sessionID,
			},
		)
		if err != nil {
			return mapBindingWriteError(err)
		}
		if rows == 0 {
			return domain.ErrWorkflowBindingRevisionConflict
		}
		if err := syncReanchoredAdmissionGeneration(ctx, q, attemptID, sessionID, generation, now); err != nil {
			return err
		}
		permit, err := loadExecutionPermit(ctx, q, attemptID)
		if err != nil {
			return err
		}
		if err := bindPathReservations(ctx, q, attemptID, sessionID, generation, permit.Fence); err != nil {
			return err
		}

		stored, found, err := s.loadBinding(ctx, q, attemptID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowBindingNotFound
		}
		result = stored
		applied = true
		return nil
	})
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}
	return result, applied, nil
}

// TransitionWorkflowAttemptBinding advances a binding that is not acquiring a
// session. It is the CAS used by reconciliation and stopping, and it validates the
// edge against the domain graph first.
//
// It deliberately cannot clear a session, touch a worktree, or delete anything: a
// binding transition records a decision, and M2 never "repairs" a dirty worktree by
// resetting it.
func (s *Store) TransitionWorkflowAttemptBinding(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
	next domain.WorkflowBindingState,
	failureCode string,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowAttemptBinding, bool, error) {
	if !next.Valid() {
		return domain.WorkflowAttemptBinding{}, false, domain.ErrInvalidWorkflowBindingState
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	var result domain.WorkflowAttemptBinding
	applied := false
	err := s.inSchedulerTx(ctx, "transition workflow attempt binding", func(q *gen.Queries) error {
		if err := executionPermitFence(ctx, q, attemptID, now, next == domain.WorkflowBindingStateSpawnRequested); err != nil {
			return err
		}
		current, found, err := s.loadBinding(ctx, q, attemptID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowBindingNotFound
		}
		if current.Revision != expectedRevision {
			return domain.ErrWorkflowBindingRevisionConflict
		}
		// The state-update path must never be how a binding ACQUIRES a session: that has
		// its own CAS with its own transaction, which also performs the cross-table write
		// onto workflow_attempts. But a transition to a session-bearing state is legal
		// when a session is already held — that is the normal running/stopping progress,
		// and refusing it would make six of the thirteen states unreachable.
		//
		// This check runs before the graph so the error names the real cause: a caller
		// that tried to reach running from intent_persisted needs to be told it has no
		// session, not that the edge is missing.
		if next.HasSession() && current.SessionID == nil {
			return domain.ErrWorkflowBindingSessionRequired
		}
		if err := current.State.ValidateTransition(next); err != nil {
			return err
		}

		rows, err := q.UpdateWorkflowAttemptBindingState(ctx, gen.UpdateWorkflowAttemptBindingStateParams{
			NextState: next, FailureCode: failureCode, UpdatedAt: now,
			AttemptID: attemptID, ExpectedRevision: expectedRevision, ExpectedState: current.State,
		})
		if err != nil {
			return err
		}
		if rows == 0 {
			return domain.ErrWorkflowBindingRevisionConflict
		}

		stored, found, err := s.loadBinding(ctx, q, attemptID)
		if err != nil {
			return err
		}
		if !found {
			return domain.ErrWorkflowBindingNotFound
		}
		result = stored
		applied = true
		return nil
	})
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}
	return result, applied, nil
}

// RecordWorkflowAttemptRepoBinding updates one repository's observed head and state
// under a per-row revision CAS.
//
// A drift is recorded as mismatch and the row is left in place: M2 never resets,
// cleans, or re-checks-out a worktree to make the observation agree with the
// expectation.
func (s *Store) RecordWorkflowAttemptRepoBinding(
	ctx context.Context,
	binding domain.WorkflowAttemptRepoBinding,
	expectedRevision int64,
	now time.Time,
) (domain.WorkflowAttemptRepoBinding, bool, error) {
	if binding.Revision == 0 {
		binding.Revision = expectedRevision
	}
	if !binding.State.Valid() {
		return domain.WorkflowAttemptRepoBinding{}, false, domain.ErrInvalidWorkflowRepoBindingState
	}
	if strings.TrimSpace(string(binding.RepoKey)) == "" || strings.TrimSpace(string(binding.AttemptID)) == "" {
		return domain.WorkflowAttemptRepoBinding{}, false, domain.ErrInvalidWorkflowRepoBinding
	}
	now = utcTime(now)

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	applied := false
	err := s.inSchedulerTx(ctx, "record workflow attempt repo binding", func(q *gen.Queries) error {
		if err := executionPermitFence(ctx, q, binding.AttemptID, now, false); err != nil {
			return err
		}
		rows, err := q.UpdateWorkflowAttemptRepoBinding(ctx, gen.UpdateWorkflowAttemptRepoBindingParams{
			ObservedHeadSha: binding.ObservedHeadSHA, SessionWorktreeRef: binding.SessionWorktreeRef,
			State: binding.State, UpdatedAt: now,
			AttemptID: binding.AttemptID, RepoKey: binding.RepoKey, ExpectedRevision: expectedRevision,
		})
		if err != nil {
			return mapRepoBindingWriteError(err)
		}
		if rows == 0 {
			return domain.ErrWorkflowBindingRevisionConflict
		}
		applied = true
		return nil
	})
	if err != nil {
		return domain.WorkflowAttemptRepoBinding{}, false, err
	}
	return binding, applied, nil
}

// ListWorkflowAttemptRepoBindings reads every repository binding for one attempt.
func (s *Store) ListWorkflowAttemptRepoBindings(
	ctx context.Context,
	attemptID domain.WorkflowAttemptID,
) ([]domain.WorkflowAttemptRepoBinding, error) {
	rows, err := s.qr.ListWorkflowAttemptRepoBindings(ctx, attemptID)
	if err != nil {
		return nil, fmt.Errorf("list workflow attempt repo bindings: %w", err)
	}
	records := make([]domain.WorkflowAttemptRepoBinding, 0, len(rows))
	for _, row := range rows {
		records = append(records, workflowAttemptRepoBindingFromGen(row))
	}
	return records, nil
}

// ListWorkflowAttemptBindingsByState returns a bounded, oldest-first page of bindings
// in any of the given states.
//
// It exists for the startup recovery sweep, which must find every binding whose
// external outcome is unproven (spawn_requested, spawn_unknown, reconcile_required,
// recovery_uncertain) without scanning the table. The states are supplied by the
// caller rather than fixed here so the sweep owns the definition of "needs
// reconciling" and this read stays a plain filter.
//
// An empty state list returns nothing instead of everything: a caller that forgot the
// filter must not be handed the whole table.
func (s *Store) ListWorkflowAttemptBindingsByState(
	ctx context.Context,
	states []domain.WorkflowBindingState,
	limit int64,
) ([]domain.WorkflowAttemptBinding, error) {
	if len(states) == 0 || limit <= 0 {
		return nil, nil
	}
	hinted := make([]domain.WorkflowBindingState, 0, len(states))
	for _, state := range states {
		if !state.Valid() {
			return nil, domain.ErrInvalidWorkflowBindingState
		}
		hinted = append(hinted, state)
	}
	rows, err := s.qr.ListWorkflowAttemptBindingsByState(ctx, gen.ListWorkflowAttemptBindingsByStateParams{
		States: hinted, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list workflow attempt bindings by state: %w", err)
	}
	bindings := make([]domain.WorkflowAttemptBinding, 0, len(rows))
	for _, row := range rows {
		bindings = append(bindings, workflowAttemptBindingFromGen(row))
	}
	return bindings, nil
}

// loadBinding reads a binding inside an open transaction.
func (s *Store) loadBinding(
	ctx context.Context,
	q *gen.Queries,
	attemptID domain.WorkflowAttemptID,
) (domain.WorkflowAttemptBinding, bool, error) {
	row, err := q.GetWorkflowAttemptBinding(ctx, attemptID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowAttemptBinding{}, false, nil
	}
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}
	return workflowAttemptBindingFromGen(row), true, nil
}

// loadBindingBySpawnRequest reads a binding by its spawn request id inside an open
// transaction.
func (s *Store) loadBindingBySpawnRequest(
	ctx context.Context,
	q *gen.Queries,
	spawnRequestID string,
) (domain.WorkflowAttemptBinding, bool, error) {
	row, err := q.GetWorkflowAttemptBindingBySpawnRequest(ctx, spawnRequestID)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowAttemptBinding{}, false, nil
	}
	if err != nil {
		return domain.WorkflowAttemptBinding{}, false, err
	}
	return workflowAttemptBindingFromGen(row), true, nil
}

// mapBindingWriteError translates the constraint failures that have a stable meaning
// into domain errors. A caller must never have to match a SQLite message string.
//
// The foreign-key case names the specific child table and parent. SQLite reports every
// FK violation as the same "FOREIGN KEY constraint failed" text, so matching that phrase
// alone would blame the repository link for an unrelated failure (a session id with no
// row, for example). Only the repo-binding insert has a parent edge into
// workflow_repositories, and callers map it at that call site.
func mapBindingWriteError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	switch {
	case strings.Contains(message, "workflow_attempt_bindings.session_id"):
		return domain.ErrWorkflowBindingSessionTaken
	case strings.Contains(message, "workflow_attempt_bindings.spawn_request_id"):
		return domain.ErrWorkflowBindingSpawningRequestConflict
	}
	return err
}

// mapRepoBindingWriteError translates a failure while inserting or updating a
// repository binding. It is separate from mapBindingWriteError because only this call
// site has a parent edge into workflow_repositories, so a foreign-key failure here
// genuinely means the repository is not part of the workflow.
func mapRepoBindingWriteError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		return domain.ErrWorkflowBindingRepoNotInWorkflow
	}
	return err
}

// workflowAttemptBindingFromGen converts one generated row. The conversion is
// deliberately field-explicit so adding a column is a compile error rather than a
// silent omission.
func workflowAttemptBindingFromGen(row gen.WorkflowAttemptBinding) domain.WorkflowAttemptBinding {
	return domain.WorkflowAttemptBinding{
		AttemptID:            row.AttemptID,
		WorkflowID:           row.WorkflowID,
		TaskID:               row.TaskID,
		SpawnRequestID:       row.SpawnRequestID,
		SpawnHash:            row.SpawnHash,
		State:                row.State,
		SessionID:            row.SessionID,
		ControllerGeneration: row.ControllerGeneration,
		BoundAt:              nullTimeToTimePtr(row.BoundAt),
		FailureCode:          row.FailureCode,
		Revision:             row.Revision,
		CreatedAt:            row.CreatedAt,
		UpdatedAt:            row.UpdatedAt,
	}
}

func workflowAttemptRepoBindingFromGen(row gen.WorkflowAttemptRepoBinding) domain.WorkflowAttemptRepoBinding {
	return domain.WorkflowAttemptRepoBinding{
		AttemptID:          row.AttemptID,
		WorkflowID:         row.WorkflowID,
		RepoKey:            row.RepoKey,
		ProjectID:          row.ProjectID,
		ExpectedBaseSHA:    row.ExpectedBaseSha,
		SessionWorktreeRef: row.SessionWorktreeRef,
		ObservedHeadSHA:    row.ObservedHeadSha,
		State:              row.State,
		Revision:           row.Revision,
		UpdatedAt:          row.UpdatedAt,
	}
}
