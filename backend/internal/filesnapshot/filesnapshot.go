// Package filesnapshot records a git worktree's files under hidden refs and
// puts them back later, so undoing a Chat turn can also undo what the agent
// changed on disk during it.
//
// A snapshot is a commit shaped like git stash's: its tree is the worktree
// (tracked and untracked files; ignored ones are skipped), its first parent is
// HEAD at capture time, and its second parent's tree is the index. Capturing
// writes through a temporary index, so the branch, the real index and the stash
// are never touched.
package filesnapshot

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

var (
	// ErrNotRepository means the workspace is not inside a git worktree.
	ErrNotRepository = errors.New("workspace is not a git repository")
	// ErrNotFound means nothing was recorded under the ref.
	ErrNotFound = errors.New("file snapshot not found")
	// ErrNestedRepository means the worktree holds another repository, such as a
	// workspace project's child repository. A snapshot carries none of its files,
	// so a restore would leave whatever changed there.
	ErrNestedRepository = errors.New("worktree contains another git repository")
)

// SessionPrefix is the ref namespace holding one session's snapshots.
func SessionPrefix(session domain.SessionID) string {
	return "refs/ao/snapshots/" + string(session) + "/"
}

// Ref names the snapshot taken before a session's turn.
func Ref(session domain.SessionID, turnID string) string {
	return SessionPrefix(session) + turnID
}

// ChangeKind says what restoring a snapshot does to one path.
type ChangeKind string

const (
	// ChangeRestored brings back a file deleted since the snapshot.
	ChangeRestored ChangeKind = "restored"
	// ChangeReverted puts back the contents of a file changed since the snapshot.
	ChangeReverted ChangeKind = "reverted"
	// ChangeRemoved deletes a file created since the snapshot.
	ChangeRemoved ChangeKind = "removed"
)

// Change is one path a restore would touch, relative to the repository root.
type Change struct {
	Path string
	Kind ChangeKind
}

// state is a worktree and its index, each as a tree-ish git accepts.
type state struct {
	worktree string
	index    string
}

// Restore is a planned return to a snapshot. Plan works it out without
// touching anything; Apply carries it out and Undo reverses an Apply.
type Restore struct {
	top     string
	current state
	target  state
	Changes []Change
	// CommitsSince counts commits on HEAD made after the snapshot. Restoring
	// changes files and never history, so they stay on the branch.
	CommitsSince int
}

