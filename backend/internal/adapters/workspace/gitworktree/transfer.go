package gitworktree

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var _ ports.WorkspaceTransfer = (*Workspace)(nil)

// indexSuffix marks the ref that holds what was staged, beside the preserved ref.
const indexSuffix = "-index"

// unfinishedGit maps git's per-worktree state files to the operation in progress.
var unfinishedGit = []struct{ file, name string }{
	{"rebase-merge", "a rebase"}, {"rebase-apply", "a rebase"}, {"MERGE_HEAD", "a merge"},
	{"CHERRY_PICK_HEAD", "a cherry-pick"}, {"REVERT_HEAD", "a revert"}, {"BISECT_LOG", "a bisect"},
}

// checkMovable refuses work that a commit and the preserved tree cannot carry.
func (w *Workspace) checkMovable(ctx context.Context, path string) error {
	out, err := w.run(ctx, w.binary, "-C", path, "rev-parse", "--absolute-git-dir")
	if err != nil {
		return err
	}
	gitDir := strings.TrimSpace(string(out))
	for _, each := range unfinishedGit {
		if _, err := os.Stat(filepath.Join(gitDir, each.file)); err == nil {
			return fmt.Errorf("%w: %s", ports.ErrTransferGitBusy, each.name)
		}
	}
	unmerged, err := w.run(ctx, w.binary, "-C", path, "ls-files", "--unmerged", "-z")
	if err != nil {
		return err
	}
	if len(unmerged) > 0 {
		return fmt.Errorf("%w: unresolved conflicts", ports.ErrTransferGitBusy)
	}
	status, err := w.run(ctx, w.binary, "-C", path, "status", "--porcelain", "-z", "--untracked-files=all")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(status), "\x00") {
		// git lists untracked files one by one; a name ending in / is a repository it will not enter.
		if dir, ok := strings.CutPrefix(entry, "?? "); ok && strings.HasSuffix(dir, "/") {
			return fmt.Errorf("%w: %s", ports.ErrTransferNestedRepo, strings.TrimSuffix(dir, "/"))
		}
	}
	return w.checkSubmodules(ctx, path)
}

// checkSubmodules refuses a submodule whose uncommitted changes or commits
// exist only in this checkout: the moved work records no more than its commit.
func (w *Workspace) checkSubmodules(ctx context.Context, path string) error {
	entries, err := w.run(ctx, w.binary, "-C", path, "ls-files", "--stage", "-z")
	if err != nil {
		return err
	}
	for _, entry := range strings.Split(string(entries), "\x00") {
		name, ok := strings.CutPrefix(entry, "160000 ")
		if !ok {
			continue
		}
		_, name, _ = strings.Cut(name, "\t")
		dir := filepath.Join(path, filepath.FromSlash(name))
		if _, err := os.Lstat(filepath.Join(dir, ".git")); err != nil {
			continue // Not checked out.
		}
		status, err := w.run(ctx, w.binary, "-C", dir, "status", "--porcelain", "--ignore-submodules=none")
		if err != nil {
			return err
		}
		unpushed, err := w.run(ctx, w.binary, "-C", dir, "rev-list", "--max-count=1", "HEAD", "--not", "--remotes")
		if err != nil {
			return err
		}
		gitDir, err := w.run(ctx, w.binary, "-C", dir, "rev-parse", "--absolute-git-dir")
		if err != nil {
			return err
		}
		// A shallow clone lists the commits it fetched, which no remote branch here may hold.
		fetched, _ := os.ReadFile(filepath.Join(strings.TrimSpace(string(gitDir)), "shallow"))
		if len(status) > 0 || !strings.Contains(string(fetched), strings.TrimSpace(string(unpushed))) {
			return fmt.Errorf("%w: %s", ports.ErrTransferSubmodule, name)
		}
		if err := w.checkSubmodules(ctx, dir); err != nil {
			return err
		}
	}
	return nil
}

