package gitworktree

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/filesnapshot"
	"github.com/aoagents/agent-orchestrator/backend/internal/gitdefault"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	defaultGitBinary              = "git"
	defaultBranchResolutionBudget = 5 * time.Second
)

var (
	ErrUnsafePath = ports.ErrWorkspaceUnsafePath
)

var (
	teardownMu    sync.Mutex
	teardownLocks = map[string]*sync.Mutex{}
)

func repoTeardownLock(repo string) (func(), error) {
	key, err := physicalAbs(repo)
	if err != nil {
		return nil, err
	}
	teardownMu.Lock()
	m := teardownLocks[key]
	if m == nil {
		m = &sync.Mutex{}
		teardownLocks[key] = m
	}
	teardownMu.Unlock()
	m.Lock()
	return m.Unlock, nil
}

var ErrPreservedConflict = ports.ErrPreservedConflict

var (
	ErrBranchCheckedOutElsewhere = ports.ErrWorkspaceBranchCheckedOutElsewhere
	ErrBranchNotFetched          = ports.ErrWorkspaceBranchNotFetched
	ErrBranchInvalid             = ports.ErrWorkspaceBranchInvalid
	ErrDefaultBranchUnresolved   = ports.ErrWorkspaceDefaultBranchUnresolved
	ErrWorktreeLocked            = ports.ErrWorkspaceLocked
	ErrPathOccupied              = ports.ErrWorkspacePathOccupied
)

type RepoResolver interface {
	RepoPath(projectID domain.ProjectID) (string, error)
}

type StaticRepoResolver map[domain.ProjectID]string

func (r StaticRepoResolver) RepoPath(projectID domain.ProjectID) (string, error) {
	path := r[projectID]
	if path == "" {
		return "", fmt.Errorf("gitworktree: no repo configured for project %q", projectID)
	}
	return path, nil
}

type Options struct {
	Binary       string
	ManagedRoot  string
	RepoResolver RepoResolver
	Logger       *slog.Logger
}

type Workspace struct {
	binary      string
	managedRoot string
	repos       RepoResolver
	run         commandRunner
	logger      *slog.Logger
	discards    sync.WaitGroup
}

type commandRunner func(ctx context.Context, binary string, args ...string) ([]byte, error)

var _ ports.Workspace = (*Workspace)(nil)
var _ ports.WorkspaceDefaultBranchRefresher = (*Workspace)(nil)
var _ ports.WorkspaceProject = (*Workspace)(nil)
var _ ports.WorkspaceObserver = (*Workspace)(nil)
var _ ports.WorkspaceReclaimer = (*Workspace)(nil)
var _ ports.WorkspacePreparationBranchCleaner = (*Workspace)(nil)

func New(opts Options) (*Workspace, error) {
	binary := opts.Binary
	if binary == "" {
		binary = defaultGitBinary
	}
	if opts.ManagedRoot == "" {
		return nil, errors.New("gitworktree: ManagedRoot is required")
	}
	if opts.RepoResolver == nil {
		return nil, errors.New("gitworktree: RepoResolver is required")
	}
	root, err := physicalAbs(opts.ManagedRoot)
	if err != nil {
		return nil, fmt.Errorf("gitworktree: managed root: %w", err)
	}
	w := &Workspace{
		binary:      binary,
		managedRoot: filepath.Clean(root),
		repos:       opts.RepoResolver,
		run:         runCommand,
		logger:      opts.Logger,
	}
	w.sweepDiscarded()
	return w, nil
}

func (w *Workspace) ResolveDefaultBranch(ctx context.Context, repoPath, configuredBranch string) (ports.WorkspaceDefaultBranch, error) {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return ports.WorkspaceDefaultBranch{}, fmt.Errorf("gitworktree: repo path: %w", err)
	}
	branch := strings.TrimSpace(configuredBranch)
	if branch == "" {
		resolver := gitdefault.New(w.binary, gitdefault.Runner(w.run))
		resolution, err := resolver.Resolve(ctx, ctx, repo)
		if err != nil {
			return ports.WorkspaceDefaultBranch{}, fmt.Errorf("gitworktree: resolve repository default for %q: %w", repo, err)
		}
		return ports.WorkspaceDefaultBranch{
			Remote:  resolution.Remote,
			Branch:  resolution.Branch,
			BaseRef: resolution.Ref,
		}, nil
	}
	remote := "origin"
	qualifiedRemote := false
	if candidateRemote, candidateBranch, ok := strings.Cut(branch, "/"); ok && candidateRemote != "" && candidateBranch != "" {
		exists, err := w.remoteExists(ctx, repo, candidateRemote)
		if err != nil {
			return ports.WorkspaceDefaultBranch{}, err
		}
		if exists {
			remote = candidateRemote
			branch = candidateBranch
			qualifiedRemote = true
		}
	}
	if err := w.validateBranch(ctx, repo, branch); err != nil {
		return ports.WorkspaceDefaultBranch{}, err
	}
	remoteRef := "refs/remotes/" + remote + "/" + branch
	if exists, err := w.refExists(ctx, repo, remoteRef); err != nil {
		return ports.WorkspaceDefaultBranch{}, err
	} else if exists {
		return ports.WorkspaceDefaultBranch{
			Remote:  remote,
			Branch:  branch,
			BaseRef: remoteRef,
		}, nil
	}
	if !qualifiedRemote {
		localRef := "refs/heads/" + branch
		if exists, err := w.refExists(ctx, repo, localRef); err != nil {
			return ports.WorkspaceDefaultBranch{}, err
		} else if exists {
			return ports.WorkspaceDefaultBranch{
				Remote:  remote,
				Branch:  branch,
				BaseRef: localRef,
			}, nil
		}
	}
	return ports.WorkspaceDefaultBranch{
		Remote:  remote,
		Branch:  branch,
		BaseRef: remoteRef,
	}, nil
}

func (w *Workspace) FetchDefaultBranch(ctx context.Context, repoPath string, target ports.WorkspaceDefaultBranch) error {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	if strings.TrimSpace(target.Remote) == "" {
		return errors.New("gitworktree: remote is required")
	}
	if strings.TrimSpace(target.Branch) == "" {
		return errors.New("gitworktree: branch is required")
	}
	wantRef := "refs/remotes/" + target.Remote + "/" + target.Branch
	localRef := "refs/heads/" + target.Branch
	if target.BaseRef == localRef {
		return nil
	}
	if target.BaseRef != wantRef {
		return fmt.Errorf("gitworktree: invalid default branch target %q (want %q)", target.BaseRef, wantRef)
	}
	if err := w.validateBranch(ctx, repo, target.Branch); err != nil {
		return err
	}
	if _, err := w.run(ctx, w.binary, fetchBranchArgs(repo, target.Remote, target.Branch)...); err != nil {
		return fmt.Errorf("gitworktree: fetch %s %s: %w", target.Remote, target.Branch, err)
	}
	return nil
}

func (w *Workspace) remoteExists(ctx context.Context, repo, remote string) (bool, error) {
	out, err := w.run(ctx, w.binary, remoteListArgs(repo)...)
	if err != nil {
		return false, fmt.Errorf("gitworktree: list remotes: %w", err)
	}
	for _, candidate := range strings.Fields(string(out)) {
		if candidate == remote {
			return true, nil
		}
	}
	return false, nil
}

