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
	ErrNotRepository    = errors.New("workspace is not a git repository")
	ErrNotFound         = errors.New("file snapshot not found")
	ErrNestedRepository = errors.New("worktree contains another git repository")
	ErrTooLarge         = errors.New("worktree is too large to snapshot")
)

const (
	maxUntrackedFiles     = 5000
	maxUntrackedFileBytes = 50 << 20
	maxUntrackedBytes     = 200 << 20
)

const snapshotsPrefix = "refs/ao/snapshots/"

const snapshotSubject = "ao file snapshot of "

func SessionPrefix(session domain.SessionID) string {
	return snapshotsPrefix + string(session) + "/"
}

func Ref(session domain.SessionID, turnID string) string {
	return SessionPrefix(session) + turnID
}

func BeforeRollbackRef(session domain.SessionID, turnID string) string {
	return SessionPrefix(session) + "before-rollback/" + turnID
}

type ChangeKind string

const (
	ChangeRestored ChangeKind = "restored"
	ChangeReverted ChangeKind = "reverted"
	ChangeRemoved  ChangeKind = "removed"
)

type Change struct {
	Path string
	Kind ChangeKind
}

type state struct {
	worktree string
	index    string
}

type Restore struct {
	top          string
	head         string
	current      state
	target       state
	indexOnly    map[string]bool
	Changes      []Change
	CommitsSince int
	PlanID       string
}

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

func (r Restore) Save(ctx context.Context, ref string) error {
	return record(ctx, r.top, r.head, r.current, ref)
}

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

func DeleteAll(ctx context.Context, repo, prefix string) error {
	listed, err := git(ctx, repo, nil, nil, "for-each-ref", "--format=delete %(refname)", prefix)
	if err != nil || listed == "" {
		return err
	}
	_, err = git(ctx, repo, nil, strings.NewReader(listed+"\n"), "update-ref", "--stdin")
	return err
}

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

func clearAssumeUnchanged(ctx context.Context, top string, env []string) error {
	out, err := git(ctx, top, env, nil, "ls-files", "-v", "-z")
	if err != nil {
		return err
	}
	var marked []string
	for _, entry := range strings.Split(out, "\x00") {
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

func gitlink(ctx context.Context, top, tree string) (string, error) {
	out, err := git(ctx, top, nil, nil, "ls-tree", "-r", "-z", tree)
	if err != nil {
		return "", err
	}
	for _, entry := range strings.Split(out, "\x00") {
		if meta, path, ok := strings.Cut(entry, "\t"); ok && strings.HasPrefix(meta, "160000 ") {
			return path, nil
		}
	}
	return "", nil
}

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

func diff(ctx context.Context, top, from, to string) ([]Change, error) {
	out, err := git(ctx, top, nil, nil, "diff-tree", "-r", "-z", "--no-renames", from, to)
	if err != nil {
		return nil, err
	}
	fields := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	var changes []Change
	for i := 0; i+1 < len(fields); i += 2 {
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

func git(ctx context.Context, dir string, env []string, stdin io.Reader, args ...string) (string, error) {
	full := append([]string{
		"-C", dir,
		"-c", "core.hooksPath=" + os.DevNull,
		"-c", "core.fsmonitor=false",
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
