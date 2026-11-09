package sessionmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SessionAdmissionProvider 由 daemon 注入可信资源、能力和预算证据，调用方不能自报余额。
type SessionAdmissionProvider interface {
	PrepareSessionAdmission(context.Context, ports.SpawnConfig) (domain.AdmissionEvidence, error)
}

// sessionAdmissionStore 复用原子准入及 Session 消费事务，所有启动路径共用此边界。
type sessionAdmissionStore interface {
	ReserveSessionAdmission(context.Context, domain.SessionAdmissionInput) (domain.SchedulerAdmission, bool, error)
	GetSchedulerAdmission(context.Context, string) (domain.SchedulerAdmission, bool, error)
	CreateAdmittedSession(context.Context, domain.SessionRecord, domain.SchedulerAdmission, domain.SessionID, time.Time) (domain.SessionRecord, bool, error)
}

// SetSessionAdmissionProvider 在 daemon 启动服务前接入统一 Admission，不允许运行时切换。
func (m *Manager) SetSessionAdmissionProvider(provider SessionAdmissionProvider) {
	m.admission = provider
}

// prepareSpawnAdmission 为普通、client request、automation 与预创建路径统一占位。
// 工作流仅复用内部许可；不存在许可时禁止降级成普通会话重新占槽。
func (m *Manager) prepareSpawnAdmission(ctx context.Context, cfg *ports.SpawnConfig) (*domain.SchedulerAdmission, error) {
	store, ok := m.store.(sessionAdmissionStore)
	if !ok {
		return nil, nil
	}
	if cfg.WorkflowSpawn != nil {
		permit, ok := ports.ExecutionPermitFromContext(ctx)
		if !ok || permit.AttemptID != cfg.WorkflowSpawn.AttemptID {
			return nil, domain.ErrAdmissionRequired
		}
		ad, found, err := store.GetSchedulerAdmission(ctx, permit.AdmissionID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, domain.ErrAdmissionRequired
		}
		return &ad, nil
	}
	if m.admission == nil {
		return nil, domain.ErrAdmissionRequired
	}
	if cfg.ClientRequestID == "" {
		cfg.ClientRequestID = "session-spawn-" + uuid.NewString()
		if cfg.AutomationRunID != nil {
			cfg.ClientRequestID = "automation-spawn-" + string(*cfg.AutomationRunID)
		}
		body, err := json.Marshal(cfg)
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		cfg.ClientRequestHash = hex.EncodeToString(sum[:])
	}
	evidence, err := m.admission.PrepareSessionAdmission(ctx, *cfg)
	if err != nil {
		return nil, err
	}
	if cfg.ClientRequestHash == "" {
		return nil, domain.ErrSchedulerAdmissionConflict
	}
	ad, _, err := store.ReserveSessionAdmission(ctx, domain.SessionAdmissionInput{ProjectID: cfg.ProjectID, Harness: cfg.Harness, Mode: cfg.RequestedMode, ClientRequestID: cfg.ClientRequestID, ClientRequestHash: domain.SessionAdmissionFingerprint(cfg.ClientRequestHash), Evidence: evidence})
	if err != nil {
		return nil, err
	}
	return &ad, nil
}

// createAdmittedSession 始终使用 Store 的事务接口；fresh=false 绝不继续启动。
func (m *Manager) createAdmittedSession(ctx context.Context, rec domain.SessionRecord, ad *domain.SchedulerAdmission, preparationID domain.SessionID) (domain.SessionRecord, bool, error) {
	store, ok := m.store.(sessionAdmissionStore)
	if !ok {
		return domain.SessionRecord{}, false, domain.ErrAdmissionRequired
	}
	return store.CreateAdmittedSession(ctx, rec, *ad, preparationID, m.clock().UTC())
}