func (w *Workspace) Create(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	if err := validateConfig(cfg); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	repo, err := w.repoPathForConfig(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.enableWorktreeConfig(ctx, repo); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.validateBranch(ctx, repo, cfg.Branch); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if cfg.RequireNewBranch {
		exists, err := w.branchNameTaken(ctx, repo, cfg.Branch, true)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		if exists {
			return ports.WorkspaceInfo{}, fmt.Errorf("%w: %q", ports.ErrWorkspaceBranchAlreadyExists, cfg.Branch)
		}
	}
	if cfg.FreshBranch && !cfg.RequireNewBranch {
		requested := cfg.Branch
		for suffix := 2; ; suffix++ {
			exists, err := w.branchNameTaken(ctx, repo, cfg.Branch, true)
			if err != nil {
				return ports.WorkspaceInfo{}, err
			}
			if !exists {
				break
			}
			if suffix > 100 {
				return ports.WorkspaceInfo{}, fmt.Errorf("gitworktree: no unused branch for %q", cfg.Branch)
			}
			cfg.Branch = fmt.Sprintf("%s-%d", requested, suffix)
		}
	}
	path, err := w.managedPath(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if info, ok, err := w.existingWorktree(ctx, repo, path, cfg); err != nil {
		return ports.WorkspaceInfo{}, err
	} else if ok {
		if cfg.FreshBranch {
			return ports.WorkspaceInfo{}, fmt.Errorf("gitworktree: prepared workspace path %q already exists", path)
		}
		refs, err := w.resolveWorktreeRefsWithBudget(ctx, repo, cfg.Branch, cfg.BaseBranch)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		info.BaseRef = refs.baseRef
		return info, nil
	}
	seedRef := ""
	seedSHA := ""
	if cfg.FreshBranch && cfg.StartRef == "" {
		refs, err := w.resolveWorktreeRefsWithBudget(ctx, repo, cfg.Branch, cfg.BaseBranch)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		cfg.BaseRef, seedRef = refs.baseRef, refs.seedRef
		if cfg.StartPoint != "" {
			seedRef = cfg.StartPoint
		}
		seedSHA, err = w.revParse(ctx, repo, seedRef)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
	}
	if cfg.FreshBranch && cfg.StartRef != "" {
		seedRef = cfg.StartRef
		seedSHA, err = w.revParse(ctx, repo, seedRef)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
	}
	baseRef, err := w.addWorktree(ctx, repo, path, cfg.Branch, cfg.BaseBranch, cfg.BaseRef, seedRef, true, cfg.RequireNewBranch)
	if err != nil {
		if cfg.FreshBranch && !errors.Is(err, ErrPathOccupied) {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			info := ports.WorkspaceInfo{Path: path, Branch: cfg.Branch, BaseSHA: seedSHA, BaseRef: cfg.BaseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: repo}
			remaining, cleanupErr := w.rollbackPreparedAdd(cleanupCtx, info)
			if cleanupErr != nil {
				return remaining, errors.Join(err, cleanupErr)
			}
		}
		return ports.WorkspaceInfo{}, err
	}
	var baseSHA string
	if cfg.FreshBranch {
		baseSHA = seedSHA
	}
	w.deleteOrphanedFileSnapshots(ctx, repo)
	return ports.WorkspaceInfo{Path: path, Branch: cfg.Branch, BaseSHA: baseSHA, BaseRef: baseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: repo}, nil
}

func (w *Workspace) rollbackPreparedAdd(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceInfo, error) {
	records, err := w.listRecords(ctx, info.RepoPath)
	if err != nil {
		return info, err
	}
	if record, ok := findWorktree(records, info.Path); ok {
		if record.Branch != info.Branch {
			info.Path = ""
			return info, fmt.Errorf("gitworktree: prepared path is registered on another branch %q", record.Branch)
		}
		if err := w.Destroy(ctx, info); err != nil {
			return info, err
		}
	} else {
		nonEmpty, err := pathExistsNonEmpty(info.Path)
		if err != nil {
			return info, err
		}
		if nonEmpty {
			return info, fmt.Errorf("gitworktree: preserve unregistered partial worktree %q: %w", info.Path, ports.ErrWorkspaceDirty)
		}
		if err := os.Remove(info.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return info, err
		}
		info.Path = ""
	}
	if err := w.DeletePreparedBranch(ctx, info); err != nil {
		return info, err
	}
	return ports.WorkspaceInfo{}, nil
}

func (w *Workspace) CreateWorkspaceProject(ctx context.Context, cfg ports.WorkspaceProjectConfig) (ports.WorkspaceProjectInfo, error) {
	if err := validateWorkspaceProjectConfig(cfg); err != nil {
		return ports.WorkspaceProjectInfo{}, err
	}
	rootRepo, err := physicalAbs(cfg.RootRepoPath)
	if err != nil {
		return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: root repo path: %w", err)
	}
	if err := w.enableWorktreeConfig(ctx, rootRepo); err != nil {
		return ports.WorkspaceProjectInfo{}, err
	}
	rootPath, err := w.managedPath(ports.WorkspaceConfig{
		ProjectID:     cfg.ProjectID,
		SessionID:     cfg.SessionID,
		Kind:          cfg.Kind,
		SessionPrefix: cfg.SessionPrefix,
		Branch:        firstNonEmpty(cfg.Branch, defaultSessionBranchName(cfg.SessionID)),
	})
	if err != nil {
		return ports.WorkspaceProjectInfo{}, err
	}
	repos := make([]workspaceProjectRepo, 0, len(cfg.Repos)+1)
	repos = append(repos, workspaceProjectRepo{
		name:       domain.RootWorkspaceRepoName,
		repoPath:   rootRepo,
		outputPath: rootPath,
		baseBranch: cfg.BaseBranch,
	})
	for _, child := range cfg.Repos {
		repoPath, err := physicalAbs(child.RepoPath)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: child repo %q path: %w", child.Name, err)
		}
		if err := w.enableWorktreeConfig(ctx, repoPath); err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: child repo %q: %w", child.Name, err)
		}
		rel, err := cleanRelativePath(child.RelativePath)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: child repo %q: %w", child.Name, err)
		}
		outPath, err := w.validateManagedPath(filepath.Join(rootPath, filepath.FromSlash(rel)))
		if err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: child repo %q path: %w", child.Name, err)
		}
		repos = append(repos, workspaceProjectRepo{
			name:         child.Name,
			relativePath: rel,
			repoPath:     repoPath,
			outputPath:   outPath,
			baseBranch:   child.BaseBranch,
			baseRef:      child.BaseRef,
		})
	}
	requestedBranch := firstNonEmpty(cfg.Branch, defaultSessionBranchName(cfg.SessionID))
	existing := make(map[int]struct{}, len(repos))
	recovering := false
	for i, repo := range repos {
		records, err := w.listRecords(ctx, repo.repoPath)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, err
		}
		record, ok := findWorktree(records, repo.outputPath)
		if !ok {
			continue
		}
		if record.Branch != requestedBranch {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf(
				"gitworktree: managed path %q already contains branch %q, want %q",
				repo.outputPath, record.Branch, requestedBranch,
			)
		}
		recovering = true
		missing, err := registeredWorktreeDirMissing(record)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, err
		}
		if !missing {
			existing[i] = struct{}{}
		}
	}
	branch := requestedBranch
	if !recovering {
		branch, err = w.workspaceProjectBranch(ctx, repos, requestedBranch, cfg.FreshBranch, cfg.RequireNewBranch)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, err
		}
	}
	remoteCtx, cancelRemote := context.WithTimeout(ctx, defaultBranchResolutionBudget)
	defer cancelRemote()
	for i := range repos {
		if i == 0 && cfg.StartRef != "" {
			repos[i].seedRef, repos[i].baseRef = cfg.StartRef, cfg.StartRef
			continue
		}
		if i == 0 {
			refs, err := w.resolveWorkspaceRootRefs(ctx, repos[i].repoPath, repos[i].baseBranch)
			if err != nil {
				return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: resolve workspace repo %q base: %w", repos[i].name, err)
			}
			repos[i].seedRef = refs.seedRef
			repos[i].baseRef = refs.baseRef
			continue
		}
		if repos[i].baseRef != "" {
			repos[i].seedRef = repos[i].baseRef
			requestedRef := "origin/" + branch
			if exists, err := w.refExists(ctx, repos[i].repoPath, requestedRef); err != nil {
				return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: inspect workspace repo %q requested branch: %w", repos[i].name, err)
			} else if exists {
				repos[i].seedRef = requestedRef
			}
			continue
		}
		refs, err := w.resolveWorktreeRefs(ctx, remoteCtx, repos[i].repoPath, branch, repos[i].baseBranch)
		if err != nil {
			return ports.WorkspaceProjectInfo{}, fmt.Errorf("gitworktree: resolve workspace repo %q base: %w", repos[i].name, err)
		}
		repos[i].seedRef = refs.seedRef
		repos[i].baseRef = refs.baseRef
	}
	created := make([]workspaceProjectRepo, 0, len(repos))
	touched := make([]ports.WorkspaceRepoInfo, 0, len(repos))
	out := ports.WorkspaceProjectInfo{Worktrees: make([]ports.WorkspaceRepoInfo, 0, len(repos))}
	fail := func(cause error) (ports.WorkspaceProjectInfo, error) {
		if cfg.FreshBranch {
			return w.rollbackWorkspaceProjectPreparation(ctx, out, touched, cause)
		}
		for i := len(created) - 1; i >= 0; i-- {
			_ = w.forceDestroyPath(ctx, created[i].repoPath, created[i].outputPath)
		}
		return ports.WorkspaceProjectInfo{}, cause
	}
	for repoIndex, repo := range repos {
		creationSHA := ""
		if cfg.FreshBranch {
			localBranch, err := w.refExists(ctx, repo.repoPath, "refs/heads/"+branch)
			if err != nil {
				return fail(err)
			}
			if localBranch {
				creationSHA = ""
			} else {
				creationSHA, err = w.revParse(ctx, repo.repoPath, repo.seedRef)
				if err != nil {
					return fail(err)
				}
			}
		}
		info := ports.WorkspaceRepoInfo{
			RepoName: repo.name, RepoPath: repo.repoPath, Path: repo.outputPath,
			Branch: branch, CreationSHA: creationSHA, BaseRef: repo.baseRef,
			SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RelativePath: repo.relativePath,
		}
		baseSHA, err := w.revParse(ctx, repo.repoPath, repo.baseRef)
		if err == nil {
			info.BaseSHA = baseSHA
			if _, ok := existing[repoIndex]; !ok {
				if cfg.FreshBranch {
					touched = append(touched, info)
				}
				baseSHA, err = w.createWorkspaceProjectRepo(ctx, repo, branch, cfg.RequireNewBranch)
				if err == nil {
					created = append(created, repo)
				}
			}
		}
		if err != nil {
			return fail(err)
		}
		info.BaseSHA = baseSHA
		out.Worktrees = append(out.Worktrees, info)
		if repo.name == domain.RootWorkspaceRepoName {
			rootSHA := baseSHA
			if cfg.FreshBranch {
				rootSHA = creationSHA
			}
			out.Root = ports.WorkspaceInfo{Path: repo.outputPath, Branch: branch, BaseSHA: rootSHA, BaseRef: repo.baseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}
		}
		if repoIndex == 0 {
			_, rootAlreadyExisted := existing[repoIndex]
			if !rootAlreadyExisted {
				if err := copyWorkspaceAssets(rootRepo, rootPath, cfg.Assets); err != nil {
					return fail(err)
				}
			}
		}
	}
	return out, nil
}

