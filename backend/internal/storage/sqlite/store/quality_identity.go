package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
)

// validateArtifactIdentity 在 writer 内连接全部持久身份；停止后交付不借用非 live 的执行许可。
func validateArtifactIdentity(ctx context.Context, q *gen.Queries, a domain.ArtifactSnapshot, f domain.SchedulerFence, now time.Time) error {
	if err := schedulerFence(ctx, q, f, now, true); err != nil {
		return err
	}
	attempt, err := q.GetWorkflowAttempt(ctx, a.AttemptID)
	if err != nil {
		return err
	}
	if attempt.WorkflowID != a.WorkflowID || attempt.TaskID != a.TaskID || attempt.SessionID == nil || *attempt.SessionID != a.SessionID || attempt.State == domain.WorkflowAttemptStateUnknown {
		return domain.ErrDeliveryBlocked
	}
	b, err := q.GetWorkflowAttemptBinding(ctx, a.AttemptID)
	if err != nil {
		return err
	}
	if b.WorkflowID != a.WorkflowID || b.TaskID != a.TaskID || b.SessionID == nil || *b.SessionID != a.SessionID || a.Generation == "" || b.ControllerGeneration != a.Generation {
		return fmt.Errorf("%w: binding identity", domain.ErrArtifactInvalid)
	}
	switch b.State {
	case domain.WorkflowBindingStateBoundReady, domain.WorkflowBindingStateRunning, domain.WorkflowBindingStateStopRequested, domain.WorkflowBindingStateStopConfirmed, domain.WorkflowBindingStateFailedConfirmed:
	default:
		return domain.ErrDeliveryBlocked
	}
	session, err := q.GetSession(ctx, a.SessionID)
	if err != nil {
		return err
	}
	if domain.SessionExecutionGeneration(getSessionRowToRecord(session)) != a.Generation {
		return fmt.Errorf("%w: session generation", domain.ErrArtifactInvalid)
	}
	ad, err := q.GetSchedulerAdmissionByAttempt(ctx, schedulerString(string(a.AttemptID)))
	if err != nil {
		return err
	}
	if ad.WorkflowID != string(a.WorkflowID) || ad.TaskID != string(a.TaskID) || ad.SessionID.String != string(a.SessionID) || ad.ControllerGeneration != a.Generation || ad.State == domain.AdmissionUncertain {
		return domain.ErrDeliveryBlocked
	}
	if !session.IsTerminated {
		if b.State != domain.WorkflowBindingStateBoundReady && b.State != domain.WorkflowBindingStateRunning {
			return domain.ErrDeliveryBlocked
		}
		if err := executionPermitFence(ctx, q, a.AttemptID, now, true); err != nil {
			return err
		}
	}
	repos, err := q.ListWorkflowRepositories(ctx, a.WorkflowID)
	if err != nil {
		return err
	}
	var repo *gen.WorkflowRepository
	for i := range repos {
		if repos[i].RepoKey == a.Repo.RepoKey {
			repo = &repos[i]
		}
	}
	if repo == nil || repo.ProjectID != a.Repo.ProjectID {
		return fmt.Errorf("%w: workflow repository", domain.ErrArtifactInvalid)
	}
	project, err := q.GetProject(ctx, repo.ProjectID)
	if err != nil {
		return err
	}
	expectedPath, err := deliveryRepoPath(project.Path, repo.PathRef)
	if err != nil {
		return err
	}
	if !sameDeliveryPath(expectedPath, a.Repo.RepoPath) || !filepath.IsAbs(a.Repo.GitCommonDir) {
		return fmt.Errorf("%w: project repository path", domain.ErrArtifactInvalid)
	}
	identity, err := reservationRepoIdentity(expectedPath)
	if err != nil {
		return err
	}
	common, err := canonicalProjectPath(a.Repo.GitCommonDir)
	if err != nil || identity != "git:"+common {
		return fmt.Errorf("%w: repository common directory", domain.ErrArtifactInvalid)
	}
	worktreeIdentity, err := reservationRepoIdentity(a.Repo.WorktreePath)
	if err != nil || worktreeIdentity != identity {
		return fmt.Errorf("%w: worktree common directory", domain.ErrArtifactInvalid)
	}
	target, ok := deliveryTargetRef(repo.TargetRef)
	actualTarget, actualOK := deliveryTargetRef(a.Repo.TargetRef)
	if !ok || !actualOK || target != actualTarget {
		return fmt.Errorf("%w: target ref", domain.ErrArtifactInvalid)
	}
	tasks, err := q.ListWorkflowTaskRepositories(ctx, gen.ListWorkflowTaskRepositoriesParams{WorkflowID: a.WorkflowID, TaskID: a.TaskID})
	if err != nil {
		return err
	}
	var task *gen.WorkflowTaskRepo
	for i := range tasks {
		if tasks[i].RepoKey == a.Repo.RepoKey {
			task = &tasks[i]
		}
	}
	if task == nil {
		return fmt.Errorf("%w: task repository", domain.ErrArtifactInvalid)
	}
	base := task.BaseSha
	if base == "" {
		base = repo.BaseSha
	}
	var allowed []string
	if err := json.Unmarshal([]byte(task.AllowedPathsJson), &allowed); err != nil {
		return err
	}
	want, err := deliveryScopes(allowed)
	if err != nil {
		return err
	}
	got, err := deliveryScopes(a.Repo.AllowedPaths)
	if err != nil || !slices.Equal(want, got) || base != "" && base != a.Repo.BaseSHA {
		return fmt.Errorf("%w: task scope or base", domain.ErrArtifactInvalid)
	}
	bindings, err := q.ListWorkflowAttemptRepoBindings(ctx, a.AttemptID)
	if err != nil {
		return err
	}
	matched := false
	for _, binding := range bindings {
		if binding.RepoKey == a.Repo.RepoKey {
			if base == "" {
				base = binding.ExpectedBaseSha
			}
			matched = binding.WorkflowID == a.WorkflowID && binding.ProjectID == a.Repo.ProjectID && binding.State == domain.WorkflowRepoBindingStateVerified && (binding.ExpectedBaseSha == "" || binding.ExpectedBaseSha == base) && sameDeliveryPath(binding.SessionWorktreeRef, a.Repo.WorktreePath)
		}
	}
	if !matched {
		return fmt.Errorf("%w: attempt repository", domain.ErrArtifactInvalid)
	}
	worktrees, err := q.ListSessionWorktrees(ctx, a.SessionID)
	if err != nil {
		return err
	}
	matched = false
	for _, w := range worktrees {
		if w.RepoName == string(a.Repo.RepoKey) && w.ProjectID == string(a.Repo.ProjectID) && sameDeliveryPath(w.RepoPath, a.Repo.RepoPath) && sameDeliveryPath(w.WorktreePath, a.Repo.WorktreePath) && w.Branch == a.Repo.Branch && (base == "" || w.BaseSha == base) && w.BaseSha == a.Repo.BaseSHA && w.CreationSha == a.Repo.CreationSHA && w.State == "active" {
			base = w.BaseSha
			matched = true
		}
	}
	if !matched || base != a.Repo.BaseSHA || !domain.ValidGitSHA(base) || !domain.ValidGitSHA(a.Repo.CreationSHA) || !domain.ValidGitSHA(a.Git.HeadSHA) || !domain.ValidGitSHA(a.Git.TreeSHA) || a.Git.IndexDigest == "" || a.Git.WorkspaceDigest == "" {
		return fmt.Errorf("%w: worktree identity or Git snapshot", domain.ErrArtifactInvalid)
	}
	for _, file := range a.Git.Files {
		p, err := reservationScope(file.Path)
		if err != nil || p == "" || (!file.Deleted && (!domain.ValidGitSHA(file.BlobSHA) || (file.Mode != "100644" && file.Mode != "100755"))) {
			return domain.ErrArtifactInvalid
		}
		inside := len(want) == 0
		for _, scope := range want {
			inside = inside || scope == "" || p == scope || strings.HasPrefix(p, scope+"/")
		}
		if !inside {
			return domain.ErrArtifactInvalid
		}
	}
	return nil
}

