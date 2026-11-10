package sessionmanager

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Multi-repo spawn refusals.
//
// They are typed because a caller has to tell "this spawn is impossible as
// described" (a refusal that cost nothing) from "a worktree was created and then
// the spawn failed" (an outcome that must be reconciled). Every one of them is
// raised before any worktree is recorded.
var (
	// ErrMultiRepoProjectUnresolved reports a spawn repo whose project id is
	// empty, is not registered, or has no repo path on record.
	ErrMultiRepoProjectUnresolved = errors.New("session: multi-repo spawn names an unresolvable repository project")
	// ErrMultiRepoDuplicateRepo reports the same repo key twice in one spawn.
	ErrMultiRepoDuplicateRepo = errors.New("session: multi-repo spawn names the same repository key twice")
	// ErrMultiRepoPathConflict reports two spawn repos that would share one
	// worktree path (same project id, so the same managed location).
	ErrMultiRepoPathConflict = errors.New("session: two spawn repositories resolve to the same worktree path")
	// ErrMultiRepoWorktreeUnverified reports a created worktree whose path is
	// missing, whose HEAD cannot be read, or whose HEAD is not the expected commit.
	ErrMultiRepoWorktreeUnverified = errors.New("session: multi-repo worktree could not be verified")
	// ErrMultiRepoWorktreeMissing reports a recorded worktree whose directory is
	// gone. Restore reports it rather than proceeding with the remaining repos.
	ErrMultiRepoWorktreeMissing = errors.New("session: multi-repo session is missing a recorded worktree")
	// ErrMultiRepoBranchMismatch reports a worktree that is not on the branch the
	// session recorded for it.
	ErrMultiRepoBranchMismatch = errors.New("session: multi-repo worktree is not on its recorded branch")
	// ErrMultiRepoRepoMismatch reports a worktree whose repository is not the
	// one the session recorded for that repo key: the directory exists and is a
	// worktree, but of some other repository.
	ErrMultiRepoRepoMismatch = errors.New("session: multi-repo worktree belongs to a different repository")
	// ErrMultiRepoHistoryDiverged reports a worktree whose recorded creation
	// commit is no longer reachable from its HEAD. Something reset or moved the
	// worktree to unrelated history, so the session no longer describes what is
	// on disk. Restoring is refused; nothing is reset, cleaned, or deleted to
	// make the check pass.
	ErrMultiRepoHistoryDiverged = errors.New("session: multi-repo worktree no longer continues from its creation commit")
)

// multiRepoPlan is one spawn repo after its project has been resolved.
type multiRepoPlan struct {
	repoKey    string
	project    domain.ProjectRecord
	baseSHA    string
	baseRef    string
	baseBranch string
}