func (w *Workspace) rollbackWorkspaceProjectPreparation(ctx context.Context, out ports.WorkspaceProjectInfo, touched []ports.WorkspaceRepoInfo, cause error) (ports.WorkspaceProjectInfo, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	keep := make(map[string]bool, len(out.Worktrees)+len(touched))
	for _, row := range out.Worktrees {
		keep[row.Path] = true
	}
	for i := len(touched) - 1; i >= 0; i-- {
		row := touched[i]
		info := ports.WorkspaceInfo{Path: row.Path, Branch: row.Branch, BaseSHA: row.CreationSHA, BaseRef: row.BaseRef, RepoPath: row.RepoPath, SessionID: row.SessionID, ProjectID: row.ProjectID}
		if _, err := w.rollbackPreparedAdd(cleanupCtx, info); err != nil {
			cause = errors.Join(cause, err)
			keep[row.Path] = true
			break
		}
		delete(keep, row.Path)
	}
	remaining := ports.WorkspaceProjectInfo{Worktrees: make([]ports.WorkspaceRepoInfo, 0, len(keep))}
	for _, row := range append(out.Worktrees, touched...) {
		if !keep[row.Path] {
			continue
		}
		remaining.Worktrees = append(remaining.Worktrees, row)
		delete(keep, row.Path)
		if row.RepoName == domain.RootWorkspaceRepoName {
			remaining.Root = ports.WorkspaceInfo{Path: row.Path, Branch: row.Branch, BaseSHA: row.CreationSHA, BaseRef: row.BaseRef, RepoPath: row.RepoPath, SessionID: row.SessionID, ProjectID: row.ProjectID}
		}
	}
	return remaining, cause
}

func (w *Workspace) resolveWorkspaceRootRefs(ctx context.Context, repo, configuredBranch string) (worktreeRefs, error) {
	branch := strings.TrimSpace(configuredBranch)
	if branch == "" {
		if out, err := w.run(ctx, w.binary, "-C", repo, "config", "--local", "--get", gitdefault.ManagedDefaultConfigKey); err == nil {
			branch = strings.TrimSpace(string(out))
		}
	}
	if branch == "" {
		return w.repairWorkspaceRootDefault(ctx, repo)
	}
	return w.resolveWorktreeRefsFromDefault(ctx, repo, "", branch)
}

func (w *Workspace) repairWorkspaceRootDefault(ctx context.Context, repo string) (worktreeRefs, error) {
	out, err := w.run(ctx, w.binary, "-C", repo, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		return worktreeRefs{}, err
	}
	branch := strings.TrimSpace(string(out))
	if err := w.validateBranch(ctx, repo, branch); err != nil {
		return worktreeRefs{}, err
	}
	ref := "refs/heads/" + branch
	exists, err := w.refExists(ctx, repo, ref)
	if err != nil {
		return worktreeRefs{}, err
	}
	if !exists {
		return worktreeRefs{}, fmt.Errorf("workspace root local branch %q has no commit", branch)
	}
	if _, err := w.run(ctx, w.binary, "-C", repo, "config", "--local", gitdefault.ManagedDefaultConfigKey, branch); err != nil {
		return worktreeRefs{}, err
	}
	return worktreeRefs{seedRef: ref, baseRef: ref}, nil
}

func copyWorkspaceAssets(sourceRoot, destinationRoot string, assets []ports.WorkspaceProjectAssetConfig) error {
	for _, asset := range assets {
		rel, err := cleanRelativePath(asset.RelativePath)
		if err != nil || hasWorkspaceManagedComponent(rel) {
			return fmt.Errorf("gitworktree: workspace asset %q: %w", asset.RelativePath, ErrUnsafePath)
		}
		source, err := physicalAbs(asset.SourcePath)
		if err != nil || !assetPathWithin(sourceRoot, source) {
			return fmt.Errorf("gitworktree: workspace asset %q source: %w", asset.RelativePath, ErrUnsafePath)
		}
		destination := filepath.Join(destinationRoot, filepath.FromSlash(rel))
		if !assetPathWithin(destinationRoot, destination) {
			return fmt.Errorf("gitworktree: workspace asset %q destination: %w", asset.RelativePath, ErrUnsafePath)
		}
		if err := copyWorkspaceAssetTree(sourceRoot, source, destination); err != nil {
			return fmt.Errorf("gitworktree: copy workspace asset %q: %w", asset.RelativePath, err)
		}
	}
	return nil
}

