package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// gateIdentity 在 writer 内固定当前策略和工件；长进程结束后重新取许可但不能跨任期/代际。
func gateIdentity(ctx context.Context, q *gen.Queries, r domain.GateRun, now time.Time) (domain.GatePolicy, error) {
	row, err := q.GetArtifact(ctx, r.ArtifactID)
	if err != nil {
		return domain.GatePolicy{}, err
	}
	a, err := artifactFromRow(row)
	if err != nil {
		return domain.GatePolicy{}, err
	}
	if a.State != "verified" || a.Fingerprint != r.ArtifactFingerprint {
		return domain.GatePolicy{}, domain.ErrArtifactStale
	}
	p, err := loadExecutionPermit(ctx, q, a.AttemptID)
	if err != nil {
		return domain.GatePolicy{}, err
	}
	if err := validateArtifactIdentity(ports.WithExecutionPermit(ctx, p), q, a, r.Fence, now); err != nil {
		return domain.GatePolicy{}, err
	}
	policyRow, err := q.GetWorkflowQualityPolicy(ctx, string(a.WorkflowID))
	if err != nil {
		return domain.GatePolicy{}, err
	}
	policy, err := qualityPolicyFromRow(policyRow)
	if err != nil {
		return policy.Policy, err
	}
	if policy.Digest != r.PolicyDigest {
		return policy.Policy, domain.ErrArtifactStale
	}
	return policy.Policy, nil
}