// createMultiRepoSessionWorkspace materialises one worktree per spawn repo.
//
// The rules it enforces, in order:
//
//  1. Every repo resolves to a registered project with a non-empty id and path,
//     and no two repos resolve to the same worktree path. Both are refusals
//     raised before any Git command runs.
//  2. Worktrees are created in repo-key order, so a retry of the same spawn
//     produces the same layout and the same root.
//  3. Every created worktree is READ BACK from disk (its HEAD commit) and only
//     then recorded. Nothing is reported ready on the strength of "git add
//     exited 0".
//  4. Any failure destroys exactly the worktrees this call created, children
//     first, and leaves the session with no worktree row.
//
// The first repo by key becomes the session's root workspace, which is what
// SessionRecord.Metadata.WorkspacePath names.
func (m *Manager) createMultiRepoSessionWorkspace(
	ctx context.Context,
	project domain.ProjectRecord,
	cfg ports.SpawnConfig,
	id domain.SessionID,
	branch string,
	baseRefs map[string]string,
) (ports.WorkspaceInfo, []ports.WorkspaceRepoInfo, error) {
	_ = project // the root project only supplies the session prefix; each repo carries its own
	plans, err := m.resolveMultiRepoPlans(ctx, cfg.Repos, baseRefs)
	if err != nil {
		return ports.WorkspaceInfo{}, nil, err
	}

	created := make([]ports.WorkspaceRepoInfo, 0, len(plans))
	rollback := func(cause error) (ports.WorkspaceInfo, []ports.WorkspaceRepoInfo, error) {
		cleanupCtx, cancel := spawnRollbackContext(ctx)
		defer cancel()
		return ports.WorkspaceInfo{}, nil, errors.Join(cause, m.rollbackMultiRepoWorktrees(cleanupCtx, created))
	}

	seenPaths := make(map[string]string, len(plans))
	freshBranch := cfg.TaskPreparation != "" || cfg.RequireNewBranch
	for _, plan := range plans {
		projectID := domain.ProjectID(plan.project.ID)
		workspaceCfg := ports.WorkspaceConfig{
			IndependentOrchestrator: cfg.IndependentOrchestrator,
			ProjectID:               projectID,
			SessionID:               id,
			Kind:                    cfg.Kind,
			SessionPrefix:           sessionPrefix(plan.project),
			Branch:                  branch,
			FreshBranch:             freshBranch,
			RequireNewBranch:        cfg.RequireNewBranch,
			BaseBranch:              plan.baseBranch,
			BaseRef:                 plan.baseRef,
			// The exact commit this repo planned from. Empty keeps the
			// adapter's default-branch seeding; non-empty makes the adapter
			// seed from it (and refuse when it cannot) instead of resolving the
			// repository default, which may have advanced since planning.
			BaseSHA: plan.baseSHA,
		}
		ws, createErr := m.workspace.Create(ctx, workspaceCfg)
		if errors.Is(createErr, ports.ErrWorkspaceBranchCheckedOutElsewhere) &&
			cfg.Kind == domain.KindOrchestrator && cfg.Branch == "" {
			// The branch is already checked out somewhere else in this repo.
			// Leave that worktree untouched and take an unused suffix, exactly as
			// the single-repo path does.
			workspaceCfg.FreshBranch = true
			ws, createErr = m.workspace.Create(ctx, workspaceCfg)
		}
		if createErr != nil {
			return rollback(fmt.Errorf("repo %s: %w", plan.repoKey, createErr))
		}
		if other, ok := seenPaths[filepath.Clean(ws.Path)]; ok {
			// Two repos landed on one directory. Destroy the one just created so
			// the conflict costs nothing beyond this spawn.
			duplicate := workspaceInfoFromRepoInfo(ports.WorkspaceRepoInfo{
				RepoName: plan.repoKey, RepoPath: plan.project.Path, Path: ws.Path,
				Branch: ws.Branch, BaseSHA: ws.BaseSHA, SessionID: id, ProjectID: projectID,
			})
			destroyCtx, cancel := spawnRollbackContext(ctx)
			_ = m.workspace.Destroy(destroyCtx, duplicate)
			cancel()
			return rollback(fmt.Errorf("%w: repos %s and %s both resolve to %s",
				ErrMultiRepoPathConflict, other, plan.repoKey, ws.Path))
		}
		seenPaths[filepath.Clean(ws.Path)] = plan.repoKey

		// The worktree's real HEAD is read back and compared against the commit
		// this repo was asked to seed from — plan.baseSHA, the REQUEST — not
		// against whatever the adapter reported. The adapter's own BaseSHA can
		// only confirm itself; the request is the ground truth this spawn owes
		// its caller. When the request names no commit, any readable HEAD is
		// accepted (that is the pre-pin behaviour) but an unreadable one still
		// fails.
		info := ports.WorkspaceRepoInfo{
			RepoName: plan.repoKey,
			RepoPath: plan.project.Path,
			Path:     ws.Path,
			Branch:   ws.Branch,
			// BaseSHA is the commit this repo was REQUIRED to start from: the
			// request when there was one, else the base the adapter resolved
			// (the comparison base for diffs). CreationSHA is the commit the
			// worktree was verified to hold.
			BaseSHA:   firstNonEmptyString(plan.baseSHA, ws.BaseSHA),
			BaseRef:   ws.BaseRef,
			SessionID: id,
			ProjectID: projectID,
		}
		created = append(created, info)
		// 先登记本次产物，再核验；核验失败也必须覆盖当前仓库的补偿。
		head, verifyErr := verifyRepoWorktreeHead(ctx, ws.Path, plan.baseSHA)
		if verifyErr != nil {
			return rollback(fmt.Errorf("repo %s: %w", plan.repoKey, verifyErr))
		}
		created[len(created)-1].CreationSHA = head
		created[len(created)-1].BaseSHA = firstNonEmptyString(info.BaseSHA, head)
	}

	for _, info := range created {
		if storeErr := m.store.UpsertSessionWorktree(ctx, domain.SessionWorktreeRecord{
			SessionID:    id,
			RepoName:     info.RepoName,
			ProjectID:    info.ProjectID,
			RepoPath:     info.RepoPath,
			Branch:       info.Branch,
			BaseSHA:      info.BaseSHA,
			BaseRef:      info.BaseRef,
			CreationSHA:  info.CreationSHA,
			WorktreePath: info.Path,
			State:        "active",
		}); storeErr != nil {
			return rollback(fmt.Errorf("record repo %s worktree: %w", info.RepoName, storeErr))
		}
	}
	if len(created) == 0 {
		return ports.WorkspaceInfo{}, nil, fmt.Errorf("%w: no repositories", ErrMultiRepoProjectUnresolved)
	}
	return workspaceInfoFromRepoInfo(created[0]), created, nil
}