func copyWorkspaceAssetTree(sourceRoot, source, destination string) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(source)
		if err != nil {
			return err
		}
		if filepath.IsAbs(target) {
			return fmt.Errorf("absolute symlink %q: %w", source, ErrUnsafePath)
		}
		resolved := filepath.Clean(filepath.Join(filepath.Dir(source), target))
		if !assetPathWithin(sourceRoot, resolved) || hasWorkspaceManagedComponent(relativeOrEmpty(sourceRoot, resolved)) {
			return fmt.Errorf("symlink %q escapes or targets managed data: %w", source, ErrUnsafePath)
		}
		physicalTarget, err := filepath.EvalSymlinks(resolved)
		if err != nil || !assetPathWithin(sourceRoot, physicalTarget) {
			return fmt.Errorf("symlink %q has an unsafe or unresolved target: %w", source, ErrUnsafePath)
		}
		if existing, err := os.Lstat(destination); err == nil {
			if existing.IsDir() {
				return fmt.Errorf("cannot overlay symlink on directory %q", destination)
			}
			if err := os.Remove(destination); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return os.Symlink(target, destination)
	}
	if info.IsDir() {
		if existing, err := os.Lstat(destination); err == nil {
			if !existing.IsDir() || existing.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("cannot overlay directory on non-directory %q", destination)
			}
		} else if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(destination, info.Mode().Perm()); err != nil {
				return err
			}
		} else {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if entry.Name() == ".git" || entry.Name() == ".ao" {
				continue
			}
			if err := copyWorkspaceAssetTree(sourceRoot, filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported file type at %q", source)
	}
	if existing, err := os.Lstat(destination); err == nil {
		if !existing.Mode().IsRegular() {
			return fmt.Errorf("cannot overlay file on non-regular path %q", destination)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if err := out.Chmod(info.Mode().Perm()); err != nil {
		_ = out.Close()
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func assetPathWithin(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), filepath.Clean(path))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func relativeOrEmpty(root, path string) string {
	rel, _ := filepath.Rel(root, path)
	return rel
}

func hasWorkspaceManagedComponent(path string) bool {
	for _, component := range strings.Split(filepath.ToSlash(path), "/") {
		if component == ".git" || component == ".ao" {
			return true
		}
	}
	return false
}

func (w *Workspace) DestroyWorkspaceProject(ctx context.Context, info ports.WorkspaceProjectInfo) error {
	var firstErr error
	for i := len(info.Worktrees) - 1; i >= 0; i-- {
		wt := info.Worktrees[i]
		if wt.Path == "" {
			continue
		}
		repoPath := wt.RepoPath
		if repoPath == "" {
			if firstErr == nil {
				firstErr = fmt.Errorf("gitworktree: missing repo path for worktree %q", wt.Path)
			}
			continue
		}
		baseSHA := wt.CreationSHA
		if baseSHA == "" {
			baseSHA = wt.BaseSHA
		}
		err := w.Destroy(ctx, ports.WorkspaceInfo{Path: wt.Path, Branch: wt.Branch, BaseSHA: baseSHA, SessionID: wt.SessionID, ProjectID: wt.ProjectID, RepoPath: repoPath})
		if errors.Is(err, ports.ErrWorkspaceDirty) {
			return err
		}
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (w *Workspace) Destroy(ctx context.Context, info ports.WorkspaceInfo) error {
	_, err := w.DestroyReclaim(ctx, info)
	return err
}

func (w *Workspace) DestroyReclaim(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	reclaim, err := w.destroy(ctx, info)
	if err == nil {
		w.deleteFileSnapshots(ctx, info)
	}
	return reclaim, err
}

func (w *Workspace) deleteFileSnapshots(ctx context.Context, info ports.WorkspaceInfo) {
	if info.SessionID == "" {
		return
	}
	repo, err := w.repoPathForInfo(info)
	if err == nil {
		err = filesnapshot.DeleteAll(ctx, repo, filesnapshot.SessionPrefix(info.SessionID))
	}
	if err != nil {
		w.logf("gitworktree: delete file snapshots for session %s: %v", info.SessionID, err)
		return
	}
	w.deleteOrphanedFileSnapshots(ctx, repo)
}

func (w *Workspace) deleteOrphanedFileSnapshots(ctx context.Context, repo string) {
	records, err := w.listRecords(ctx, repo)
	if err == nil {
		paths := make([]string, len(records))
		for i, record := range records {
			paths[i] = record.Path
		}
		err = filesnapshot.DeleteOrphans(ctx, repo, paths)
	}
	if err != nil {
		w.logf("gitworktree: delete orphaned file snapshots in %s: %v", repo, err)
	}
}

func (w *Workspace) DeletePreparedBranch(ctx context.Context, info ports.WorkspaceInfo) error {
	if info.Branch == "" || info.BaseSHA == "" {
		return nil
	}
	repo, err := w.repoPathForInfo(info)
	if err != nil {
		return err
	}
	if err := w.validateBranch(ctx, repo, info.Branch); err != nil {
		return err
	}
	unlock, err := repoTeardownLock(repo)
	if err != nil {
		return err
	}
	defer unlock()
	if err := w.requireReachableRepo(repo); err != nil {
		return err
	}
	branchRef := "refs/heads/" + info.Branch
	exists, err := w.refExists(ctx, repo, branchRef)
	if err != nil || !exists {
		return err
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return err
	}
	if _, checkedOut := findWorktreeByBranch(records, info.Branch); checkedOut {
		return fmt.Errorf("gitworktree: prepared branch %q is still checked out", info.Branch)
	}
	tip, err := w.revParse(ctx, repo, branchRef)
	if err != nil {
		return err
	}
	if tip != info.BaseSHA {
		return nil
	}
	if _, err := w.run(ctx, w.binary, "-C", repo, "update-ref", "-d", branchRef, tip); err != nil {
		return fmt.Errorf("gitworktree: delete unmodified prepared branch %q: %w", info.Branch, err)
	}
	return nil
}

func (w *Workspace) destroy(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceReclaim, error) {
	if info.Path == "" {
		return ports.WorkspaceReclaimAlreadyAbsent, fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	repo, err := w.repoPathForInfo(info)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, err
	}
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, err
	}
	unlock, err := repoTeardownLock(repo)
	if err != nil {
		return ports.WorkspaceReclaimAlreadyAbsent, fmt.Errorf("gitworktree: repo path: %w", err)
	}
	defer unlock()
	reclaim := ports.WorkspaceReclaimRemoved
	if _, statErr := os.Lstat(path); errors.Is(statErr, os.ErrNotExist) {
		reclaim = ports.WorkspaceReclaimAlreadyAbsent
	}
	if err := w.requireReachableRepo(repo); err != nil {
		return reclaim, err
	}
	if info.BaseSHA != "" {
		records, err := w.listRecords(ctx, repo)
		if err != nil {
			return reclaim, err
		}
		if _, registered := findWorktree(records, path); !registered {
			nonEmpty, err := pathExistsNonEmpty(path)
			if err != nil {
				return reclaim, err
			}
			if nonEmpty {
				return reclaim, fmt.Errorf("gitworktree: preserve unregistered partial worktree %q: %w", path, ports.ErrWorkspaceDirty)
			}
		}
	}
	if handled, err := w.discardWorktree(ctx, repo, path); handled {
		return reclaim, err
	}
	_, removeErr := w.run(ctx, w.binary, worktreeRemoveArgs(repo, path)...)
	if removeErr != nil && w.liftStaleAddLock(ctx, repo, path) {
		_, removeErr = w.run(ctx, w.binary, worktreeRemoveArgs(repo, path)...)
	}
	if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
		return reclaim, fmt.Errorf("gitworktree: worktree prune: %w", err)
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return reclaim, err
	}
	if rec, ok := findWorktree(records, path); ok {
		if removeErr != nil {
			if rec.Locked || isLockedWorktreeRemoveError(removeErr) {
				return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: %w (worktree remove: %w)", path, ports.ErrWorkspaceLocked, removeErr)
			}
			dirty, statusErr := w.isDirty(ctx, path)
			if statusErr == nil && dirty {
				return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: %w (worktree remove: %w)", path, ports.ErrWorkspaceDirty, removeErr)
			}
			if statusErr != nil {
				return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune (worktree remove: %w; dirty probe: %w)", path, removeErr, statusErr)
			}
			return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune (worktree remove: %w)", path, removeErr)
		}
		return reclaim, fmt.Errorf("gitworktree: refusing to remove %q: path is still registered after git worktree prune", path)
	}
	if err := removeAllWithRetry(ctx, path); err != nil {
		if errors.Is(err, errRemoveRetryExhausted) {
			return reclaim, fmt.Errorf("gitworktree: remove unregistered path %q: %w (cause: %w)", path, ports.ErrWorkspaceDeferred, err)
		}
		return reclaim, fmt.Errorf("gitworktree: remove unregistered path %q: %w", path, err)
	}
	return reclaim, nil
}

func (w *Workspace) ForceDestroy(ctx context.Context, info ports.WorkspaceInfo) error {
	if info.Path == "" {
		return fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	repo, err := w.repoPathForInfo(info)
	if err != nil {
		return err
	}
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	unlock, err := repoTeardownLock(repo)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	defer unlock()
	if err := w.requireReachableRepo(repo); err != nil {
		return err
	}
	if discarded, moved := w.discard(path); moved {
		if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
			w.undiscard(discarded, path)
			return fmt.Errorf("gitworktree: worktree prune: %w", err)
		}
		w.removeInBackground(discarded)
		return nil
	}
	_, _ = w.run(ctx, w.binary, worktreeForceRemoveArgs(repo, path)...)
	if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
		return fmt.Errorf("gitworktree: worktree prune: %w", err)
	}
	if err := removeAllWithRetry(ctx, path); err != nil {
		return fmt.Errorf("gitworktree: force remove path %q: %w", path, err)
	}
	return nil
}

func (w *Workspace) StashUncommitted(ctx context.Context, info ports.WorkspaceInfo) (string, error) {
	if info.Path == "" {
		return "", fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	if info.SessionID == "" {
		return "", errors.New("gitworktree: session id is required for StashUncommitted")
	}
	repo, err := w.repoPathForInfo(info)
	if err != nil {
		return "", err
	}
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return "", err
	}
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("gitworktree: stale worktree %q: %w", path, ports.ErrWorkspaceStale)
		}
		return "", fmt.Errorf("gitworktree: stat worktree %q: %w", path, err)
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return "", err
	}
	if _, ok := findWorktree(records, path); !ok {
		return "", fmt.Errorf("gitworktree: worktree %q is not registered: %w", path, ports.ErrWorkspaceStale)
	}

	dirty, err := w.isDirty(ctx, path)
	if err != nil {
		if isNotGitRepositoryError(err) {
			return "", fmt.Errorf("gitworktree: stale worktree %q: %w", path, ports.ErrWorkspaceStale)
		}
		return "", fmt.Errorf("gitworktree: StashUncommitted dirty check: %w", err)
	}
	if !dirty {
		return "", nil
	}

	if skipCount, err := w.countIgnoredPaths(ctx, path); err == nil {
		slog.InfoContext(ctx, "gitworktree: StashUncommitted skipping ignored paths",
			"session", string(info.SessionID),
			"skipped_count", skipCount,
		)
	}

	tmpIdx, err := os.CreateTemp("", "ao-preserve-idx-*")
	if err != nil {
		return "", fmt.Errorf("gitworktree: reserve temp index path: %w", err)
	}
	tmpIdxPath := tmpIdx.Name()
	_ = tmpIdx.Close()
	_ = os.Remove(tmpIdxPath)
	defer func() { _ = os.Remove(tmpIdxPath) }()

	addCmd := aoprocess.CommandContext(ctx, w.binary, addAllTempIndexArgs(path)...)
	addCmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+tmpIdxPath)
	if out, err := addCmd.CombinedOutput(); err != nil {
		return "", commandError{args: append([]string{w.binary}, addAllTempIndexArgs(path)...), output: string(out), err: err}
	}

	writeTreeCmd := aoprocess.CommandContext(ctx, w.binary, writeTreeArgs(path)...)
	writeTreeCmd.Env = append(os.Environ(), "GIT_INDEX_FILE="+tmpIdxPath)
	treeOut, err := writeTreeCmd.CombinedOutput()
	if err != nil {
		return "", commandError{args: append([]string{w.binary}, writeTreeArgs(path)...), output: string(treeOut), err: err}
	}
	treeSHA := strings.TrimSpace(string(treeOut))

	headOut, headErr := w.run(ctx, w.binary, revParseHeadArgs(path)...)
	headSHA := ""
	if headErr == nil {
		headSHA = strings.TrimSpace(string(headOut))
	}

	if headSHA != "" {
		headTreeOut, err := w.run(ctx, w.binary, "-C", path, "rev-parse", headSHA+"^{tree}")
		if err == nil {
			headTreeSHA := strings.TrimSpace(string(headTreeOut))
			if headTreeSHA == treeSHA {
				return "", nil
			}
		}
	}

	msg := "ao preserved " + string(info.SessionID)
	commitOut, err := w.run(ctx, w.binary, commitTreeArgs(path, treeSHA, headSHA, msg)...)
	if err != nil {
		return "", fmt.Errorf("gitworktree: commit-tree: %w", err)
	}
	commitSHA := strings.TrimSpace(string(commitOut))

	ref := "refs/ao/preserved/" + string(info.SessionID)
	if _, err := w.run(ctx, w.binary, updateRefArgs(path, ref, commitSHA)...); err != nil {
		return "", fmt.Errorf("gitworktree: update-ref %q: %w", ref, err)
	}
	return ref, nil
}

func isNotGitRepositoryError(err error) bool {
	return strings.Contains(err.Error(), "not a git repository")
}

func (w *Workspace) countIgnoredPaths(ctx context.Context, worktree string) (int, error) {
	out, err := w.run(ctx, w.binary, ignoredCountArgs(worktree)...)
	if err != nil {
		return 0, fmt.Errorf("gitworktree: count ignored: %w", err)
	}
	count := 0
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "!! ") {
			count++
		}
	}
	return count, nil
}

