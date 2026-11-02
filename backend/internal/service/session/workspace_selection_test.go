package session

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// checkedOutSelectionService serves a session checked out at repo whose
// compare range starts at base.
func checkedOutSelectionService(repo, base string) *Service {
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{Branch: "session", WorkspacePath: repo, DiffBaseSHA: base, DiffBaseRef: "main"}}
	return &Service{store: st, commitSelections: newCommitSelectionCache(commitSelectionCacheSize)}
}

// selectionFixture commits three changes on the checked-out session branch,
// one file each, and returns the base and the three commits oldest first.
func selectionFixture(t *testing.T) (repo, base string, commits []string) {
	t.Helper()
	repo = newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	base = gitHead(t, repo)
	runGit(t, repo, "switch", "-c", "session")
	commits = []string{
		commitWorkspaceFile(t, repo, "a.txt", "a\n", "add a"),
		commitWorkspaceFile(t, repo, "b.txt", "b\n", "add b"),
		commitWorkspaceFile(t, repo, "c.txt", "c\n", "add c"),
	}
	return repo, base, commits
}

func selectionPaths(selection WorkspaceCommitSelection) []string {
	paths := make([]string, 0, len(selection.Files))
	for _, file := range selection.Files {
		paths = append(paths, file.Path)
	}
	return paths
}

func TestCommitSelectionAdjacentUsesSimpleRange(t *testing.T) {
	repo, base, commits := selectionFixture(t)
	svc := checkedOutSelectionService(repo, base)
	ctx := context.Background()

	// Newest first, as the menu lists them; the order sent does not matter.
	selection, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, []string{commits[2], commits[1]}, "")
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Contiguous || !selection.Combinable || selection.Conflict != nil {
		t.Fatalf("selection = %+v, want one contiguous range", selection)
	}
	if got := selectionPaths(selection); !slices.Equal(got, []string{"b.txt", "c.txt"}) {
		t.Fatalf("paths = %v, want b.txt and c.txt", got)
	}
	if selection.Summary != (WorkspaceSummary{Files: 2, Additions: 2}) || selection.Files[0].Size != 2 || selection.Files[0].Editable {
		t.Fatalf("summary = %+v, files = %+v", selection.Summary, selection.Files)
	}
	if selection.View.Kind != WorkspaceViewCheckedOut || selection.View.BaseSHA != base {
		t.Fatalf("view = %+v, want the checked-out range", selection.View)
	}

	diffs, err := svc.GetWorkspaceDiffs(ctx, "ao-1", WorkspaceDiffInput{Scope: WorkspaceDiffCommitted, CommitSHAs: []string{commits[1], commits[2]}, Paths: []string{"c.txt", "b.txt"}, ContextLines: 3})
	if err != nil {
		t.Fatal(err)
	}
	want := runGit(t, repo, "diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--unified=3", commits[0], commits[2], "--", "b.txt", "c.txt")
	if len(diffs.Groups) != 1 || diffs.Groups[0].Patch != want || !slices.Equal(diffs.Groups[0].IncludedPaths, []string{"b.txt", "c.txt"}) {
		t.Fatalf("diffs = %+v, want git diff %s..%s", diffs, commits[0], commits[2])
	}
}