// Capture records the workspace's files and points every ref at the snapshot.
func Capture(ctx context.Context, workspace string, refs ...string) error {
	top, err := toplevel(ctx, workspace)
	if err != nil {
		return err
	}
	head, err := git(ctx, top, nil, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	current, err := capture(ctx, top)
	if err != nil {
		return err
	}
	identity := []string{
		"GIT_AUTHOR_NAME=Agent Orchestrator", "GIT_AUTHOR_EMAIL=agent-orchestrator@localhost",
		"GIT_COMMITTER_NAME=Agent Orchestrator", "GIT_COMMITTER_EMAIL=agent-orchestrator@localhost",
	}
	index, err := git(ctx, top, identity, nil, "commit-tree", current.index, "-p", head, "-m", "ao file snapshot index")
	if err != nil {
		return err
	}
	snapshot, err := git(ctx, top, identity, nil, "commit-tree", current.worktree, "-p", head, "-p", index, "-m", "ao file snapshot")
	if err != nil {
		return err
	}
	var updates strings.Builder
	for _, ref := range refs {
		fmt.Fprintf(&updates, "update %s %s\n", ref, snapshot)
	}
	_, err = git(ctx, top, nil, strings.NewReader(updates.String()), "update-ref", "--stdin")
	return err
}

// Plan works out what restoring the snapshot under ref would change.
func Plan(ctx context.Context, workspace, ref string) (Restore, error) {
	top, err := toplevel(ctx, workspace)
	if err != nil {
		return Restore{}, err
	}
	snapshot, err := git(ctx, top, nil, nil, "rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		var exit *commandError
		if errors.As(err, &exit) && exit.code == 1 {
			return Restore{}, fmt.Errorf("%w: %s", ErrNotFound, ref)
		}
		return Restore{}, err
	}
	target := state{worktree: snapshot + "^{tree}", index: snapshot + "^2^{tree}"}
	count, err := git(ctx, top, nil, nil, "rev-list", "--count", snapshot+"^1..HEAD")
	if err != nil {
		return Restore{}, err
	}
	commitsSince, err := strconv.Atoi(count)
	if err != nil {
		return Restore{}, fmt.Errorf("count commits since snapshot: %w", err)
	}
	nested, err := ignoredRepository(ctx, top)
	if err != nil {
		return Restore{}, err
	}
	if nested != "" {
		return Restore{}, fmt.Errorf("%w: %s", ErrNestedRepository, nested)
	}
	current, err := capture(ctx, top)
	if err != nil {
		return Restore{}, err
	}
	changes, nested, err := diff(ctx, top, current.worktree, target.worktree)
	if err != nil {
		return Restore{}, err
	}
	if nested != "" {
		return Restore{}, fmt.Errorf("%w: %s", ErrNestedRepository, nested)
	}
	return Restore{top: top, current: current, target: target, Changes: changes, CommitsSince: commitsSince}, nil
}

// Apply writes the snapshot's files and index entries for every planned change.
func (r Restore) Apply(ctx context.Context) error {
	var write, remove []string
	for _, change := range r.Changes {
		if change.Kind == ChangeRemoved {
			remove = append(remove, change.Path)
		} else {
			write = append(write, change.Path)
		}
	}
	return r.write(ctx, r.target, write, remove)
}

// Undo puts back the files and index entries Apply replaced.
func (r Restore) Undo(ctx context.Context) error {
	var write, remove []string
	for _, change := range r.Changes {
		if change.Kind == ChangeRestored {
			remove = append(remove, change.Path)
		} else {
			write = append(write, change.Path)
		}
	}
	return r.write(ctx, r.current, write, remove)
}

// DeleteAll removes every snapshot ref under prefix.
func DeleteAll(ctx context.Context, repo, prefix string) error {
	listed, err := git(ctx, repo, nil, nil, "for-each-ref", "--format=delete %(refname)", prefix)
	if err != nil || listed == "" {
		return err
	}
	_, err = git(ctx, repo, nil, strings.NewReader(listed+"\n"), "update-ref", "--stdin")
	return err
}

// write makes the worktree and index match to for the given paths: removals
// first, so a file can replace a directory it used to be and the reverse.
func (r Restore) write(ctx context.Context, to state, write, remove []string) error {
	for _, path := range remove {
		if err := removeFile(r.top, path); err != nil {
			return err
		}
	}
	if len(write) > 0 {
		dir, err := os.MkdirTemp("", "ao-file-snapshot-*")
		if err != nil {
			return fmt.Errorf("create temporary index directory: %w", err)
		}
		defer func() { _ = os.RemoveAll(dir) }()
		env := []string{"GIT_INDEX_FILE=" + filepath.Join(dir, "index")}
		if _, err := git(ctx, r.top, env, nil, "read-tree", to.worktree); err != nil {
			return err
		}
		if _, err := git(ctx, r.top, env, nulList(write), "checkout-index", "--force", "-z", "--stdin"); err != nil {
			return err
		}
	}
	paths := append(append([]string(nil), write...), remove...)
	if len(paths) == 0 {
		return nil
	}
	_, err := git(ctx, r.top, nil, nulList(paths),
		"--literal-pathspecs", "reset", "--quiet", to.index, "--pathspec-from-file=-", "--pathspec-file-nul")
	return err
}

// capture records the worktree and the index as trees through a copy of the
// real index, so files whose stat data git already holds are not re-read.
func capture(ctx context.Context, top string) (state, error) {
	realIndex, err := git(ctx, top, nil, nil, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return state{}, err
	}
	dir, err := os.MkdirTemp("", "ao-file-snapshot-*")
	if err != nil {
		return state{}, fmt.Errorf("create temporary index directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	index := filepath.Join(dir, "index")
	if err := copyIndex(realIndex, index); err != nil {
		return state{}, err
	}
	env := []string{"GIT_INDEX_FILE=" + index}
	var out state
	if out.index, err = git(ctx, top, env, nil, "write-tree"); err != nil {
		return state{}, err
	}
	if _, err := git(ctx, top, env, nil, "add", "--all"); err != nil {
		return state{}, err
	}
	if out.worktree, err = git(ctx, top, env, nil, "write-tree"); err != nil {
		return state{}, err
	}
	return out, nil
}

// copyIndex copies the index with its modification time. Git compares an
// entry's stat data against the index's own mtime to catch files edited in the
// same instant the index was written; a fresh mtime would hide those edits.
func copyIndex(from, to string) error {
	info, err := os.Stat(from)
	if err != nil {
		return fmt.Errorf("read index: %w", err)
	}
	source, err := os.Open(from)
	if err != nil {
		return fmt.Errorf("read index: %w", err)
	}
	defer func() { _ = source.Close() }()
	target, err := os.Create(to)
	if err != nil {
		return fmt.Errorf("copy index: %w", err)
	}
	if _, err := io.Copy(target, source); err != nil {
		_ = target.Close()
		return fmt.Errorf("copy index: %w", err)
	}
	if err := target.Close(); err != nil {
		return fmt.Errorf("copy index: %w", err)
	}
	if err := os.Chtimes(to, info.ModTime(), info.ModTime()); err != nil {
		return fmt.Errorf("copy index: %w", err)
	}
	return nil
}

// ignoredRepository names a repository at an ignored path in the worktree,
// which is where a workspace project keeps its child repositories.
func ignoredRepository(ctx context.Context, top string) (string, error) {
	out, err := git(ctx, top, nil, nil, "ls-files", "-z", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return "", err
	}
	for _, entry := range strings.Split(out, "\x00") {
		dir, isDir := strings.CutSuffix(entry, "/")
		if !isDir {
			continue
		}
		if _, err := os.Lstat(filepath.Join(top, filepath.FromSlash(dir), ".git")); err == nil {
			return dir, nil
		}
	}
	return "", nil
}

// diff lists what moving the worktree from tree from to tree to changes. A
// nested repository that changed is named instead of listed: a restore never
// rewrites one.
func diff(ctx context.Context, top, from, to string) ([]Change, string, error) {
	out, err := git(ctx, top, nil, nil, "diff-tree", "-r", "-z", "--no-renames", from, to)
	if err != nil {
		return nil, "", err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var changes []Change
	for i := 0; i+1 < len(fields); i += 2 {
		// ":<old mode> <new mode> <old id> <new id> <status>", then the path.
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if len(meta) != 5 {
			return nil, "", fmt.Errorf("parse git diff-tree output %q", fields[i])
		}
		if meta[0] == "160000" || meta[1] == "160000" {
			return nil, fields[i+1], nil
		}
		kind := ChangeReverted
		switch meta[4] {
		case "A":
			kind = ChangeRestored
		case "D":
			kind = ChangeRemoved
		}
		changes = append(changes, Change{Path: fields[i+1], Kind: kind})
	}
	return changes, "", nil
}

// removeFile deletes a file and then each parent directory it leaves empty,
// stopping at the repository root or the first directory that still holds
// something, ignored files included.
func removeFile(top, path string) error {
	full := filepath.Join(top, filepath.FromSlash(path))
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", path, err)
	}
	dir := filepath.Dir(full)
	for dir != top && strings.HasPrefix(dir, top) && os.Remove(dir) == nil {
		dir = filepath.Dir(dir)
	}
	return nil
}

// toplevel returns the root of the worktree at workspace. A workspace nested in
// some other repository, such as a home directory under version control, is
// refused: snapshotting or restoring it would act on that whole repository.
func toplevel(ctx context.Context, workspace string) (string, error) {
	top, err := git(ctx, workspace, nil, nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotRepository, err)
	}
	top = filepath.Clean(top)
	topInfo, err := os.Stat(top)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotRepository, err)
	}
	workspaceInfo, err := os.Stat(workspace)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrNotRepository, err)
	}
	if !os.SameFile(topInfo, workspaceInfo) {
		return "", fmt.Errorf("%w: %s is inside the repository at %s", ErrNotRepository, workspace, top)
	}
	return top, nil
}

