package domain

import (
	"errors"
	"strings"
	"time"
)

// WorkflowBindingState is the durable progress of one attempt's intent to become a
// running session.
//
// The states are deliberately more granular than "not started / started" because the
// dangerous interval is the one where AO cannot prove whether an external spawn
// happened. A single boolean would force that interval to be reported as success or
// failure, and both are lies:
//
//	unbound            nothing recorded yet
//	intent_persisted   the binding row and its repo expectations are durable; no
//	                   spawn has been attempted
//	spawn_requested    a spawn was requested; its answer is not known yet
//	session_provisioning  the session row exists and is bound to this attempt, but its
//	                   worktree/controller is not ready, so the provider may not have
//	                   started
//	bound_ready        a session is bound and proven ready
//	running            the attempt is executing in that session
//	spawn_unknown      a spawn was issued and its outcome cannot be established
//	reconcile_required evidence must be gathered before anything else may happen
//	failed_confirmed   the operation provably produced no external side effect
//	stop_requested     a stop was requested; its receipt is not known yet
//	stop_confirmed     the stop is proven
//	stop_unknown       the stop outcome cannot be established
//	recovery_uncertain the session, host, or worktree evidence is insufficient
type WorkflowBindingState string

// Binding states persisted by the attempt binding lifecycle.
const (
	WorkflowBindingStateUnbound             WorkflowBindingState = "unbound"
	WorkflowBindingStateIntentPersisted     WorkflowBindingState = "intent_persisted"
	WorkflowBindingStateSpawnRequested      WorkflowBindingState = "spawn_requested"
	WorkflowBindingStateSessionProvisioning WorkflowBindingState = "session_provisioning"
	WorkflowBindingStateBoundReady          WorkflowBindingState = "bound_ready"
	WorkflowBindingStateRunning             WorkflowBindingState = "running"
	WorkflowBindingStateSpawnUnknown        WorkflowBindingState = "spawn_unknown"
	WorkflowBindingStateReconcileRequired   WorkflowBindingState = "reconcile_required"
	WorkflowBindingStateFailedConfirmed     WorkflowBindingState = "failed_confirmed"
	WorkflowBindingStateStopRequested       WorkflowBindingState = "stop_requested"
	WorkflowBindingStateStopConfirmed       WorkflowBindingState = "stop_confirmed"
	WorkflowBindingStateStopUnknown         WorkflowBindingState = "stop_unknown"
	WorkflowBindingStateRecoveryUncertain   WorkflowBindingState = "recovery_uncertain"
)

// Valid reports whether s is a supported binding state.
func (s WorkflowBindingState) Valid() bool {
	switch s {
	case WorkflowBindingStateUnbound, WorkflowBindingStateIntentPersisted,
		WorkflowBindingStateSpawnRequested, WorkflowBindingStateSessionProvisioning,
		WorkflowBindingStateBoundReady, WorkflowBindingStateRunning,
		WorkflowBindingStateSpawnUnknown, WorkflowBindingStateReconcileRequired,
		WorkflowBindingStateFailedConfirmed, WorkflowBindingStateStopRequested,
		WorkflowBindingStateStopConfirmed, WorkflowBindingStateStopUnknown,
		WorkflowBindingStateRecoveryUncertain:
		return true
	default:
		return false
	}
}

// Terminal reports whether no further transition is legal. A terminal binding is
// evidence, not a tombstone: the session and worktree it names are never cleaned up
// by a state transition.
func (s WorkflowBindingState) Terminal() bool {
	return s == WorkflowBindingStateFailedConfirmed || s == WorkflowBindingStateStopConfirmed
}

// HasSession reports whether a binding in this state must name a session. It mirrors
// the CHECK constraint in migration 20261103143320688 so the store and the database
// agree about which states are allowed to carry a session.
//
// session_provisioning is included because the state means exactly what its name says:
// the session row exists and AO is still preparing its workspace or controller. Leaving
// it out would have made the state unable to name the very session it describes, which
// is why it previously had no reachable use.
func (s WorkflowBindingState) HasSession() bool {
	switch s {
	case WorkflowBindingStateSessionProvisioning,
		WorkflowBindingStateBoundReady, WorkflowBindingStateRunning,
		WorkflowBindingStateStopRequested, WorkflowBindingStateStopConfirmed,
		WorkflowBindingStateStopUnknown, WorkflowBindingStateRecoveryUncertain:
		return true
	default:
		return false
	}
}