func TestCommitSelectionNonAdjacentReplaysWithoutTouchingIndexWorktreeOrRefs(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	runGit(t, repo, "switch", "-c", "feature")
	first := commitWorkspaceFile(t, repo, "a.txt", "a\n", "add a")
	commitWorkspaceFile(t, repo, "b.txt", "b\n", "add b")
	commitWorkspaceFile(t, repo, "README.md", "hello\nfeature\n", "extend readme")
	last := commitWorkspaceFile(t, repo, "c.txt", "c\n", "add c")
	runGit(t, repo, "switch", "-c", "session", "main")
	// Staged, unstaged and untracked changes the replay must not disturb.
	writeWorkspaceFile(t, repo, "README.md", "staged\n")
	runGit(t, repo, "add", "README.md")
	writeWorkspaceFile(t, repo, "README.md", "staged\nunstaged\n")
	writeWorkspaceFile(t, repo, "scratch.txt", "untracked\n")
	head := gitHead(t, repo)
	status := runGit(t, repo, "status", "--porcelain")
	refs := runGit(t, repo, "for-each-ref")
	index, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	svc := branchViewService(repo)
	ctx := context.Background()
	view := WorkspaceView{Branch: "feature"}
	selection, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", view, []string{last, first}, "")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Contiguous || !selection.Combinable || selection.Conflict != nil {
		t.Fatalf("selection = %+v, want combined non-adjacent commits", selection)
	}
	if got := selectionPaths(selection); !slices.Equal(got, []string{"a.txt", "c.txt"}) {
		t.Fatalf("paths = %v, want a.txt and c.txt without the skipped commits", got)
	}

	diffs, err := svc.GetWorkspaceDiffs(ctx, "ao-1", WorkspaceDiffInput{Scope: WorkspaceDiffCommitted, View: view, CommitSHAs: []string{first, last}, Paths: []string{"a.txt", "c.txt", "README.md"}, ContextLines: 3})
	if err != nil {
		t.Fatal(err)
	}
	patch := diffs.Groups[0].Patch
	if !strings.Contains(patch, "+a") || !strings.Contains(patch, "+c") || strings.Contains(patch, "feature") || diffs.WorkspaceVersion == "" {
		t.Fatalf("patch = %q, want a.txt and c.txt only", patch)
	}
	after, err := svc.GetWorkspaceSelectionFileRevision(ctx, "ao-1", WorkspaceSelectionRevisionInput{Path: "c.txt", Side: WorkspaceBlobAfter, Scope: WorkspaceDiffCommitted, View: view, CommitSHAs: []string{first, last}})
	if err != nil {
		t.Fatal(err)
	}
	before, err := svc.GetWorkspaceSelectionFileRevision(ctx, "ao-1", WorkspaceSelectionRevisionInput{Path: "c.txt", Side: WorkspaceBlobBefore, View: view, CommitSHAs: []string{first, last}})
	if err != nil {
		t.Fatal(err)
	}
	if after.Content != "c\n" || before.Exists {
		t.Fatalf("revisions = before %+v, after %+v", before, after)
	}

	gotIndex, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	if string(gotIndex) != string(index) {
		t.Fatal("the replay rewrote the index")
	}
	if got := gitHead(t, repo); got != head {
		t.Fatalf("HEAD = %s, want %s", got, head)
	}
	if got := runGit(t, repo, "status", "--porcelain"); got != status {
		t.Fatalf("status = %q, want %q", got, status)
	}
	if got := runGit(t, repo, "for-each-ref"); got != refs {
		t.Fatalf("refs = %q, want %q", got, refs)
	}
	if entries, err := os.ReadDir(tmp); err != nil || len(entries) != 0 {
		t.Fatalf("temp dir entries = %v (%v), want the scratch index removed", entries, err)
	}
}

func TestCommitSelectionReportsConflict(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	base := gitHead(t, repo)
	runGit(t, repo, "switch", "-c", "session")
	first := commitWorkspaceFile(t, repo, "notes.txt", "one\n", "start notes")
	commitWorkspaceFile(t, repo, "notes.txt", "one\ntwo\n", "add two")
	last := commitWorkspaceFile(t, repo, "notes.txt", "one\ntwo\nthree\n", "add three")
	svc := checkedOutSelectionService(repo, base)
	ctx := context.Background()

	selection, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, []string{first, last}, "")
	if err != nil {
		t.Fatal(err)
	}
	want := WorkspaceCommitSelectionConflict{CommitSHA: last, Paths: []string{"notes.txt"}, Reason: WorkspaceCommitConflict}
	if selection.Combinable || selection.Conflict == nil || selection.Conflict.CommitSHA != want.CommitSHA || !slices.Equal(selection.Conflict.Paths, want.Paths) || selection.Conflict.Reason != want.Reason || len(selection.Files) != 0 {
		t.Fatalf("selection = %+v conflict = %+v, want %+v", selection, selection.Conflict, want)
	}

	_, err = svc.GetWorkspaceDiffs(ctx, "ao-1", WorkspaceDiffInput{Scope: WorkspaceDiffCommitted, CommitSHAs: []string{first, last}, Paths: []string{"notes.txt"}, ContextLines: 3})
	apiError := requireAPIError(t, err, apierr.KindConflict, "WORKSPACE_COMMITS_NOT_COMBINABLE")
	if apiError.Details["commitSha"] != last || apiError.Details["reason"] != "conflict" {
		t.Fatalf("details = %+v", apiError.Details)
	}
	_, err = svc.GetWorkspaceSelectionFileRevision(ctx, "ao-1", WorkspaceSelectionRevisionInput{Path: "notes.txt", Side: WorkspaceBlobAfter, CommitSHAs: []string{first, last}})
	requireAPIError(t, err, apierr.KindConflict, "WORKSPACE_COMMITS_NOT_COMBINABLE")
}