// deliveryTargetRef 只接受无歧义的本地分支，裸 main 与 refs/heads/main 等价。
func deliveryTargetRef(ref string) (string, bool) {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	if ref == "" || ref == "HEAD" || strings.HasPrefix(ref, "refs/") || strings.ContainsAny(ref, " ~^:?*[\\\t\r\n\x00") || strings.Contains(ref, "..") || strings.Contains(ref, "@{") {
		return "", false
	}
	for _, p := range strings.Split(ref, "/") {
		if p == "" || strings.HasPrefix(p, ".") || strings.HasSuffix(p, ".") || strings.HasSuffix(p, ".lock") {
			return "", false
		}
	}
	return "refs/heads/" + ref, true
}

// deliveryScopes 保留集合语义，排序不会修改调用方传入的快照指纹。
func deliveryScopes(paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, p := range paths {
		value, err := reservationScope(p)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	slices.Sort(result)
	return slices.Compact(result), nil
}

// sameDeliveryPath 比较规范化的绝对路径，拒绝空值和相对路径伪装。
func sameDeliveryPath(a, b string) bool {
	if !filepath.IsAbs(a) || !filepath.IsAbs(b) {
		return false
	}
	x, ex := canonicalProjectPath(a)
	y, ey := canonicalProjectPath(b)
	return ex == nil && ey == nil && x == y
}

// artifactReservation 要求新快照仍持有当前任期、同 Session/代际的路径占位。
func artifactReservation(ctx context.Context, q *gen.Queries, a domain.ArtifactSnapshot, f domain.SchedulerFence) error {
	rows, err := q.ListAttemptPathReservations(ctx, string(a.AttemptID))
	if err != nil {
		return err
	}
	found := false
	common, err := canonicalProjectPath(a.Repo.GitCommonDir)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if r.RepoKey == string(a.Repo.RepoKey) {
			if r.ReleasedAt.Valid || r.OwnerID != f.OwnerID || r.Epoch != f.Epoch || r.SessionID != string(a.SessionID) || r.Generation != a.Generation || r.ProjectID != string(a.Repo.ProjectID) || r.RepoIdentity != "git:"+common {
				return domain.ErrPathReservationConflict
			}
			found = true
		}
	}
	if !found {
		return domain.ErrPathReservationConflict
	}
	return nil
}