// resolveMultiRepoPlans validates the repository list and resolves each entry to
// a registered project, a base ref, and that project's configured base branch.
//
// Resolution happens for every repo before the first Git command, so an
// unregistered project or a duplicate path refuses the spawn without creating
// anything.
func (m *Manager) resolveMultiRepoPlans(
	ctx context.Context,
	repos []ports.SpawnRepo,
	baseRefs map[string]string,
) ([]multiRepoPlan, error) {
	if len(repos) == 0 {
		return nil, fmt.Errorf("%w: the repository list is empty", ErrMultiRepoProjectUnresolved)
	}
	ordered := append([]ports.SpawnRepo(nil), repos...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].RepoKey < ordered[j].RepoKey })

	plans := make([]multiRepoPlan, 0, len(ordered))
	seenKeys := make(map[string]struct{}, len(ordered))
	seenProjects := make(map[domain.ProjectID]string, len(ordered))
	for _, repo := range ordered {
		key := strings.TrimSpace(repo.RepoKey)
		if key == "" {
			return nil, fmt.Errorf("%w: a spawn repo has no repo key", ErrMultiRepoProjectUnresolved)
		}
		if _, duplicate := seenKeys[key]; duplicate {
			return nil, fmt.Errorf("%w: %s", ErrMultiRepoDuplicateRepo, key)
		}
		seenKeys[key] = struct{}{}
		if strings.TrimSpace(string(repo.ProjectID)) == "" {
			return nil, fmt.Errorf("%w: repo %s has no project id", ErrMultiRepoProjectUnresolved, key)
		}
		if other, duplicate := seenProjects[repo.ProjectID]; duplicate {
			return nil, fmt.Errorf("%w: repos %s and %s both name project %s",
				ErrMultiRepoPathConflict, other, key, repo.ProjectID)
		}
		seenProjects[repo.ProjectID] = key

		record, ok, err := m.store.GetProject(ctx, string(repo.ProjectID))
		if err != nil {
			return nil, fmt.Errorf("%w: load project %s: %s", ErrMultiRepoProjectUnresolved, repo.ProjectID, err.Error())
		}
		if !ok {
			return nil, fmt.Errorf("%w: project %s (repo %s) is not registered",
				ErrMultiRepoProjectUnresolved, repo.ProjectID, key)
		}
		if record.ID == "" || strings.TrimSpace(record.Path) == "" {
			return nil, fmt.Errorf("%w: project %s (repo %s) has no repo path on record",
				ErrMultiRepoProjectUnresolved, repo.ProjectID, key)
		}
		// 所有 pin 在第一个 Worktree 创建前解析，后续仍由适配器再次核验。
		if repo.BaseSHA != "" {
			_, decodeErr := hex.DecodeString(repo.BaseSHA)
			if len(repo.BaseSHA) != 40 || decodeErr != nil {
				return nil, fmt.Errorf("%w: repo %s requires a full hexadecimal commit id", ports.ErrWorkspaceBaseSHAUnavailable, key)
			}
			resolved, ok := spawnGitSingleLine(ctx, record.Path, "rev-parse", "--verify", "--end-of-options", repo.BaseSHA+"^{commit}")
			if !ok || !strings.EqualFold(resolved, repo.BaseSHA) {
				return nil, fmt.Errorf("%w: repo %s does not contain commit %s", ports.ErrWorkspaceBaseSHAUnavailable, key, repo.BaseSHA)
			}
			repo.BaseSHA = resolved
		}
		var refs map[string]string
		if repo.BaseSHA == "" {
			refs = m.refreshDefaultBranchesBestEffort(ctx, record)
		}
		for path, ref := range refs {
			if _, exists := baseRefs[path]; !exists {
				if baseRefs == nil {
					baseRefs = make(map[string]string, len(refs))
				}
				baseRefs[path] = ref
			}
		}
		plans = append(plans, multiRepoPlan{
			repoKey: key,
			project: record,
			// Deliberately NOT trimmed: the adapter is the single validator of
			// a pinned commit, and trimming here would turn a caller's
			// whitespace-only BaseSHA into "no pin", silently spawning from the
			// default branch instead of refusing the request.
			baseSHA:    repo.BaseSHA,
			baseRef:    baseRefs[filepath.Clean(record.Path)],
			baseBranch: record.Config.WorktreeBaseBranch(),
		})
	}
	return plans, nil
}