// stagedCommit records the index as a commit when it differs from HEAD, or "".
func (w *Workspace) stagedCommit(ctx context.Context, path, head string) (string, error) {
	tree, err := w.run(ctx, w.binary, "-C", path, "write-tree")
	if err != nil {
		return "", err
	}
	if headTree, err := w.revParse(ctx, path, head+"^{tree}"); err != nil || headTree == strings.TrimSpace(string(tree)) {
		return "", err
	}
	return w.commitTree(ctx, path, strings.TrimSpace(string(tree)), head, "staged changes")
}

func (w *Workspace) commitTree(ctx context.Context, path, tree, parent, message string) (string, error) {
	out, err := w.run(ctx, w.binary, "-C", path, "-c", "user.name=Orchestrator", "-c", "user.email=orchestrator@localhost",
		"commit-tree", tree, "-p", parent, "-m", message)
	return strings.TrimSpace(string(out)), err
}

var gitCredentials = regexp.MustCompile(`://[^/\s@]+@`)

// gitSaid is the end of a git command's output, without credentials in URLs.
func gitSaid(out []byte) string {
	lines := strings.Split(strings.TrimSpace(gitCredentials.ReplaceAllString(string(out), "://")), "\n")
	if len(lines) > 4 {
		lines = lines[len(lines)-4:]
	}
	text := strings.Join(lines, " ")
	if len(text) > 400 {
		text = text[:400]
	}
	return text
}

// CheckTransfer refuses a worktree whose work a move cannot carry.
func (w *Workspace) CheckTransfer(ctx context.Context, info ports.WorkspaceInfo) error {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	if err := w.checkMovable(ctx, path); err != nil {
		return err
	}
	out, err := w.run(ctx, w.binary, "-C", path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || strings.TrimSpace(string(out)) != info.Branch {
		return fmt.Errorf("%w: %s", ports.ErrDetachedHead, info.Branch)
	}
	return nil
}

// BundleTransfer writes to bundlePath what origin may not have of the
// worktree's work: the branch's unpushed commits and, at remotePreservedRef,
// the uncommitted work (preservedRef) with what is staged beside it. Nothing is
// written when origin has it all. It returns the branch head, and whether there
// was uncommitted work and a bundle.
func (w *Workspace) BundleTransfer(ctx context.Context, info ports.WorkspaceInfo, preservedRef, remotePreservedRef, bundlePath string) (head string, uncommitted, bundled bool, err error) {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return "", false, false, err
	}
	if !strings.HasPrefix(remotePreservedRef, "refs/ao/preserved/") {
		return "", false, false, fmt.Errorf("gitworktree: invalid preserved ref %q", remotePreservedRef)
	}
	if err := w.CheckTransfer(ctx, info); err != nil {
		return "", false, false, err
	}
	if head, err = w.revParse(ctx, path, "HEAD"); err != nil {
		return "", false, false, err
	}
	staged, err := w.stagedCommit(ctx, path, head)
	if err != nil {
		return "", false, false, err
	}
	if preservedRef == "" && staged != "" {
		// A staged edit undone on disk: the files match HEAD, only the index differs.
		if preservedRef, err = w.commitTree(ctx, path, head+"^{tree}", head, "ao preserved"); err != nil {
			return "", false, false, err
		}
	}
	tips := []string{"refs/heads/" + info.Branch}
	defer func() {
		for _, ref := range tips[1:] {
			_, _ = w.run(ctx, w.binary, "-C", path, "update-ref", "-d", ref)
		}
	}()
	for ref, target := range map[string]string{remotePreservedRef: preservedRef, remotePreservedRef + indexSuffix: staged} {
		if target == "" {
			continue
		}
		if _, err := w.run(ctx, w.binary, "-C", path, "update-ref", ref, target); err != nil {
			return "", false, false, err
		}
		tips = append(tips, ref)
	}
	uncommitted = preservedRef != ""
	unpushed, err := w.run(ctx, w.binary, append(append([]string{"-C", path, "rev-list", "--count"}, tips...), "--not", "--remotes")...)
	if err != nil {
		return "", false, false, err
	}
	if strings.TrimSpace(string(unpushed)) == "0" {
		return head, uncommitted, false, nil
	}
	if err := w.refuseLFSFiles(ctx, path, tips); err != nil {
		return "", false, false, err
	}
	if out, err := w.run(ctx, w.binary, append(append([]string{"-C", path, "bundle", "create", "--quiet", bundlePath}, tips...), "--not", "--remotes")...); err != nil {
		return "", false, false, fmt.Errorf("gitworktree: bundle: %w: %s", err, gitSaid(out))
	}
	return head, uncommitted, true, nil
}

