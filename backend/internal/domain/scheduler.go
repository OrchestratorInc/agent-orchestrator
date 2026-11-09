package domain

import (
	"errors"
	"strings"
	"time"
)

// SchedulerDefaultGlobalCap 是未配置全局策略时的并发上限。
const (
	SchedulerDefaultGlobalCap  int64 = 3
	SchedulerHardCap           int64 = 10
	SchedulerDefaultAdapterCap int64 = 2
)

// ErrAdmissionRequired 表示缺少启动所需的有效准入凭据。
var (
	ErrAdmissionRequired          = errors.New("trusted admission evidence is required")
	ErrSchedulerFence             = errors.New("scheduler leadership fence is stale or unrecovered")
	ErrSchedulerPolicy            = errors.New("invalid or stale scheduler policy")
	ErrSchedulerCapacity          = errors.New("scheduler capacity is exhausted or unknown")
	ErrSchedulerAdmissionConflict = errors.New("scheduler admission conflicts with durable state")
)

// SchedulerFence 标识当前唯一调度任期，不代表执行已经发生。
type SchedulerFence struct {
	OwnerID string
	Epoch   int64
}

// PolicyChange 绑定一次 Operator 政策变更及其审计，不保存原始批准凭据。
type PolicyChange struct {
	IdempotencyKey, RequestFingerprint, ActorRef, Reason string
	Now                                                  time.Time
}

// SchedulerLeader 通过租约与 revision CAS 隔离旧调度器；恢复完成前禁止准入。
type SchedulerLeader struct {
	Fence      SchedulerFence
	LeaseUntil time.Time
	Revision   int64
	Recovered  bool
	Cursor     string
}

// SchedulerPolicy 是 Operator 批准的资源边界；动态容量只能进一步收紧。
type SchedulerPolicy struct {
	ID                string
	Scope             string
	ScopeID           string
	MaxConcurrent     int64
	CPURecoverPercent int64
	CPUSoftPercent    int64
	CPUHardPercent    int64
	RAMSoftPercent    int64
	RAMHardPercent    int64
	RAMFloorBytes     int64
	Revision          int64
}

// DefaultSchedulerPolicy 返回全局或单 Harness 的保守初始策略。
func DefaultSchedulerPolicy(harness AgentHarness) SchedulerPolicy {
	p := SchedulerPolicy{ID: "global", Scope: "global", MaxConcurrent: SchedulerDefaultGlobalCap,
		CPURecoverPercent: 65, CPUSoftPercent: 75, CPUHardPercent: 85, RAMSoftPercent: 25, RAMHardPercent: 15, RAMFloorBytes: 2 << 30, Revision: 1}
	if harness != "" {
		p.ID = "adapter:" + string(harness)
		p.Scope = "adapter"
		p.ScopeID = string(harness)
		p.MaxConcurrent = SchedulerDefaultAdapterCap
		p.Revision = 0
	}
	return p
}

// Validate 拒绝可越过硬上限、阈值次序及作用域边界的策略。
func (p SchedulerPolicy) Validate() error {
	if p.MaxConcurrent < 1 || p.MaxConcurrent > SchedulerHardCap || p.Revision < 0 ||
		p.CPURecoverPercent <= 0 || p.CPURecoverPercent >= p.CPUSoftPercent || p.CPUSoftPercent >= p.CPUHardPercent || p.CPUHardPercent > 100 ||
		p.RAMHardPercent < 0 || p.RAMHardPercent >= p.RAMSoftPercent || p.RAMSoftPercent >= 100 || p.RAMFloorBytes < 0 {
		return ErrSchedulerPolicy
	}
	if p.Scope == "global" && p.ScopeID == "" && p.ID == "global" {
		return nil
	}
	if p.Scope == "adapter" && p.ScopeID != "" && p.ID == "adapter:"+p.ScopeID && (AgentHarness(p.ScopeID).IsKnown() || AgentHarness(p.ScopeID) == HarnessFake) {
		return nil
	}
	return ErrSchedulerPolicy
}

