package gitworktree

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type taskNamingMove struct {
	Original ports.WorkspaceProjectInfo
	Target   ports.WorkspaceProjectInfo
	GitDirs  []string
}

func (w *Workspace) taskNamingJournal(projectID domain.ProjectID, id domain.SessionID) (string, error) {
	if err := validatePathComponent("project id", string(projectID)); err != nil {
		return "", err
	}
	if err := validatePathComponent("session id", string(id)); err != nil {
		return "", err
	}
	return w.validateManagedPath(filepath.Join(w.managedRoot, string(projectID), ".ao-task-naming-"+string(id)+".json"))
}

func checkoutGitDir(path string) (string, error) {
	body, err := os.ReadFile(filepath.Join(path, ".git"))
	if err != nil {
		return "", err
	}
	gitDir, ok := strings.CutPrefix(strings.TrimSpace(string(body)), "gitdir: ")
	if !ok || gitDir == "" {
		return "", errors.New("gitworktree: task checkout has no linked Git directory")
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(path, gitDir)
	}
	return physicalAbs(gitDir)
}

// repairTaskWorkspaceLink updates only the admin backlink for this AO-owned
// checkout. `git worktree repair <path>` also scans every registered worktree.
func (w *Workspace) repairTaskWorkspaceLink(ctx context.Context, repo ports.WorkspaceRepoInfo) error {
	gitDir, err := checkoutGitDir(repo.Path)
	if err != nil {
		return fmt.Errorf("gitworktree: task checkout %q Git directory: %w", repo.Path, err)
	}
	commonOut, err := w.run(ctx, w.binary, "-C", repo.RepoPath, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("gitworktree: resolve common Git directory for %q: %w", repo.RepoPath, err)
	}
	commonDir := strings.TrimSpace(string(commonOut))
	if !filepath.IsAbs(commonDir) {
		commonDir = filepath.Join(repo.RepoPath, commonDir)
	}
	commonDir, err = physicalAbs(commonDir)
	if err != nil {
		return fmt.Errorf("gitworktree: resolve common Git directory for %q: %w", repo.RepoPath, err)
	}
	worktreesDir, err := physicalAbs(filepath.Join(commonDir, "worktrees"))
	if err != nil {
		return fmt.Errorf("gitworktree: resolve worktree metadata directory for %q: %w", repo.RepoPath, err)
	}
	if filepath.Dir(gitDir) != worktreesDir {
		return fmt.Errorf("gitworktree: task checkout %q is not registered under repository %q", repo.Path, repo.RepoPath)
	}
	gitdirFile := filepath.Join(gitDir, "gitdir")
	fileInfo, err := os.Lstat(gitdirFile)
	if err != nil {
		return fmt.Errorf("gitworktree: inspect task checkout backlink %q: %w", gitdirFile, err)
	}
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("gitworktree: task checkout backlink %q is not a regular file", gitdirFile)
	}
	oldLink, err := os.ReadFile(gitdirFile)
	if err != nil {
		return fmt.Errorf("gitworktree: read task checkout backlink %q: %w", gitdirFile, err)
	}
	link := filepath.Join(repo.Path, ".git")
	link, err = physicalAbs(link)
	if err != nil {
		return fmt.Errorf("gitworktree: resolve task checkout backlink %q: %w", link, err)
	}
	if len(oldLink) > 0 && !filepath.IsAbs(strings.TrimRight(string(oldLink), "\r\n")) {
		link, err = filepath.Rel(gitDir, link)
		if err != nil {
			return fmt.Errorf("gitworktree: make task checkout backlink relative: %w", err)
		}
	}
	if strings.TrimRight(string(oldLink), "\r\n") == link {
		return nil
	}
	if err := replaceWorktreeGitdir(gitdirFile, []byte(link+"\n"), fileInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("gitworktree: update task checkout backlink %q: %w", gitdirFile, err)
	}
	return nil
}

func replaceWorktreeGitdir(path string, contents []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".ao-worktree-gitdir-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(contents); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	return errors.Join(dir.Sync(), dir.Close())
}

func writeTaskNamingMove(ctx context.Context, path string, move taskNamingMove) error {
	body, err := json.Marshal(move)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ao-task-naming-*.tmp")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err = f.Write(body); err == nil {
		err = f.Sync()
	}
	if err := errors.Join(err, f.Close(), ctx.Err()); err != nil {
		return err
	}
	// Publish complete bytes atomically without replacing another attempt's intent.
	if err := os.Link(f.Name(), path); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		dir, err := os.Open(filepath.Dir(path))
		if err != nil {
			return err
		}
		return errors.Join(dir.Sync(), dir.Close())
	}
	return nil
}

