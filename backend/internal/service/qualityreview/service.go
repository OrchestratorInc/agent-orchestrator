// Package qualityreview 接受可信独立审查者的结构化回执；人工结论也不能替代 Gate 的真实退出码。
package qualityreview

import (
	"context"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Store 将身份与 CAS 校验留在持久 writer 事务，服务层只负责可信请求与实际 Git 观察。
type Store interface {
	GetArtifact(context.Context, string) (domain.ArtifactSnapshot, bool, error)
	GetWorkflowQualityPolicy(context.Context, domain.WorkflowID) (domain.WorkflowQualityPolicy, bool, error)
	GetQualityReview(context.Context, string) (domain.QualityReview, bool, error)
	BeginQualityReview(context.Context, domain.QualityReview, time.Time) (domain.QualityReview, error)
	FinishQualityReview(context.Context, string, int64, domain.QualityReviewActor, domain.ReviewVerdict, []domain.QualityFinding, int64, domain.SchedulerFence, time.Time) (domain.QualityReview, error)
}

// Service 的策略来自服务端可信装配；HTTP 不能指定修复上限、Reviewer 身份或任期。
type Service struct {
	Store     Store
	Artifacts *artifact.Service
	Clock     func() time.Time
}

// reviewActor 从已验证的 principal 派生身份，Agent capability 不能退化为人工授权。
func reviewActor(c principal.Caller) (domain.QualityReviewActor, error) {
	if c.IsOperator() && c.OperatorTokenFingerprint != "" {
		return domain.QualityReviewActor{OperatorRef: c.OperatorTokenFingerprint}, nil
	}
	if c.IsAgent() && c.LeaseProven && c.ExecutionPermit != nil {
		p := c.ExecutionPermit
		if p.AttemptID == c.AttemptID && p.SessionID == c.SessionID && p.Generation != "" {
			return domain.QualityReviewActor{AttemptID: p.AttemptID, SessionID: p.SessionID, Generation: p.Generation}, nil
		}
	}
	return domain.QualityReviewActor{}, domain.ErrDeliveryBlocked
}

// Begin 冻结实际代码与目标 SHA，独立身份仍由 writer 二次核验，不启动任何 Provider。
func (s *Service) Begin(ctx context.Context, id, artifactID string, caller principal.Caller, fence domain.SchedulerFence) (domain.QualityReview, error) {
	actor, err := reviewActor(caller)
	if err != nil {
		return domain.QualityReview{}, err
	}
	a, found, err := s.Store.GetArtifact(ctx, artifactID)
	if err != nil {
		return domain.QualityReview{}, err
	}
	if !found || a.State != "verified" {
		return domain.QualityReview{}, domain.ErrArtifactStale
	}
	if err := s.Artifacts.Revalidate(ctx, a); err != nil {
		return domain.QualityReview{}, err
	}
	target, err := gitworktree.DeliveryTargetSHA(ctx, a)
	if err != nil {
		return domain.QualityReview{}, err
	}
	p, found, err := s.Store.GetWorkflowQualityPolicy(ctx, a.WorkflowID)
	if err != nil {
		return domain.QualityReview{}, err
	}
	if !found {
		return domain.QualityReview{}, domain.ErrDeliveryBlocked
	}
	return s.Store.BeginQualityReview(ctx, domain.QualityReview{ID: id, ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyRevision: p.Policy.Revision, PolicyDigest: p.Digest, TargetSHA: target, Actor: actor, Fence: fence}, s.Clock())
}

// Finish 拒绝旧策略、旧目标、旧工件或旧审查身份；两轮修复用尽后持久化 blocked 并升级给操作者。
func (s *Service) Finish(ctx context.Context, id string, revision int64, verdict domain.ReviewVerdict, findings []domain.QualityFinding, caller principal.Caller, fence domain.SchedulerFence) (domain.QualityReview, error) {
	actor, err := reviewActor(caller)
	if err != nil {
		return domain.QualityReview{}, err
	}
	r, found, err := s.Store.GetQualityReview(ctx, id)
	if err != nil {
		return r, err
	}
	if !found || r.Actor != actor || r.Fence != fence {
		return r, domain.ErrArtifactStale
	}
	a, found, err := s.Store.GetArtifact(ctx, r.ArtifactID)
	if err != nil {
		return r, err
	}
	if !found || a.State != "verified" || a.Fingerprint != r.ArtifactFingerprint {
		return r, domain.ErrArtifactStale
	}
	if err := s.Artifacts.Revalidate(ctx, a); err != nil {
		return r, err
	}
	target, err := gitworktree.DeliveryTargetSHA(ctx, a)
	if err != nil {
		return r, err
	}
	if target != r.TargetSHA {
		return r, domain.ErrArtifactStale
	}
	p, found, err := s.Store.GetWorkflowQualityPolicy(ctx, a.WorkflowID)
	if err != nil {
		return r, err
	}
	if !found || p.Digest != r.PolicyDigest {
		return r, domain.ErrArtifactStale
	}
	return s.Store.FinishQualityReview(ctx, id, revision, actor, verdict, findings, p.Policy.MaxFixes, fence, s.Clock())
}