// verifyRepoWorktreeHead reads the worktree's real HEAD and checks it against the
// commit the adapter said it created.
//
// An expected commit that the adapter did not report is allowed: a branch created
// without a pinned seed has no adapter-side base, and requiring one would refuse
// every ordinary session branch. An unreadable or empty HEAD is never allowed,
// because that is the case where "the worktree exists" and "the worktree is the
// one we meant" have silently diverged.
func verifyRepoWorktreeHead(ctx context.Context, path, expected string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%w: the adapter reported an empty worktree path", ErrMultiRepoWorktreeUnverified)
	}
	head, ok := spawnGitSingleLine(ctx, path, "rev-parse", "HEAD")
	if !ok {
		return "", fmt.Errorf("%w: %s: git rev-parse HEAD returned nothing", ErrMultiRepoWorktreeUnverified, path)
	}
	if expected != "" && !strings.EqualFold(expected, head) {
		return "", fmt.Errorf("%w: %s: HEAD is %s, expected %s",
			ErrMultiRepoWorktreeUnverified, path, head, expected)
	}
	return head, nil
}

// verifyRepoWorktreeBranch 核对真实符号分支，detached HEAD 不能冒充记录中的分支。
func verifyRepoWorktreeBranch(ctx context.Context, path, branch string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("%w: empty worktree path", ErrMultiRepoWorktreeMissing)
	}
	if strings.TrimSpace(branch) == "" {
		return fmt.Errorf("%w: %s: the session recorded no branch", ErrMultiRepoBranchMismatch, path)
	}
	current, ok := spawnGitSingleLine(ctx, path, "symbolic-ref", "--short", "HEAD")
	if !ok {
		return fmt.Errorf("%w: %s: git symbolic-ref HEAD returned nothing", ErrMultiRepoBranchMismatch, path)
	}
	if current != branch {
		return fmt.Errorf("%w: %s is on %s, expected %s", ErrMultiRepoBranchMismatch, path, current, branch)
	}
	return nil
}

// verifyRepoWorktreeRepo 核对持久化 Project/Repo 身份与两个真实 Git 根目录。
// common-dir 可以外置，必须直接比较，不能从其父目录反推授权仓库。
func (m *Manager) verifyRepoWorktreeRepo(ctx context.Context, row ports.WorkspaceRepoInfo) error {
	recorded := strings.TrimSpace(row.RepoPath)
	expectedProject := strings.TrimSpace(string(row.ProjectID))
	if recorded == "" || expectedProject == "" || !filepath.IsAbs(row.RepoPath) || !filepath.IsAbs(row.Path) {
		return fmt.Errorf("%w: repo %s has no durable project identity", ErrMultiRepoRepoMismatch, row.RepoName)
	}
	project, found, err := m.store.GetProject(ctx, expectedProject)
	if err != nil {
		return fmt.Errorf("%w: load project %s: %s", ErrMultiRepoRepoMismatch, expectedProject, err.Error())
	}
	if !found || project.ID != string(row.ProjectID) || !sameWorkspacePath(row.RepoPath, project.Path) {
		return fmt.Errorf("%w: repo %s is not registered at project %s", ErrMultiRepoRepoMismatch, row.RepoName, expectedProject)
	}
	for _, path := range []string{row.Path, row.RepoPath} {
		top, ok := spawnGitSingleLine(ctx, path, "rev-parse", "--show-toplevel")
		if !ok || !sameWorkspacePath(top, path) {
			return fmt.Errorf("%w: %s is not the recorded Git root", ErrMultiRepoRepoMismatch, path)
		}
	}
	actual, expected := worktreeCommonDir(ctx, row.Path), worktreeCommonDir(ctx, row.RepoPath)
	if actual == "" || expected == "" || !sameWorkspacePath(actual, expected) {
		return fmt.Errorf("%w: repo %s Git common directories do not match", ErrMultiRepoRepoMismatch, row.RepoName)
	}
	return nil
}

// verifyRepoWorktreeHistory 只允许 BaseSHA→CreationSHA→现场 HEAD 的历史延续。
// 创建证据必须是完整 commit；允许其后合法提交和 dirty，不重置任何工作区。
func verifyRepoWorktreeHistory(ctx context.Context, row ports.WorkspaceRepoInfo, head string) error {
	creation := row.CreationSHA
	if !worktreeContainsCommit(ctx, row.Path, creation) {
		return fmt.Errorf("%w: repo %s has no valid creation commit", ErrMultiRepoWorktreeUnverified, row.RepoName)
	}
	if row.BaseSHA != "" {
		if !worktreeContainsCommit(ctx, row.Path, row.BaseSHA) {
			return fmt.Errorf("%w: repo %s has no valid base commit", ErrMultiRepoWorktreeUnverified, row.RepoName)
		}
		ancestor, err := gitIsAncestor(ctx, row.Path, row.BaseSHA, creation)
		if err != nil {
			return err
		}
		if !ancestor {
			return fmt.Errorf("%w: repo %s base is not an ancestor of its creation commit", ErrMultiRepoHistoryDiverged, row.RepoName)
		}
	}
	ancestor, err := gitIsAncestor(ctx, row.Path, creation, head)
	if err != nil {
		return err
	}
	if !ancestor {
		return fmt.Errorf("%w: %s is at %s, which does not contain the recorded creation commit %s",
			ErrMultiRepoHistoryDiverged, row.Path, head, creation)
	}
	return nil
}

