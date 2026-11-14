package storagemgr

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/attachmentstore"
)

// osJunkNames are ignored files the operating system drops into folders; they
// hold nothing anyone needs back.
var osJunkNames = nameSet(".DS_Store", "Thumbs.db", "desktop.ini")

// keptIgnored lists the git-ignored entries in worktree that removing it would
// lose for good: a .env, a local database, uploads. `git worktree remove` only
// refuses on tracked changes and untracked files, so these would otherwise go
// with the worktree. Entries that come back on their own are left out:
// symlinks (only the link goes), rebuildable dependency and build folders,
// AO's own control files, and OS junk.
func keptIgnored(ctx context.Context, worktree string) ([]string, error) {
	// matching: an ignored folder is listed once ("node_modules/"), and the
	// ignored files of a folder that is not itself ignored one by one.
	out, err := gitCommand(ctx, worktree, "status", "--porcelain=v1", "-z", "--ignored=matching", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	var kept []string
	for _, record := range bytes.Split(out, []byte{0}) {
		line := string(record)
		if !strings.HasPrefix(line, "!! ") {
			continue
		}
		raw := strings.TrimPrefix(line, "!! ")
		dir := strings.HasSuffix(raw, "/")
		rel := strings.TrimSuffix(raw, "/")
		if rel == "" {
			continue
		}
		rebuildable, err := ignoredRebuildable(ctx, worktree, rel, dir)
		if err != nil {
			return nil, err
		}
		if !rebuildable {
			kept = append(kept, raw)
		}
	}
	sort.Strings(kept)
	return kept, nil
}

func ignoredRebuildable(ctx context.Context, worktree, rel string, dir bool) (bool, error) {
	abs := filepath.Join(worktree, filepath.FromSlash(rel))
	info, err := os.Lstat(abs)
	if err != nil {
		return true, nil //nolint:nilerr // gone since git listed it: nothing to lose
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return true, nil
	}
	base := path.Base(rel)
	if dir {
		if rel == attachmentstore.WorkspaceDir {
			// Projections of attachments AO stores canonically elsewhere.
			return true, nil
		}
		if !offloadableNames[base] || isProtected(rel) {
			return false, nil
		}
		if packageManaged[base] {
			return true, nil
		}
		protected, err := containsProtected(ctx, abs)
		if err != nil {
			return false, err
		}
		return !protected, nil
	}
	if osJunkNames[base] {
		return true, nil
	}
	return aoManagedFile(worktree, rel), nil
}

// aoManagedFile reports a control file an AO agent adapter installed: one
// named by the AO-marked .gitignore beside it (the marker covers itself), or
// AO's Copilot agent profile. The same ownership markers the workspace readers
// trust.
func aoManagedFile(worktree, rel string) bool {
	dir, name := path.Dir(rel), path.Base(rel)
	if dir == ".github/agents" && strings.HasPrefix(name, "ao-") && strings.HasSuffix(name, ".agent.md") {
		return fileContains(filepath.Join(worktree, filepath.FromSlash(rel)), hookutil.CopilotAgentProfileSentinel)
	}
	marker := filepath.Join(worktree, filepath.FromSlash(dir), ".gitignore")
	data, err := readSmallFile(marker)
	if err != nil || !strings.Contains(string(data), hookutil.GitignoreSentinel) {
		return false
	}
	for _, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "/") && strings.TrimPrefix(line, "/") == name {
			return true
		}
	}
	return false
}

func fileContains(file, needle string) bool {
	data, err := readSmallFile(file)
	return err == nil && strings.Contains(string(data), needle)
}

// readSmallFile reads a regular file of at most 64 KiB, the size of AO's
// marker files; anything else is not one of them.
func readSmallFile(file string) ([]byte, error) {
	info, err := os.Lstat(file)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 64<<10 {
		return nil, fmt.Errorf("%s is not a small regular file", file)
	}
	return os.ReadFile(file) //nolint:gosec // path sits inside an AO-managed worktree
}

// describeKept names what a worktree removal would lose, for the plan's reason.
func describeKept(kept []string, unknown bool) string {
	if unknown {
		return "its ignored files could not be checked"
	}
	if len(kept) == 0 {
		return ""
	}
	const shown = 3
	names := strings.Join(kept[:min(shown, len(kept))], ", ")
	if len(kept) > shown {
		names += fmt.Sprintf(" and %d more", len(kept)-shown)
	}
	return "holds ignored files that cannot be rebuilt: " + names
}
