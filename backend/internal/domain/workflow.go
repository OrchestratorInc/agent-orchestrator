package domain

import (
	"errors"
	"path"
	"strings"
	"time"
)

// WorkflowID identifies one durable multi-repository workflow.
type WorkflowID string

// WorkflowTaskID identifies one task within a workflow.
type WorkflowTaskID string

// WorkflowAttemptID identifies one durable attempt to execute a task.
type WorkflowAttemptID string

// WorkflowRepoKey is the stable, workflow-local key for one repository binding.
type WorkflowRepoKey string

// WorkflowState is the durable lifecycle of a workflow definition.
type WorkflowState string

const (
	// WorkflowStateDraft is a newly created workflow that has not been activated.
	WorkflowStateDraft WorkflowState = "draft"
	// WorkflowStateActive is a workflow whose task graph may progress.
	WorkflowStateActive WorkflowState = "active"
	// WorkflowStatePaused is a workflow whose progression was explicitly paused.
	WorkflowStatePaused WorkflowState = "paused"
	// WorkflowStateCompleted means every required task reached a successful terminal state.
	WorkflowStateCompleted WorkflowState = "completed"
	// WorkflowStateFailed means the workflow was terminally failed.
	WorkflowStateFailed WorkflowState = "failed"
	// WorkflowStateCancelled means the workflow was explicitly cancelled.
	WorkflowStateCancelled WorkflowState = "cancelled"
)

// Valid reports whether s is a supported durable workflow state.
func (s WorkflowState) Valid() bool {
	switch s {
	case WorkflowStateDraft, WorkflowStateActive, WorkflowStatePaused,
		WorkflowStateCompleted, WorkflowStateFailed, WorkflowStateCancelled:
		return true
	default:
		return false
	}
}

// WorkflowTaskKind classifies the work represented by a task.
type WorkflowTaskKind string

// Workflow task kinds accepted by the durable Workflow schema.
const (
	WorkflowTaskKindAnalysis    WorkflowTaskKind = "analysis"
	WorkflowTaskKindDevelopment WorkflowTaskKind = "development"
	WorkflowTaskKindTesting     WorkflowTaskKind = "testing"
	WorkflowTaskKindReview      WorkflowTaskKind = "review"
	WorkflowTaskKindIntegration WorkflowTaskKind = "integration"
	WorkflowTaskKindApproval    WorkflowTaskKind = "approval"
)

// Valid reports whether k is a supported workflow task kind.
func (k WorkflowTaskKind) Valid() bool {
	switch k {
	case WorkflowTaskKindAnalysis, WorkflowTaskKindDevelopment, WorkflowTaskKindTesting,
		WorkflowTaskKindReview, WorkflowTaskKindIntegration, WorkflowTaskKindApproval:
		return true
	default:
		return false
	}
}

// WorkflowTaskState is the durable lifecycle of one task. Display status is
// still derived by services; these values record the facts needed to recover it.
type WorkflowTaskState string

// Workflow task states persisted by the Workflow lifecycle.
const (
	WorkflowTaskStatePending             WorkflowTaskState = "pending"
	WorkflowTaskStateReady               WorkflowTaskState = "ready"
	WorkflowTaskStateClaimed             WorkflowTaskState = "claimed"
	WorkflowTaskStateRunning             WorkflowTaskState = "running"
	WorkflowTaskStateVerifying           WorkflowTaskState = "verifying"
	WorkflowTaskStateReadyForIntegration WorkflowTaskState = "ready_for_integration"
	WorkflowTaskStateAwaitingApproval    WorkflowTaskState = "awaiting_approval"
	WorkflowTaskStateIntegrating         WorkflowTaskState = "integrating"
	WorkflowTaskStateRetryWait           WorkflowTaskState = "retry_wait"
	WorkflowTaskStateBlocked             WorkflowTaskState = "blocked"
	WorkflowTaskStateFailed              WorkflowTaskState = "failed"
	WorkflowTaskStateCancelled           WorkflowTaskState = "cancelled"
	WorkflowTaskStateSucceeded           WorkflowTaskState = "succeeded"
)

// Valid reports whether s is a supported durable task state.
func (s WorkflowTaskState) Valid() bool {
	switch s {
	case WorkflowTaskStatePending, WorkflowTaskStateReady, WorkflowTaskStateClaimed,
		WorkflowTaskStateRunning, WorkflowTaskStateVerifying, WorkflowTaskStateReadyForIntegration,
		WorkflowTaskStateAwaitingApproval, WorkflowTaskStateIntegrating, WorkflowTaskStateRetryWait,
		WorkflowTaskStateBlocked, WorkflowTaskStateFailed, WorkflowTaskStateCancelled,
		WorkflowTaskStateSucceeded:
		return true
	default:
		return false
	}
}