// worktreeContainsCommit 拒绝 abbreviated SHA、ref 表达式与非 commit 对象。
func worktreeContainsCommit(ctx context.Context, path, sha string) bool {
	_, err := hex.DecodeString(sha)
	if len(sha) != 40 || err != nil {
		return false
	}
	resolved, ok := spawnGitSingleLine(ctx, path, "rev-parse", "--verify", "--end-of-options", sha+"^{commit}")
	return ok && strings.EqualFold(resolved, sha)
}

// gitIsAncestor reports whether ancestor is reachable from descendant in the
// repository containing path. A non-zero exit is only "no" when git says so
// (exit 1); any other failure is an error, so a caller never records divergence
// for a question git could not answer.
// Git 失败只作为文本诊断，调用方只能依据本层 WorktreeUnverified 分类，不能视作已证实的历史分叉。
func gitIsAncestor(ctx context.Context, path, ancestor, descendant string) (bool, error) {
	cmd := aoprocess.CommandContext(ctx, "git",
		"-C", path, "merge-base", "--is-ancestor", "--end-of-options", ancestor, descendant)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("%w: %s: merge-base --is-ancestor %s %s: %v: %s",
		ErrMultiRepoWorktreeUnverified, path, ancestor, descendant, err.Error(), strings.TrimSpace(string(out)))
}

// sameWorkspacePath compares two paths the way a filesystem on this host does:
// git reports paths with forward slashes even on Windows, and the platform is
// case-insensitive there.
func sameWorkspacePath(a, b string) bool {
	ca, cb := comparableWorkspacePath(a), comparableWorkspacePath(b)
	if ca == cb {
		return true
	}
	return runtime.GOOS == "windows" && strings.EqualFold(ca, cb)
}

// comparableWorkspacePath cleans a path and resolves it through symlinks, so a
// repository reached through a link and one reached directly compare equal.
func comparableWorkspacePath(path string) string {
	clean := filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(clean); err == nil {
		clean = resolved
	}
	return filepath.Clean(clean)
}

// rollbackMultiRepoWorktrees destroys the worktrees one spawn created, newest
// first, and then drops their rows. It never touches another session's worktree:
// every entry comes from this call's own created slice.
func (m *Manager) rollbackMultiRepoWorktrees(ctx context.Context, created []ports.WorkspaceRepoInfo) error {
	var firstErr error
	for i := len(created) - 1; i >= 0; i-- {
		info := created[i]
		if info.Path == "" {
			continue
		}
		err := m.workspace.Destroy(ctx, workspaceInfoFromRepoInfo(ports.WorkspaceRepoInfo{
			RepoName: info.RepoName, RepoPath: info.RepoPath, Path: info.Path,
			Branch: info.Branch, BaseSHA: firstNonEmptyString(info.CreationSHA, info.BaseSHA),
			SessionID: info.SessionID, ProjectID: info.ProjectID,
		}))
		if err != nil && firstErr == nil {
			firstErr = fmt.Errorf("roll back repo %s worktree: %w", info.RepoName, err)
		}
	}
	if len(created) > 0 {
		if err := m.store.DeleteSessionWorktrees(ctx, created[0].SessionID); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("drop rolled-back worktree rows: %w", err)
		}
	}
	return firstErr
}

// multiRepoRows 从持久化记录恢复授权归属；当前目录只供验证，不能成为身份来源。
// 缺少身份的旧记录仍交给恢复检查闭锁，不伪造根 Session 的 ProjectID。
func (m *Manager) multiRepoRows(rows []domain.SessionWorktreeRecord) []ports.WorkspaceRepoInfo {
	out := make([]ports.WorkspaceRepoInfo, 0, len(rows))
	for _, row := range rows {
		out = append(out, ports.WorkspaceRepoInfo{
			RepoName:    row.RepoName,
			RepoPath:    row.RepoPath,
			Path:        row.WorktreePath,
			Branch:      row.Branch,
			BaseSHA:     row.BaseSHA,
			BaseRef:     row.BaseRef,
			CreationSHA: row.CreationSHA,
			SessionID:   row.SessionID,
			ProjectID:   row.ProjectID,
		})
	}
	return out
}