func (w *Workspace) ApplyPreserved(ctx context.Context, info ports.WorkspaceInfo, ref string) error {
	if info.Path == "" {
		return fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	if ref == "" {
		return errors.New("gitworktree: ApplyPreserved: ref must not be empty")
	}

	resolveOut, err := w.run(ctx, w.binary, revParseVerifyArgs(info.Path, ref)...)
	if err != nil {
		return fmt.Errorf("gitworktree: ApplyPreserved resolve ref %q: %w", ref, err)
	}
	commitSHA := strings.TrimSpace(string(resolveOut))

	applyErr := w.runCherryPickNoCommit(ctx, info.Path, commitSHA)
	if applyErr != nil {
		return fmt.Errorf("%w: %w", ErrPreservedConflict, applyErr)
	}

	if _, err := w.run(ctx, w.binary, deleteRefArgs(info.Path, ref)...); err != nil {
		slog.WarnContext(ctx, "gitworktree: ApplyPreserved could not delete preserve ref",
			"ref", ref,
			"err", err,
		)
	}
	return nil
}

func (w *Workspace) AddExclude(ctx context.Context, info ports.WorkspaceInfo, patterns ...string) error {
	if len(patterns) == 0 {
		return nil
	}
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	out, err := w.run(ctx, w.binary, "-C", path, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("gitworktree: AddExclude resolve git common dir: %w", err)
	}
	gitDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(path, gitDir)
	}
	infoDir := filepath.Join(gitDir, "info")
	if err := os.MkdirAll(infoDir, 0o750); err != nil {
		return fmt.Errorf("gitworktree: AddExclude create info dir: %w", err)
	}
	excludePath := filepath.Join(infoDir, "exclude")
	existing, _ := os.ReadFile(excludePath)
	var toAdd []string
	for _, p := range patterns {
		if !strings.Contains(string(existing), p) {
			toAdd = append(toAdd, p)
		}
	}
	if len(toAdd) == 0 {
		return nil
	}
	f, err := os.OpenFile(excludePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("gitworktree: AddExclude open exclude: %w", err)
	}
	defer func() { _ = f.Close() }()
	prefix := ""
	if len(existing) > 0 && !strings.HasSuffix(string(existing), "\n") {
		prefix = "\n"
	}
	if _, err := f.WriteString(prefix + strings.Join(toAdd, "\n") + "\n"); err != nil {
		return fmt.Errorf("gitworktree: AddExclude write exclude: %w", err)
	}
	return nil
}

const (
	maxObservedWorkspaceChanges = 500
	maxObservedWorkspaceCommits = 20
)

func (w *Workspace) ObserveWorkspace(ctx context.Context, info ports.WorkspaceInfo) (ports.WorkspaceObservation, error) {
	path := strings.TrimSpace(info.Path)
	if path == "" {
		return ports.WorkspaceObservation{}, errors.New("gitworktree: observe workspace path is required")
	}
	validated, err := w.validateManagedPath(path)
	if err != nil {
		return ports.WorkspaceObservation{}, err
	}
	path = validated

	head, err := w.run(ctx, w.binary, "-C", path, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return ports.WorkspaceObservation{}, fmt.Errorf("gitworktree: observe HEAD: %w", err)
	}
	branch := strings.TrimSpace(info.Branch)
	if out, branchErr := w.run(ctx, w.binary, "-C", path, "branch", "--show-current"); branchErr == nil {
		if current := strings.TrimSpace(string(out)); current != "" {
			branch = current
		}
	}
	statusOut, err := w.run(ctx, w.binary, "-C", path, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return ports.WorkspaceObservation{}, fmt.Errorf("gitworktree: observe status: %w", err)
	}
	changes, staged, untracked := parseObservedWorkspaceChanges(string(statusOut), maxObservedWorkspaceChanges)

	logFormat := "%H%x1f%s%x1f%aI%x1e"
	logOut, err := w.run(ctx, w.binary, "-C", path, "log", "-n", fmt.Sprintf("%d", maxObservedWorkspaceCommits), "--pretty=format:"+logFormat)
	if err != nil {
		return ports.WorkspaceObservation{}, fmt.Errorf("gitworktree: observe log: %w", err)
	}
	return ports.WorkspaceObservation{
		Path:      path,
		Branch:    branch,
		HeadSHA:   strings.TrimSpace(string(head)),
		Dirty:     len(changes) > 0,
		Staged:    staged,
		Untracked: untracked,
		Changes:   changes,
		Commits:   parseObservedWorkspaceCommits(string(logOut)),
	}, nil
}

func parseObservedWorkspaceChanges(output string, limit int) ([]ports.WorkspaceChange, bool, bool) {
	lines := strings.Split(strings.ReplaceAll(output, "\r\n", "\n"), "\n")
	changes := make([]ports.WorkspaceChange, 0, min(len(lines), limit))
	var staged, untracked bool
	for _, line := range lines {
		if len(line) < 3 {
			continue
		}
		status := line[:2]
		path := strings.TrimSpace(line[3:])
		if path == "" {
			continue
		}
		if status == "??" {
			untracked = true
		} else if status[0] != ' ' && status[0] != '?' {
			staged = true
		}
		if len(changes) < limit {
			changes = append(changes, ports.WorkspaceChange{Path: path, Status: status})
		}
	}
	return changes, staged, untracked
}

func parseObservedWorkspaceCommits(output string) []ports.WorkspaceCommit {
	records := strings.Split(output, "\x1e")
	commits := make([]ports.WorkspaceCommit, 0, len(records))
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x1f", 3)
		if len(fields) != 3 {
			continue
		}
		commits = append(commits, ports.WorkspaceCommit{
			SHA:        strings.TrimSpace(fields[0]),
			Subject:    strings.TrimSpace(fields[1]),
			AuthoredAt: strings.TrimSpace(fields[2]),
		})
	}
	return commits
}

func (w *Workspace) runCherryPickNoCommit(ctx context.Context, worktree, commitSHA string) error {
	args := cherryPickNoCommitArgs(worktree, commitSHA)
	cmd := aoprocess.CommandContext(ctx, w.binary, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return commandError{args: append([]string{w.binary}, args...), output: string(out), err: err}
	}
	return nil
}

func (w *Workspace) Restore(ctx context.Context, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, error) {
	if err := validateConfig(cfg); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	repo, err := w.repoPathForConfig(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	if err := w.enableWorktreeConfig(ctx, repo); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	path, err := w.restorePath(cfg)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	recreateBranch := cfg.Branch
	if rec, ok := findWorktree(records, path); ok {
		missing, err := registeredWorktreeDirMissing(rec)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		if !missing {
			branch := rec.Branch
			if branch == "" {
				branch = cfg.Branch
			}
			return ports.WorkspaceInfo{Path: path, Branch: branch, BaseRef: cfg.BaseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: repo}, nil
		}
		if rec.Branch != "" {
			recreateBranch = rec.Branch
		}
	} else if cfg.LastBranch != "" && cfg.LastBranch != cfg.Branch {
		exists, err := w.refExists(ctx, repo, "refs/heads/"+cfg.LastBranch)
		if err != nil {
			return ports.WorkspaceInfo{}, err
		}
		if _, elsewhere := findWorktreeByBranch(records, cfg.LastBranch); exists && !elsewhere {
			recreateBranch = cfg.LastBranch
		}
	}
	if nonEmpty, err := pathExistsNonEmpty(path); err != nil {
		return ports.WorkspaceInfo{}, err
	} else if nonEmpty {
		if cfg.Path == "" {
			return ports.WorkspaceInfo{}, fmt.Errorf("gitworktree: refusing to restore %q: path exists and is not a registered worktree", path)
		}
		if _, err := moveStrayPathAside(path); err != nil {
			return ports.WorkspaceInfo{}, err
		}
	}
	if err := w.validateBranch(ctx, repo, recreateBranch); err != nil {
		return ports.WorkspaceInfo{}, err
	}
	baseRef, err := w.addWorktree(ctx, repo, path, recreateBranch, cfg.BaseBranch, cfg.BaseRef, "", false, false)
	if err != nil {
		return ports.WorkspaceInfo{}, err
	}
	return ports.WorkspaceInfo{Path: path, Branch: recreateBranch, BaseRef: baseRef, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID, RepoPath: repo}, nil
}

func (w *Workspace) existingWorktree(ctx context.Context, repo, path string, cfg ports.WorkspaceConfig) (ports.WorkspaceInfo, bool, error) {
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return ports.WorkspaceInfo{}, false, err
	}
	if rec, ok := findWorktree(records, path); ok {
		missing, err := registeredWorktreeDirMissing(rec)
		if err != nil {
			return ports.WorkspaceInfo{}, false, err
		}
		if missing {
			return ports.WorkspaceInfo{}, false, nil
		}
		branch := rec.Branch
		if branch == "" {
			branch = cfg.Branch
		}
		return ports.WorkspaceInfo{Path: path, Branch: branch, SessionID: cfg.SessionID, ProjectID: cfg.ProjectID}, true, nil
	}
	return ports.WorkspaceInfo{}, false, nil
}

func registeredWorktreeDirMissing(rec worktreeRecord) (bool, error) {
	info, err := os.Stat(rec.Path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return false, fmt.Errorf("gitworktree: stat registered worktree %q: %w", rec.Path, err)
		}
		if rec.Locked {
			return false, fmt.Errorf(
				"%w: %q (branch %q) is registered but its directory is missing; unlock it (`git worktree unlock %s`) and retry, or remove the registration manually",
				ErrWorktreeLocked, rec.Path, rec.Branch, rec.Path,
			)
		}
		return true, nil
	}
	if !info.IsDir() {
		return false, fmt.Errorf("gitworktree: registered worktree %q is not a directory", rec.Path)
	}
	return false, nil
}