// WorkflowOperation identifies one idempotent Workflow write command.
type WorkflowOperation string

// Workflow write operations persisted in the idempotency ledger.
const (
	WorkflowOperationCreate        WorkflowOperation = "workflow.create"
	WorkflowOperationActivate      WorkflowOperation = "workflow.activate"
	WorkflowOperationClaimTask     WorkflowOperation = "task.claim"
	WorkflowOperationStartAttempt  WorkflowOperation = "attempt.start"
	WorkflowOperationSettleAttempt WorkflowOperation = "attempt.settle"
	WorkflowOperationRetryTask     WorkflowOperation = "task.retry"
	// WorkflowOperationResolveAttempt records an operator's decided outcome for
	// an attempt whose lease expired. It is the only exit from UNKNOWN.
	WorkflowOperationResolveAttempt WorkflowOperation = "attempt.resolve"
)

// WorkflowAttemptState is the durable state of one claim/worker attempt.
type WorkflowAttemptState string

// Workflow attempt states persisted by the execution lease lifecycle.
const (
	WorkflowAttemptStateClaimed   WorkflowAttemptState = "claimed"
	WorkflowAttemptStateRunning   WorkflowAttemptState = "running"
	WorkflowAttemptStateSettling  WorkflowAttemptState = "settling"
	WorkflowAttemptStateSucceeded WorkflowAttemptState = "succeeded"
	WorkflowAttemptStateFailed    WorkflowAttemptState = "failed"
	// WorkflowAttemptStateUnknown blocks another claim until reconciliation
	// proves whether the previous attempt produced an external side effect.
	WorkflowAttemptStateUnknown WorkflowAttemptState = "unknown"
)

// Valid reports whether s is a supported durable attempt state.
func (s WorkflowAttemptState) Valid() bool {
	switch s {
	case WorkflowAttemptStateClaimed, WorkflowAttemptStateRunning, WorkflowAttemptStateSettling,
		WorkflowAttemptStateSucceeded, WorkflowAttemptStateFailed, WorkflowAttemptStateUnknown:
		return true
	default:
		return false
	}
}

// Workflow is the durable user-authored workflow definition. Its status is a
// stored lifecycle fact; task and execution presentation is derived elsewhere.
type Workflow struct {
	ID             WorkflowID    `json:"id"`
	Title          string        `json:"title"`
	Goal           string        `json:"goal"`
	OwnerSessionID *SessionID    `json:"ownerSessionId,omitempty"`
	State          WorkflowState `json:"state"`
	Revision       int64         `json:"revision"`
	CreatedAt      time.Time     `json:"createdAt"`
	UpdatedAt      time.Time     `json:"updatedAt"`
}

// Validate checks the required durable identity and lifecycle fields.
func (w Workflow) Validate() error {
	if strings.TrimSpace(string(w.ID)) == "" ||
		strings.TrimSpace(w.Title) == "" ||
		strings.TrimSpace(w.Goal) == "" ||
		w.Revision < 1 ||
		w.CreatedAt.IsZero() ||
		w.UpdatedAt.IsZero() ||
		w.UpdatedAt.Before(w.CreatedAt) {
		return ErrInvalidWorkflow
	}
	if !w.State.Valid() {
		return ErrInvalidWorkflowState
	}
	return nil
}

// WorkflowRepository binds a workflow to one registered repository snapshot.
// PathRef is relative to the registered project root; empty means the root.
type WorkflowRepository struct {
	WorkflowID WorkflowID      `json:"workflowId"`
	RepoKey    WorkflowRepoKey `json:"repoKey"`
	ProjectID  ProjectID       `json:"projectId"`
	PathRef    string          `json:"pathRef"`
	BaseSHA    string          `json:"baseSha"`
	TargetRef  string          `json:"targetRef"`
}

// Validate checks the durable repository binding identity.
func (r WorkflowRepository) Validate() error {
	if strings.TrimSpace(string(r.WorkflowID)) == "" ||
		strings.TrimSpace(string(r.RepoKey)) == "" ||
		strings.TrimSpace(string(r.ProjectID)) == "" {
		return ErrInvalidWorkflowRepository
	}
	if _, ok := CanonicalWorkflowPath(r.PathRef, true); !ok {
		return ErrInvalidWorkflowRepository
	}
	return nil
}