// SessionAcquisitionTargets lists the states that the bind operation may land on.
//
// Keeping this list explicit is what separates "acquire a session" from "progress with a
// session already held": only these two may be the target of a bind, and the store
// refuses any other target rather than silently binding into a running state.
func SessionAcquisitionTargets() []WorkflowBindingState {
	return []WorkflowBindingState{
		WorkflowBindingStateSessionProvisioning,
		WorkflowBindingStateBoundReady,
	}
}

// IsSessionAcquisitionTarget reports whether a bind may land on this state.
func (s WorkflowBindingState) IsSessionAcquisitionTarget() bool {
	for _, target := range SessionAcquisitionTargets() {
		if target == s {
			return true
		}
	}
	return false
}

// PreSession reports whether this state must not name a session yet.
func (s WorkflowBindingState) PreSession() bool {
	return s == WorkflowBindingStateUnbound || s == WorkflowBindingStateIntentPersisted ||
		s == WorkflowBindingStateSpawnRequested
}

// bindingTransitions is the legal transition graph. An edge that is not listed here
// is a typed error, never a silent write: every one of these states exists to
// prevent a specific mistake, and accepting an arbitrary write would erase it.
var bindingTransitions = map[WorkflowBindingState][]WorkflowBindingState{
	WorkflowBindingStateUnbound: {
		WorkflowBindingStateIntentPersisted,
		WorkflowBindingStateFailedConfirmed,
	},
	WorkflowBindingStateIntentPersisted: {
		WorkflowBindingStateSpawnRequested,
		// BindWorkflowAttemptSession records the session as soon as one is known.
		// A caller that persisted the intent and then spawned inline legitimately
		// jumps straight here; requiring an intermediate spawn_requested write would
		// force two transactions for one fact.
		WorkflowBindingStateBoundReady,
		// A spawn that answered with an existing-but-unready session lands here.
		WorkflowBindingStateSessionProvisioning,
		// The same inline spawn can lose its answer. Because intent_persisted may lead
		// directly to a spawn, it must also be able to record an unknown or
		// reconciliation-required outcome; otherwise the only honest result would be
		// unreachable and the caller would have to lie.
		WorkflowBindingStateSpawnUnknown,
		WorkflowBindingStateReconcileRequired,
		WorkflowBindingStateFailedConfirmed,
	},
	WorkflowBindingStateSpawnRequested: {
		WorkflowBindingStateSessionProvisioning,
		// The normal success edge: the spawn answered and the session is recorded.
		WorkflowBindingStateBoundReady,
		// The dangerous edge: the spawn was issued and no answer arrived. Going to
		// spawn_unknown (or straight to reconciliation) is the only honest move,
		// and neither may be followed by another spawn attempt.
		WorkflowBindingStateSpawnUnknown,
		WorkflowBindingStateReconcileRequired,
		WorkflowBindingStateFailedConfirmed,
	},
	WorkflowBindingStateSessionProvisioning: {
		// A session that finishes provisioning is bound. Re-binding to bound_ready is
		// also the promotion path, so it must be reachable from here.
		WorkflowBindingStateBoundReady,
		WorkflowBindingStateSpawnUnknown,
		WorkflowBindingStateReconcileRequired,
		WorkflowBindingStateFailedConfirmed,
	},
	WorkflowBindingStateSpawnUnknown: {
		// Evidence about the existing session can promote it; nothing may spawn a
		// replacement. A session found by reconciliation may still be provisioning, so
		// that landing state is legal here too.
		WorkflowBindingStateReconcileRequired,
		WorkflowBindingStateSessionProvisioning,
		WorkflowBindingStateBoundReady,
		WorkflowBindingStateFailedConfirmed,
	},
	WorkflowBindingStateReconcileRequired: {
		WorkflowBindingStateSessionProvisioning,
		WorkflowBindingStateBoundReady,
		WorkflowBindingStateRecoveryUncertain,
		WorkflowBindingStateFailedConfirmed,
	},
	WorkflowBindingStateBoundReady: {
		WorkflowBindingStateRunning,
		WorkflowBindingStateRecoveryUncertain,
		WorkflowBindingStateStopRequested,
	},
	WorkflowBindingStateRunning: {
		WorkflowBindingStateStopRequested,
		WorkflowBindingStateRecoveryUncertain,
	},
	WorkflowBindingStateRecoveryUncertain: {
		WorkflowBindingStateBoundReady,
		WorkflowBindingStateRunning,
		WorkflowBindingStateStopRequested,
		WorkflowBindingStateFailedConfirmed,
	},
	WorkflowBindingStateStopRequested: {
		WorkflowBindingStateStopConfirmed,
		WorkflowBindingStateStopUnknown,
	},
	WorkflowBindingStateStopUnknown: {
		// Only a later receipt can settle an unknown stop; it never re-requests.
		WorkflowBindingStateStopConfirmed,
		WorkflowBindingStateRecoveryUncertain,
	},
	WorkflowBindingStateStopConfirmed:   {},
	WorkflowBindingStateFailedConfirmed: {},
}