func (w *Workspace) addWorktree(ctx context.Context, repo, path, branch, baseBranch, baseRef, seedRef string, resolveExistingBase, requireNewBranch bool) (string, error) {
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return "", err
	}
	if conflict, ok := findWorktreeByBranch(records, branch); ok && filepath.Clean(conflict.Path) != filepath.Clean(path) {
		return "", fmt.Errorf("%w: %q is checked out at %q", ErrBranchCheckedOutElsewhere, branch, conflict.Path)
	}
	force, err := staleRegistrationForPath(records, path)
	if err != nil {
		return "", err
	}
	if err := w.clearOrphanedWorktreePath(ctx, repo, path, records); err != nil {
		return "", err
	}

	localBranch, err := w.refExists(ctx, repo, "refs/heads/"+branch)
	if err != nil {
		return "", err
	}
	if localBranch {
		if requireNewBranch {
			return "", fmt.Errorf("%w: %q", ports.ErrWorkspaceBranchAlreadyExists, branch)
		}
		if resolveExistingBase {
			refs, err := w.resolveWorktreeRefsWithBudget(ctx, repo, branch, baseBranch)
			if err != nil {
				if errors.Is(err, errNoBaseRef) {
					return "", fmt.Errorf("%w: %q has no local head, no remote, and no tag — run `git fetch` then retry", ErrBranchNotFetched, branch)
				}
				return "", err
			}
			baseRef = refs.baseRef
		}
		if _, err := w.run(ctx, w.binary, worktreeAddBranchArgs(repo, path, branch, force)...); err != nil {
			return "", fmt.Errorf("gitworktree: worktree add existing branch %q: %w", branch, err)
		}
		return baseRef, nil
	}
	if baseRef == "" {
		refs, resolveErr := w.resolveWorktreeRefsWithBudget(ctx, repo, branch, baseBranch)
		err = resolveErr
		if err != nil {
			if errors.Is(err, errNoBaseRef) {
				return "", fmt.Errorf("%w: %q has no local head, no remote, and no tag — run `git fetch` then retry", ErrBranchNotFetched, branch)
			}
			return "", err
		}
		baseRef = refs.baseRef
		if err := w.addNewBranchWorktree(ctx, repo, branch, path, refs.seedRef, force); err != nil {
			return "", fmt.Errorf("gitworktree: worktree add branch %q from %q: %w", branch, refs.seedRef, err)
		}
		return baseRef, nil
	}
	if seedRef == "" {
		seedRef = baseRef
		requestedRef := "origin/" + branch
		if exists, err := w.refExists(ctx, repo, requestedRef); err != nil {
			return "", err
		} else if exists {
			seedRef = requestedRef
		}
	}

	if err := w.addNewBranchWorktree(ctx, repo, branch, path, seedRef, force); err != nil {
		return "", fmt.Errorf("gitworktree: worktree add branch %q from %q: %w", branch, seedRef, err)
	}
	return baseRef, nil
}

func staleRegistrationForPath(records []worktreeRecord, path string) (bool, error) {
	rec, ok := findWorktree(records, path)
	if !ok {
		return false, nil
	}
	return registeredWorktreeDirMissing(rec)
}

func (w *Workspace) clearOrphanedWorktreePath(ctx context.Context, repo, path string, records []worktreeRecord) error {
	if _, ok := findWorktree(records, path); ok {
		return nil
	}
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("gitworktree: inspect worktree path %q: %w", path, err)
	}
	if !fi.IsDir() {
		return fmt.Errorf("%w: %q exists and is not a directory; move or delete it and retry", ErrPathOccupied, path)
	}
	nonEmpty, err := pathExistsNonEmpty(path)
	if err != nil || !nonEmpty {
		return err
	}
	orphaned, err := w.isOrphanedWorktreeOf(ctx, repo, path)
	if err != nil {
		return err
	}
	if !orphaned {
		return fmt.Errorf("%w: %q already exists and is not a git worktree of %q; move or delete it and retry", ErrPathOccupied, path, repo)
	}
	aside, err := movePathAside(path, ".orphaned-"+time.Now().UTC().Format("20060102T150405Z"))
	if err != nil {
		return err
	}
	w.logf("gitworktree: moved orphaned worktree folder %q aside to %q (git no longer tracked it); its files are kept there", path, aside)
	return nil
}

func (w *Workspace) isOrphanedWorktreeOf(ctx context.Context, repo, path string) (bool, error) {
	gitFile := filepath.Join(path, ".git")
	fi, err := os.Lstat(gitFile)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("gitworktree: inspect %q: %w", gitFile, err)
	}
	if !fi.Mode().IsRegular() {
		return false, nil
	}
	data, err := os.ReadFile(gitFile)
	if err != nil {
		return false, fmt.Errorf("gitworktree: read %q: %w", gitFile, err)
	}
	line, _, _ := strings.Cut(string(data), "\n")
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(line), "gitdir:")
	gitdir = strings.TrimSpace(gitdir)
	if !ok || gitdir == "" {
		return false, nil
	}
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(path, gitdir)
	}
	if _, err := os.Lstat(gitdir); err == nil {
		return false, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("gitworktree: inspect worktree admin dir %q: %w", gitdir, err)
	}
	out, err := w.run(ctx, w.binary, "-C", repo, "rev-parse", "--git-common-dir")
	if err != nil {
		return false, fmt.Errorf("gitworktree: resolve git common dir: %w", err)
	}
	commonDir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo, commonDir)
	}
	adminRoot, err := physicalAbs(filepath.Join(commonDir, "worktrees"))
	if err != nil {
		return false, fmt.Errorf("gitworktree: resolve worktree admin root: %w", err)
	}
	adminDir, err := physicalAbs(gitdir)
	if err != nil {
		return false, fmt.Errorf("gitworktree: resolve worktree admin dir %q: %w", gitdir, err)
	}
	return filepath.Dir(adminDir) == adminRoot, nil
}

func (w *Workspace) addNewBranchWorktree(ctx context.Context, repo, branch, path, baseRef string, force bool) error {
	_, err := w.run(ctx, w.binary, worktreeAddNewBranchArgs(repo, branch, path, baseRef, force)...)
	if err == nil {
		return nil
	}
	if force || !isMissingRegisteredWorktreeError(err) {
		return err
	}
	created, refErr := w.refExists(ctx, repo, "refs/heads/"+branch)
	if refErr != nil {
		return errors.Join(err, refErr)
	}
	retryArgs := worktreeAddNewBranchArgs(repo, branch, path, baseRef, true)
	if created {
		retryArgs = worktreeAddBranchArgs(repo, path, branch, true)
	}
	if _, retryErr := w.run(ctx, w.binary, retryArgs...); retryErr != nil {
		return errors.Join(err, retryErr)
	}
	return nil
}

type workspaceProjectRepo struct {
	name         string
	relativePath string
	repoPath     string
	outputPath   string
	baseBranch   string
	seedRef      string
	baseRef      string
}

func (w *Workspace) workspaceProjectBranch(ctx context.Context, repos []workspaceProjectRepo, requested string, fresh, requireNewBranch bool) (string, error) {
	branch := strings.TrimSpace(requested)
	if branch == "" {
		return "", errors.New("gitworktree: branch is required")
	}
	for i := 0; i < 100; i++ {
		candidate := branch
		if i > 0 {
			candidate = fmt.Sprintf("%s-%d", branch, i+1)
		}
		free, err := w.workspaceProjectBranchFree(ctx, repos, candidate, fresh)
		if err != nil {
			return "", err
		}
		if free {
			return candidate, nil
		}
		if requireNewBranch {
			return "", fmt.Errorf("%w: %q", ports.ErrWorkspaceBranchAlreadyExists, requested)
		}
	}
	return "", fmt.Errorf("gitworktree: could not find free workspace branch for %q", branch)
}

func (w *Workspace) workspaceProjectBranchFree(ctx context.Context, repos []workspaceProjectRepo, branch string, fresh bool) (bool, error) {
	for _, repo := range repos {
		if err := w.validateBranch(ctx, repo.repoPath, branch); err != nil {
			return false, err
		}
		exists, err := w.branchNameTaken(ctx, repo.repoPath, branch, fresh)
		if err != nil {
			return false, err
		}
		if exists {
			return false, nil
		}
		records, err := w.listRecords(ctx, repo.repoPath)
		if err != nil {
			return false, err
		}
		if conflict, ok := findWorktreeByBranch(records, branch); ok && filepath.Clean(conflict.Path) != filepath.Clean(repo.outputPath) {
			return false, nil
		}
	}
	return true, nil
}

func (w *Workspace) branchNameTaken(ctx context.Context, repo, branch string, includeRemotes bool) (bool, error) {
	exists, err := w.refExists(ctx, repo, "refs/heads/"+branch)
	if err != nil || exists || !includeRemotes {
		return exists, err
	}
	out, err := w.run(ctx, w.binary, "-C", repo, "for-each-ref", "--format=%(refname)", "refs/remotes")
	if err != nil {
		return false, fmt.Errorf("gitworktree: list remote branches: %w", err)
	}
	for _, ref := range strings.Fields(string(out)) {
		if strings.HasSuffix(ref, "/"+branch) {
			return true, nil
		}
	}
	return false, nil
}

