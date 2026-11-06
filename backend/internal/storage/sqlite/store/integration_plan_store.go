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

// CreateIntegrationPlan 在一个 writer 事务核对完整仓库、可信提交和不可变预演，禁止部分保存。
func (s *Store) CreateIntegrationPlan(ctx context.Context, plan domain.IntegrationPlan, previews []domain.IntegrationRepoPreview, fence domain.SchedulerFence, now time.Time) (domain.IntegrationPlan, error) {
	if plan.ID == "" || plan.WorkflowID == "" || plan.Revision != 1 || len(previews) == 0 || plan.PlanDigest != plan.Digest(previews) {
		return plan, domain.ErrArtifactInvalid
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	err := s.inSchedulerTx(ctx, "create integration preview plan", func(q *gen.Queries) error {
		if err := schedulerFence(ctx, q, fence, now, true); err != nil {
			return err
		}
		old, err := q.GetIntegrationPlan(ctx, plan.ID)
		if err == nil {
			if old.WorkflowID != string(plan.WorkflowID) || old.PlanDigest != plan.PlanDigest {
				return domain.ErrArtifactStale
			}
			plan = integrationPlanFromRow(old)
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		ids := make([]string, 0, len(previews))
		for _, p := range previews {
			ids = append(ids, p.ArtifactID)
		}
		if err := integrationArtifacts(ctx, q, plan.WorkflowID, ids, fence, now); err != nil {
			return err
		}
		if err := q.InsertIntegrationPlan(ctx, gen.InsertIntegrationPlanParams{ID: plan.ID, WorkflowID: string(plan.WorkflowID), Status: plan.Status, Revision: plan.Revision, PlanDigest: plan.PlanDigest, CreatedAt: plan.CreatedAt.UTC()}); err != nil {
			return err
		}
		status := "clean"
		for i, p := range previews {
			if p.PlanID != plan.ID || p.Revision != 1 || p.MergeMethod != "merge" || !domain.ValidGitSHA(p.ExpectedTargetSHA) || !domain.ValidGitSHA(p.TaskCommitSHA) || p.PreviewDigest != p.Digest() || !domain.ValidGitSHA(p.TreeSHA) || len(p.DiffDigest) != 64 || (i > 0 && previews[i-1].RepoKey >= p.RepoKey) {
				return domain.ErrArtifactInvalid
			}
			switch p.Status {
			case "clean":
				if len(p.ConflictPaths) != 0 || !domain.ValidGitSHA(p.MergeCommitSHA) {
					return domain.ErrArtifactInvalid
				}
			case "conflict":
				if len(p.ConflictPaths) == 0 || p.MergeCommitSHA != "" {
					return domain.ErrArtifactInvalid
				}
				status = "conflict"
			default:
				return domain.ErrDeliveryBlocked
			}
			artifactRow, err := q.GetArtifact(ctx, p.ArtifactID)
			if err != nil {
				return err
			}
			a, err := artifactFromRow(artifactRow)
			if err != nil || a.WorkflowID != plan.WorkflowID || a.Repo.RepoKey != p.RepoKey || a.Fingerprint != p.ArtifactFingerprint || a.Repo.TargetRef != p.TargetRef {
				return domain.ErrArtifactStale
			}
			commitRow, err := q.GetTaskCommit(ctx, p.TaskCommitID)
			if err != nil {
				return err
			}
			commit := taskCommitFromRow(commitRow)
			if commit.State != "committed" || commit.ArtifactID != p.ArtifactID || commit.CommitSHA != p.TaskCommitSHA || commit.TargetSHA != p.ExpectedTargetSHA || commit.PolicyDigest != p.PolicyDigest {
				return domain.ErrDeliveryBlocked
			}
			paths, err := json.Marshal(p.ConflictPaths)
			if err != nil {
				return err
			}
			if err := q.InsertIntegrationRepoPreview(ctx, gen.InsertIntegrationRepoPreviewParams{PlanID: p.PlanID, RepoKey: string(p.RepoKey), ArtifactID: p.ArtifactID, TaskCommitID: p.TaskCommitID, ArtifactFingerprint: p.ArtifactFingerprint, PolicyDigest: p.PolicyDigest, TargetRef: p.TargetRef, ExpectedTargetSha: p.ExpectedTargetSHA, TaskCommitSha: p.TaskCommitSHA, MergeMethod: p.MergeMethod, Status: p.Status, TreeSha: p.TreeSHA, MergeCommitSha: p.MergeCommitSHA, DiffDigest: p.DiffDigest, ConflictPathsJson: string(paths), PreviewDigest: p.PreviewDigest, Revision: p.Revision}); err != nil {
				return err
			}
		}
		if plan.Status != status {
			return domain.ErrArtifactInvalid
		}
		return nil
	})
	return plan, err
}

// GetIntegrationPlan 读取不可变计划；已记录 clean 不等于当前仍可执行，写操作必须再次验证。
func (s *Store) GetIntegrationPlan(ctx context.Context, id string) (domain.IntegrationPlan, bool, error) {
	row, err := s.qr.GetIntegrationPlan(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.IntegrationPlan{}, false, nil
	}
	if err != nil {
		return domain.IntegrationPlan{}, false, err
	}
	return integrationPlanFromRow(row), true, nil
}

// ListIntegrationRepoPreviews 按 RepoKey 返回冻结证据，不暴露本机路径。
func (s *Store) ListIntegrationRepoPreviews(ctx context.Context, id string) ([]domain.IntegrationRepoPreview, error) {
	rows, err := s.qr.GetIntegrationRepoPreviews(ctx, id)
	if err != nil {
		return nil, err
	}
	result := make([]domain.IntegrationRepoPreview, 0, len(rows))
	for _, row := range rows {
		var paths []string
		if err := json.Unmarshal([]byte(row.ConflictPathsJson), &paths); err != nil {
			return nil, err
		}
		p := domain.IntegrationRepoPreview{PlanID: row.PlanID, RepoKey: domain.WorkflowRepoKey(row.RepoKey), ArtifactID: row.ArtifactID, TaskCommitID: row.TaskCommitID, ArtifactFingerprint: row.ArtifactFingerprint, PolicyDigest: row.PolicyDigest, TargetRef: row.TargetRef, ExpectedTargetSHA: row.ExpectedTargetSha, TaskCommitSHA: row.TaskCommitSha, MergeMethod: row.MergeMethod, Status: row.Status, TreeSHA: row.TreeSha, MergeCommitSHA: row.MergeCommitSha, DiffDigest: row.DiffDigest, ConflictPaths: paths, PreviewDigest: row.PreviewDigest, Revision: row.Revision}
		if p.PreviewDigest != p.Digest() {
			return nil, domain.ErrArtifactInvalid
		}
		result = append(result, p)
	}
	return result, nil
}

// ValidateIntegrationArtifacts 在预演前拒绝缺仓、部分提交、旧质量证据和旧执行身份。
func (s *Store) ValidateIntegrationArtifacts(ctx context.Context, workflowID domain.WorkflowID, ids []string, fence domain.SchedulerFence, now time.Time) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.inSchedulerTx(ctx, "validate integration artifacts", func(q *gen.Queries) error { return integrationArtifacts(ctx, q, workflowID, ids, fence, now) })
}

// integrationArtifacts 要求每个选中 Attempt 的全部仓库同时交付；RepoKey 和物理仓库均不能重复。
func integrationArtifacts(ctx context.Context, q *gen.Queries, workflowID domain.WorkflowID, ids []string, fence domain.SchedulerFence, now time.Time) error {
	if len(ids) == 0 || len(ids) > 32 {
		return domain.ErrArtifactInvalid
	}
	if err := schedulerFence(ctx, q, fence, now, true); err != nil {
		return err
	}
	seen := make(map[domain.WorkflowRepoKey]bool)
	physical := make(map[string]bool)
	attempts := make(map[domain.WorkflowAttemptID][]domain.WorkflowRepoKey)
	tasks := make(map[domain.WorkflowAttemptID]domain.WorkflowTaskID)
	for _, id := range ids {
		row, err := q.GetArtifact(ctx, id)
		if err != nil {
			return err
		}
		a, err := artifactFromRow(row)
		if err != nil {
			return err
		}
		if a.WorkflowID != workflowID || seen[a.Repo.RepoKey] {
			return domain.ErrArtifactStale
		}
		identity, err := canonicalProjectPath(a.Repo.GitCommonDir)
		if err != nil || physical[identity] {
			return domain.ErrArtifactInvalid
		}
		physical[identity], seen[a.Repo.RepoKey] = true, true
		commitRow, err := q.GetArtifactTaskCommit(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return domain.ErrDeliveryBlocked
		}
		if err != nil {
			return err
		}
		c := taskCommitFromRow(commitRow)
		if c.State != "committed" {
			return domain.ErrDeliveryBlocked
		}
		c.Fence = fence
		current := c
		if _, err := taskCommitEvidence(ctx, q, &current, now); err != nil {
			return err
		}
		if !sameTaskCommitIntent(c, current) {
			return domain.ErrArtifactStale
		}
		attempts[a.AttemptID] = append(attempts[a.AttemptID], a.Repo.RepoKey)
		tasks[a.AttemptID] = a.TaskID
	}
	for attempt, keys := range attempts {
		scopes, err := q.ListWorkflowTaskRepositories(ctx, gen.ListWorkflowTaskRepositoriesParams{WorkflowID: workflowID, TaskID: tasks[attempt]})
		if err != nil {
			return err
		}
		if len(scopes) != len(keys) {
			return domain.ErrDeliveryBlocked
		}
	}
	return nil
}

func integrationPlanFromRow(row gen.WorkflowIntegrationPlan) domain.IntegrationPlan {
	return domain.IntegrationPlan{ID: row.ID, WorkflowID: domain.WorkflowID(row.WorkflowID), Status: row.Status, Revision: row.Revision, PlanDigest: row.PlanDigest, CreatedAt: row.CreatedAt}
}