// worktreeCommonDir 读取真实 Git 共享目录，兼容 linked worktree 与外置 Git 目录。
func worktreeCommonDir(ctx context.Context, worktreePath string) string {
	common, ok := spawnGitSingleLine(ctx, worktreePath, "rev-parse", "--git-common-dir")
	if !ok {
		return ""
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(worktreePath, common)
	}
	return filepath.Clean(common)
}

// restoreMultiRepoSessionWorkspace verifies EVERY recorded worktree before it
// reports the session restored.
//
// The failure mode this replaces is specific: proceeding with the first row left
// a session that looked restored while one of its repositories was gone, which
// then surfaced much later as an agent working in a directory that does not
// exist. A missing directory, a detached HEAD, or a worktree on a different
// branch is therefore a typed error, and no worktree is reset, cleaned, or
// deleted to make the check pass.
func (m *Manager) restoreMultiRepoSessionWorkspace(
	ctx context.Context,
	rec domain.SessionRecord,
	rows []ports.WorkspaceRepoInfo,
) (ports.WorkspaceInfo, error) {
	if len(rows) == 0 {
		return ports.WorkspaceInfo{}, fmt.Errorf("%w: %s has no worktree rows", ErrMultiRepoWorktreeMissing, rec.ID)
	}
	if _, err := m.verifyMultiRepoRows(ctx, rows); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	root, err := m.restoreMultiRepoRows(ctx, rows)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	for _, row := range rows {
		if err := m.upsertWorkspaceProjectRowState(ctx, row, "active"); err != nil {
			return ports.WorkspaceInfo{}, fmt.Errorf("mark repo %s active: %w", row.RepoName, err)
		}
	}
	if err := m.restoreAttachments(ctx, rec.ID, root); err != nil {
		return ports.WorkspaceInfo{}, fmt.Errorf("restore attachments: %w", err)
	}
	return root, nil
}

// ObserveSessionWorktrees 以持久化授权集合核验 1..N 个显式 Repo，返回实时 Git 事实。
// Legacy 返回 nil；任何一个 Repo 无法核验时整体失败，绝不返回部分可信结果。
func (m *Manager) ObserveSessionWorktrees(ctx context.Context, id domain.SessionID) ([]ports.RepoWorktreeObservation, error) {
	rec, found, err := m.store.GetSession(ctx, id)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, fmt.Errorf("%w: session %s", ErrMultiRepoWorktreeMissing, id)
	}
	return m.observeSessionWorktrees(ctx, rec)
}

// observeSessionWorktrees 复用启动 gate 内已读取的 Session，绑定、Repo 记录与 Git 仍实时核验。
// 内部恢复不额外消费 Legacy 的回读阶段；外部观察始终先按 ID 重读 Session。
func (m *Manager) observeSessionWorktrees(ctx context.Context, rec domain.SessionRecord) ([]ports.RepoWorktreeObservation, error) {
	rows, multi, err := m.multiRepoSessionRows(ctx, rec)
	if err != nil {
		return nil, err
	}
	if !multi {
		return nil, nil
	}
	return m.verifyMultiRepoRows(ctx, rows)
}

// VerifySessionWorktrees 保留既有调用方，所有校验统一经过实时观察。
func (m *Manager) VerifySessionWorktrees(ctx context.Context, id domain.SessionID) error {
	_, err := m.ObserveSessionWorktrees(ctx, id)
	return err
}

// verifyMultiRepoRows 共享创建后恢复与实时观察的只读校验，dirty 成果允许保留。
func (m *Manager) verifyMultiRepoRows(ctx context.Context, rows []ports.WorkspaceRepoInfo) ([]ports.RepoWorktreeObservation, error) {
	observed := make([]ports.RepoWorktreeObservation, 0, len(rows))
	for _, row := range rows {
		missing, statErr := workspacePathMissing(row.Path)
		if statErr != nil {
			return nil, statErr
		}
		if missing {
			return nil, fmt.Errorf("%w: repo %s: %s is gone", ErrMultiRepoWorktreeMissing, row.RepoName, row.Path)
		}
		if err := m.verifyRepoWorktreeRepo(ctx, row); err != nil {
			return nil, fmt.Errorf("repo %s: %w", row.RepoName, err)
		}
		if err := verifyRepoWorktreeBranch(ctx, row.Path, row.Branch); err != nil {
			return nil, fmt.Errorf("repo %s: %w", row.RepoName, err)
		}
		head, err := verifyRepoWorktreeHead(ctx, row.Path, "")
		if err != nil {
			return nil, fmt.Errorf("repo %s: %w", row.RepoName, err)
		}
		if err := verifyRepoWorktreeHistory(ctx, row, head); err != nil {
			return nil, fmt.Errorf("repo %s: %w", row.RepoName, err)
		}
		// 即使目录与 HEAD 可读，损坏的索引也不能被当作 ready；不输出文件内容。
		status, err := aoprocess.CommandContext(ctx, "git", "--no-optional-locks", "-C", row.Path, "status", "--porcelain").Output()
		if err != nil {
			return nil, fmt.Errorf("%w: repo %s status: %w", ErrMultiRepoWorktreeUnverified, row.RepoName, err)
		}
		observed = append(observed, ports.RepoWorktreeObservation{
			RepoKey: domain.WorkflowRepoKey(row.RepoName), ProjectID: row.ProjectID,
			WorktreeRef: row.Path, Branch: row.Branch, HeadSHA: head,
			BaseSHA: row.BaseSHA, Dirty: len(status) != 0,
		})
	}
	return observed, nil
}

