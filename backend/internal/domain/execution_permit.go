package domain

import (
	"crypto/sha256"
	"encoding/hex"
	"time"
)

// SessionAdmissionFingerprint 固定存储准入指纹长度，同时保留 Session 原有的版本化客户端指纹。
func SessionAdmissionFingerprint(clientRequestHash string) string {
	sum := sha256.Sum256([]byte(clientRequestHash))
	return hex.EncodeToString(sum[:])
}

// SessionExecutionGeneration 使用当前执行界面的代际，避免把 TUI 的空 Chat 字段当作归属。
func SessionExecutionGeneration(rec SessionRecord) string {
	if NormalizeSessionMode(rec.Mode) == SessionModeChat {
		return rec.Metadata.ControllerGeneration
	}
	return rec.Metadata.RuntimeLaunchID
}

// ExecutionPermit 是 daemon 内部持有的执行快照；不得序列化或传给 Agent。
// Attempt 没有独立 revision，因此以 UpdatedAt、状态、租约及绑定 revision 共同执行 CAS。
type ExecutionPermit struct {
	AdmissionID       string
	AdmissionRevision int64
	Fence             SchedulerFence
	AttemptID         WorkflowAttemptID
	WorkflowID        WorkflowID
	TaskID            WorkflowTaskID
	WorkerID          string
	LeaseToken        string
	LeaseUntil        time.Time
	AttemptUpdatedAt  time.Time
	AttemptState      WorkflowAttemptState
	SessionID         SessionID
	BindingRevision   int64
	Generation        string
}

// SessionReattachPermit 只允许重附着现存 Provider 并更新观察代际，不是业务执行许可。
// Owner 与持久快照共同防止旧 daemon 的慢连接覆盖新任期或新 Controller。
// Execution 可保存 M2 无 Admission 的观察快照；不能转为业务执行上下文。
type SessionReattachPermit struct {
	SessionID SessionID
	Owner     SessionControllerOwner
	Fence     SchedulerFence
	Admission SchedulerAdmission
	Execution ExecutionPermit
}