// ResourceSample 保留 CPU 差分证据，首样、回退及过期都不能代表空闲。
type ResourceSample struct {
	ObservedAt, PreviousAt                      time.Time
	TotalBytes, AvailableBytes                  uint64
	CPUTotalTicks, CPUBusyTicks                 uint64
	PreviousCPUTotalTicks, PreviousCPUBusyTicks uint64
	ReasonCode                                  string
}

// AdmissionEvidence 仅由可信服务提供；Store 重新读取策略、余额及资源占用。
type AdmissionEvidence struct {
	Fence                            SchedulerFence
	ExpectedPolicyRevisions          map[string]int64
	Resources                        ResourceSample
	Capability                       HarnessCapabilitySnapshot
	EffectiveGlobalCap               int64
	ReservationNanos                 *int64
	ReservationBasis, ReservationRef string
	Now                              time.Time
}

// SchedulerAdmission 是执行身份的持久占位；UNKNOWN 不因 TTL 释放。
type SchedulerAdmission struct {
	ID                                  string
	AttemptID                           WorkflowAttemptID
	WorkflowID                          WorkflowID
	TaskID                              WorkflowTaskID
	SessionID                           SessionID
	ControllerGeneration                string
	ProjectID                           ProjectID
	Harness                             AgentHarness
	Mode                                SessionMode
	ClientRequestID, RequestFingerprint string
	Fence                               SchedulerFence
	State                               string
	Revision                            int64
	CreatedAt, UpdatedAt                time.Time
}

// AdmissionReserved 表示资源已预留，尚不能推定执行已开始。
const (
	AdmissionReserved  = "reserved"
	AdmissionActive    = "active"
	AdmissionUncertain = "uncertain"
	AdmissionReleased  = "released"
)

// ClaimAdmissionInput 将资源、预算预留与原有 Task CAS 放入同一事务。
type ClaimAdmissionInput struct {
	Attempt                                        WorkflowAttempt
	ExpectedTaskRevision, ExpectedWorkflowRevision int64
	IdempotencyKey, RequestFingerprint             string
	Evidence                                       AdmissionEvidence
}

// SessionAdmissionInput 供所有手动 Session 启动入口使用稳定请求键。
type SessionAdmissionInput struct {
	ProjectID                          ProjectID
	Harness                            AgentHarness
	Mode                               SessionMode
	ClientRequestID, ClientRequestHash string
	Evidence                           AdmissionEvidence
}

// SchedulerDecision 只保存非秘密原因码和策略版本，不记录租约或原始能力凭据。
type SchedulerDecision struct {
	ID              int64
	AdmissionID     string
	WorkflowID      WorkflowID
	TaskID          WorkflowTaskID
	Fence           SchedulerFence
	ReasonCode      string
	PolicyRevisions map[string]int64
	CreatedAt       time.Time
}

// SchedulerWorkflowState 持久化公平轮转的 deficit 与暂停事实。
type SchedulerWorkflowState struct {
	WorkflowID      WorkflowID
	CandidateOffset int64
	Deficit         int64
	Weight          int64
	Paused          bool
	Revision        int64
}

// SchedulerRetryDue 保留已核验可重试任务的最早时间，不推断 UNKNOWN 可重试。
type SchedulerRetryDue struct {
	AttemptID      WorkflowAttemptID
	Harness        AgentHarness
	WorkflowID     WorkflowID
	TaskID         WorkflowTaskID
	NextEligibleAt time.Time
	ReasonCode     string
	PolicyRevision int64
}

// ValidSchedulerReason 限制审计为机器原因码，避免任意秘密文本进入 CDC。
func ValidSchedulerReason(reason string) bool {
	if reason == "" || len(reason) > 80 {
		return false
	}
	return strings.IndexFunc(reason, func(r rune) bool { return (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' }) < 0
}