// restoreMultiRepoRows re-attaches every verified worktree and returns the
// session root: the row the record names as the workspace path, else the first
// row in recorded order.
func (m *Manager) restoreMultiRepoRows(
	ctx context.Context,
	rows []ports.WorkspaceRepoInfo,
) (ports.WorkspaceInfo, error) {
	var root ports.WorkspaceRepoInfo
	haveRoot := false
	for _, row := range rows {
		restored, err := m.workspace.Restore(ctx, ports.WorkspaceConfig{
			ProjectID: row.ProjectID,
			SessionID: row.SessionID,
			Branch:    row.Branch,
			BaseRef:   row.BaseRef,
			RepoPath:  row.RepoPath,
			Path:      row.Path,
		})
		if err != nil {
			return ports.WorkspaceInfo{}, fmt.Errorf("repo %s: %w", row.RepoName, err)
		}
		row.Path = restored.Path
		row.Branch = restored.Branch
		row.BaseRef = firstNonEmptyString(restored.BaseRef, row.BaseRef)
		if !haveRoot {
			root, haveRoot = row, true
		}
	}
	if !haveRoot {
		return ports.WorkspaceInfo{}, fmt.Errorf("%w: no worktree row could be restored", ErrMultiRepoWorktreeMissing)
	}
	return workspaceInfoFromRepoInfo(root), nil
}

// workflowWorktreeBindingStore 只读 M2 的持久化意图及授权 Repo 集合。
// Spawn 尚未写 SessionID 时仍通过通用 ClientRequestID 查询，绝不依赖 ID 前缀。
type workflowWorktreeBindingStore interface {
	workflowAttemptBindingStore
	GetWorkflowAttemptBindingBySpawnRequest(context.Context, string) (domain.WorkflowAttemptBinding, bool, error)
	ListWorkflowAttemptRepoBindings(context.Context, domain.WorkflowAttemptID) ([]domain.WorkflowAttemptRepoBinding, error)
}

// workflowWorktreeExpectations 从已绑定 Session 或 Spawn 意图恢复预期 Repo 集合。
// 旧 Workflow 无显式 Repo 时保持 Legacy；已知绑定但无法读取预期集合时闭锁。
func (m *Manager) workflowWorktreeExpectations(ctx context.Context, rec domain.SessionRecord) ([]domain.WorkflowAttemptRepoBinding, error) {
	bindings, supported := m.store.(workflowAttemptBindingStore)
	if !supported {
		return nil, nil
	}
	binding, found, err := bindings.GetWorkflowAttemptBindingBySession(ctx, rec.ID)
	if err != nil {
		return nil, err
	}
	repos, supported := m.store.(workflowWorktreeBindingStore)
	if !found && supported && rec.ClientRequestID != "" {
		binding, found, err = repos.GetWorkflowAttemptBindingBySpawnRequest(ctx, rec.ClientRequestID)
		if err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, nil
	}
	if !supported {
		return nil, fmt.Errorf("%w: session %s cannot read bound repository expectations", ErrMultiRepoWorktreeUnverified, rec.ID)
	}
	expected, err := repos.ListWorkflowAttemptRepoBindings(ctx, binding.AttemptID)
	if err != nil || len(expected) == 0 {
		return expected, err
	}
	if err := binding.Validate(); err != nil ||
		(binding.SessionID != nil && *binding.SessionID != rec.ID) ||
		(rec.ClientRequestID != "" && rec.ClientRequestID != binding.SpawnRequestID) {
		return nil, fmt.Errorf("%w: session %s has inconsistent workflow identity", ErrMultiRepoRepoMismatch, rec.ID)
	}
	for _, repo := range expected {
		if err := repo.Validate(); err != nil || repo.AttemptID != binding.AttemptID || repo.WorkflowID != binding.WorkflowID {
			return nil, fmt.Errorf("%w: session %s has inconsistent repository expectations", ErrMultiRepoRepoMismatch, rec.ID)
		}
	}
	return expected, nil
}

