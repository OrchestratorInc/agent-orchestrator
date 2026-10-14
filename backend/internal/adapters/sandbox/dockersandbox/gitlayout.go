package dockersandbox

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Host git obeys a commondir file, a worktree admin dir's commondir or gitdir
// file, and a worktree's .git pointer. Any of these, rewritten by the
// container, could point host git at an attacker's config. So the git dir is
// mounted read-only, with only objects, refs and logs writable, plus (per
// agent) the session's own worktree admin dir minus its pointer files.

// gitLayout is the host git metadata a sandbox mounts, resolved from the
// project's repository rather than from anything the container can write.
type gitLayout struct {
	dir            string // the repository's common git dir
	admin          string // per-agent: <dir>/worktrees/<name> of this workspace
	worktreeConfig bool   // extensions.worktreeConfig is on: mount config.worktree read-only
}

// hostGit runs git on the host with fsmonitor and hooks disabled, so even a
// tampered config cannot make these probes run a command.
func (s *Sandbox) hostGit(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	argv := append([]string{"-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", "-C", dir}, args...)
	out, err := s.run.Output(ctx, "git", argv...)
	return strings.TrimSpace(string(out)), err
}

func (s *Sandbox) gitCommonDir(ctx context.Context, dir string) (string, error) {
	out, err := s.hostGit(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("%w: resolve git dir for %s: %w", ports.ErrSandboxMisconfigured, dir, err)
	}
	if out == "" {
		return "", fmt.Errorf("%w: resolve git dir for %s: git printed nothing", ports.ErrSandboxMisconfigured, dir)
	}
	return out, nil
}