// RecoverTaskWorkspace finishes a journaled move even when SQLite still has the
// old path or no path. Linked Git directory identities prevent adopting another
// checkout that appeared at either location after the interruption.
func (w *Workspace) RecoverTaskWorkspace(ctx context.Context, projectID domain.ProjectID, id domain.SessionID) (ports.WorkspaceProjectInfo, bool, error) {
	var move taskNamingMove
	path, err := w.taskNamingJournal(projectID, id)
	if err != nil {
		return move.Original, false, err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return move.Original, false, nil
	}
	if err != nil {
		return move.Original, true, err
	}
	if err := json.Unmarshal(body, &move); err != nil {
		return move.Original, true, fmt.Errorf("gitworktree: invalid task naming journal: %w", err)
	}
	for _, info := range []ports.WorkspaceProjectInfo{move.Original, move.Target} {
		root, err := w.validateManagedPath(info.Root.Path)
		if err != nil || filepath.Dir(root) != filepath.Join(w.managedRoot, string(projectID)) || info.Root.ProjectID != projectID || info.Root.SessionID != id {
			return move.Original, true, errors.New("gitworktree: task naming journal has an invalid owner or path")
		}
	}
	if move.Original.Root.Path == move.Target.Root.Path || len(move.Original.Worktrees) != len(move.Target.Worktrees) {
		return move.Original, true, errors.New("gitworktree: invalid task naming destination")
	}
	for i, repo := range move.Original.Worktrees {
		relative, err := filepath.Rel(move.Original.Root.Path, repo.Path)
		target := move.Target.Worktrees[i]
		if err != nil || target.Path != filepath.Join(move.Target.Root.Path, relative) || target.RepoPath != repo.RepoPath || target.Branch != move.Target.Root.Branch {
			return move.Original, true, errors.New("gitworktree: task naming repository destination changed")
		}
	}
	info := move.Original
	if _, err := checkoutGitDir(move.Target.Root.Path); err == nil {
		info = move.Target
	} else if _, err := os.Lstat(move.Target.Root.Path); !errors.Is(err, os.ErrNotExist) {
		return info, true, errors.New("gitworktree: task naming destination is occupied or inaccessible")
	}
	repos := append([]ports.WorkspaceRepoInfo(nil), info.Worktrees...)
	if len(repos) == 0 {
		repos = []ports.WorkspaceRepoInfo{{RepoPath: info.Root.RepoPath, Path: info.Root.Path, Branch: info.Root.Branch}}
	}
	if len(repos) != len(move.GitDirs) || len(repos) == 0 {
		return info, true, errors.New("gitworktree: task naming journal has invalid repositories")
	}
	for i, repo := range repos {
		validated, pathErr := w.validateManagedPath(repo.Path)
		inside, err := pathWithin(info.Root.Path, validated)
		if pathErr != nil || err != nil || !inside {
			return info, true, errors.New("gitworktree: task naming repository is outside its workspace")
		}
		gitDir, err := checkoutGitDir(repo.Path)
		if err != nil || gitDir != move.GitDirs[i] {
			return info, true, fmt.Errorf("gitworktree: task naming checkout identity changed: %s", repo.Path)
		}
	}
	if info.Root.Path == move.Original.Root.Path {
		for _, repo := range repos {
			out, err := w.run(ctx, w.binary, "-C", repo.Path, "branch", "--show-current")
			if err != nil {
				return info, true, err
			}
			branch := strings.TrimSpace(string(out))
			if branch == move.Target.Root.Branch {
				continue
			}
			if branch != repo.Branch {
				return info, true, errors.New("gitworktree: interrupted task branch changed")
			}
			if _, err := w.run(ctx, w.binary, "-C", repo.Path, "branch", "-m", move.Target.Root.Branch); err != nil {
				return info, true, err
			}
		}
		if err := moveTaskDirectory(info.Root.Path, move.Target.Root.Path); err != nil {
			return info, true, err
		}
	}
	info, err = w.RepairTaskWorkspace(ctx, move.Target)
	return info, true, err
}

// CompleteTaskWorkspaceNaming is called only after SQLite accepts the actual
// paths, or after the caller has successfully destroyed this unlaunched checkout.
func (w *Workspace) CompleteTaskWorkspaceNaming(ctx context.Context, info ports.WorkspaceProjectInfo) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := w.taskNamingJournal(info.Root.ProjectID, info.Root.SessionID)
	if err != nil {
		return err
	}
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var move taskNamingMove
	if err := json.Unmarshal(body, &move); err != nil {
		return err
	}
	if move.Original.Root.SessionID != info.Root.SessionID || move.Target.Root.SessionID != info.Root.SessionID ||
		move.Original.Root.ProjectID != info.Root.ProjectID || move.Target.Root.ProjectID != info.Root.ProjectID ||
		(info.Root.Path != move.Original.Root.Path && info.Root.Path != move.Target.Root.Path) {
		return errors.New("gitworktree: task naming acknowledgement owner or path changed")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