// CanTransitionTo reports whether a binding may move from s to next. Same-state writes are
// rejected, so a caller expresses a plain replay through the revision CAS instead.
func (s WorkflowBindingState) CanTransitionTo(next WorkflowBindingState) bool {
	if !s.Valid() || !next.Valid() {
		return false
	}
	for _, allowed := range bindingTransitions[s] {
		if allowed == next {
			return true
		}
	}
	return false
}

// ValidateTransition returns a typed error when s may not become next.
func (s WorkflowBindingState) ValidateTransition(next WorkflowBindingState) error {
	if !s.Valid() {
		return ErrInvalidWorkflowBindingState
	}
	if !next.Valid() {
		return ErrInvalidWorkflowBindingState
	}
	if !s.CanTransitionTo(next) {
		return ErrInvalidWorkflowBindingTransition
	}
	return nil
}

// WorkflowRepoBindingState is how one repository's worktree compared to what the
// attempt expected.
type WorkflowRepoBindingState string

// Repository binding states.
const (
	WorkflowRepoBindingStatePending  WorkflowRepoBindingState = "pending"
	WorkflowRepoBindingStateVerified WorkflowRepoBindingState = "verified"
	WorkflowRepoBindingStateMismatch WorkflowRepoBindingState = "mismatch"
	WorkflowRepoBindingStateMissing  WorkflowRepoBindingState = "missing"
	WorkflowRepoBindingStateUnknown  WorkflowRepoBindingState = "unknown"
)

// Valid reports whether s is a supported repository binding state.
func (s WorkflowRepoBindingState) Valid() bool {
	switch s {
	case WorkflowRepoBindingStatePending, WorkflowRepoBindingStateVerified,
		WorkflowRepoBindingStateMismatch, WorkflowRepoBindingStateMissing,
		WorkflowRepoBindingStateUnknown:
		return true
	default:
		return false
	}
}