// resolveGit resolves the git dir from req.RepoPath, checks the workspace
// belongs to it, and prepares the host files the mount layout binds.
func (s *Sandbox) resolveGit(ctx context.Context, req ports.SandboxRequest) (gitLayout, error) {
	if req.RepoPath == "" {
		return gitLayout{}, fmt.Errorf("%w: the project has no repository path", ports.ErrSandboxMisconfigured)
	}
	dir, err := s.gitCommonDir(ctx, req.RepoPath)
	if err != nil {
		return gitLayout{}, err
	}
	wsDir, err := s.gitCommonDir(ctx, req.WorkspacePath)
	if err != nil {
		return gitLayout{}, err
	}
	if physicalPath(wsDir) != physicalPath(dir) {
		return gitLayout{}, fmt.Errorf("%w: worktree %s does not belong to repository %s; its git metadata may have been modified",
			ports.ErrSandboxMisconfigured, req.WorkspacePath, req.RepoPath)
	}
	g := gitLayout{dir: dir}
	for _, sub := range []string{"objects", "refs"} {
		if err := requireDir(filepath.Join(dir, sub)); err != nil {
			return gitLayout{}, err
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil { //nolint:gosec // git's own mode for its metadata dirs
		return gitLayout{}, fmt.Errorf("sandbox git logs dir: %w", err)
	}
	if err := requireDir(filepath.Join(dir, "logs")); err != nil {
		return gitLayout{}, err
	}
	if req.Config.Mode == domain.SandboxModeShared {
		if err := os.MkdirAll(filepath.Join(dir, "worktrees"), 0o755); err != nil { //nolint:gosec // git's own mode for its metadata dirs
			return gitLayout{}, fmt.Errorf("sandbox git worktrees dir: %w", err)
		}
		if err := requireDir(filepath.Join(dir, "worktrees")); err != nil {
			return gitLayout{}, err
		}
	} else {
		if g.admin, err = findWorktreeAdmin(dir, req.WorkspacePath); err != nil {
			return gitLayout{}, err
		}
		// --default makes an unset key print false, so any error is real and
		// fails closed rather than leaving config.worktree writable.
		on, err := s.hostGit(ctx, req.RepoPath, "config", "--bool", "--default", "false", "extensions.worktreeConfig")
		if err != nil {
			return gitLayout{}, fmt.Errorf("%w: read extensions.worktreeConfig for %s: %w", ports.ErrSandboxMisconfigured, req.RepoPath, err)
		}
		if g.worktreeConfig = on == "true"; g.worktreeConfig {
			if err := ensureEmptyFile(filepath.Join(g.admin, "config.worktree")); err != nil {
				return gitLayout{}, err
			}
		}
	}
	return g, nil
}

// gitMounts lists the git metadata binds, parents before children.
func gitMounts(req ports.SandboxRequest, g gitLayout) []mount {
	m := []mount{
		{Path: g.dir, ReadOnly: true},
		{Path: filepath.Join(g.dir, "objects")},
		{Path: filepath.Join(g.dir, "refs")},
		{Path: filepath.Join(g.dir, "logs")},
	}
	if req.Config.Mode == domain.SandboxModeShared {
		// Sessions that join the container later need their admin dirs.
		return append(m, mount{Path: filepath.Join(g.dir, "worktrees")})
	}
	m = append(m,
		mount{Path: g.admin},
		mount{Path: filepath.Join(g.admin, "commondir"), ReadOnly: true},
		mount{Path: filepath.Join(g.admin, "gitdir"), ReadOnly: true},
		mount{Path: filepath.Join(req.WorkspacePath, ".git"), ReadOnly: true},
	)
	if g.worktreeConfig {
		m = append(m, mount{Path: filepath.Join(g.admin, "config.worktree"), ReadOnly: true})
	}
	return m
}

// findWorktreeAdmin returns the git dir's admin dir for workspace: the one
// entry under <gitDir>/worktrees whose gitdir file names <workspace>/.git.
// The worktree's own .git file is never trusted for this.
func findWorktreeAdmin(gitDir, workspace string) (string, error) {
	want := physicalPath(filepath.Join(workspace, ".git"))
	root := filepath.Join(gitDir, "worktrees")
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("%w: read %s: %w", ports.ErrSandboxMisconfigured, root, err)
	}
	var matches []string
	for _, e := range entries {
		admin := filepath.Join(root, e.Name())
		if fi, err := os.Lstat(admin); err != nil || !fi.IsDir() {
			continue
		}
		body, ok := readSmallRegular(filepath.Join(admin, "gitdir"))
		if !ok {
			continue
		}
		p := strings.TrimSpace(body)
		if p == "" {
			continue
		}
		if !filepath.IsAbs(p) {
			p = filepath.Join(admin, p)
		}
		if physicalPath(p) == want {
			matches = append(matches, admin)
		}
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("%w: worktree %s matches %d admin dirs under %s, want exactly one",
			ports.ErrSandboxMisconfigured, workspace, len(matches), root)
	}
	admin := matches[0]
	for _, f := range []string{filepath.Join(admin, "commondir"), filepath.Join(admin, "gitdir"), filepath.Join(workspace, ".git")} {
		if err := requireRegular(f); err != nil {
			return "", err
		}
	}
	return admin, nil
}

// readSmallRegular reads a short regular file without following a link or
// blocking on a FIFO planted in its place.
func readSmallRegular(path string) (string, bool) {
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", false
	}
	defer func() { _ = f.Close() }()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return "", false
	}
	b, err := io.ReadAll(io.LimitReader(f, 4096))
	if err != nil {
		return "", false
	}
	return string(b), true
}

func requireDir(path string) error {
	if fi, err := os.Lstat(path); err != nil || !fi.IsDir() {
		return fmt.Errorf("%w: %s must be a directory", ports.ErrSandboxMisconfigured, path)
	}
	return nil
}

func requireRegular(path string) error {
	if fi, err := os.Lstat(path); err != nil || !fi.Mode().IsRegular() {
		return fmt.Errorf("%w: %s must be a regular file", ports.ErrSandboxMisconfigured, path)
	}
	return nil
}

// ensureEmptyFile creates path empty when it is absent, so it can be bound
// read-only, and requires a regular file otherwise.
func ensureEmptyFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644) //nolint:gosec // git's own mode for config files
	switch {
	case err == nil:
		if err := f.Close(); err != nil {
			return fmt.Errorf("sandbox git %s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrExist):
		return fmt.Errorf("sandbox git %s: %w", path, err)
	}
	return requireRegular(path)
}

// requireMountSources fails before `docker run`, because --mount does not
// create a missing bind source.
func requireMountSources(mounts []mount) error {
	for _, m := range mounts {
		if m.Tmpfs {
			continue
		}
		if _, err := os.Stat(m.Path); err != nil {
			return fmt.Errorf("%w: mount source %s is missing: %w", ports.ErrSandboxMisconfigured, m.Path, err)
		}
	}
	return nil
}
