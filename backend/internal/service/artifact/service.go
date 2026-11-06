// Package artifact 从可信持久绑定构造 Git 工件；不启动 Worker，也不以模型文本授予质量状态。
package artifact

import (
	"context"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/gitworktree"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/google/uuid"
)

// Store 只暴露工件所需的持久事实；SaveArtifact 在 writer 事务再次核对身份和任期。
type Store interface {
	GetWorkflowAttempt(context.Context, domain.WorkflowAttemptID) (domain.WorkflowAttempt, bool, error)
	GetWorkflowAttemptBinding(context.Context, domain.WorkflowAttemptID) (domain.WorkflowAttemptBinding, bool, error)
	ListWorkflowAttemptRepoBindings(context.Context, domain.WorkflowAttemptID) ([]domain.WorkflowAttemptRepoBinding, error)
	ListWorkflowRepositories(context.Context, domain.WorkflowID) ([]domain.WorkflowRepository, error)
	ListWorkflowTaskRepositories(context.Context, domain.WorkflowID, domain.WorkflowTaskID) ([]domain.WorkflowTaskRepository, error)
	ListSessionWorktrees(context.Context, domain.SessionID) ([]domain.SessionWorktreeRecord, error)
	SaveArtifact(context.Context, domain.ArtifactSnapshot, domain.SchedulerFence, time.Time) (domain.ArtifactSnapshot, bool, error)
	ReleasePathReservations(context.Context, domain.WorkflowAttemptID, domain.SchedulerFence, time.Time) (bool, error)
}

// Service 在短 DB 事务之外观察 Git，落库时仍执行精确 CAS；Scratch 必须由 daemon 提供隔离的数据目录。
type Service struct {
	Store   Store
	Scratch string
	Clock   func() time.Time
}

// Snapshot 冻结本 Attempt 所有绑定 Repo；任一仓缺证据即返回错误，部分已落库工件不能推导聚合通过。
func (s *Service) Snapshot(ctx context.Context, id domain.WorkflowAttemptID, revision int64, fence domain.SchedulerFence) ([]domain.ArtifactSnapshot, error) {
	if s.Store == nil || s.Scratch == "" || s.Clock == nil || revision < 1 {
		return nil, domain.ErrArtifactInvalid
	}
	attempt, ok, err := s.Store.GetWorkflowAttempt(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok || attempt.State == domain.WorkflowAttemptStateUnknown {
		return nil, domain.ErrDeliveryBlocked
	}
	binding, ok, err := s.Store.GetWorkflowAttemptBinding(ctx, id)
	if err != nil {
		return nil, err
	}
	if !ok || binding.SessionID == nil || binding.ControllerGeneration == "" || binding.WorkflowID != attempt.WorkflowID || binding.TaskID != attempt.TaskID {
		return nil, domain.ErrArtifactInvalid
	}
	bound, err := s.Store.ListWorkflowAttemptRepoBindings(ctx, id)
	if err != nil {
		return nil, err
	}
	repos, err := s.Store.ListWorkflowRepositories(ctx, attempt.WorkflowID)
	if err != nil {
		return nil, err
	}
	scopes, err := s.Store.ListWorkflowTaskRepositories(ctx, attempt.WorkflowID, attempt.TaskID)
	if err != nil {
		return nil, err
	}
	worktrees, err := s.Store.ListSessionWorktrees(ctx, *binding.SessionID)
	if err != nil {
		return nil, err
	}
	if len(scopes) == 0 || len(bound) != len(scopes) {
		return nil, domain.ErrArtifactInvalid
	}
	var out []domain.ArtifactSnapshot
	for _, scope := range scopes {
		bi := slices.IndexFunc(bound, func(v domain.WorkflowAttemptRepoBinding) bool { return v.RepoKey == scope.RepoKey })
		ri := slices.IndexFunc(repos, func(v domain.WorkflowRepository) bool { return v.RepoKey == scope.RepoKey })
		wi := slices.IndexFunc(worktrees, func(v domain.SessionWorktreeRecord) bool { return v.RepoName == string(scope.RepoKey) })
		if bi < 0 || ri < 0 || wi < 0 {
			return nil, domain.ErrArtifactInvalid
		}
		b, r, w := bound[bi], repos[ri], worktrees[wi]
		if r.TargetRef == "" {
			return nil, domain.ErrDeliveryBlocked
		}
		if !strings.HasPrefix(r.TargetRef, "refs/") {
			r.TargetRef = "refs/heads/" + r.TargetRef
		}
		if b.State != domain.WorkflowRepoBindingStateVerified || b.ProjectID != r.ProjectID || w.ProjectID != r.ProjectID || b.SessionWorktreeRef != w.WorktreePath {
			return nil, domain.ErrArtifactInvalid
		}
		base := scope.BaseSHA
		if base == "" {
			base = r.BaseSHA
		}
		if base == "" {
			base = w.BaseSHA
		}
		if b.ExpectedBaseSHA != "" && b.ExpectedBaseSHA != base {
			return nil, domain.ErrArtifactInvalid
		}
		repo := domain.DeliveryRepo{RepoKey: scope.RepoKey, ProjectID: r.ProjectID, RepoPath: w.RepoPath, WorktreePath: w.WorktreePath, Branch: w.Branch, TargetRef: r.TargetRef, BaseSHA: base, CreationSHA: w.CreationSHA, AllowedPaths: slices.Clone(scope.AllowedPaths)}
		slices.Sort(repo.AllowedPaths)
		repo, git, err := gitworktree.InspectDelivery(ctx, repo, s.Scratch)
		if err != nil {
			return nil, err
		}
		a := domain.ArtifactSnapshot{ID: uuid.NewString(), WorkflowID: attempt.WorkflowID, TaskID: attempt.TaskID, AttemptID: id, SessionID: *binding.SessionID, Generation: binding.ControllerGeneration, Repo: repo, Git: git, ArtifactRevision: revision, State: "verified", Revision: 1, CreatedAt: s.Clock().UTC()}
		a.Fingerprint = a.Digest()
		a, _, err = s.Store.SaveArtifact(ctx, a, fence, s.Clock().UTC())
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	if _, err := s.Store.ReleasePathReservations(ctx, id, fence, s.Clock().UTC()); err != nil {
		return nil, err
	}
	return out, nil
}

// Revalidate 在每次质量/写操作前读取实际 Git；范围外 dirty、索引、HEAD 和 scope 漂移同样使证据失效。
func (s *Service) Revalidate(ctx context.Context, a domain.ArtifactSnapshot) error {
	if a.Fingerprint == "" || a.Digest() != a.Fingerprint {
		return domain.ErrArtifactInvalid
	}
	repo, git, err := gitworktree.InspectDelivery(ctx, a.Repo, s.Scratch)
	if err != nil {
		return err
	}
	a.Repo, a.Git = repo, git
	if a.Digest() != a.Fingerprint {
		return domain.ErrArtifactStale
	}
	return nil
}