func (w *Workspace) createWorkspaceProjectRepo(ctx context.Context, repo workspaceProjectRepo, branch string, requireNewBranch bool) (string, error) {
	baseRef := strings.TrimSpace(repo.baseRef)
	if baseRef == "" {
		return "", errors.New("gitworktree: workspace repository base was not resolved before creation")
	}
	seedRef := firstNonEmpty(strings.TrimSpace(repo.seedRef), baseRef)
	baseSHA, err := w.revParse(ctx, repo.repoPath, baseRef)
	if err != nil {
		return "", err
	}
	if _, err := w.addWorktree(ctx, repo.repoPath, repo.outputPath, branch, repo.baseBranch, baseRef, seedRef, false, requireNewBranch); err != nil {
		return "", fmt.Errorf("gitworktree: workspace repo %q worktree add branch %q from %q: %w", repo.name, branch, seedRef, err)
	}
	return baseSHA, nil
}

func (w *Workspace) forceDestroyPath(ctx context.Context, repo, path string) error {
	unlock, err := repoTeardownLock(repo)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	defer unlock()
	if err := w.requireReachableRepo(repo); err != nil {
		return err
	}
	if discarded, ok := w.discard(path); ok {
		if err := w.pruneWorktrees(ctx, repo); err != nil {
			w.undiscard(discarded, path)
			return err
		}
		w.removeInBackground(discarded)
		return nil
	}
	_, _ = w.run(ctx, w.binary, worktreeForceRemoveArgs(repo, path)...)
	if err := w.pruneWorktrees(ctx, repo); err != nil {
		return err
	}
	if err := removeAllWithRetry(ctx, path); err != nil {
		return fmt.Errorf("gitworktree: force remove path %q: %w", path, err)
	}
	return nil
}

func (w *Workspace) pruneWorktrees(ctx context.Context, repo string) error {
	if _, err := w.run(ctx, w.binary, worktreePruneArgs(repo)...); err != nil {
		return fmt.Errorf("gitworktree: worktree prune: %w", err)
	}
	return nil
}

func isMissingRegisteredWorktreeError(err error) bool {
	return strings.Contains(err.Error(), "is a missing but already registered worktree")
}

const gitAddLockReason = "initializing"

func (w *Workspace) liftStaleAddLock(ctx context.Context, repo, path string) bool {
	records, err := w.listRecords(ctx, repo)
	if err != nil {
		return false
	}
	rec, ok := findWorktree(records, path)
	if !ok || !rec.Locked || rec.LockReason != gitAddLockReason {
		return false
	}
	_, err = w.run(ctx, w.binary, "-C", repo, "worktree", "unlock", path)
	return err == nil
}

func isLockedWorktreeRemoveError(err error) bool {
	return strings.Contains(err.Error(), "cannot remove a locked working tree")
}

func (w *Workspace) revParse(ctx context.Context, repo, ref string) (string, error) {
	out, err := w.run(ctx, w.binary, "-C", repo, "rev-parse", "--verify", ref)
	if err != nil {
		return "", fmt.Errorf("gitworktree: rev-parse %q: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (w *Workspace) validateBranch(ctx context.Context, repo, branch string) error {
	_, err := w.run(ctx, w.binary, checkRefFormatBranchArgs(repo, branch)...)
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if ctx.Err() != nil || (errors.As(err, &exitErr) && exitErr.ExitCode() == -1) {
		return fmt.Errorf("gitworktree: branch name check for %q did not finish: %w", branch, errors.Join(err, ctx.Err()))
	}
	return fmt.Errorf("%w: %q (%w)", ErrBranchInvalid, branch, err)
}

var errNoBaseRef = errors.New("gitworktree: no base ref found")

type worktreeRefs struct {
	seedRef string
	baseRef string
}

func (w *Workspace) resolveWorktreeRefs(ctx, remoteCtx context.Context, repo, branch, baseBranch string) (worktreeRefs, error) {
	if strings.TrimSpace(baseBranch) != "" {
		return w.resolveWorktreeRefsFromDefault(ctx, repo, branch, baseBranch)
	}
	resolver := gitdefault.New(w.binary, gitdefault.Runner(w.run))
	remote, requestedRef, requestedExists, requestedErr := resolver.FindCachedRemoteBranch(ctx, repo, branch)
	if requestedErr != nil && !errors.Is(requestedErr, gitdefault.ErrUnresolved) {
		return worktreeRefs{}, fmt.Errorf("gitworktree: inspect requested branch for %q: %w", repo, requestedErr)
	}
	resolution, err := resolver.Resolve(ctx, remoteCtx, repo)
	if err != nil {
		if errors.Is(err, gitdefault.ErrUnresolved) {
			if requestedExists {
				return worktreeRefs{seedRef: requestedRef, baseRef: requestedRef}, nil
			}
			return worktreeRefs{}, fmt.Errorf(
				"%w: %s; configure this repository's primary remote and cached HEAD (for example, `git -C %q remote set-head <remote> <branch>`) and retry",
				ErrDefaultBranchUnresolved, strings.TrimPrefix(err.Error(), gitdefault.ErrUnresolved.Error()+": "),
				repo,
			)
		}
		return worktreeRefs{}, fmt.Errorf("gitworktree: resolve repository default for %q: %w", repo, err)
	}
	slog.Debug("gitworktree: resolved automatic default branch",
		"repo", repo,
		"remote", resolution.Remote,
		"branch", resolution.Branch,
		"source", resolution.Source,
	)

	if exists, err := w.refExists(ctx, repo, resolution.Ref); err != nil {
		return worktreeRefs{}, err
	} else if exists {
		seedRef := resolution.Ref
		if requestedExists {
			slog.Debug("gitworktree: resolved requested remote branch",
				"repo", repo,
				"remote", remote,
				"branch", branch,
			)
			seedRef = requestedRef
		}
		return worktreeRefs{seedRef: seedRef, baseRef: resolution.Ref}, nil
	}
	return worktreeRefs{}, fmt.Errorf("%w: resolved default branch %q from %q, but ref %q is unavailable", errNoBaseRef, resolution.Branch, resolution.Source, resolution.Ref)
}

func (w *Workspace) resolveWorktreeRefsWithBudget(ctx context.Context, repo, branch, baseBranch string) (worktreeRefs, error) {
	remoteCtx, cancelRemote := context.WithTimeout(ctx, defaultBranchResolutionBudget)
	defer cancelRemote()
	return w.resolveWorktreeRefs(ctx, remoteCtx, repo, branch, baseBranch)
}

func (w *Workspace) resolveWorktreeRefsFromDefault(ctx context.Context, repo, branch, defaultBranch string) (worktreeRefs, error) {
	requestedRef := "origin/" + branch
	requestedExists, err := w.refExists(ctx, repo, requestedRef)
	if err != nil {
		return worktreeRefs{}, err
	}
	baseCandidates := configuredBaseRefCandidates(defaultBranch)
	for _, ref := range baseCandidates {
		exists, err := w.refExists(ctx, repo, ref)
		if err != nil {
			return worktreeRefs{}, err
		}
		if exists {
			seedRef := ref
			if requestedExists {
				seedRef = requestedRef
			}
			return worktreeRefs{seedRef: seedRef, baseRef: ref}, nil
		}
	}

	if requestedExists {
		return worktreeRefs{seedRef: requestedRef, baseRef: requestedRef}, nil
	}
	tagRef := "refs/tags/" + branch
	fallbackCandidates := []string{branch, tagRef}
	for _, ref := range fallbackCandidates {
		exists, err := w.refExists(ctx, repo, ref)
		if err != nil {
			return worktreeRefs{}, err
		}
		if exists {
			return worktreeRefs{seedRef: ref, baseRef: ref}, nil
		}
	}
	allCandidates := append([]string{requestedRef}, baseCandidates...)
	allCandidates = append(allCandidates, fallbackCandidates...)
	return worktreeRefs{}, fmt.Errorf("%w for branch %q (tried %s)", errNoBaseRef, branch, strings.Join(allCandidates, ", "))
}

func (w *Workspace) refExists(ctx context.Context, repo, ref string) (bool, error) {
	_, err := w.run(ctx, w.binary, revParseVerifyArgs(repo, ref)...)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, fmt.Errorf("gitworktree: verify ref %q: %w", ref, err)
}

func (w *Workspace) isDirty(ctx context.Context, path string) (bool, error) {
	out, err := w.run(ctx, w.binary, statusPorcelainArgs(path)...)
	if err != nil {
		return false, fmt.Errorf("gitworktree: status %q: %w", path, err)
	}
	return strings.TrimSpace(string(out)) != "", nil
}

func (w *Workspace) listRecords(ctx context.Context, repo string) ([]worktreeRecord, error) {
	out, err := w.run(ctx, w.binary, worktreeListPorcelainArgs(repo)...)
	if err != nil {
		return nil, fmt.Errorf("gitworktree: worktree list: %w", err)
	}
	records, err := parseWorktreePorcelain(string(out))
	if err != nil {
		return nil, fmt.Errorf("gitworktree: parse worktree list: %w", err)
	}
	return records, nil
}

func (w *Workspace) repoPath(project domain.ProjectID) (string, error) {
	repo, err := w.repos.RepoPath(project)
	if err != nil {
		return "", err
	}
	if repo == "" {
		return "", fmt.Errorf("gitworktree: no repo configured for project %q", project)
	}
	abs, err := physicalAbs(repo)
	if err != nil {
		return "", fmt.Errorf("gitworktree: repo path: %w", err)
	}
	return abs, nil
}

func (w *Workspace) repoPathForInfo(info ports.WorkspaceInfo) (string, error) {
	if info.RepoPath != "" {
		repo, err := physicalAbs(info.RepoPath)
		if err != nil {
			return "", fmt.Errorf("gitworktree: repo path: %w", err)
		}
		return repo, nil
	}
	if info.ProjectID == "" {
		return "", errors.New("gitworktree: project id is required")
	}
	return w.repoPath(info.ProjectID)
}

func (w *Workspace) requireReachableRepo(repo string) error {
	if info, err := os.Stat(repo); err == nil && info.IsDir() {
		return nil
	}
	return fmt.Errorf("gitworktree: repository %q is no longer on disk: %w", repo, ports.ErrWorkspaceRepoUnavailable)
}

func (w *Workspace) repoPathForConfig(cfg ports.WorkspaceConfig) (string, error) {
	if cfg.RepoPath != "" {
		repo, err := physicalAbs(cfg.RepoPath)
		if err != nil {
			return "", fmt.Errorf("gitworktree: repo path: %w", err)
		}
		return repo, nil
	}
	return w.repoPath(cfg.ProjectID)
}

func (w *Workspace) enableWorktreeConfig(ctx context.Context, repo string) error {
	if _, err := os.Stat(filepath.Join(repo, ".git")); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("gitworktree: inspect repository metadata: %w", err)
	}
	args := []string{"-C", repo, "config", "extensions.worktreeConfig", "true"}
	cmd := aoprocess.CommandContext(ctx, w.binary, args...)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("gitworktree: enable per-worktree config: %w", commandError{
			args: append([]string{w.binary}, args...), output: string(out), err: err,
		})
	}
	return nil
}