// WorkflowAttemptBinding is one attempt's durable intent and outcome. It never
// carries the attempt's lease token: that capability stays in workflow_attempts
// where exactly one response may return it.
type WorkflowAttemptBinding struct {
	AttemptID  WorkflowAttemptID `json:"attemptId"`
	WorkflowID WorkflowID        `json:"workflowId"`
	TaskID     WorkflowTaskID    `json:"taskId"`

	// SpawnRequestID is derived from the attempt identity, so replaying a Bind
	// reuses it and the database refuses a second spawn.
	SpawnRequestID string `json:"spawnRequestId"`
	// SpawnHash is the 64-character request fingerprint. A different hash under the
	// same request id is a conflict, matching the Workflow idempotency rule.
	SpawnHash string `json:"-"`

	State WorkflowBindingState `json:"state"`

	SessionID            *SessionID `json:"sessionId,omitempty"`
	ControllerGeneration string     `json:"controllerGeneration,omitempty"`
	BoundAt              *time.Time `json:"boundAt,omitempty"`
	FailureCode          string     `json:"failureCode,omitempty"`

	Revision  int64     `json:"revision"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Validate checks identity, fingerprint shape, and state coherence.
func (b WorkflowAttemptBinding) Validate() error {
	if strings.TrimSpace(string(b.AttemptID)) == "" ||
		strings.TrimSpace(string(b.WorkflowID)) == "" ||
		strings.TrimSpace(string(b.TaskID)) == "" ||
		strings.TrimSpace(b.SpawnRequestID) == "" ||
		len(b.SpawnHash) != 64 ||
		b.Revision < 1 ||
		b.CreatedAt.IsZero() ||
		b.UpdatedAt.IsZero() ||
		b.UpdatedAt.Before(b.CreatedAt) {
		return ErrInvalidWorkflowBinding
	}
	if !b.State.Valid() {
		return ErrInvalidWorkflowBindingState
	}
	if b.State.HasSession() {
		if b.SessionID == nil || strings.TrimSpace(string(*b.SessionID)) == "" || b.BoundAt == nil {
			return ErrInvalidWorkflowBindingSession
		}
	}
	if b.State.PreSession() && b.SessionID != nil {
		return ErrInvalidWorkflowBindingSession
	}
	return nil
}

// WorkflowAttemptRepoBinding is one repository's binding facts for an attempt.
type WorkflowAttemptRepoBinding struct {
	AttemptID  WorkflowAttemptID `json:"attemptId"`
	WorkflowID WorkflowID        `json:"workflowId"`
	RepoKey    WorkflowRepoKey   `json:"repoKey"`
	ProjectID  ProjectID         `json:"projectId"`

	ExpectedBaseSHA    string                   `json:"expectedBaseSha,omitempty"`
	SessionWorktreeRef string                   `json:"sessionWorktreeRef,omitempty"`
	ObservedHeadSHA    string                   `json:"observedHeadSha,omitempty"`
	State              WorkflowRepoBindingState `json:"state"`

	Revision  int64     `json:"revision"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Validate checks identity and enum membership.
func (r WorkflowAttemptRepoBinding) Validate() error {
	if strings.TrimSpace(string(r.AttemptID)) == "" ||
		strings.TrimSpace(string(r.WorkflowID)) == "" ||
		strings.TrimSpace(string(r.RepoKey)) == "" ||
		strings.TrimSpace(string(r.ProjectID)) == "" ||
		r.Revision < 1 ||
		r.UpdatedAt.IsZero() {
		return ErrInvalidWorkflowRepoBinding
	}
	if !r.State.Valid() {
		return ErrInvalidWorkflowRepoBindingState
	}
	return nil
}

// Workflow binding errors.
var (
	ErrInvalidWorkflowBinding           = errors.New("invalid workflow attempt binding")
	ErrInvalidWorkflowBindingState      = errors.New("invalid workflow binding state")
	ErrInvalidWorkflowBindingTransition = errors.New("invalid workflow binding state transition")
	ErrInvalidWorkflowBindingSession    = errors.New("workflow binding session does not match its state")
	ErrInvalidWorkflowRepoBinding       = errors.New("invalid workflow attempt repo binding")
	ErrInvalidWorkflowRepoBindingState  = errors.New("invalid workflow repo binding state")
	// ErrWorkflowBindingSpawningRequestConflict means a different spawn hash was
	// presented under an existing spawn request id.
	ErrWorkflowBindingSpawningRequestConflict = errors.New("workflow spawn request conflicts with another binding")
	// ErrWorkflowBindingSessionTaken means the session already belongs to another
	// attempt binding.
	ErrWorkflowBindingSessionTaken = errors.New("workflow session is already bound to another attempt")
	// ErrWorkflowBindingNotFound means no binding exists for the requested identity.
	ErrWorkflowBindingNotFound = errors.New("workflow attempt binding not found")
	// ErrWorkflowBindingRevisionConflict is the CAS loss signal for binding reads and
	// writes.
	ErrWorkflowBindingRevisionConflict = errors.New("workflow binding revision conflict")
	// ErrWorkflowBindingRepoNotInWorkflow means a repo binding named a repository the
	// workflow never registered.
	ErrWorkflowBindingRepoNotInWorkflow = errors.New("workflow repository is not part of this workflow")
	// ErrWorkflowBindingAttemptMismatch means the attempt does not belong to the
	// workflow and task the binding names.
	ErrWorkflowBindingAttemptMismatch = errors.New("workflow attempt does not belong to this task")
	// ErrWorkflowBindingAttemptAlreadyBound means workflow_attempts.session_id was
	// already written, so a second bind would create two truths.
	ErrWorkflowBindingAttemptAlreadyBound = errors.New("workflow attempt already has a session")
	// ErrWorkflowBindingSessionRequired means a transition tried to reach a
	// session-bearing state without acquiring a session first. Acquiring a session must
	// go through the bind CAS, which also mirrors it onto the attempt row.
	ErrWorkflowBindingSessionRequired = errors.New("workflow binding must acquire a session through the bind operation")
)