func TestCommitSelectionReplayIgnoresDiffConfig(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	// Settings a user may carry that would strip the patch's context or
	// print submodule bumps as a log the replay cannot apply.
	runGit(t, repo, "config", "diff.context", "0")
	runGit(t, repo, "config", "diff.submodule", "log")
	lines := make([]string, 100)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %d", i+1)
	}
	edit := func(n int) string {
		lines[n-1] = fmt.Sprintf("edited %d", n)
		return strings.Join(lines, "\n") + "\n"
	}
	oldLink, newLink := strings.Repeat("1", 40), strings.Repeat("2", 40)
	writeWorkspaceFile(t, repo, "long.txt", strings.Join(lines, "\n")+"\n")
	runGit(t, repo, "add", "long.txt")
	runGit(t, repo, "update-index", "--add", "--cacheinfo", "160000,"+oldLink+",sub")
	runGit(t, repo, "commit", "-m", "long file and submodule")
	base := gitHead(t, repo)
	runGit(t, repo, "switch", "-c", "session")
	first := commitWorkspaceFile(t, repo, "long.txt", edit(10), "edit line 10")
	commitWorkspaceFile(t, repo, "long.txt", edit(50), "edit line 50")
	runGit(t, repo, "update-index", "--cacheinfo", "160000,"+newLink+",sub")
	last := commitWorkspaceFile(t, repo, "long.txt", edit(90), "edit line 90 and bump sub")
	svc := checkedOutSelectionService(repo, base)
	ctx := context.Background()

	selection, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, []string{first, last}, "")
	if err != nil {
		t.Fatal(err)
	}
	if selection.Contiguous || !selection.Combinable || selection.Conflict != nil {
		t.Fatalf("selection = %+v conflict = %+v, want distant hunks combined", selection, selection.Conflict)
	}
	after, err := svc.GetWorkspaceSelectionFileRevision(ctx, "ao-1", WorkspaceSelectionRevisionInput{Path: "long.txt", Side: WorkspaceBlobAfter, Scope: WorkspaceDiffCommitted, CommitSHAs: []string{first, last}})
	if err != nil {
		t.Fatal(err)
	}
	lines[49] = "line 50"
	if want := strings.Join(lines, "\n") + "\n"; after.Content != want {
		t.Fatalf("combined long.txt = %q, want lines 10 and 90 edited and line 50 untouched", after.Content)
	}
	if got := strings.TrimSpace(runGit(t, repo, "rev-parse", selection.after+":sub")); got != newLink {
		t.Fatalf("combined sub = %s, want the bumped gitlink %s", got, newLink)
	}
}

func TestCommitSelectionMergedSideBranchIsOneRange(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	base := gitHead(t, repo)
	runGit(t, repo, "switch", "-c", "side")
	s1 := commitWorkspaceFile(t, repo, "s1.txt", "s1\n", "side one")
	s2 := commitWorkspaceFile(t, repo, "s2.txt", "s2\n", "side two")
	runGit(t, repo, "switch", "-c", "feature", "main")
	f1 := commitWorkspaceFile(t, repo, "f1.txt", "f1\n", "feature one")
	f2 := commitWorkspaceFile(t, repo, "f2.txt", "f2\n", "feature two")
	runGit(t, repo, "merge", "--no-ff", "-m", "merge side", "side")
	merge := gitHead(t, repo)
	commitWorkspaceFile(t, repo, "g.txt", "g\n", "after the merge")
	runGit(t, repo, "switch", "-c", "session", "main")
	svc := branchViewService(repo)
	ctx := context.Background()
	view := WorkspaceView{Branch: "feature"}

	for _, tc := range []struct {
		name   string
		shas   []string
		before string
		paths  []string
	}{
		{"every commit but the tip", []string{merge, s2, s1, f2, f1}, base, []string{"f1.txt", "f2.txt", "s1.txt", "s2.txt"}},
		{"middle to newest", []string{merge, s2, s1, f2}, f1, []string{"f2.txt", "s1.txt", "s2.txt"}},
	} {
		selection, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", view, tc.shas, "")
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if !selection.Contiguous || !selection.Combinable || selection.before != tc.before || selection.after != merge {
			t.Fatalf("%s: selection = %+v conflict = %+v, want the range %s..%s", tc.name, selection, selection.Conflict, tc.before, merge)
		}
		if got := selectionPaths(selection); !slices.Equal(got, tc.paths) {
			t.Fatalf("%s: paths = %v, want %v", tc.name, got, tc.paths)
		}
	}
}