func nulList(paths []string) io.Reader {
	return strings.NewReader(strings.Join(paths, "\x00") + "\x00")
}

type commandError struct {
	args   []string
	code   int
	output string
	err    error
}

func (e *commandError) Error() string {
	if e.output == "" {
		return fmt.Sprintf("git %s: %v", strings.Join(e.args, " "), e.err)
	}
	return fmt.Sprintf("git %s: %v: %s", strings.Join(e.args, " "), e.err, e.output)
}

func (e *commandError) Unwrap() error { return e.err }

// git runs one git command in dir and returns its trimmed standard output.
func git(ctx context.Context, dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	full := append([]string{
		"-C", dir,
		// Index writes and ref updates can fire repository hooks; a snapshot is
		// AO's bookkeeping, not a change the project's hooks should see.
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
		// A scratch index written as a split index could expire shared index
		// files the real index still uses.
		"-c", "core.splitIndex=false",
		"-c", "splitIndex.sharedIndexExpire=never",
	}, args...)
	cmd := aoprocess.CommandContext(ctx, "git", full...)
	cmd.Env = append(append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never"), env...)
	cmd.Stdin = stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		code := -1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		}
		return "", &commandError{args: args, code: code, output: strings.TrimSpace(stderr.String()), err: err}
	}
	return strings.TrimRight(stdout.String(), "\n"), nil
}