// lfsPointer starts every Git LFS pointer file. A bundle carries the pointer
// and not the file, which the destination could not check out.
var lfsPointer = []byte("version https://git-lfs.github.com/spec/v1")

// refuseLFSFiles refuses work whose new files include Git LFS pointers.
func (w *Workspace) refuseLFSFiles(ctx context.Context, path string, tips []string) error {
	// Pointers are small; larger blobs are left out of the listing.
	objects, err := w.run(ctx, w.binary, append(append([]string{"-C", path, "rev-list", "--objects", "--no-object-names", "--filter=blob:limit=1024"}, tips...), "--not", "--remotes")...)
	if err != nil || len(objects) == 0 {
		return err
	}
	types, err := w.runInput(ctx, objects, "-C", path, "cat-file", "--batch-check=%(objecttype) %(objectname)")
	if err != nil {
		return err
	}
	var blobs bytes.Buffer
	for _, line := range strings.Split(string(types), "\n") {
		if name, ok := strings.CutPrefix(line, "blob "); ok {
			blobs.WriteString(name + "\n")
		}
	}
	if blobs.Len() == 0 {
		return nil
	}
	contents, err := w.runInput(ctx, blobs.Bytes(), "-C", path, "cat-file", "--batch")
	if err != nil {
		return err
	}
	if bytes.Contains(contents, lfsPointer) {
		return ports.ErrTransferLFS
	}
	return nil
}

func (w *Workspace) runInput(ctx context.Context, input []byte, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, w.binary, args...)
	cmd.Stdin = bytes.NewReader(input)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("gitworktree: git %s: %w", args[2], err)
	}
	return out, nil
}

// FetchTransfer brings a moved branch's head, and at remotePreservedRef its
// uncommitted work, into the repository: from bundlePath when set, and from
// origin for commits the bundle builds on, or for the head when there is no
// bundle.
func (w *Workspace) FetchTransfer(ctx context.Context, repoPath, branch, head, bundlePath, remotePreservedRef string) error {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	if err := w.validateBranch(ctx, repo, branch); err != nil {
		return err
	}
	if remotePreservedRef != "" && !strings.HasPrefix(remotePreservedRef, "refs/ao/preserved/") {
		return fmt.Errorf("gitworktree: invalid preserved ref %q", remotePreservedRef)
	}
	fetchOrigin := func(what string) error {
		if out, err := w.run(ctx, w.binary, "-C", repo, "fetch", "--quiet", "origin"); err != nil {
			return fmt.Errorf("%w: %s, and fetching origin failed: %s", ports.ErrTransferFetch, what, gitSaid(out))
		}
		return nil
	}
	if bundlePath == "" {
		if _, err := w.run(ctx, w.binary, "-C", repo, "cat-file", "-e", head+"^{commit}"); err == nil {
			return nil
		}
		if err := fetchOrigin("this computer does not have " + head); err != nil {
			return err
		}
		if _, err := w.run(ctx, w.binary, "-C", repo, "cat-file", "-e", head+"^{commit}"); err != nil {
			return fmt.Errorf("%w: origin does not have %s", ports.ErrTransferFetch, head)
		}
		return nil
	}
	if _, err := w.run(ctx, w.binary, "-C", repo, "bundle", "verify", "--quiet", bundlePath); err != nil {
		if err := fetchOrigin("the moved work builds on commits this computer does not have"); err != nil {
			return err
		}
		if out, err := w.run(ctx, w.binary, "-C", repo, "bundle", "verify", "--quiet", bundlePath); err != nil {
			return fmt.Errorf("%w: neither this computer nor origin has the commits the moved work builds on: %s", ports.ErrTransferFetch, gitSaid(out))
		}
	}
	// The head lands in FETCH_HEAD; AlignTransferBranch then points the branch at it.
	refspecs := []string{"refs/heads/" + branch}
	if remotePreservedRef != "" {
		// The trailing * also takes the staged state's ref, which is not always there.
		refspecs = append(refspecs, "+"+remotePreservedRef+"*:"+remotePreservedRef+"*")
	}
	if out, err := w.run(ctx, w.binary, append([]string{"-C", repo, "fetch", "--quiet", bundlePath}, refspecs...)...); err != nil {
		return fmt.Errorf("%w: %s", ports.ErrTransferFetch, gitSaid(out))
	}
	return nil
}

