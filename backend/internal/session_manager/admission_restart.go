package sessionmanager

import (
	"context"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// sessionRestartStore 是真实持久化准入边界；旧纯内存替身不伪造数据库占位。
type sessionRestartStore interface {
	AdmitSessionRestart(context.Context, domain.SessionRecord, string, domain.AdmissionEvidence) error
}

// admitSessionRestart 覆盖 Restore、Resume 和自动重连；真实 Store 缺少准入证据时闭锁。
// Workflow 复用现有内部许可，普通 Session 由 daemon 提供新证据。
func (m *Manager) admitSessionRestart(ctx context.Context, rec domain.SessionRecord, generation string) error {
	store, ok := m.store.(sessionRestartStore)
	if !ok {
		return nil
	}
	var evidence domain.AdmissionEvidence
	if p, ok := ports.ExecutionPermitFromContext(ctx); ok && p.SessionID == rec.ID {
		evidence = domain.AdmissionEvidence{Fence: p.Fence, Now: m.clock().UTC()}
	} else {
		if m.admission == nil {
			return domain.ErrAdmissionRequired
		}
		var err error
		evidence, err = m.admission.PrepareSessionAdmission(ctx, ports.SpawnConfig{ProjectID: rec.ProjectID, Harness: rec.Harness, Kind: rec.Kind, RequestedMode: domain.NormalizeSessionMode(rec.Mode)})
		if err != nil {
			return err
		}
		evidence.ReplacingSessionID = rec.ID
	}
	return store.AdmitSessionRestart(ctx, rec, generation, evidence)
}

// restartRecord 返回最近准备阶段已持久化的同代际快照；其他 Controller 替换不会被刷新隐藏。
func (m *Manager) restartRecord(ctx context.Context, expected domain.SessionRecord) (domain.SessionRecord, error) {
	if _, ok := m.store.(sessionRestartStore); !ok {
		return expected, nil
	}
	current, err := m.getRecord(ctx, expected.ID)
	if err != nil {
		return domain.SessionRecord{}, err
	}
	if current.IsTerminated != expected.IsTerminated || current.Harness != expected.Harness || current.Mode != expected.Mode || domain.SessionExecutionGeneration(current) != domain.SessionExecutionGeneration(expected) {
		return domain.SessionRecord{}, domain.ErrSchedulerAdmissionConflict
	}
	return current, nil
}