// WorkflowTask is one independently addressable unit in a workflow DAG.
type WorkflowTask struct {
	ID           WorkflowTaskID    `json:"id"`
	WorkflowID   WorkflowID        `json:"workflowId"`
	Kind         WorkflowTaskKind  `json:"kind"`
	Title        string            `json:"title"`
	Brief        string            `json:"brief"`
	State        WorkflowTaskState `json:"state"`
	Priority     int64             `json:"priority"`
	AgentHarness AgentHarness      `json:"agentHarness,omitempty"`
	MaxAttempts  int64             `json:"maxAttempts"`
	Revision     int64             `json:"revision"`
	CreatedAt    time.Time         `json:"createdAt"`
	UpdatedAt    time.Time         `json:"updatedAt"`
}

// Validate checks task identity, kind, state, and optimistic-concurrency fields.
func (t WorkflowTask) Validate() error {
	if strings.TrimSpace(string(t.ID)) == "" ||
		strings.TrimSpace(string(t.WorkflowID)) == "" ||
		strings.TrimSpace(t.Title) == "" ||
		t.MaxAttempts < 1 ||
		t.Revision < 1 ||
		t.CreatedAt.IsZero() ||
		t.UpdatedAt.IsZero() ||
		t.UpdatedAt.Before(t.CreatedAt) {
		return ErrInvalidWorkflowTask
	}
	if !t.Kind.Valid() {
		return ErrInvalidWorkflowTaskKind
	}
	if !t.State.Valid() {
		return ErrInvalidWorkflowTaskState
	}
	return nil
}

// WorkflowEdge records a directed dependency: from_task_id must complete
// before to_task_id can become ready. Graph-wide cycle checks belong to M1-02.
type WorkflowEdge struct {
	WorkflowID WorkflowID     `json:"workflowId"`
	FromTaskID WorkflowTaskID `json:"fromTaskId"`
	ToTaskID   WorkflowTaskID `json:"toTaskId"`
}

// Validate checks edge identity and rejects self-dependencies.
func (e WorkflowEdge) Validate() error {
	if strings.TrimSpace(string(e.WorkflowID)) == "" ||
		strings.TrimSpace(string(e.FromTaskID)) == "" ||
		strings.TrimSpace(string(e.ToTaskID)) == "" ||
		e.FromTaskID == e.ToTaskID {
		return ErrInvalidWorkflowEdge
	}
	return nil
}

// WorkflowTaskRepository scopes one task to a repository already bound to its
// workflow. An empty AllowedPaths list is stored as an empty JSON array.
type WorkflowTaskRepository struct {
	WorkflowID   WorkflowID      `json:"workflowId"`
	TaskID       WorkflowTaskID  `json:"taskId"`
	RepoKey      WorkflowRepoKey `json:"repoKey"`
	AllowedPaths []string        `json:"allowedPaths"`
	BaseSHA      string          `json:"baseSha"`
}

// Validate checks the task/repository binding and rejects duplicate or blank paths.
func (r WorkflowTaskRepository) Validate() error {
	if strings.TrimSpace(string(r.WorkflowID)) == "" ||
		strings.TrimSpace(string(r.TaskID)) == "" ||
		strings.TrimSpace(string(r.RepoKey)) == "" {
		return ErrInvalidWorkflowTaskRepository
	}
	seen := make(map[string]struct{}, len(r.AllowedPaths))
	for _, allowedPath := range r.AllowedPaths {
		canonical, ok := CanonicalWorkflowPath(allowedPath, false)
		if !ok {
			return ErrInvalidWorkflowTaskRepository
		}
		if _, exists := seen[canonical]; exists {
			return ErrInvalidWorkflowTaskRepository
		}
		seen[canonical] = struct{}{}
	}
	return nil
}

// canonicalWorkflowRelativePath validates and canonicalizes a repo-relative
// path using slash separators. It rejects absolute paths, drive/stream syntax,
// NUL bytes, and parent traversal before the path can reach a worker.
func canonicalWorkflowRelativePath(value string, allowEmpty bool) (string, bool) {
	if value == "" && allowEmpty {
		return "", true
	}
	if value == "" || strings.TrimSpace(value) != value ||
		strings.ContainsRune(value, '\x00') || strings.Contains(value, "\\") ||
		strings.Contains(value, ":") || path.IsAbs(value) {
		return "", false
	}
	for _, segment := range strings.Split(value, "/") {
		if segment == ".." {
			return "", false
		}
	}
	canonical := path.Clean(value)
	if canonical == ".." || strings.HasPrefix(canonical, "../") {
		return "", false
	}
	if canonical == "." {
		return "", true
	}
	return canonical, true
}

