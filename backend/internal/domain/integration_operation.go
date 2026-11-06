package domain

import "time"

// IntegrationOperation 记录一次精确批准的操作；重放只能读取或对账，不能复活旧写权限。
type IntegrationOperation struct {
	ID            string         `json:"id"`
	PlanID        string         `json:"planId"`
	WorkflowID    WorkflowID     `json:"workflowId"`
	Action        string         `json:"action"`
	RequestDigest string         `json:"requestDigest"`
	State         string         `json:"state"`
	Revision      int64          `json:"revision"`
	Fence         SchedulerFence `json:"-"`
	ApprovalRef   string         `json:"-"`
	CreatedAt     time.Time      `json:"createdAt"`
	RequestJSON   string         `json:"-"`
}

// IntegrationRepoOperation 区分未开始、已请求和未知副作用；已成功仓库不会自动回滚。
type IntegrationRepoOperation struct {
	OperationID string          `json:"operationId"`
	RepoKey     WorkflowRepoKey `json:"repoKey"`
	State       string          `json:"state"`
	ObservedSHA string          `json:"observedSha"`
	Revision    int64           `json:"revision"`
}

// IntegrationApplyRequest 是人工看到的全部批准内容，Plan 和有序仓库证据必须与持久记录逐字一致。
type IntegrationApplyRequest struct {
	IdempotencyKey   string                   `json:"idempotencyKey"`
	ExpectedRevision int64                    `json:"expectedRevision"`
	PlanDigest       string                   `json:"planDigest"`
	Entries          []IntegrationRepoPreview `json:"entries"`
	PushTargets      []IntegrationPushTarget  `json:"pushTargets,omitempty"`
}

// IntegrationPushTarget 只引用服务端登记的本地 bare 仓库；批准不携带可执行 URL 或任意路径。
type IntegrationPushTarget struct {
	RepoKey           WorkflowRepoKey `json:"repoKey"`
	RemoteKey         string          `json:"remoteKey"`
	RemoteIdentity    string          `json:"remoteIdentity"`
	TargetRef         string          `json:"targetRef"`
	ExpectedRemoteSHA string          `json:"expectedRemoteSha"`
	CommitSHA         string          `json:"commitSha"`
}