// AlignTransferBranch points the local branch at head when it is missing or
// behind head. A local branch with commits head lacks is left alone and
// returns ErrTransferBranchDiverged.
func (w *Workspace) AlignTransferBranch(ctx context.Context, repoPath, branch, head string) error {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return fmt.Errorf("gitworktree: repo path: %w", err)
	}
	if err := w.validateBranch(ctx, repo, branch); err != nil {
		return err
	}
	local := "refs/heads/" + branch
	exists, err := w.refExists(ctx, repo, local)
	if err != nil {
		return err
	}
	if !exists {
		_, err := w.run(ctx, w.binary, "-C", repo, "branch", "--no-track", branch, head)
		return err
	}
	current, err := w.revParse(ctx, repo, local)
	if err != nil || current == head {
		return err
	}
	if _, err := w.run(ctx, w.binary, "-C", repo, "merge-base", "--is-ancestor", current, head); err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return fmt.Errorf("%w: %s", ports.ErrTransferBranchDiverged, branch)
		}
		return err
	}
	// Refuses a branch checked out in a worktree, which a spawn could not use anyway.
	_, err = w.run(ctx, w.binary, "-C", repo, "branch", "--force", "--no-track", branch, head)
	return err
}

// RevParse resolves a ref in the repository.
func (w *Workspace) RevParse(ctx context.Context, repoPath, ref string) (string, error) {
	repo, err := physicalAbs(repoPath)
	if err != nil {
		return "", fmt.Errorf("gitworktree: repo path: %w", err)
	}
	return w.revParse(ctx, repo, ref)
}

// ReleaseTransferBranch detaches HEAD in the worktree, keeping its files.
func (w *Workspace) ReleaseTransferBranch(ctx context.Context, info ports.WorkspaceInfo) error {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	if out, err := w.run(ctx, w.binary, "-C", path, "switch", "--detach"); err != nil {
		return fmt.Errorf("gitworktree: detach %s: %w: %s", info.Branch, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// RestoreTransferIndex puts the index back as it was on the source, which
// ApplyPreserved leaves fully staged: to the staged state that came along, or
// to nothing staged.
func (w *Workspace) RestoreTransferIndex(ctx context.Context, info ports.WorkspaceInfo, remotePreservedRef string) error {
	path, err := w.validateManagedPath(info.Path)
	if err != nil {
		return err
	}
	staged := remotePreservedRef + indexSuffix
	exists, err := w.refExists(ctx, path, staged)
	if err != nil {
		return err
	}
	if !exists {
		if out, err := w.run(ctx, w.binary, "-C", path, "reset", "-q"); err != nil {
			return fmt.Errorf("gitworktree: unstage moved work: %w: %s", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	defer func() { _, _ = w.run(ctx, w.binary, "-C", path, "update-ref", "-d", staged) }()
	if out, err := w.run(ctx, w.binary, "-C", path, "read-tree", staged); err != nil {
		return fmt.Errorf("gitworktree: restore staged work: %w: %s", err, strings.TrimSpace(string(out)))
	}
	// Refresh the cached file stats; files that differ from the index report as such.
	_, _ = w.run(ctx, w.binary, "-C", path, "update-index", "-q", "--refresh")
	return nil
}
