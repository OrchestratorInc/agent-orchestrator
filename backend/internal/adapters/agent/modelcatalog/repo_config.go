package modelcatalog

import (
	"context"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// repoConfigRefs are the commits besides the working tree that a launch
// worktree is commonly seeded from. A missing ref is skipped.
var repoConfigRefs = []string{"HEAD", "refs/remotes/origin/HEAD"}

// repoMayHoldConfig reports whether the repository containing workingDir may
// hold any of the named config files (slash-separated, relative to a
// directory) for a session AO launches.
//
// Sessions run in AO worktrees seeded from a commit that is chosen per session
// (the project base branch, the repository default, or an existing remote
// branch), so the copy in the checkout is not necessarily the copy the session
// sees: it may be staged, uncommitted, untracked, or from another branch.
// Repository config is therefore never read for the default; its possible
// presence only makes the default unresolved. Every directory from the git
// root down to workingDir is checked, both on disk and in repoConfigRefs, so a
// file deleted locally but still committed also counts. Outside a git
// repository only workingDir itself is checked on disk.
func repoMayHoldConfig(workingDir string, names ...string) bool {
	if workingDir == "" || len(names) == 0 {
		return false
	}
	top, prefix, ok := gitTopLevel(workingDir)
	if !ok {
		return anyFileExists(workingDir, names)
	}
	var repoPaths []string
	for _, dir := range repoDirsDownTo(prefix) {
		if anyFileExists(filepath.Join(top, filepath.FromSlash(dir)), names) {
			return true
		}
		for _, name := range names {
			repoPaths = append(repoPaths, path.Join(dir, name))
		}
	}
	for _, ref := range repoConfigRefs {
		if gitRefHoldsAny(top, ref, repoPaths) {
			return true
		}
	}
	return false
}

// repoDirsDownTo lists the repository-relative directories from the root ("")
// down to prefix, e.g. "a/b/" yields "", "a", "a/b".
func repoDirsDownTo(prefix string) []string {
	dirs := []string{""}
	current := ""
	for _, part := range strings.Split(strings.Trim(prefix, "/"), "/") {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		dirs = append(dirs, current)
	}
	return dirs
}

func anyFileExists(dir string, names []string) bool {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(name))); err == nil {
			return true
		}
	}
	return false
}

func gitTopLevel(dir string) (top, prefix string, ok bool) {
	out, err := runGit(dir, "rev-parse", "--show-toplevel", "--show-prefix")
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(strings.TrimRight(string(out), "\r\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		return "", "", false
	}
	top = filepath.FromSlash(strings.TrimSpace(lines[0]))
	if len(lines) > 1 {
		prefix = strings.TrimSpace(lines[1])
	}
	return top, prefix, true
}

// gitRefHoldsAny reports whether ref's tree contains any of repoPaths. An
// unknown ref (for example no origin/HEAD) holds nothing.
func gitRefHoldsAny(top, ref string, repoPaths []string) bool {
	if _, err := runGit(top, "rev-parse", "--verify", "--quiet", ref+"^{tree}"); err != nil {
		return false
	}
	args := append([]string{"ls-tree", "-r", "--full-tree", "--name-only", ref, "--"}, repoPaths...)
	out, err := runGit(top, args...)
	return err == nil && strings.TrimSpace(string(out)) != ""
}

var runGit = func(dir string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return cmd.Output()
}
