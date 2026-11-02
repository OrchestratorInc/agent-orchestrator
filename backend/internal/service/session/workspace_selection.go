package session

import (
	"bufio"
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

const (
	// maxSelectionCommits caps how many commits one selection may tick.
	maxSelectionCommits = 250
	// maxSelectionRuns caps the non-adjacent runs one selection replays.
	maxSelectionRuns = 100
	// maxSelectionRangeCommits caps the commits a since-base range is
	// searched for membership. A ticked commit older than that is not found.
	maxSelectionRangeCommits = 5000
	// maxSelectionConflictPaths caps the paths a conflict reports.
	maxSelectionConflictPaths = 20
	// commitSelectionCacheSize is how many resolved selections stay cached.
	commitSelectionCacheSize = 64
	// gitTreeSizeBatch is how many paths one ls-tree call sizes.
	gitTreeSizeBatch = 500
)

// WorkspaceCommitConflictReason says why ticked commits cannot be combined.
type WorkspaceCommitConflictReason string

const (
	// WorkspaceCommitConflict means a later commit's changes do not apply
	// without the commits between it and an earlier ticked one.
	WorkspaceCommitConflict WorkspaceCommitConflictReason = "conflict"
	// WorkspaceCommitUnsupportedHistory means a later ticked commit has no
	// parent, so it has no changes to replay onto the earlier ones.
	WorkspaceCommitUnsupportedHistory WorkspaceCommitConflictReason = "unsupported_history"
)

// WorkspaceCommitSelectionConflict names the commit a selection stopped at
// and the paths that did not apply.
type WorkspaceCommitSelectionConflict struct {
	CommitSHA string
	Paths     []string
	Reason    WorkspaceCommitConflictReason
}

// WorkspaceCommitSelection is a set of ticked commits, or a view's whole
// range, resolved to one (before, after) revision pair and its changed files.
// A selection that cannot be combined has no files and names its Conflict.
type WorkspaceCommitSelection struct {
	SessionID  domain.SessionID
	View       WorkspaceViewInfo
	Contiguous bool
	Combinable bool
	Conflict   *WorkspaceCommitSelectionConflict
	Files      []WorkspaceFileSummary
	Summary    WorkspaceSummary
	Truncated  bool
	// root, before and after are the revisions every read of the selection
	// uses. after is a commit, or a tree when non-adjacent runs were
	// combined. before is empty when the range is empty.
	root, before, after string
}

// WorkspaceSelectionRevisionInput reads one side of a file in a commit
// selection. CommitSHA is the legacy single-commit alias for CommitSHAs.
type WorkspaceSelectionRevisionInput struct {
	Snapshot         bool
	Path             string
	Side             WorkspaceFileBlobSide
	Scope            WorkspaceDiffScope
	View             WorkspaceView
	CommitSHA        string
	CommitSHAs       []string
	WorkspaceVersion string
	ExpectedRevision string
}

// checkedOut reports whether v selects the checked-out worktree.
func (v WorkspaceView) checkedOut() bool {
	return v.Branch == "" && v.PRNumber == 0 && v.SourceURL == ""
}

// GetWorkspaceCommitSelection resolves ticked commits of a view, or its whole
// range when shas is empty, to their combined changed files. A checked-out
// selection is fenced by workspaceVersion; a branch or pull request view by
// its expected SHAs.
func (s *Service) GetWorkspaceCommitSelection(ctx context.Context, id domain.SessionID, v WorkspaceView, shas []string, workspaceVersion string) (WorkspaceCommitSelection, error) {
	ctx, cancel := context.WithTimeout(ctx, workspaceReviewTimeout)
	defer cancel()
	shas, err := selectionCommitSHAs("", shas)
	if err != nil {
		return WorkspaceCommitSelection{}, err
	}
	selection, _, err := s.workspaceSelection(ctx, id, v, shas, workspaceVersion, false)
	if err != nil {
		return WorkspaceCommitSelection{}, err
	}
	selection.Files = slices.Clone(selection.Files)
	return selection, nil
}

// GetWorkspaceSelectionFileRevision reads one side of a file in a commit
// selection: the selection's before revision, or its after revision. Both
// are immutable, so the worktree is never read.
func (s *Service) GetWorkspaceSelectionFileRevision(ctx context.Context, id domain.SessionID, input WorkspaceSelectionRevisionInput) (WorkspaceFileRevision, error) {
	ctx, cancel := context.WithTimeout(ctx, workspaceReviewTimeout)
	defer cancel()
	shas, err := selectionCommitSHAs(input.CommitSHA, input.CommitSHAs)
	if err != nil {
		return WorkspaceFileRevision{}, err
	}
	if input.Scope != "" && input.Scope != WorkspaceDiffCommitted {
		return WorkspaceFileRevision{}, commitScopeRequired()
	}
	if input.Side != WorkspaceBlobBefore && input.Side != WorkspaceBlobAfter {
		return WorkspaceFileRevision{}, apierr.Invalid("INVALID_WORKSPACE_REVISION_SIDE", "side must be before or after", nil)
	}
	rel, err := cleanWorkspaceRelativePath(input.Path)
	if err != nil {
		return WorkspaceFileRevision{}, err
	}
	var selection WorkspaceCommitSelection
	var version string
	if input.Snapshot {
		selection, err = s.snapshotSelection(ctx, id, input)
		version = hashWorkspaceReviewValue(selection.root, selection.before, selection.after)
	} else {
		selection, version, err = s.workspaceSelection(ctx, id, input.View, shas, input.WorkspaceVersion, true)
	}
	if err != nil {
		return WorkspaceFileRevision{}, err
	}
	if !selection.Combinable {
		return WorkspaceFileRevision{}, commitsNotCombinable(selection.Conflict)
	}
	result := WorkspaceFileRevision{SessionID: id, Path: rel, Side: input.Side, WorkspaceVersion: version, Encoding: "utf-8"}
	if selection.after == "" {
		selection.after = selection.View.TipSHA
	}
	if selection.before == "" {
		selection.before = selection.after
	}
	if selection.after == "" {
		return result, nil
	}
	// A path outside the selection's files is unchanged, so both sides read
	// the same content.
	status, previous := WorkspaceFileUnmodified, ""
	for _, file := range selection.Files {
		if file.Path == rel {
			status, previous = file.Status, file.PreviousPath
			break
		}
	}
	spec := selection.after + ":" + rel
	if input.Side == WorkspaceBlobBefore {
		if status == WorkspaceFileAdded {
			return result, nil
		}
		path := rel
		if previous != "" {
			path = previous
		}
		spec = selection.before + ":" + path
	} else if status == WorkspaceFileDeleted {
		return result, nil
	}
	data, size, exists, truncated, err := readGitRevision(ctx, selection.root, spec)
	if err != nil {
		return WorkspaceFileRevision{}, err
	}
	return completeWorkspaceFileRevision(result, data, size, exists, truncated, input.ExpectedRevision)
}

// getSelectionDiffs is GetWorkspaceDiffs for a commit selection: one patch of
// the selection's before..after for the requested paths.
func (s *Service) getSelectionDiffs(ctx context.Context, id domain.SessionID, input WorkspaceDiffInput, shas []string) (WorkspaceDiffs, error) {
	paths := make([]string, 0, len(input.Paths))
	seen := map[string]struct{}{}
	for _, rawPath := range input.Paths {
		clean, err := cleanWorkspaceRelativePath(rawPath)
		if err != nil {
			return WorkspaceDiffs{}, err
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		paths = append(paths, clean)
	}
	selection, version, err := s.workspaceSelection(ctx, id, input.View, shas, input.WorkspaceVersion, true)
	if err != nil {
		return WorkspaceDiffs{}, err
	}
	if !selection.Combinable {
		return WorkspaceDiffs{}, commitsNotCombinable(selection.Conflict)
	}
	slices.Sort(paths)
	group := WorkspaceDiffGroup{IncludedPaths: paths}
	result := WorkspaceDiffs{SessionID: id, WorkspaceVersion: version, Groups: []WorkspaceDiffGroup{group}}
	if selection.before == "" {
		return result, nil
	}
	diffPaths := slices.Clone(paths)
	for _, file := range selection.Files {
		if _, ok := seen[file.Path]; ok && file.PreviousPath != "" {
			diffPaths = append(diffPaths, file.PreviousPath)
		}
	}
	slices.Sort(diffPaths)
	diffPaths = slices.Compact(diffPaths)
	args := []string{"diff", "--no-ext-diff", "--no-textconv", "--find-renames", fmt.Sprintf("--unified=%d", input.ContextLines)}
	if input.IgnoreWhitespace {
		args = append(args, "--ignore-all-space")
	}
	args = append(append(args, selection.before, selection.after, "--"), diffPaths...)
	out, truncated, err := gitWorkspaceOutputCapped(ctx, selection.root, maxWorkspaceDiffGroupBytes, args...)
	if err != nil {
		// As in GetWorkspaceDiffs, a failed patch is reported on its group.
		result.Groups[0].Errors = []WorkspaceDiffError{{Code: "WORKSPACE_DIFF_FAILED", Message: "Unable to load this repository's diff"}}
		return result, nil //nolint:nilerr // The failure is carried on the group.
	}
	result.Groups[0].Patch, result.Groups[0].Truncated = truncateUTF8(out, maxWorkspaceDiffGroupBytes)
	result.Groups[0].Truncated = result.Groups[0].Truncated || truncated
	return result, nil
}

// workspaceSelection resolves a view and its ticked commits to a selection,
// with the workspaceVersion reads of it report. A checked-out selection
// reports, and is fenced by, the manifest version when withVersion is set or
// a version was sent; a view reports its revision pair's hash instead.
func (s *Service) workspaceSelection(ctx context.Context, id domain.SessionID, v WorkspaceView, shas []string, workspaceVersion string, withVersion bool) (WorkspaceCommitSelection, string, error) {
	var version string
	if v.checkedOut() && (withVersion || workspaceVersion != "") {
		current, err := s.workspaceDiffManifest(ctx, id, workspaceVersion)
		if err != nil {
			return WorkspaceCommitSelection{}, "", err
		}
		if workspaceVersion != "" && workspaceVersion != current.WorkspaceVersion {
			return WorkspaceCommitSelection{}, "", apierr.Conflict("WORKSPACE_SNAPSHOT_STALE", "Workspace changed while the commits were loading", map[string]any{"workspaceVersion": current.WorkspaceVersion})
		}
		version = current.WorkspaceVersion
	}
	view, err := s.resolveWorkspaceView(ctx, id, v)
	if err != nil {
		return WorkspaceCommitSelection{}, "", err
	}
	selection, err := s.commitSelection(ctx, view, shas)
	if err != nil {
		return WorkspaceCommitSelection{}, "", err
	}
	selection.SessionID = id
	selection.View = view.WorkspaceViewInfo
	if !v.checkedOut() {
		version = hashWorkspaceReviewValue(selection.root, selection.before, selection.after)
	}
	return selection, version, nil
}

// selectionCommitSHAs validates a selection's SHAs, trimmed and deduplicated
// in their given order. commitSHA is the legacy single-commit alias. Only a
// full SHA is accepted, so no unvalidated string reaches Git.
func selectionCommitSHAs(commitSHA string, shas []string) ([]string, error) {
	commitSHA = strings.TrimSpace(commitSHA)
	if commitSHA != "" {
		if len(shas) > 0 {
			return nil, apierr.Invalid("WORKSPACE_COMMIT_SELECTION_CONFLICT", "Send commitSha or commitShas, not both", nil)
		}
		shas = []string{commitSHA}
	}
	out := make([]string, 0, len(shas))
	seen := map[string]struct{}{}
	for _, raw := range shas {
		sha := strings.TrimSpace(raw)
		if sha == "" {
			continue
		}
		if _, ok := seen[sha]; ok {
			continue
		}
		if !fullCommitSHA.MatchString(sha) {
			return nil, apierr.Invalid("INVALID_WORKSPACE_COMMIT_SHA", "Commit SHAs must be full object names", nil)
		}
		seen[sha] = struct{}{}
		out = append(out, sha)
	}
	if len(out) > maxSelectionCommits {
		return nil, apierr.Invalid("WORKSPACE_COMMIT_SELECTION_TOO_LARGE", "Too many commits are selected", map[string]any{"limit": maxSelectionCommits})
	}
	return out, nil
}

func commitScopeRequired() error {
	return apierr.Invalid("WORKSPACE_COMMIT_SCOPE_REQUIRED", "Commits and other branches require the committed scope", nil)
}

func commitsNotCombinable(conflict *WorkspaceCommitSelectionConflict) error {
	details := map[string]any{}
	if conflict != nil {
		details = map[string]any{"commitSha": conflict.CommitSHA, "paths": conflict.Paths, "reason": string(conflict.Reason)}
	}
	return apierr.Conflict("WORKSPACE_COMMITS_NOT_COMBINABLE", "The selected commits cannot be combined", details)
}

// commitSelection is resolveCommitSelection behind the selection cache. Every
// key part is an immutable SHA or argument, so a cached entry never goes
// stale; a moved branch resolves to a new tip and so a new key.
func (s *Service) commitSelection(ctx context.Context, view resolvedWorkspaceView, shas []string) (WorkspaceCommitSelection, error) {
	if view.rangeBefore == "" || view.TipSHA == "" {
		return resolveCommitSelection(ctx, view, shas)
	}
	sorted := slices.Clone(shas)
	slices.Sort(sorted)
	key := strings.Join([]string{view.root, view.rangeBefore, view.TipSHA, strings.Join(view.logArgs, "\x01"), strings.Join(sorted, ",")}, "\x00")
	if selection, ok := s.commitSelections.get(key); ok {
		return selection, nil
	}
	selection, err := resolveCommitSelection(ctx, view, shas)
	if err != nil {
		return WorkspaceCommitSelection{}, err
	}
	s.commitSelections.put(key, selection)
	return selection, nil
}

// commitRun is a chain of ticked commits, each the first parent of the next,
// or ticked commits whose history makes them one range base..last.
type commitRun struct {
	first, last string
	// base is first's first parent, empty for a root commit.
	base string
}

// resolveCommitSelection resolves shas, or the whole range when there are
// none, to one (before, after) pair. Ticked commits split into runs of
// adjacent commits: one run is the plain range base..last, and later runs are
// replayed onto the first in a scratch index. Only objects are written; HEAD,
// the index, the worktree and refs are never touched.
func resolveCommitSelection(ctx context.Context, view resolvedWorkspaceView, shas []string) (WorkspaceCommitSelection, error) {
	selection := WorkspaceCommitSelection{Contiguous: true, Combinable: true, Files: []WorkspaceFileSummary{}, root: view.root}
	if view.rangeBefore == "" || view.TipSHA == "" {
		if len(shas) > 0 {
			return WorkspaceCommitSelection{}, commitNotFound()
		}
		return selection, nil
	}
	if len(shas) == 0 {
		selection.before, selection.after = view.rangeBefore, view.TipSHA
	} else {
		runs, err := selectionCommitRuns(ctx, view, shas)
		if err != nil {
			return WorkspaceCommitSelection{}, err
		}
		selection.before = runs[0].base
		if selection.before == "" {
			tree, ok := workspaceEmptyTree(ctx, view.root)
			if !ok {
				return WorkspaceCommitSelection{}, fmt.Errorf("resolve the empty tree in %s", view.root)
			}
			selection.before = tree
		}
		if len(runs) == 1 {
			selection.after = runs[0].last
		} else {
			if len(runs) > maxSelectionRuns {
				return WorkspaceCommitSelection{}, apierr.Invalid("WORKSPACE_COMMIT_SELECTION_TOO_LARGE", "The selected commits are split into too many separate ranges", map[string]any{"limit": maxSelectionRuns})
			}
			selection.Contiguous = false
			tree, conflict, err := replayCommitRuns(ctx, view.root, runs)
			if err != nil {
				return WorkspaceCommitSelection{}, err
			}
			if conflict != nil {
				selection.Combinable = false
				selection.Conflict = conflict
				selection.before = ""
				return selection, nil
			}
			selection.after = tree
		}
	}
	files, truncated, err := selectionFiles(ctx, view.root, selection.before, selection.after)
	if err != nil {
		return WorkspaceCommitSelection{}, err
	}
	selection.Files = files
	selection.Truncated = truncated
	selection.Summary = workspaceSummaryFromFiles(files)
	return selection, nil
}

func commitNotFound() error {
	return apierr.NotFound("WORKSPACE_COMMIT_NOT_FOUND", "Commit is not available in this workspace comparison")
}

// selectionCommitRuns checks every SHA belongs to the view's range and splits
// them, oldest first, into runs of adjacent commits. One rev-list call gives
// membership, order and parents.
func selectionCommitRuns(ctx context.Context, view resolvedWorkspaceView, shas []string) ([]commitRun, error) {
	limit := maxSelectionRangeCommits
	if view.RangeMode == WorkspaceRangeRecent {
		limit = maxRecentCommits
	}
	args := append([]string{"rev-list", "--topo-order", "--parents", "--max-count=" + strconv.Itoa(limit)}, view.logArgs...)
	out, err := gitWorkspaceOutput(ctx, view.root, args...)
	if err != nil {
		return nil, err
	}
	position := map[string]int{}
	parents := map[string][]string{}
	for i, line := range strings.Split(strings.TrimSpace(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		position[fields[0]] = i
		parents[fields[0]] = fields[1:]
	}
	firstParent := func(sha string) string {
		if len(parents[sha]) == 0 {
			return ""
		}
		return parents[sha][0]
	}
	ordered := slices.Clone(shas)
	for _, sha := range ordered {
		if _, ok := position[sha]; !ok {
			return nil, commitNotFound()
		}
	}
	// rev-list lists newest first, so the highest position is the oldest.
	slices.SortFunc(ordered, func(a, b string) int { return position[b] - position[a] })
	// A truncated listing hides ancestors, so only a complete one may stand
	// in for the history between the ticked commits.
	if len(position) < limit {
		if run, ok := ancestryClosedRun(parents, view.rangeBefore, ordered); ok {
			return []commitRun{run}, nil
		}
	}
	runs := []commitRun{{first: ordered[0], last: ordered[0], base: firstParent(ordered[0])}}
	for _, sha := range ordered[1:] {
		if run := &runs[len(runs)-1]; firstParent(sha) == run.last {
			run.last = sha
			continue
		}
		runs = append(runs, commitRun{first: sha, last: sha, base: firstParent(sha)})
	}
	return runs, nil
}

// ancestryClosedRun reads ticked commits (oldest first) as one range when the
// history between them allows it, so a merged side branch ticked with its
// merge is one diff rather than runs that each carry the side's changes. The
// newest ticked commit n must reach every other one. The unticked listed
// ancestors of n must then be nothing (the range rangeBefore..n) or exactly
// the listed ancestors of one commit m (the range m..n). Either "before" must
// descend from rangeBefore, as only then are the unlisted ancestors of n
// already in it.
func ancestryClosedRun(parents map[string][]string, rangeBefore string, ordered []string) (commitRun, bool) {
	newest := ordered[len(ordered)-1]
	reach := listedAncestors(parents, newest)
	ticked := map[string]struct{}{}
	for _, sha := range ordered {
		if _, ok := reach[sha]; !ok {
			return commitRun{}, false
		}
		ticked[sha] = struct{}{}
	}
	unticked := map[string]struct{}{}
	for sha := range reach {
		if _, ok := ticked[sha]; !ok {
			unticked[sha] = struct{}{}
		}
	}
	if len(unticked) == 0 {
		return commitRun{first: ordered[0], last: newest, base: rangeBefore}, descendsFrom(parents, reach, rangeBefore)
	}
	// m is the one unticked commit no other unticked commit has as a parent.
	heads := maps.Clone(unticked)
	for sha := range unticked {
		for _, parent := range parents[sha] {
			delete(heads, parent)
		}
	}
	if len(heads) != 1 {
		return commitRun{}, false
	}
	var head string
	for sha := range heads {
		head = sha
	}
	below := listedAncestors(parents, head)
	if len(below) != len(unticked) || !descendsFrom(parents, below, rangeBefore) {
		return commitRun{}, false
	}
	for sha := range below {
		if _, ok := unticked[sha]; !ok {
			return commitRun{}, false
		}
	}
	return commitRun{first: ordered[0], last: newest, base: head}, true
}

// listedAncestors is sha and its ancestors among the listed commits.
func listedAncestors(parents map[string][]string, sha string) map[string]struct{} {
	seen := map[string]struct{}{sha: {}}
	stack := []string{sha}
	for len(stack) > 0 {
		next := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, parent := range parents[next] {
			if _, listed := parents[parent]; !listed {
				continue
			}
			if _, ok := seen[parent]; !ok {
				seen[parent] = struct{}{}
				stack = append(stack, parent)
			}
		}
	}
	return seen
}

// descendsFrom reports whether one of commits has rangeBefore as a parent.
// rangeBefore is never listed, so that is the only way commits reach it.
func descendsFrom(parents map[string][]string, commits map[string]struct{}, rangeBefore string) bool {
	for sha := range commits {
		if slices.Contains(parents[sha], rangeBefore) {
			return true
		}
	}
	return false
}

// replayCommitRuns applies each later run's changes onto the first run's tree
// in a scratch index and writes the combined tree. The scratch index is a
// temporary file, so the session's real index is never read or written. A
// run whose changes do not apply returns a conflict instead of a tree.
func replayCommitRuns(ctx context.Context, root string, runs []commitRun) (string, *WorkspaceCommitSelectionConflict, error) {
	for _, run := range runs[1:] {
		if run.base == "" {
			return "", &WorkspaceCommitSelectionConflict{CommitSHA: run.first, Paths: []string{}, Reason: WorkspaceCommitUnsupportedHistory}, nil
		}
	}
	// Git creates the index (and its .lock) itself inside a private
	// directory, which is removed whole even after a killed git leaves the
	// lock behind.
	dir, err := os.MkdirTemp("", "ao-review-idx-*")
	if err != nil {
		return "", nil, fmt.Errorf("reserve a scratch index: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	indexPath := filepath.Join(dir, "index")
	// A split index would write its shared part into the repository.
	env := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err := gitWorkspaceOutputEnv(ctx, root, env, "-c", "core.splitIndex=false", "read-tree", runs[0].last); err != nil {
		return "", nil, err
	}
	for _, run := range runs[1:] {
		paths, err := applyCommitRun(ctx, root, env, run)
		if err != nil {
			return "", nil, err
		}
		if paths != nil {
			return "", &WorkspaceCommitSelectionConflict{CommitSHA: run.first, Paths: paths, Reason: WorkspaceCommitConflict}, nil
		}
	}
	out, err := gitWorkspaceOutputEnv(ctx, root, env, "-c", "core.splitIndex=false", "write-tree")
	if err != nil {
		return "", nil, err
	}
	return strings.TrimSpace(out), nil, nil
}

// applyCommitRun pipes run's patch into the scratch index named by env. It
// returns the paths that did not apply, non-nil exactly when the patch was
// rejected.
func applyCommitRun(ctx context.Context, root string, env []string, run commitRun) ([]string, error) {
	// Explicit prefixes, context, submodule format and no color keep the
	// patch in the shape git apply reads whatever diff configuration the
	// repository carries.
	diff := workspaceGitCommand(ctx, root, "diff", "--binary", "--full-index", "--find-renames", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3", "--submodule=short", "--ignore-submodules=none", "--src-prefix=a/", "--dst-prefix=b/", run.base, run.last, "--")
	diffStderr := &cappedWorkspaceOutput{limit: 64 * 1024}
	diff.Stderr = diffStderr
	patch, err := diff.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := diff.Start(); err != nil {
		return nil, err
	}
	reader := bufio.NewReaderSize(patch, 64*1024)
	if _, peekErr := reader.Peek(1); peekErr != nil {
		// An empty patch (a run with no changes) has nothing to apply, and
		// git apply rejects empty input.
		_ = patch.Close()
		if waitErr := diff.Wait(); waitErr != nil {
			return nil, classifyWorkspaceGitError(ctx, waitErr, strings.TrimSpace(diffStderr.buffer.String()))
		}
		if !errors.Is(peekErr, io.EOF) {
			return nil, peekErr
		}
		return nil, nil
	}
	apply := workspaceGitCommand(ctx, root, "-c", "core.splitIndex=false", "apply", "--cached", "--whitespace=nowarn")
	apply.Env = append(apply.Env, env...)
	apply.Stdin = reader
	applyStderr := &cappedWorkspaceOutput{limit: 64 * 1024}
	apply.Stderr = applyStderr
	applyErr := apply.Run()
	// Closing the read side stops a diff that git apply gave up reading.
	_ = patch.Close()
	diffErr := diff.Wait()
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if applyErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(applyErr, &exitErr) {
			return nil, applyErr
		}
		return applyConflictPaths(applyStderr.buffer.String()), nil
	}
	if diffErr != nil {
		return nil, classifyWorkspaceGitError(ctx, diffErr, strings.TrimSpace(diffStderr.buffer.String()))
	}
	return nil, nil
}

// applyConflictLine matches the git apply errors that name a rejected path.
var applyConflictLine = regexp.MustCompile(`^error: (?:patch failed: (.+):\d+|(.+): (?:does not exist in index|already exists in index|patch does not apply))$`)

// applyConflictPaths reads the rejected paths from git apply's stderr,
// deduplicated and capped. It is never nil.
func applyConflictPaths(stderr string) []string {
	paths := []string{}
	seen := map[string]struct{}{}
	for _, line := range strings.Split(stderr, "\n") {
		match := applyConflictLine.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		path := match[1]
		if path == "" {
			path = match[2]
		}
		if _, ok := seen[path]; ok || !utf8.ValidString(path) {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
		if len(paths) == maxSelectionConflictPaths {
			break
		}
	}
	return paths
}

// selectionFiles lists the files changed between before and after. Sizes are
// read from after's tree, never the worktree, and nothing is editable.
func selectionFiles(ctx context.Context, root, before, after string) ([]WorkspaceFileSummary, bool, error) {
	statuses, previous, counts, err := workspaceDiffStats(ctx, root, before, after)
	if err != nil {
		return nil, false, err
	}
	paths := make([]string, 0, len(statuses))
	for rel := range statuses {
		paths = append(paths, rel)
	}
	slices.Sort(paths)
	truncated := len(paths) > maxWorkspaceFiles
	if truncated {
		paths = paths[:maxWorkspaceFiles]
	}
	present := make([]string, 0, len(paths))
	for _, rel := range paths {
		if statuses[rel] != WorkspaceFileDeleted {
			present = append(present, rel)
		}
	}
	sizes, err := gitTreeBlobSizes(ctx, root, after, present)
	if err != nil {
		return nil, false, err
	}
	files := make([]WorkspaceFileSummary, 0, len(paths))
	for _, rel := range paths {
		status := statuses[rel]
		lines, hasTextCounts := counts[rel]
		file := WorkspaceFileSummary{Path: rel, PreviousPath: previous[rel], Status: status, Additions: lines[0], Deletions: lines[1], Size: sizes[rel], Binary: status != WorkspaceFileDeleted && !hasTextCounts}
		file.FileFingerprint = summaryFingerprint(file)
		files = append(files, file)
	}
	return files, truncated, nil
}

// gitTreeBlobSizes reads the blob sizes of paths in tree, in batches.
func gitTreeBlobSizes(ctx context.Context, root, tree string, paths []string) (map[string]int64, error) {
	sizes := make(map[string]int64, len(paths))
	for start := 0; start < len(paths); start += gitTreeSizeBatch {
		end := min(start+gitTreeSizeBatch, len(paths))
		args := append([]string{"ls-tree", "-r", "-z", "--long", "--full-tree", tree, "--"}, paths[start:end]...)
		out, err := gitWorkspaceOutput(ctx, root, args...)
		if err != nil {
			return nil, err
		}
		for _, record := range splitNUL(out) {
			meta, rel, ok := strings.Cut(record, "\t")
			fields := strings.Fields(meta)
			if !ok || len(fields) != 4 {
				continue
			}
			if size, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
				sizes[rel] = size
			}
		}
	}
	return sizes, nil
}

// commitSelectionCache is a small LRU of resolved selections. A nil cache
// caches nothing.
type commitSelectionCache struct {
	mu      sync.Mutex
	limit   int
	order   *list.List
	entries map[string]*list.Element
}

type commitSelectionEntry struct {
	key       string
	selection WorkspaceCommitSelection
}

func newCommitSelectionCache(limit int) *commitSelectionCache {
	return &commitSelectionCache{limit: limit, order: list.New(), entries: map[string]*list.Element{}}
}

func (c *commitSelectionCache) get(key string) (WorkspaceCommitSelection, bool) {
	if c == nil {
		return WorkspaceCommitSelection{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	element, ok := c.entries[key]
	if !ok {
		return WorkspaceCommitSelection{}, false
	}
	c.order.MoveToFront(element)
	entry, _ := element.Value.(commitSelectionEntry)
	return entry.selection, true
}

func (c *commitSelectionCache) put(key string, selection WorkspaceCommitSelection) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if element, ok := c.entries[key]; ok {
		element.Value = commitSelectionEntry{key: key, selection: selection}
		c.order.MoveToFront(element)
		return
	}
	c.entries[key] = c.order.PushFront(commitSelectionEntry{key: key, selection: selection})
	for c.order.Len() > c.limit {
		oldest := c.order.Back()
		entry, _ := oldest.Value.(commitSelectionEntry)
		delete(c.entries, entry.key)
		c.order.Remove(oldest)
	}
}

func (c *commitSelectionCache) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}