// CanonicalWorkflowPath validates and canonicalizes a repo-relative path for a
// workflow repository binding or task scope. It rejects absolute paths,
// drive/stream syntax, NUL bytes, and parent traversal, and returns false when
// the value is not a usable relative path. allowEmpty admits the project root.
//
// Every writer of a workflow path (service canonicalization and store inserts)
// must go through this one function so two spellings of one directory cannot
// both be stored.
func CanonicalWorkflowPath(value string, allowEmpty bool) (string, bool) {
	return canonicalWorkflowRelativePath(value, allowEmpty)
}

// WorkflowAttempt records one durable claim and its execution lease. SessionID
// remains optional in M1: creating an attempt never starts an Agent Session.
type WorkflowAttempt struct {
	ID          WorkflowAttemptID    `json:"id"`
	WorkflowID  WorkflowID           `json:"workflowId"`
	TaskID      WorkflowTaskID       `json:"taskId"`
	AttemptNo   int64                `json:"attemptNo"`
	SessionID   *SessionID           `json:"sessionId,omitempty"`
	WorkerID    string               `json:"workerId,omitempty"`
	LeaseToken  string               `json:"-"`
	LeaseUntil  *time.Time           `json:"leaseUntil,omitempty"`
	HeartbeatAt *time.Time           `json:"heartbeatAt,omitempty"`
	State       WorkflowAttemptState `json:"state"`
	StartedAt   *time.Time           `json:"startedAt,omitempty"`
	EndedAt     *time.Time           `json:"endedAt,omitempty"`
	ErrorCode   string               `json:"errorCode,omitempty"`
	CreatedAt   time.Time            `json:"createdAt"`
	UpdatedAt   time.Time            `json:"updatedAt"`
}

// Validate checks attempt identity and requires a lease for claim-bearing states.
func (a WorkflowAttempt) Validate() error {
	if strings.TrimSpace(string(a.ID)) == "" ||
		strings.TrimSpace(string(a.WorkflowID)) == "" ||
		strings.TrimSpace(string(a.TaskID)) == "" ||
		a.AttemptNo < 1 ||
		a.CreatedAt.IsZero() ||
		a.UpdatedAt.IsZero() ||
		a.UpdatedAt.Before(a.CreatedAt) {
		return ErrInvalidWorkflowAttempt
	}
	if !a.State.Valid() {
		return ErrInvalidWorkflowAttemptState
	}
	if a.State == WorkflowAttemptStateClaimed ||
		a.State == WorkflowAttemptStateRunning ||
		a.State == WorkflowAttemptStateSettling {
		if strings.TrimSpace(a.WorkerID) == "" ||
			strings.TrimSpace(a.LeaseToken) == "" ||
			a.LeaseUntil == nil {
			return ErrInvalidWorkflowAttemptLease
		}
	}
	return nil
}

// Workflow domain validation errors.
var (
	ErrInvalidWorkflow               = errors.New("invalid workflow")
	ErrInvalidWorkflowState          = errors.New("invalid workflow state")
	ErrInvalidWorkflowRepository     = errors.New("invalid workflow repository")
	ErrInvalidWorkflowTask           = errors.New("invalid workflow task")
	ErrInvalidWorkflowTaskKind       = errors.New("invalid workflow task kind")
	ErrInvalidWorkflowTaskState      = errors.New("invalid workflow task state")
	ErrInvalidWorkflowEdge           = errors.New("invalid workflow edge")
	ErrInvalidWorkflowTaskRepository = errors.New("invalid workflow task repository")
	ErrInvalidWorkflowAttempt        = errors.New("invalid workflow attempt")
	ErrInvalidWorkflowAttemptState   = errors.New("invalid workflow attempt state")
	ErrInvalidWorkflowAttemptLease   = errors.New("invalid workflow attempt lease")
	ErrWorkflowIdempotencyConflict   = errors.New("workflow idempotency key conflicts with another request")
	ErrWorkflowRevisionConflict      = errors.New("workflow revision conflict")
	ErrWorkflowTaskNotClaimable      = errors.New("workflow task is not claimable")
	ErrWorkflowAttemptLeaseConflict  = errors.New("workflow attempt lease conflict")
	ErrInvalidWorkflowTransition     = errors.New("invalid workflow state transition")
	ErrWorkflowProjectNotFound       = errors.New("workflow repository project not found")
	ErrWorkflowProjectArchived       = errors.New("workflow repository project is archived")
)
