package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// SetWorkflowQualityPolicy 只接收服务端已批准策略；配置 CAS 与当前任期共同防止并发覆盖。
func (s *Store) SetWorkflowQualityPolicy(ctx context.Context, w domain.WorkflowID, p domain.GatePolicy, revision int64, actor string, f domain.SchedulerFence, now time.Time) (domain.WorkflowQualityPolicy, error) {
	if actor == "" || p.ID == "" || p.Revision < 1 || len(p.Rules) == 0 || p.ValidFor <= 0 || p.MaxFixes < 1 || p.MaxFixes > 3 {
		return domain.WorkflowQualityPolicy{}, domain.ErrDeliveryBlocked
	}
	data, err := json.Marshal(p)
	if err != nil {
		return domain.WorkflowQualityPolicy{}, err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err = s.inSchedulerTx(ctx, "set workflow quality policy", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, f, now, true); err != nil {
			return err
		}
		pending, err := q.HasPendingTaskCommit(ctx, string(w))
		if err != nil {
			return err
		}
		if pending {
			return domain.ErrDeliveryBlocked
		}
		var n int64
		if revision == 0 {
			n, err = q.SetWorkflowQualityPolicy(ctx, gen.SetWorkflowQualityPolicyParams{WorkflowID: string(w), PolicyJson: string(data), PolicyDigest: p.Digest(), ActorRef: actor, UpdatedAt: now.UTC()})
		} else {
			n, err = q.UpdateWorkflowQualityPolicy(ctx, gen.UpdateWorkflowQualityPolicyParams{WorkflowID: string(w), PolicyJson: string(data), PolicyDigest: p.Digest(), ActorRef: actor, UpdatedAt: now.UTC(), ExpectedRevision: revision})
		}
		if err != nil {
			return err
		}
		if n != 1 {
			return domain.ErrArtifactStale
		}
		return nil
	})
	return domain.WorkflowQualityPolicy{WorkflowID: w, Policy: p, Digest: p.Digest(), Revision: revision + 1}, err
}

// GetWorkflowQualityPolicy 读取唯一策略关联；没有策略的历史工作流保持原 M3 语义。
func (s *Store) GetWorkflowQualityPolicy(ctx context.Context, w domain.WorkflowID) (domain.WorkflowQualityPolicy, bool, error) {
	r, err := s.qr.GetWorkflowQualityPolicy(ctx, string(w))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.WorkflowQualityPolicy{}, false, nil
	}
	if err != nil {
		return domain.WorkflowQualityPolicy{}, false, err
	}
	p, err := qualityPolicyFromRow(r)
	return p, err == nil, err
}

// qualityPolicyFromRow 重新验证完整策略摘要，配置损坏或同版本变更不能复用证据。
func qualityPolicyFromRow(r gen.WorkflowQualityPolicy) (domain.WorkflowQualityPolicy, error) {
	p := domain.WorkflowQualityPolicy{WorkflowID: domain.WorkflowID(r.WorkflowID), Digest: r.PolicyDigest, Revision: r.Revision}
	if err := json.Unmarshal([]byte(r.PolicyJson), &p.Policy); err != nil {
		return p, err
	}
	if p.Policy.Digest() != p.Digest {
		return p, domain.ErrArtifactInvalid
	}
	return p, nil
}