// BeginGateRun 持久记录运行意图；同 ID 的 RUNNING 不允许重复启动，调用方必须保守恢复。
func (s *Store) BeginGateRun(ctx context.Context, r domain.GateRun, now time.Time) (domain.GateRun, bool, error) {
	if r.ID == "" || len(r.Fingerprint) != 64 {
		return r, false, domain.ErrArtifactInvalid
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	created := false
	err := s.inSchedulerTx(ctx, "begin quality gate", func(q *gen.Queries) error {
		p, err := gateIdentity(ctx, q, r, now)
		if err != nil {
			return err
		}
		old, err := q.GetGateRun(ctx, r.ID)
		if err == nil {
			if old.ArtifactID != r.ArtifactID || old.Fingerprint != r.Fingerprint || old.PolicyDigest != r.PolicyDigest || old.OwnerID != r.Fence.OwnerID || old.Epoch != r.Fence.Epoch {
				return domain.ErrArtifactStale
			}
			r, err = gateFromRow(old)
			return err
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		unresolved, err := q.HasUnresolvedGateRun(ctx, r.ArtifactID)
		if err != nil {
			return err
		}
		if unresolved {
			return domain.ErrDeliveryBlocked
		}
		artifactRow, err := q.GetArtifact(ctx, r.ArtifactID)
		if err != nil {
			return err
		}
		pending, err := q.HasPendingTaskCommit(ctx, artifactRow.WorkflowID)
		if err != nil {
			return err
		}
		if pending {
			return domain.ErrDeliveryBlocked
		}
		r.CreatedAt, r.ExpiresAt, r.Revision, r.State = now.UTC(), now.Add(p.ValidFor).UTC(), 1, "RUNNING"
		if err := q.InsertGateRun(ctx, gen.InsertGateRunParams{ID: r.ID, ArtifactID: r.ArtifactID, ArtifactFingerprint: r.ArtifactFingerprint, PolicyDigest: r.PolicyDigest, Fingerprint: r.Fingerprint, OwnerID: r.Fence.OwnerID, Epoch: r.Fence.Epoch, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}); err != nil {
			return err
		}
		created = true
		return nil
	})
	return r, created, err
}

// FinishGateRun 逐规则核对真实结果并 CAS；缺失规则或异常 SKIPPED 永远不能汇总成 PASS。
func (s *Store) FinishGateRun(ctx context.Context, id string, revision int64, fingerprint string, results []domain.GateResult, f domain.SchedulerFence, now time.Time) (domain.GateRun, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	var result domain.GateRun
	err := s.inSchedulerTx(ctx, "finish quality gate", func(q *gen.Queries) error {
		row, err := q.GetGateRun(ctx, id)
		if err != nil {
			return err
		}
		r, err := gateFromRow(row)
		if err != nil {
			return err
		}
		if r.Fence != f || r.Fingerprint != fingerprint || r.Revision != revision || r.State != "RUNNING" {
			return domain.ErrArtifactStale
		}
		p, err := gateIdentity(ctx, q, r, now)
		if err != nil {
			return err
		}
		state := "PASS"
		passed := 0
		artifactRow, err := q.GetArtifact(ctx, r.ArtifactID)
		if err != nil {
			return err
		}
		snapshot, err := artifactFromRow(artifactRow)
		if err != nil {
			return err
		}
		if !r.ExpiresAt.After(now) || len(results) != len(p.Rules) {
			return domain.ErrArtifactStale
		}
		for i, rule := range p.Rules {
			rr := results[i]
			if rr.RuleID != rule.ID || len(rr.Log) > rule.MaxLogBytes {
				return domain.ErrArtifactInvalid
			}
			switch rr.State {
			case "PASS":
				if rr.ExitCode == nil || *rr.ExitCode != 0 || rr.LogDigest == "" {
					return domain.ErrDeliveryBlocked
				}
				passed++
			case "SKIPPED":
				if rr.Reason == "" || rr.Reason != rule.SkipReason(snapshot) {
					return domain.ErrDeliveryBlocked
				}
			case "FAIL", "BLOCKED", "UNVERIFIED":
				if rule.Required || rr.State == "UNVERIFIED" {
					state = rr.State
				}
			default:
				return domain.ErrDeliveryBlocked
			}
		}
		if passed == 0 && state == "PASS" {
			state = "BLOCKED"
		}
		data, _ := json.Marshal(results)
		n, err := q.FinishGateRun(ctx, gen.FinishGateRunParams{ID: id, ExpectedRevision: revision, OwnerID: f.OwnerID, Epoch: f.Epoch, State: state, ResultsJson: string(data)})
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrArtifactStale
		}
		r.State, r.Results, r.Revision = state, results, revision+1
		result = r
		return nil
	})
	return result, err
}

// GetGateRun 读取持久证据；调用方仍须校验当前策略、工件和有效期。
func (s *Store) GetGateRun(ctx context.Context, id string) (domain.GateRun, bool, error) {
	r, err := s.qr.GetGateRun(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.GateRun{}, false, nil
	}
	if err != nil {
		return domain.GateRun{}, false, err
	}
	v, err := gateFromRow(r)
	return v, err == nil, err
}

// ListGateRuns 用于只读协调视图，原始日志不得进入 CDC。
func (s *Store) ListGateRuns(ctx context.Context, w domain.WorkflowID) ([]domain.GateRun, error) {
	rows, err := s.qr.ListGateRuns(ctx, string(w))
	if err != nil {
		return nil, err
	}
	out := make([]domain.GateRun, 0, len(rows))
	for _, r := range rows {
		v, err := gateFromRow(r)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// gateFromRow 只恢复持久结果，不因重启将 RUNNING 升为通过。
func gateFromRow(r gen.WorkflowGateRun) (domain.GateRun, error) {
	v := domain.GateRun{ID: r.ID, ArtifactID: r.ArtifactID, ArtifactFingerprint: r.ArtifactFingerprint, PolicyDigest: r.PolicyDigest, Fingerprint: r.Fingerprint, State: r.State, Fence: domain.SchedulerFence{OwnerID: r.OwnerID, Epoch: r.Epoch}, Revision: r.Revision, CreatedAt: r.CreatedAt, ExpiresAt: r.ExpiresAt}
	err := json.Unmarshal([]byte(r.ResultsJson), &v.Results)
	return v, err
}
