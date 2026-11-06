package domain

import "time"

// PathReservationView 只投影任务路径占位；不暴露物理仓库路径或执行凭据。
type PathReservationView struct {
	ID         int64             `json:"id"`
	AttemptID  WorkflowAttemptID `json:"attemptId"`
	TaskID     WorkflowTaskID    `json:"taskId"`
	RepoKey    WorkflowRepoKey   `json:"repoKey"`
	ScopePath  string            `json:"scopePath"`
	SessionID  SessionID         `json:"sessionId"`
	LeaseUntil time.Time         `json:"leaseUntil"`
	ReleasedAt *time.Time        `json:"releasedAt,omitempty"`
	Revision   int64             `json:"revision"`
}

// CoordinationTaskView 联接已有任务、最新 Attempt、Session 和准入事实，不持久化第二套状态。
type CoordinationTaskView struct {
	TaskID              WorkflowTaskID           `json:"taskId"`
	State               WorkflowTaskState        `json:"state"`
	Revision            int64                    `json:"revision"`
	Dependencies        []WorkflowTaskID         `json:"dependencies"`
	Harness             AgentHarness             `json:"harness"`
	Model               string                   `json:"model"`
	AttemptID           WorkflowAttemptID        `json:"attemptId"`
	AttemptState        WorkflowAttemptState     `json:"attemptState"`
	AttemptNo           int64                    `json:"attemptNo"`
	SessionID           SessionID                `json:"sessionId"`
	SessionStatus       string                   `json:"sessionStatus"`
	SessionReadiness    string                   `json:"sessionReadiness"`
	BindingState        string                   `json:"bindingState"`
	HeartbeatAt         time.Time                `json:"heartbeatAt"`
	LeaseUntil          time.Time                `json:"leaseUntil"`
	LeaseValid          bool                     `json:"leaseValid"`
	AdmissionState      string                   `json:"admissionState"`
	AdmissionRevision   int64                    `json:"admissionRevision"`
	CapabilityReadiness string                   `json:"capabilityReadiness"`
	CapabilitySource    string                   `json:"capabilitySource"`
	CapabilityExpiresAt time.Time                `json:"capabilityExpiresAt"`
	BudgetReservations  []CoordinationBudgetView `json:"budgetReservations"`
	BudgetQuality       string                   `json:"budgetQuality"`
	BlockedReason       string                   `json:"blockedReason"`
	Repositories        []WorkflowTaskRepository `json:"repositories"`
}

// CoordinationBudgetView 分政策展示预留，不把重叠作用域的金额相加冒充总支出。
type CoordinationBudgetView struct {
	PolicyID    string `json:"policyId"`
	Window      string `json:"window"`
	AmountNanos *int64 `json:"amountNanos"`
	State       string `json:"state"`
	Revision    int64  `json:"revision"`
}

// CoordinationMessageView 仅携带最近消息的类型、身份及版本；正文沿既有消息接口按权限读取。
type CoordinationMessageView struct {
	ID            string                       `json:"id"`
	TaskID        *WorkflowTaskID              `json:"taskId,omitempty"`
	ToTaskID      *WorkflowTaskID              `json:"toTaskId,omitempty"`
	Kind          WorkflowMessageKind          `json:"kind"`
	DeliveryState WorkflowMessageDeliveryState `json:"deliveryState"`
	Revision      int64                        `json:"revision"`
	CreatedAt     time.Time                    `json:"createdAt"`
}

// CoordinationSummaryView 是只读观察；各事实的 revision/时间保留，不能作为执行授权输入。
type CoordinationSummaryView struct {
	WorkflowID   WorkflowID                `json:"workflowId"`
	ObservedAt   time.Time                 `json:"observedAt"`
	Tasks        []CoordinationTaskView    `json:"tasks"`
	Reservations []PathReservationView     `json:"reservations"`
	Messages     []CoordinationMessageView `json:"messages"`
	Truncated    bool                      `json:"truncated"`
}
