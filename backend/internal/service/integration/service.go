// Package integration 在既有质量证据之后生成多仓预演，再由 Operator 精确审批。
package integration

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/principal"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/qualitygate"
)

// Store 只读取服务端工件、任务提交和预演台账，不接受外部仓库路径或自报 SHA。
type Store interface {
	GetArtifact(context.Context, string) (domain.ArtifactSnapshot, bool, error)
	GetArtifactTaskCommit(context.Context, string) (domain.TaskCommit, bool, error)
	GetGateRun(context.Context, string) (domain.GateRun, bool, error)
	GetIntegrationPlan(context.Context, string) (domain.IntegrationPlan, bool, error)
	ListIntegrationRepoPreviews(context.Context, string) ([]domain.IntegrationRepoPreview, error)
	ValidateIntegrationArtifacts(context.Context, domain.WorkflowID, []string, domain.SchedulerFence, time.Time) error
	CreateIntegrationPlan(context.Context, domain.IntegrationPlan, []domain.IntegrationRepoPreview, domain.SchedulerFence, time.Time) (domain.IntegrationPlan, error)
}

// Service 在专用 Worktree 预演，完整结果落库后才允许后续批准。
type Service struct {
	Store     Store
	Gates     *qualitygate.Service
	Scratch   string
	Clock     func() time.Time
	Approvals *principal.ApprovalStore
	// BareRemotes 仅由可信宿主注入；daemon 默认空表，不开放真实网络传输或路径注册 API。
	BareRemotes map[string]string
}

// Preview 校验每个 Attempt 的完整仓库集合；重复请求返回同一冻结计划，不重建预演。
func (s *Service) Preview(ctx context.Context, planID string, workflowID domain.WorkflowID, artifactIDs []string, fence domain.SchedulerFence) (domain.IntegrationPlan, []domain.IntegrationRepoPreview, error) {
	if planID == "" || workflowID == "" || len(artifactIDs) == 0 || len(artifactIDs) > 32 || s.Store == nil || s.Gates == nil || s.Clock == nil || s.Scratch == "" {
		return domain.IntegrationPlan{}, nil, domain.ErrArtifactInvalid
	}
	ids := slices.Clone(artifactIDs)
	slices.Sort(ids)
	old, found, err := s.Store.GetIntegrationPlan(ctx, planID)
	if err != nil {
		return old, nil, err
	}
	if found {
		entries, err := s.Store.ListIntegrationRepoPreviews(ctx, planID)
		if err != nil {
			return old, nil, err
		}
		oldIDs := make([]string, 0, len(entries))
		for _, e := range entries {
			oldIDs = append(oldIDs, e.ArtifactID)
		}
		slices.Sort(oldIDs)
		if old.WorkflowID != workflowID || !slices.Equal(ids, oldIDs) || old.PlanDigest != old.Digest(entries) {
			return old, nil, domain.ErrArtifactStale
		}
		return old, entries, nil
	}
	if err := s.Store.ValidateIntegrationArtifacts(ctx, workflowID, ids, fence, s.Clock()); err != nil {
		return domain.IntegrationPlan{}, nil, err
	}
	previews := make([]domain.IntegrationRepoPreview, 0, len(ids))
	seen := make(map[domain.WorkflowRepoKey]bool, len(ids))
	for _, artifactID := range ids {
		a, found, err := s.Store.GetArtifact(ctx, artifactID)
		if err != nil {
			return domain.IntegrationPlan{}, nil, err
		}
		if !found || a.WorkflowID != workflowID || a.State != "verified" || seen[a.Repo.RepoKey] {
			return domain.IntegrationPlan{}, nil, domain.ErrArtifactStale
		}
		commit, found, err := s.Store.GetArtifactTaskCommit(ctx, artifactID)
		if err != nil {
			return domain.IntegrationPlan{}, nil, err
		}
		if !found || commit.State != "committed" || commit.ArtifactFingerprint != a.Fingerprint || commit.ArtifactRevision != a.ArtifactRevision {
			return domain.IntegrationPlan{}, nil, domain.ErrDeliveryBlocked
		}
		seen[a.Repo.RepoKey] = true
		gate, found, err := s.Store.GetGateRun(ctx, commit.GateID)
		if err != nil {
			return domain.IntegrationPlan{}, nil, err
		}
		if !found {
			return domain.IntegrationPlan{}, nil, domain.ErrDeliveryBlocked
		}
		if err := s.Gates.RevalidatePolicy(ctx, a, gate); err != nil {
			return domain.IntegrationPlan{}, nil, err
		}
		result, err := gitworktree.PreviewDelivery(ctx, a, commit.CommitSHA, s.Scratch)
		if err != nil {
			return domain.IntegrationPlan{}, nil, err
		}
		if result.ExpectedTargetSHA != commit.TargetSHA {
			return domain.IntegrationPlan{}, nil, domain.ErrArtifactStale
		}
		result.PlanID, result.TaskCommitID, result.PolicyDigest = planID, commit.ID, commit.PolicyDigest
		result.PreviewDigest = result.Digest()
		previews = append(previews, result)
	}
	slices.SortFunc(previews, func(a, b domain.IntegrationRepoPreview) int {
		return cmp.Compare(a.RepoKey, b.RepoKey)
	})
	status := "clean"
	for _, preview := range previews {
		if preview.Status == "conflict" {
			status = "conflict"
		}
		a, found, err := s.Store.GetArtifact(ctx, preview.ArtifactID)
		if err != nil {
			return domain.IntegrationPlan{}, nil, err
		}
		if !found {
			return domain.IntegrationPlan{}, nil, domain.ErrArtifactStale
		}
		if err := gitworktree.VerifyIntegrationPreview(ctx, a, preview); err != nil {
			return domain.IntegrationPlan{}, nil, err
		}
	}
	plan := domain.IntegrationPlan{ID: planID, WorkflowID: workflowID, Status: status, Revision: 1, CreatedAt: s.Clock().UTC()}
	plan.PlanDigest = plan.Digest(previews)
	plan, err = s.Store.CreateIntegrationPlan(ctx, plan, previews, fence, s.Clock().UTC())
	return plan, previews, err
}