func TestCommitSelectionRootCommitUsesEmptyTree(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	root := gitHead(t, repo)
	second := commitWorkspaceFile(t, repo, "later.txt", "later\n", "later")
	svc := branchViewService(repo)
	ctx := context.Background()
	// main is the default branch, so its view lists its recent commits back
	// to the root commit.
	view := WorkspaceView{Branch: "main"}

	selection, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", view, []string{root}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := selectionPaths(selection); !slices.Equal(got, []string{".gitignore", "README.md", "src/app.go"}) || selection.Files[1].Status != WorkspaceFileAdded {
		t.Fatalf("files = %+v, want the root commit's files added", selection.Files)
	}
	before, err := svc.GetWorkspaceSelectionFileRevision(ctx, "ao-1", WorkspaceSelectionRevisionInput{Path: "README.md", Side: WorkspaceBlobBefore, View: view, CommitSHAs: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	after, err := svc.GetWorkspaceSelectionFileRevision(ctx, "ao-1", WorkspaceSelectionRevisionInput{Path: "README.md", Side: WorkspaceBlobAfter, View: view, CommitSHAs: []string{root}})
	if err != nil {
		t.Fatal(err)
	}
	if before.Exists || after.Content != "hello\n" {
		t.Fatalf("revisions = before %+v, after %+v", before, after)
	}

	// A later run without a parent has nothing to replay onto earlier ones.
	_, conflict, err := replayCommitRuns(ctx, repo, []commitRun{{first: second, last: second, base: root}, {first: root, last: root}})
	if err != nil {
		t.Fatal(err)
	}
	if conflict == nil || conflict.Reason != WorkspaceCommitUnsupportedHistory || conflict.CommitSHA != root {
		t.Fatalf("conflict = %+v, want unsupported history at the root commit", conflict)
	}
}

func TestCommitSelectionAllEqualsBaseToTip(t *testing.T) {
	repo := newWorkspaceRepo(t)
	runGit(t, repo, "branch", "-M", "main")
	base := gitHead(t, repo)
	runGit(t, repo, "switch", "-c", "feature")
	commitWorkspaceFile(t, repo, "feature.txt", "one\n", "add feature")
	commitWorkspaceFile(t, repo, "README.md", "hello\nfeature\n", "extend readme")
	tip := commitWorkspaceFile(t, repo, "feature.txt", "one\ntwo\n", "extend feature")
	runGit(t, repo, "switch", "-c", "session", "main")
	svc := branchViewService(repo)
	ctx := context.Background()

	selection, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{Branch: "feature"}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if !selection.Contiguous || !selection.Combinable || selection.View.TipSHA != tip || selection.View.BaseSHA != base {
		t.Fatalf("selection = %+v, want the whole base..tip range", selection)
	}
	want := strings.Fields(runGit(t, repo, "diff", "--name-only", base, tip))
	if got := selectionPaths(selection); !slices.Equal(got, want) {
		t.Fatalf("paths = %v, want %v", got, want)
	}
	history, err := svc.GetWorkspaceHistory(ctx, "ao-1", WorkspaceView{Branch: "feature"})
	if err != nil {
		t.Fatal(err)
	}
	if history.RangeSummary == nil || selection.Summary != *history.RangeSummary {
		t.Fatalf("summary = %+v, want the history's range summary %+v", selection.Summary, history.RangeSummary)
	}
}

func TestCommitSelectionRejectsOptionLikeShortAndForeignShas(t *testing.T) {
	repo, base, commits := selectionFixture(t)
	runGit(t, repo, "switch", "main")
	foreign := commitWorkspaceFile(t, repo, "main.txt", "main\n", "advance main")
	runGit(t, repo, "switch", "session")
	svc := checkedOutSelectionService(repo, base)
	ctx := context.Background()

	for _, sha := range []string{"--output=x", commits[0][:7], strings.ToUpper(commits[0])} {
		_, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, []string{sha}, "")
		requireAPIError(t, err, apierr.KindInvalid, "INVALID_WORKSPACE_COMMIT_SHA")
	}
	_, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, []string{commits[0], foreign}, "")
	requireAPIError(t, err, apierr.KindNotFound, "WORKSPACE_COMMIT_NOT_FOUND")
	_, err = svc.GetWorkspaceDiffs(ctx, "ao-1", WorkspaceDiffInput{Scope: WorkspaceDiffCommitted, CommitSHAs: []string{base}, Paths: []string{"a.txt"}})
	requireAPIError(t, err, apierr.KindNotFound, "WORKSPACE_COMMIT_NOT_FOUND")

	tooMany := make([]string, maxSelectionCommits+1)
	for i := range tooMany {
		tooMany[i] = fmt.Sprintf("%040x", i)
	}
	_, err = svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, tooMany, "")
	requireAPIError(t, err, apierr.KindInvalid, "WORKSPACE_COMMIT_SELECTION_TOO_LARGE")
}

