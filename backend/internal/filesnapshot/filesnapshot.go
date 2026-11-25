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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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
	// ErrNestedRepository means the worktree holds another repository: a
	// submodule, a repository cloned into it, or a workspace project's child
	// repository. A snapshot records at most the commit it has checked out, never
	// its files, so a restore would leave whatever changed inside it.
	ErrNestedRepository = errors.New("worktree contains another git repository")
	// ErrTooLarge means the worktree's untracked files are past what a snapshot
	// taken before every turn may copy into the repository.
	ErrTooLarge = errors.New("worktree is too large to snapshot")
)

// Limits on the untracked files a snapshot copies. The turn waits while they are
// hashed, and every version of each stays in the repository's object store until
// the session's snapshots are deleted.
const (
	maxUntrackedFiles     = 5000
	maxUntrackedFileBytes = 50 << 20
	maxUntrackedBytes     = 200 << 20
)

// snapshotsPrefix is the ref namespace holding every session's snapshots.
const snapshotsPrefix = "refs/ao/snapshots/"

// snapshotSubject starts a snapshot commit's message, followed by the worktree
// it was taken in, so DeleteOrphans can tell when that worktree is gone.
const snapshotSubject = "ao file snapshot of "

// SessionPrefix is the ref namespace holding one session's snapshots.
func SessionPrefix(session domain.SessionID) string {
	return snapshotsPrefix + string(session) + "/"
}

// Ref names the snapshot taken before a session's turn.
func Ref(session domain.SessionID, turnID string) string {
	return SessionPrefix(session) + turnID
}

// BeforeRollbackRef names the record of the worktree just before a rollback
// restored the snapshot of turnID, so what the restore overwrote stays
// reachable instead of being left to garbage collection.
func BeforeRollbackRef(session domain.SessionID, turnID string) string {
	return SessionPrefix(session) + "before-rollback/" + turnID
}

// ChangeKind says what restoring a snapshot does to one path.
type ChangeKind string

const (
	// ChangeRestored brings back a file deleted since the snapshot.
	ChangeRestored ChangeKind = "restored"
	// ChangeReverted puts back the contents or the staged state of a file
	// changed since the snapshot.
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
	head    string
	current state
	target  state
	// indexOnly holds the listed paths whose files already match the snapshot,
	// so only their index entries change.
	indexOnly map[string]bool
	Changes   []Change
	// CommitsSince counts commits on HEAD made after the snapshot. Restoring
	// changes files and never history, so they stay on the branch.
	CommitsSince int
	// PlanID identifies this plan. It changes whenever the worktree, the index or
	// HEAD does, so a caller can tell the files still match a list it showed.
	PlanID string
}

// Capture records the workspace's files and points every ref at the snapshot. It
// refuses with ErrTooLarge, recording nothing, when the untracked files are past
// the limits above.
func Capture(ctx context.Context, workspace string, refs ...string) error {
	top, err := toplevel(ctx, workspace)
	if err != nil {
		return err
	}
	head, err := git(ctx, top, nil, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return err
	}
	if err := checkUntracked(ctx, top); err != nil {
		return err
	}
	current, err := capture(ctx, top, nil)
	if err != nil {
		return err
	}
	return record(ctx, top, head, current, refs...)
}

// Save records the worktree and index as Plan found them under ref, before Apply
// overwrites them. `git stash apply <ref>` brings them back.
func (r Restore) Save(ctx context.Context, ref string) error {
	return record(ctx, r.top, r.head, r.current, ref)
}

// record writes files as a commit shaped like git stash's on top of head and
// points every ref at it.
func record(ctx context.Context, top, head string, files state, refs ...string) error {
	identity := []string{
		"GIT_AUTHOR_NAME=Agent Orchestrator", "GIT_AUTHOR_EMAIL=agent-orchestrator@localhost",
		"GIT_COMMITTER_NAME=Agent Orchestrator", "GIT_COMMITTER_EMAIL=agent-orchestrator@localhost",
	}
	index, err := git(ctx, top, identity, nil, "commit-tree", files.index, "-p", head, "-m", "ao file snapshot index")
	if err != nil {
		return err
	}
	snapshot, err := git(ctx, top, identity, nil, "commit-tree", files.worktree, "-p", head, "-p", index, "-m", snapshotSubject+top)
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
	head, err := git(ctx, top, nil, nil, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return Restore{}, err
	}
	count, err := git(ctx, top, nil, nil, "rev-list", "--count", snapshot+"^1.."+head)
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
	if err := checkUntracked(ctx, top); err != nil {
		return Restore{}, err
	}
	current, err := capture(ctx, top, nil)
	if err != nil {
		return Restore{}, err
	}
	changes, err := diff(ctx, top, current.worktree, target.worktree)
	if err != nil {
		return Restore{}, err
	}
	if ignored := overwritten(top, changes); len(ignored) > 0 {
		// The capture skipped these as ignored, but Apply would write over them,
		// so record them too: Save then keeps them and PlanID covers them.
		if current, err = capture(ctx, top, ignored); err != nil {
			return Restore{}, err
		}
		if changes, err = diff(ctx, top, current.worktree, target.worktree); err != nil {
			return Restore{}, err
		}
	}
	for _, tree := range []string{current.worktree, target.worktree} {
		nested, err := gitlink(ctx, top, tree)
		if err != nil {
			return Restore{}, err
		}
		if nested != "" {
			return Restore{}, fmt.Errorf("%w: %s", ErrNestedRepository, nested)
		}
	}
	staged, err := diff(ctx, top, current.index, target.index)
	if err != nil {
		return Restore{}, err
	}
	changes, indexOnly := withIndexOnly(changes, staged)
	id := sha256.Sum256([]byte(strings.Join([]string{snapshot, head, current.worktree, current.index}, "\n")))
	return Restore{
		top: top, head: head, current: current, target: target, indexOnly: indexOnly,
		Changes: changes, CommitsSince: commitsSince, PlanID: hex.EncodeToString(id[:16]),
	}, nil
}