func physicalAbs(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	}
	parent := filepath.Dir(abs)
	base := filepath.Base(abs)
	for parent != "." && parent != string(os.PathSeparator) {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, base), nil
		}
		base = filepath.Join(filepath.Base(parent), base)
		parent = filepath.Dir(parent)
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		return filepath.Join(resolved, base), nil
	}
	return abs, nil
}

func validateConfig(cfg ports.WorkspaceConfig) error {
	if cfg.ProjectID == "" {
		return errors.New("gitworktree: project id is required")
	}
	if err := validatePathComponent("project id", string(cfg.ProjectID)); err != nil {
		return err
	}
	if cfg.Kind == domain.KindOrchestrator {
		prefix := resolvedSessionPrefix(cfg)
		if err := validatePathComponent("session prefix", prefix); err != nil {
			return err
		}
	} else {
		if cfg.SessionID == "" {
			return errors.New("gitworktree: session id is required")
		}
		if err := validatePathComponent("session id", string(cfg.SessionID)); err != nil {
			return err
		}
	}
	if cfg.Branch == "" {
		return errors.New("gitworktree: branch is required")
	}
	return nil
}

func validateWorkspaceProjectConfig(cfg ports.WorkspaceProjectConfig) error {
	if err := validateConfig(ports.WorkspaceConfig{
		ProjectID:     cfg.ProjectID,
		SessionID:     cfg.SessionID,
		Kind:          cfg.Kind,
		SessionPrefix: cfg.SessionPrefix,
		Branch:        firstNonEmpty(cfg.Branch, defaultSessionBranchName(cfg.SessionID)),
		BaseBranch:    cfg.BaseBranch,
	}); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.RootRepoPath) == "" {
		return errors.New("gitworktree: root repo path is required")
	}
	for _, repo := range cfg.Repos {
		if strings.TrimSpace(repo.Name) == "" {
			return errors.New("gitworktree: child repo name is required")
		}
		if err := validatePathComponent("child repo name", repo.Name); err != nil {
			return err
		}
		if strings.TrimSpace(repo.RepoPath) == "" {
			return fmt.Errorf("gitworktree: child repo %q path is required", repo.Name)
		}
		if _, err := cleanRelativePath(repo.RelativePath); err != nil {
			return fmt.Errorf("gitworktree: child repo %q: %w", repo.Name, err)
		}
	}
	return nil
}

func validatePathComponent(name, value string) error {
	if strings.ContainsAny(value, `/\`) {
		return fmt.Errorf("%w: %s %q must not contain path separators", ErrUnsafePath, name, value)
	}
	if value == "." || value == ".." {
		return fmt.Errorf("%w: %s %q must not be a path-traversal component", ErrUnsafePath, name, value)
	}
	return nil
}

func (w *Workspace) managedPath(cfg ports.WorkspaceConfig) (string, error) {
	var path string
	if cfg.Kind == domain.KindOrchestrator {
		prefix := resolvedSessionPrefix(cfg)
		path = filepath.Join(w.managedRoot, string(cfg.ProjectID), "orchestrator", prefix+"-orchestrator")
	} else {
		path = filepath.Join(w.managedRoot, string(cfg.ProjectID), string(cfg.SessionID))
	}
	return w.validateManagedPath(path)
}

func (w *Workspace) restorePath(cfg ports.WorkspaceConfig) (string, error) {
	if cfg.Path != "" {
		return w.validateManagedPath(cfg.Path)
	}
	return w.managedPath(cfg)
}

func resolvedSessionPrefix(cfg ports.WorkspaceConfig) string {
	if p := strings.TrimSpace(cfg.SessionPrefix); p != "" {
		return p
	}
	id := string(cfg.ProjectID)
	if len(id) <= 12 {
		return id
	}
	return id[:12]
}

func defaultSessionBranchName(id domain.SessionID) string {
	return "ao/" + string(id)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func cleanRelativePath(path string) (string, error) {
	rel := filepath.ToSlash(strings.TrimSpace(path))
	if rel == "" {
		return "", errors.New("relative path is required")
	}
	if strings.HasPrefix(rel, "/") {
		return "", fmt.Errorf("%w: relative path %q must not be absolute", ErrUnsafePath, path)
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(rel)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("%w: relative path %q escapes the workspace root", ErrUnsafePath, path)
	}
	return clean, nil
}

func (w *Workspace) validateManagedPath(path string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("%w: empty path", ErrUnsafePath)
	}
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%w: %q is not absolute", ErrUnsafePath, path)
	}
	clean := filepath.Clean(path)
	if clean != path {
		return "", fmt.Errorf("%w: %q is not clean", ErrUnsafePath, path)
	}
	physical, err := physicalAbs(clean)
	if err != nil {
		return "", fmt.Errorf("gitworktree: resolve path %q: %w", path, err)
	}
	clean = physical
	inside, err := pathWithin(w.managedRoot, clean)
	if err != nil {
		return "", err
	}
	if !inside || clean == w.managedRoot {
		return "", fmt.Errorf("%w: %q is outside managed root %q", ErrUnsafePath, clean, w.managedRoot)
	}
	return clean, nil
}

func pathWithin(root, path string) (bool, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false, fmt.Errorf("gitworktree: compare paths: %w", err)
	}
	return rel == "." || (rel != "" && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))), nil
}

func findWorktree(records []worktreeRecord, path string) (worktreeRecord, bool) {
	clean := filepath.Clean(path)
	for _, rec := range records {
		if filepath.Clean(rec.Path) == clean {
			return rec, true
		}
	}
	return worktreeRecord{}, false
}

func findWorktreeByBranch(records []worktreeRecord, branch string) (worktreeRecord, bool) {
	for _, rec := range records {
		if rec.Branch == branch {
			return rec, true
		}
	}
	return worktreeRecord{}, false
}

func pathExistsNonEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err == nil {
		return len(entries) > 0, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, fmt.Errorf("gitworktree: inspect path %q: %w", path, err)
}

func moveStrayPathAside(path string) (string, error) {
	return movePathAside(path, ".stray")
}

func movePathAside(path, suffix string) (string, error) {
	for i := 0; i < 100; i++ {
		candidate := path + suffix
		if i > 0 {
			candidate = fmt.Sprintf("%s%s-%d", path, suffix, i+1)
		}
		if _, err := os.Lstat(candidate); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("gitworktree: inspect move-aside destination %q: %w", candidate, err)
		}
		if err := os.Rename(path, candidate); err != nil {
			return "", fmt.Errorf("gitworktree: move path %q aside to %q: %w", path, candidate, err)
		}
		return candidate, nil
	}
	return "", fmt.Errorf("gitworktree: move path %q aside: no available destination", path)
}

func runCommand(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, args...)
	cmd.Env = os.Environ()
	if readsGitMessages(args) {
		cmd.Env = untranslatedGitEnv(cmd.Env)
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, commandError{args: append([]string{binary}, args...), output: string(out), err: err}
	}
	return out, nil
}

func readsGitMessages(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "-C" || args[i] == "-c" {
			i++
			continue
		}
		return args[i] == "worktree" || args[i] == "status"
	}
	return false
}

func untranslatedGitEnv(environ []string) []string {
	all := ""
	for _, kv := range environ {
		if val, ok := strings.CutPrefix(kv, "LC_ALL="); ok {
			all = val
		}
	}
	out := make([]string, 0, len(environ)+2)
	for _, kv := range environ {
		key, _, _ := strings.Cut(kv, "=")
		switch {
		case key == "LC_ALL" || key == "LC_MESSAGES":
		case all != "" && (key == "LANG" || strings.HasPrefix(key, "LC_")):
		default:
			out = append(out, kv)
		}
	}
	if all != "" {
		out = append(out, "LANG="+all)
	}
	return append(out, "LC_MESSAGES=C")
}

type commandError struct {
	args   []string
	output string
	err    error
}

func (e commandError) Error() string {
	if strings.TrimSpace(e.output) == "" {
		return fmt.Sprintf("%s: %v", strings.Join(e.args, " "), e.err)
	}
	return fmt.Sprintf("%s: %v: %s", strings.Join(e.args, " "), e.err, strings.TrimSpace(e.output))
}

func (e commandError) Unwrap() error { return e.err }
