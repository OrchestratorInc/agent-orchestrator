package domain

import "time"

// WorkflowReplanInput 将新只读 Planner 与可信停止记录绑定，不替换旧 DAG 或工件。
type WorkflowReplanInput struct {
	WorkflowID         WorkflowID
	StopInterventionID string
	ExpectedRevision   int64
	Planner            WorkflowTask
	Repositories       []WorkflowTaskRepository
	IdempotencyKey     string
	RequestFingerprint string
}

// WorkflowPlanPatch 是只允许追加任务和显式停用任务的候选；旧图和工件保持可追踪。
type WorkflowPlanPatch struct {
	Tasks            []WorkflowTask           `json:"tasks"`
	Dependencies     []WorkflowEdge           `json:"dependencies"`
	TaskRepositories []WorkflowTaskRepository `json:"taskRepositories"`
	RetireTaskIDs    []WorkflowTaskID         `json:"retireTaskIds"`
}

// WorkflowPlanCandidate 将不可变候选绑定到原始 Chat turn、停止回执和图版本。
type WorkflowPlanCandidate struct {
	ID                 string            `json:"id"`
	WorkflowID         WorkflowID        `json:"workflowId"`
	SourceAttemptID    WorkflowAttemptID `json:"sourceAttemptId"`
	SourceTurnID       string            `json:"sourceTurnId"`
	StopInterventionID string            `json:"stopInterventionId"`
	ExpectedRevision   int64             `json:"expectedRevision"`
	Digest             string            `json:"digest"`
	Patch              WorkflowPlanPatch `json:"patch"`
	IdempotencyKey     string            `json:"-"`
	RequestFingerprint string            `json:"-"`
	StateFingerprint   string            `json:"-"`
	ConfirmedRevision  int64             `json:"confirmedRevision"`
	CreatedAt          time.Time         `json:"createdAt"`
}