// withIndexOnly adds to the worktree's changes every path only the index
// changes, such as a file the turn staged without editing it, and names those
// paths.
func withIndexOnly(changes, staged []Change) ([]Change, map[string]bool) {
	listed := make(map[string]bool, len(changes))
	for _, change := range changes {
		listed[change.Path] = true
	}
	var indexOnly map[string]bool
	for _, change := range staged {
		if listed[change.Path] {
			continue
		}
		if indexOnly == nil {
			indexOnly = map[string]bool{}
		}
		indexOnly[change.Path] = true
		changes = append(changes, Change{Path: change.Path, Kind: ChangeReverted})
	}
	if indexOnly != nil {
		slices.SortFunc(changes, func(a, b Change) int { return strings.Compare(a.Path, b.Path) })
	}
	return changes, indexOnly
}

// Apply writes the snapshot's files and index entries for every planned change.
func (r Restore) Apply(ctx context.Context) error {
	var write, remove []string
	for _, change := range r.Changes {
		switch {
		case r.indexOnly[change.Path]:
		case change.Kind == ChangeRemoved:
			remove = append(remove, change.Path)
		default:
			write = append(write, change.Path)
		}
	}
	return r.write(ctx, r.target, write, remove)
}

// Undo puts back the files and index entries Apply replaced.
func (r Restore) Undo(ctx context.Context) error {
	var write, remove []string
	for _, change := range r.Changes {
		switch {
		case r.indexOnly[change.Path]:
		case change.Kind == ChangeRestored:
			remove = append(remove, change.Path)
		default:
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

// DeleteOrphans removes the snapshots of every session in repo whose worktree is
// gone, however the session ended. A session keeps its snapshots while any of
// them was taken in one of worktrees, the paths of repo's registered worktrees,
// or when a snapshot does not say where it was taken.
func DeleteOrphans(ctx context.Context, repo string, worktrees []string) error {
	listed, err := git(ctx, repo, nil, nil, "for-each-ref", "--format=%(refname)%00%(subject)", snapshotsPrefix)
	if err != nil || listed == "" {
		return err
	}
	var live []os.FileInfo
	for _, path := range worktrees {
		if info, err := os.Stat(path); err == nil {
			live = append(live, info)
		}
	}
	keep := map[string]bool{}
	var sessions []string
	for _, line := range strings.Split(listed, "\n") {
		ref, subject, _ := strings.Cut(line, "\x00")
		session, _, _ := strings.Cut(strings.TrimPrefix(ref, snapshotsPrefix), "/")
		if _, seen := keep[session]; !seen {
			sessions = append(sessions, session)
		}
		path, ok := strings.CutPrefix(subject, snapshotSubject)
		keep[session] = keep[session] || !ok || isAny(path, live)
	}
	for _, session := range sessions {
		if keep[session] {
			continue
		}
		if err := DeleteAll(ctx, repo, SessionPrefix(domain.SessionID(session))); err != nil {
			return err
		}
	}
	return nil
}

// isAny reports whether path is one of the files described by infos.
func isAny(path string, infos []os.FileInfo) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	for _, candidate := range infos {
		if os.SameFile(info, candidate) {
			return true
		}
	}
	return false
}

// write makes the worktree match to for the given paths, removals first so a
// file can replace a directory it used to be and the reverse, and then the index
// match it for every planned change.
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
	if len(r.Changes) == 0 {
		return nil
	}
	paths := make([]string, len(r.Changes))
	for i, change := range r.Changes {
		paths[i] = change.Path
	}
	_, err := git(ctx, r.top, nil, nulList(paths),
		"--literal-pathspecs", "reset", "--quiet", to.index, "--pathspec-from-file=-", "--pathspec-file-nul")
	return err
}

// capture records the worktree and the index as trees through a copy of the
// real index, so files whose stat data git already holds are not re-read. The
// worktree tree leaves out ignored files other than those under ignored.
func capture(ctx context.Context, top string, ignored []string) (state, error) {
	realIndex, err := git(ctx, top, nil, nil, "rev-parse", "--git-path", "index")
	if err != nil {
		return state{}, err
	}
	if !filepath.IsAbs(realIndex) {
		realIndex = filepath.Join(top, realIndex)
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
	if err := clearAssumeUnchanged(ctx, top, env); err != nil {
		return state{}, err
	}
	if _, err := git(ctx, top, env, nil, "add", "--all"); err != nil {
		return state{}, err
	}
	if len(ignored) > 0 {
		if _, err := git(ctx, top, env, nulList(ignored),
			"--literal-pathspecs", "add", "--force", "--pathspec-from-file=-", "--pathspec-file-nul"); err != nil {
			return state{}, err
		}
	}
	if out.worktree, err = git(ctx, top, env, nil, "write-tree"); err != nil {
		return state{}, err
	}
	return out, nil
}

// clearAssumeUnchanged drops the assume-unchanged mark from every entry of the
// temporary index env names. git add skips files with the mark, so a snapshot
// would miss their edits; the real index keeps its marks.
func clearAssumeUnchanged(ctx context.Context, top string, env []string) error {
	out, err := git(ctx, top, env, nil, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	var marked []string
	for _, entry := range strings.Split(out, "\x00") {
		// "<tag> <path>"; a lowercase tag marks the entry assume-unchanged.
		if len(entry) > 2 && entry[0] >= 'a' && entry[0] <= 'z' {
			marked = append(marked, entry[2:])
		}
	}
	if len(marked) == 0 {
		return nil
	}
	_, err = git(ctx, top, env, nulList(marked), "update-index", "--no-assume-unchanged", "-z", "--stdin")
	return err
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

// overwritten names what is on disk where Apply would write a path the worktree
// tree lacks: an ignored file or directory at that path, or an ignored file or
// symlink where one of its parent directories goes. Writing the path replaces
// them.
func overwritten(top string, changes []Change) []string {
	seen := map[string]bool{}
	var paths []string
	for _, change := range changes {
		if change.Kind != ChangeRestored {
			continue
		}
		if _, err := os.Lstat(filepath.Join(top, filepath.FromSlash(change.Path))); err == nil && !seen[change.Path] {
			seen[change.Path] = true
			paths = append(paths, change.Path)
		}
		for dir := change.Path; strings.Contains(dir, "/"); {
			dir = dir[:strings.LastIndexByte(dir, '/')]
			info, err := os.Lstat(filepath.Join(top, filepath.FromSlash(dir)))
			if err == nil && !info.IsDir() && !seen[dir] {
				seen[dir] = true
				paths = append(paths, dir)
			}
		}
	}
	return paths
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

// gitlink names a submodule or nested repository recorded in tree, if any. git
// add records one only as the commit it has checked out, so edits inside it are
// invisible to a snapshot whether or not that commit changed.
func gitlink(ctx context.Context, top, tree string) (string, error) {
	out, err := git(ctx, top, nil, nil, "ls-tree", "-r", "-z", tree)
	if err != nil {
		return "", err
	}
	for _, entry := range strings.Split(out, "\x00") {
		// "<mode> <type> <id>", a tab, then the path.
		if meta, path, ok := strings.Cut(entry, "\t"); ok && strings.HasPrefix(meta, "160000 ") {
			return path, nil
		}
	}
	return "", nil
}

// checkUntracked refuses a worktree whose untracked files are too many or too
// large to copy into the object store before every turn.
func checkUntracked(ctx context.Context, top string) error {
	out, err := git(ctx, top, nil, nil, "ls-files", "-z", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	var files int
	var total int64
	for _, path := range strings.Split(out, "\x00") {
		if path == "" {
			continue
		}
		files++
		if files > maxUntrackedFiles {
			return fmt.Errorf("%w: more than %d untracked files", ErrTooLarge, maxUntrackedFiles)
		}
		info, err := os.Lstat(filepath.Join(top, filepath.FromSlash(path)))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if info.Size() > maxUntrackedFileBytes {
			return fmt.Errorf("%w: %s is larger than %d MiB", ErrTooLarge, path, maxUntrackedFileBytes>>20)
		}
		total += info.Size()
		if total > maxUntrackedBytes {
			return fmt.Errorf("%w: untracked files total more than %d MiB", ErrTooLarge, maxUntrackedBytes>>20)
		}
	}
	return nil
}

// diff lists what moving the worktree from tree from to tree to changes.
func diff(ctx context.Context, top, from, to string) ([]Change, error) {
	out, err := git(ctx, top, nil, nil, "diff-tree", "-r", "-z", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var changes []Change
	for i := 0; i+1 < len(fields); i += 2 {
		// ":<old mode> <new mode> <old id> <new id> <status>", then the path.
		meta := strings.Fields(strings.TrimPrefix(fields[i], ":"))
		if len(meta) != 5 {
			return nil, fmt.Errorf("parse git diff-tree output %q", fields[i])
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
	return changes, nil
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