func TestCommitShaAndCommitShasAreMutuallyExclusive(t *testing.T) {
	repo, base, commits := selectionFixture(t)
	svc := checkedOutSelectionService(repo, base)
	ctx := context.Background()

	_, err := svc.GetWorkspaceDiffs(ctx, "ao-1", WorkspaceDiffInput{Scope: WorkspaceDiffCommitted, CommitSHA: commits[0], CommitSHAs: []string{commits[1]}, Paths: []string{"a.txt"}})
	requireAPIError(t, err, apierr.KindInvalid, "WORKSPACE_COMMIT_SELECTION_CONFLICT")
	_, err = svc.GetWorkspaceSelectionFileRevision(ctx, "ao-1", WorkspaceSelectionRevisionInput{Path: "a.txt", Side: WorkspaceBlobAfter, CommitSHA: commits[0], CommitSHAs: []string{commits[1]}})
	requireAPIError(t, err, apierr.KindInvalid, "WORKSPACE_COMMIT_SELECTION_CONFLICT")

	// A selection reads committed revisions only.
	_, err = svc.GetWorkspaceDiffs(ctx, "ao-1", WorkspaceDiffInput{Scope: WorkspaceDiffStaged, CommitSHAs: []string{commits[1]}, Paths: []string{"a.txt"}})
	requireAPIError(t, err, apierr.KindInvalid, "WORKSPACE_COMMIT_SCOPE_REQUIRED")
	_, err = svc.GetWorkspaceDiffs(ctx, "ao-1", WorkspaceDiffInput{Scope: WorkspaceDiffUncommitted, View: WorkspaceView{Branch: "main"}, Paths: []string{"a.txt"}})
	requireAPIError(t, err, apierr.KindInvalid, "WORKSPACE_COMMIT_SCOPE_REQUIRED")

	// The legacy single commitSha still reads that one commit.
	after, err := svc.GetWorkspaceSelectionFileRevision(ctx, "ao-1", WorkspaceSelectionRevisionInput{Path: "b.txt", Side: WorkspaceBlobAfter, Scope: WorkspaceDiffCommitted, CommitSHA: commits[1]})
	if err != nil {
		t.Fatal(err)
	}
	if after.Content != "b\n" {
		t.Fatalf("after = %+v, want b.txt at its commit", after)
	}
}

func TestCommitSelectionCachesByImmutableInputs(t *testing.T) {
	repo, base, commits := selectionFixture(t)
	svc := checkedOutSelectionService(repo, base)
	ctx := context.Background()

	first, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, []string{commits[0], commits[2]}, "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, []string{commits[2], commits[0]}, "")
	if err != nil {
		t.Fatal(err)
	}
	if svc.commitSelections.len() != 1 || first.after != again.after || !slices.Equal(selectionPaths(first), selectionPaths(again)) {
		t.Fatalf("cache entries = %d, selections %+v and %+v; want one shared entry", svc.commitSelections.len(), first, again)
	}
	// A new commit moves the tip, so the same ticks resolve under a new key.
	commitWorkspaceFile(t, repo, "d.txt", "d\n", "add d")
	if _, err := svc.GetWorkspaceCommitSelection(ctx, "ao-1", WorkspaceView{}, []string{commits[0], commits[2]}, ""); err != nil {
		t.Fatal(err)
	}
	if svc.commitSelections.len() != 2 {
		t.Fatalf("cache entries = %d, want a second entry for the new tip", svc.commitSelections.len())
	}

	cache := newCommitSelectionCache(2)
	cache.put("a", WorkspaceCommitSelection{after: "a"})
	cache.put("b", WorkspaceCommitSelection{after: "b"})
	cache.get("a")
	cache.put("c", WorkspaceCommitSelection{after: "c"})
	if _, ok := cache.get("b"); ok || cache.len() != 2 {
		t.Fatal("the least recently used entry was not evicted")
	}
	if got, ok := cache.get("a"); !ok || got.after != "a" {
		t.Fatalf("entry a = %+v, %v; want it kept", got, ok)
	}
}
