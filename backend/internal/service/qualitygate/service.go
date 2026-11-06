// Package qualitygate 将可信策略、冻结交付树与真实进程结果连接到质量侧车，不启动 Agent 或结算任务。
package qualitygate

import (
	"context"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/testgate"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/artifact"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
)

// Store 的 writer 校验重新读取当前执行事实，绝不复用启动时已经过期的 permit。
type Store interface {
	GetArtifact(context.Context, string) (domain.ArtifactSnapshot, bool, error)
	SetWorkflowQualityPolicy(context.Context, domain.WorkflowID, domain.GatePolicy, int64, string, domain.SchedulerFence, time.Time) (domain.WorkflowQualityPolicy, error)
	GetWorkflowQualityPolicy(context.Context, domain.WorkflowID) (domain.WorkflowQualityPolicy, bool, error)
	BeginGateRun(context.Context, domain.GateRun, time.Time) (domain.GateRun, bool, error)
	FinishGateRun(context.Context, string, int64, string, []domain.GateResult, domain.SchedulerFence, time.Time) (domain.GateRun, error)
}

// Service 的 Registry 只由 daemon 可信配置装配；外部请求没有传入 argv/env 的入口。
type Service struct {
	Store     Store
	Artifacts *artifact.Service
	Registry  map[string]domain.GatePolicy
	Scratch   string
	Clock     func() time.Time
}

// RevalidateEvidence 在 Git 写入前检查完整规则、工具字节和工件；数据库 PASS 本身不能延长证据有效性。
func (s *Service) RevalidateEvidence(ctx context.Context, a domain.ArtifactSnapshot, run domain.GateRun) error {
	if err := s.RevalidatePolicy(ctx, a, run); err != nil {
		return err
	}
	return s.Artifacts.Revalidate(ctx, a)
}

// RevalidatePolicy 核验可信策略和工具字节；提交后的原索引与工作区摘要由 Git 适配器核验。
func (s *Service) RevalidatePolicy(ctx context.Context, a domain.ArtifactSnapshot, run domain.GateRun) error {
	if run.ArtifactID != a.ID || run.ArtifactFingerprint != a.Fingerprint || run.State != "PASS" || !run.ExpiresAt.After(s.Clock()) {
		return domain.ErrArtifactStale
	}
	p, found, err := s.Store.GetWorkflowQualityPolicy(ctx, a.WorkflowID)
	if err != nil {
		return err
	}
	if !found || p.Digest != run.PolicyDigest {
		return domain.ErrArtifactStale
	}
	trusted, ok := s.Registry[p.Policy.ID]
	if !ok || trusted.Digest() != p.Digest {
		return domain.ErrDeliveryBlocked
	}
	fingerprint, err := testgate.Fingerprint(a, p.Policy)
	if err != nil {
		return err
	}
	if fingerprint != run.Fingerprint {
		return domain.ErrArtifactStale
	}
	return nil
}

// Configure 仅允许现有 OS 人工审批解析出的 Operator，选择已审核策略并执行 revision CAS。
func (s *Service) Configure(ctx context.Context, w domain.WorkflowID, id string, revision int64, caller principal.Caller, f domain.SchedulerFence) (domain.WorkflowQualityPolicy, error) {
	if !caller.IsOperator() || caller.OperatorTokenFingerprint == "" {
		return domain.WorkflowQualityPolicy{}, domain.ErrDeliveryBlocked
	}
	p, ok := s.Registry[id]
	if !ok {
		return domain.WorkflowQualityPolicy{}, domain.ErrDeliveryBlocked
	}
	if err := testgate.ValidatePolicy(p); err != nil {
		return domain.WorkflowQualityPolicy{}, err
	}
	return s.Store.SetWorkflowQualityPolicy(ctx, w, p, revision, caller.OperatorTokenFingerprint, f, s.Clock())
}

// Run 持久意图后执行冻结树；重复 RUNNING 保持未验证，同指纹已完成结果才可复用。
func (s *Service) Run(ctx context.Context, id, artifactID string, f domain.SchedulerFence) (domain.GateRun, error) {
	a, found, err := s.Store.GetArtifact(ctx, artifactID)
	if err != nil {
		return domain.GateRun{}, err
	}
	if !found || a.State != "verified" {
		return domain.GateRun{}, domain.ErrArtifactStale
	}
	p, found, err := s.Store.GetWorkflowQualityPolicy(ctx, a.WorkflowID)
	if err != nil {
		return domain.GateRun{}, err
	}
	if !found {
		return domain.GateRun{}, domain.ErrDeliveryBlocked
	}
	approved, ok := s.Registry[p.Policy.ID]
	if !ok || approved.Digest() != p.Digest {
		return domain.GateRun{}, domain.ErrDeliveryBlocked
	}
	if err := s.Artifacts.Revalidate(ctx, a); err != nil {
		return domain.GateRun{}, err
	}
	fingerprint, err := testgate.Fingerprint(a, p.Policy)
	if err != nil {
		return domain.GateRun{}, err
	}
	r, created, err := s.Store.BeginGateRun(ctx, domain.GateRun{ID: id, ArtifactID: a.ID, ArtifactFingerprint: a.Fingerprint, PolicyDigest: p.Digest, Fingerprint: fingerprint, Fence: f}, s.Clock())
	if err != nil {
		return r, err
	}
	if !created {
		if r.State == "RUNNING" || !r.ExpiresAt.After(s.Clock()) {
			return r, domain.ErrDeliveryBlocked
		}
		return r, nil
	}
	dir, err := gitworktree.MaterializeDelivery(ctx, a, s.Scratch)
	if err != nil {
		return r, err
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()
	results := make([]domain.GateResult, 0, len(p.Policy.Rules))
	for _, rule := range p.Policy.Rules {
		if !cleanup {
			results = append(results, domain.GateResult{RuleID: rule.ID, State: "UNVERIFIED", Reason: "previous_rule_unverified", StartedAt: s.Clock(), FinishedAt: s.Clock()})
			continue
		}
		if reason := rule.SkipReason(a); reason != "" {
			results = append(results, domain.GateResult{RuleID: rule.ID, State: "SKIPPED", Reason: reason, StartedAt: s.Clock(), FinishedAt: s.Clock()})
			continue
		}
		result := testgate.Run(ctx, rule, dir)
		cleanup = cleanup && result.State != "UNVERIFIED"
		results = append(results, result)
	}
	if err := s.Artifacts.Revalidate(ctx, a); err != nil {
		return r, err
	}
	current, err := testgate.Fingerprint(a, p.Policy)
	if err != nil {
		return r, err
	}
	if current != fingerprint {
		return r, domain.ErrArtifactStale
	}
	if err := gitworktree.VerifyMaterializedDelivery(ctx, a, dir); err != nil {
		return r, err
	}
	return s.Store.FinishGateRun(ctx, id, r.Revision, fingerprint, results, f, s.Clock())
}