// multiRepoSessionRows 按持久化绑定与显式 Repo 身份识别 M2，不按工作树数量降级。
// 普通 Workspace 项目仍使用原恢复流程；M2 授权集合必须与记录逐 key 对齐。
func (m *Manager) multiRepoSessionRows(ctx context.Context, rec domain.SessionRecord) ([]ports.WorkspaceRepoInfo, bool, error) {
	rows, err := m.store.ListSessionWorktrees(ctx, rec.ID)
	if err != nil {
		return nil, false, err
	}
	expected, err := m.workflowWorktreeExpectations(ctx, rec)
	if err != nil {
		return nil, false, err
	}
	project, err := m.loadProject(ctx, rec.ProjectID)
	if err != nil {
		return nil, false, err
	}
	if len(expected) == 0 && project.Kind.WithDefault() == domain.ProjectKindWorkspace {
		return nil, false, nil
	}
	explicit := len(expected) > 0
	for _, row := range rows {
		explicit = explicit || row.ProjectID != "" || row.RepoPath != "" ||
			(row.RepoName != "" && row.RepoName != domain.RootWorkspaceRepoName)
	}
	if !explicit {
		return nil, false, nil
	}
	if len(rows) == 0 {
		return nil, true, fmt.Errorf("%w: session %s has no explicit repository records", ErrMultiRepoWorktreeMissing, rec.ID)
	}
	if _, supported := m.store.(workflowWorktreeBindingStore); !supported {
		return nil, true, fmt.Errorf("%w: session %s cannot prove repository bindings", ErrMultiRepoWorktreeUnverified, rec.ID)
	}
	byKey := make(map[string]domain.SessionWorktreeRecord, len(rows))
	rootFound := false
	for _, row := range rows {
		if strings.TrimSpace(row.RepoName) == "" || row.SessionID != rec.ID || row.ProjectID == "" || strings.TrimSpace(row.RepoPath) == "" {
			return nil, true, fmt.Errorf("%w: session %s has incomplete repository identity", ErrMultiRepoRepoMismatch, rec.ID)
		}
		if _, duplicate := byKey[row.RepoName]; duplicate {
			return nil, true, fmt.Errorf("%w: %s", ErrMultiRepoDuplicateRepo, row.RepoName)
		}
		byKey[row.RepoName] = row
		if sameWorkspacePath(row.WorktreePath, rec.Metadata.WorkspacePath) {
			rootFound = true
			if row.Branch != rec.Metadata.Branch {
				return nil, true, fmt.Errorf("%w: session %s root branch differs from its repository record", ErrMultiRepoBranchMismatch, rec.ID)
			}
		}
	}
	if !rootFound {
		return nil, true, fmt.Errorf("%w: session %s root is not a recorded repository worktree", ErrMultiRepoRepoMismatch, rec.ID)
	}
	for _, repo := range expected {
		row, found := byKey[string(repo.RepoKey)]
		if !found {
			return nil, true, fmt.Errorf("%w: expected repo %s", ErrMultiRepoWorktreeMissing, repo.RepoKey)
		}
		if row.ProjectID != repo.ProjectID ||
			(repo.SessionWorktreeRef != "" && !sameWorkspacePath(row.WorktreePath, repo.SessionWorktreeRef)) ||
			(repo.ExpectedBaseSHA != "" && (!strings.EqualFold(row.BaseSHA, repo.ExpectedBaseSHA) || !strings.EqualFold(row.CreationSHA, repo.ExpectedBaseSHA))) {
			return nil, true, fmt.Errorf("%w: repo %s differs from its workflow binding", ErrMultiRepoRepoMismatch, repo.RepoKey)
		}
		delete(byKey, string(repo.RepoKey))
	}
	if len(expected) > 0 && len(byKey) != 0 {
		return nil, true, fmt.Errorf("%w: session %s has an unexpected repository record", ErrMultiRepoRepoMismatch, rec.ID)
	}
	infos := m.multiRepoRows(rows)
	sortRepoInfosByKey(infos)
	return infos, true, nil
}

// sortRepoInfosByKey gives the repo infos one deterministic order (repo key), so
// a caller that reports or compares them cannot depend on map or row order.
func sortRepoInfosByKey(infos []ports.WorkspaceRepoInfo) {
	sort.SliceStable(infos, func(i, j int) bool { return infos[i].RepoName < infos[j].RepoName })
}
