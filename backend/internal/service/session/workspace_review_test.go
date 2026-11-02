package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestWorkspaceManifestMatchesReviewSnapshotWithoutUnchangedInventory(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "hello\nchanged\n")
	writeWorkspaceFile(t, repo, "notes.txt", "new\n")
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := NewWithDeps(Deps{Store: st})

	manifest, err := svc.GetWorkspaceManifest(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.WorkspaceVersion != files.WorkspaceVersion {
		t.Fatalf("manifest version = %q, files version = %q", manifest.WorkspaceVersion, files.WorkspaceVersion)
	}
	if len(manifest.Files) != 2 {
		t.Fatalf("manifest files = %+v, want only two changed files", manifest.Files)
	}
	for _, file := range manifest.Files {
		if file.Status == WorkspaceFileUnmodified {
			t.Fatalf("manifest contains unchanged file %+v", file)
		}
	}
	if len(files.Files) <= len(manifest.Files) {
		t.Fatalf("legacy files = %d, manifest files = %d; want unchanged inventory only in legacy response", len(files.Files), len(manifest.Files))
	}
}

func workspaceReviewService(t *testing.T, repo string) *Service {
	t.Helper()
	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	return &Service{store: store}
}

func TestWorkspaceBinaryFingerprintTracksSameSizeRewrites(t *testing.T) {
	for _, kind := range []string{"untracked", "scratch"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if kind == "untracked" {
				root = newWorkspaceRepo(t)
			}
			store := newFakeStore()
			store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: root}}
			if kind == "scratch" {
				store.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
				record := store.sessions["ao-1"]
				record.ProjectID = "scratch"
				store.sessions["ao-1"] = record
			}
			svc := &Service{store: store}
			file := filepath.Join(root, "clip.wav")
			modified := time.Date(2026, time.October, 17, 12, 0, 0, 100_000_000, time.UTC)
			writeAndRead := func(content string, stamp time.Time) WorkspaceFileDetail {
				t.Helper()
				if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(file, stamp, stamp); err != nil {
					t.Fatal(err)
				}
				detail, err := svc.GetWorkspaceFile(context.Background(), "ao-1", "clip.wav", "")
				if err != nil {
					t.Fatal(err)
				}
				return detail
			}
			before := writeAndRead("first\x00clip", modified)
			after := writeAndRead("other\x00clip", modified.Add(time.Millisecond))
			if !before.Binary || !after.Binary || before.Size != after.Size || before.Status != after.Status {
				t.Fatalf("expected same-size binary rewrite: before=%+v after=%+v", before, after)
			}
			if before.FileFingerprint == after.FileFingerprint {
				t.Fatal("same-size binary rewrite kept the old fingerprint")
			}
			unchanged, err := svc.GetWorkspaceFile(context.Background(), "ao-1", "clip.wav", "")
			if err != nil {
				t.Fatal(err)
			}
			if unchanged.FileFingerprint != after.FileFingerprint || unchanged.WorkspaceVersion != after.WorkspaceVersion {
				t.Fatal("unchanged refetch changed the media version")
			}
		})
	}
}

func TestWorkspaceFileRevisionUsesSectionSpecificSides(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged addition\n")
	runGit(t, repo, "add", "README.md")
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged addition\nunstaged addition\n")
	svc := workspaceReviewService(t, repo)

	tests := []struct {
		name    string
		scope   WorkspaceDiffScope
		side    WorkspaceFileBlobSide
		want    string
		notWant string
	}{
		{name: "staged before is HEAD", scope: WorkspaceDiffStaged, side: WorkspaceBlobBefore, want: "hello\n", notWant: "staged addition"},
		{name: "staged after is index", scope: WorkspaceDiffStaged, side: WorkspaceBlobAfter, want: "staged addition", notWant: "unstaged addition"},
		{name: "unstaged before is index", scope: WorkspaceDiffUnstaged, side: WorkspaceBlobBefore, want: "staged addition", notWant: "unstaged addition"},
		{name: "unstaged after is worktree", scope: WorkspaceDiffUnstaged, side: WorkspaceBlobAfter, want: "unstaged addition", notWant: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := svc.GetWorkspaceFileRevision(context.Background(), "ao-1", "README.md", tc.scope, tc.side, "", "")
			if err != nil {
				t.Fatal(err)
			}
			if !got.Exists || got.Binary || got.Truncated || got.Revision == "" {
				t.Fatalf("revision metadata = %#v", got)
			}
			if !strings.Contains(got.Content, tc.want) || (tc.notWant != "" && strings.Contains(got.Content, tc.notWant)) {
				t.Fatalf("content = %q, want %q and not %q", got.Content, tc.want, tc.notWant)
			}
		})
	}
}

func TestWorkspaceFileRevisionRepresentsAbsentUntrackedBeforeSide(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "notes.txt", "new\n")
	svc := workspaceReviewService(t, repo)

	before, err := svc.GetWorkspaceFileRevision(context.Background(), "ao-1", "notes.txt", WorkspaceDiffUntracked, WorkspaceBlobBefore, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if before.Exists || before.Content != "" || before.Revision != "" {
		t.Fatalf("before = %#v, want explicit absent side", before)
	}
	after, err := svc.GetWorkspaceFileRevision(context.Background(), "ao-1", "notes.txt", WorkspaceDiffUntracked, WorkspaceBlobAfter, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !after.Exists || after.Content != "new\n" {
		t.Fatalf("after = %#v", after)
	}
}

func TestGetWorkspaceDiffsBatchesPathsAndHonorsWhitespace(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "goodbye\n")
	writeWorkspaceFile(t, repo, "src/app.go", "package main\n\nfunc main() {}\n")
	svc := workspaceReviewService(t, repo)
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetWorkspaceDiffs(context.Background(), "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffCombined, Paths: []string{"README.md", "src/app.go"}, ContextLines: 3,
		IgnoreWhitespace: true, WorkspaceVersion: files.WorkspaceVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 1 || len(got.Groups[0].IncludedPaths) != 2 {
		t.Fatalf("groups = %#v", got.Groups)
	}
	if !strings.Contains(got.Groups[0].Patch, "README.md") || !strings.Contains(got.Groups[0].Patch, "src/app.go") {
		t.Fatalf("patch did not contain both files:\n%s", got.Groups[0].Patch)
	}
	if got.WorkspaceVersion != files.WorkspaceVersion {
		t.Fatalf("workspace version = %q, want %q", got.WorkspaceVersion, files.WorkspaceVersion)
	}
}

func TestCommitReviewUsesOnlyTheSelectedCommit(t *testing.T) {
	repo := newWorkspaceRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	runGit(t, repo, "switch", "-c", "ao/commit-review")
	writeWorkspaceFile(t, repo, "README.md", "first commit\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "first change")
	first := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	writeWorkspaceFile(t, repo, "README.md", "second commit\n")
	runGit(t, repo, "add", "README.md")
	runGit(t, repo, "commit", "-m", "second change")

	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{Branch: "ao/commit-review", WorkspacePath: repo, DiffBaseSHA: base, DiffBaseRef: "main"}}
	svc := &Service{store: store}
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Commits) != 2 || files.Commits[0].Subject != "second change" || files.Commits[1].Subject != "first change" {
		t.Fatalf("commits = %+v, want newest first", files.Commits)
	}
	history, err := svc.GetWorkspaceHistory(context.Background(), "ao-1", WorkspaceView{})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Commits) != 2 || history.Commits[0].Subject != "second change" || history.Commits[1].Subject != "first change" {
		t.Fatalf("history commits = %+v, want newest first", history.Commits)
	}
	head := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	if view := history.View; view.Kind != WorkspaceViewCheckedOut || view.Name != "ao/commit-review" || view.TipSHA != head || view.BaseSHA != base || view.RangeMode != WorkspaceRangeSinceBase || history.RangeSummary != nil {
		t.Fatalf("history view = %+v, range summary = %+v; want the checked-out base..HEAD range and no range summary", view, history.RangeSummary)
	}

	diffs, err := svc.GetWorkspaceDiffs(context.Background(), "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffCommitted, CommitSHA: first, Paths: []string{"README.md"}, ContextLines: 3, WorkspaceVersion: files.WorkspaceVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs.Groups) != 1 || !strings.Contains(diffs.Groups[0].Patch, "+first commit") || strings.Contains(diffs.Groups[0].Patch, "+second commit") {
		t.Fatalf("selected commit patch = %q", diffs.Groups[0].Patch)
	}

	detail, err := svc.GetWorkspaceFileAtCommit(context.Background(), "ao-1", "README.md", first)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Content != "first commit\n" || !strings.Contains(detail.Diff, "+first commit") || detail.Editable {
		t.Fatalf("selected commit detail = %#v", detail)
	}
	after, err := svc.GetWorkspaceSelectionFileRevision(context.Background(), "ao-1", WorkspaceSelectionRevisionInput{Path: "README.md", Side: WorkspaceBlobAfter, Scope: WorkspaceDiffCommitted, CommitSHA: first, WorkspaceVersion: files.WorkspaceVersion})
	if err != nil {
		t.Fatal(err)
	}
	if after.Content != "first commit\n" {
		t.Fatalf("selected commit after = %q", after.Content)
	}
}

func TestWorkspaceCommitListStopsAtTheCap(t *testing.T) {
	repo := newWorkspaceRepo(t)
	base := strings.TrimSpace(runGit(t, repo, "rev-parse", "HEAD"))
	fixture := make([]fixtureCommit, maxCommitLogCommits+1)
	for i := range fixture {
		fixture[i] = fixtureCommit{message: fmt.Sprintf("change %d", i), files: map[string]string{fmt.Sprintf("changes/%03d.txt", i): "change\n"}}
	}
	commits := importCommits(t, repo, "ao/commit-cap", base, fixture)
	runGit(t, repo, "switch", "ao/commit-cap")

	store := newFakeStore()
	store.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{Branch: "ao/commit-cap", WorkspacePath: repo, DiffBaseSHA: base, DiffBaseRef: "main"}}
	files, err := (&Service{store: store}).ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(files.Commits) != maxCommitLogCommits || !files.CommitsTruncated || files.Commits[0].SHA != commits[len(commits)-1] {
		t.Fatalf("listed %d commits, truncated=%v; want the newest %d and truncated", len(files.Commits), files.CommitsTruncated, maxCommitLogCommits)
	}
}

func TestCommitReviewRejectsCommitOutsideComparison(t *testing.T) {
	repo := newWorkspaceRepo(t)
	svc := workspaceReviewService(t, repo)
	_, err := svc.GetWorkspaceDiffs(context.Background(), "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffCommitted, CommitSHA: strings.Repeat("a", 40), Paths: []string{"README.md"}, ContextLines: 3,
	})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "WORKSPACE_COMMIT_NOT_FOUND" {
		t.Fatalf("error = %#v, want WORKSPACE_COMMIT_NOT_FOUND", err)
	}
}

func TestGetWorkspaceDiffsRejectsStaleWorkspaceVersion(t *testing.T) {
	repo := newWorkspaceRepo(t)
	svc := workspaceReviewService(t, repo)
	_, err := svc.GetWorkspaceDiffs(context.Background(), "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffCombined, Paths: []string{"README.md"}, ContextLines: 3, WorkspaceVersion: "stale",
	})
	var apiError *apierr.Error
	if !strings.Contains(err.Error(), "Workspace changed") || !errors.As(err, &apiError) || apiError.Code != "WORKSPACE_SNAPSHOT_STALE" {
		t.Fatalf("error = %#v, want WORKSPACE_SNAPSHOT_STALE", err)
	}
}

func TestWorkspaceFileRevisionRejectsStaleWorkspaceVersion(t *testing.T) {
	repo := newWorkspaceRepo(t)
	svc := workspaceReviewService(t, repo)

	_, err := svc.GetWorkspaceFileRevision(context.Background(), "ao-1", "README.md", WorkspaceDiffCombined, WorkspaceBlobAfter, "stale", "")
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "WORKSPACE_SNAPSHOT_STALE" {
		t.Fatalf("error = %#v, want WORKSPACE_SNAPSHOT_STALE", err)
	}
}

func TestCappedWorkspaceOutputRetainsBoundedPrefix(t *testing.T) {
	writer := &cappedWorkspaceOutput{limit: 5}
	if written, err := writer.Write([]byte("abcdefgh")); err != nil || written != 8 {
		t.Fatalf("Write = %d, %v", written, err)
	}
	if got := writer.buffer.String(); got != "abcde" || !writer.truncated {
		t.Fatalf("output = %q truncated=%v", got, writer.truncated)
	}
}

func TestSearchWorkspaceFilesReturnsPaginatedPathMatches(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "docs/alpha.md", "a\n")
	writeWorkspaceFile(t, repo, "docs/alphabet.md", "b\n")
	writeWorkspaceFile(t, repo, "docs/beta.md", "c\n")
	svc := workspaceReviewService(t, repo)

	first, err := svc.SearchWorkspaceFiles(context.Background(), "ao-1", "ALPHA", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Results) != 1 || first.NextCursor == "" || !strings.Contains(first.Results[0].Path, "alpha") {
		t.Fatalf("first page = %#v", first)
	}
	second, err := svc.SearchWorkspaceFiles(context.Background(), "ao-1", "alpha", first.NextCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Results) != 1 || second.Results[0].Path == first.Results[0].Path || second.NextCursor != "" {
		t.Fatalf("second page = %#v", second)
	}
}

func workspaceChildRepo(t *testing.T, root, name, content string) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "init")
	runGit(t, dir, "config", "user.email", "ao@example.com")
	runGit(t, dir, "config", "user.name", "AO Tests")
	writeWorkspaceFile(t, dir, "service.go", content)
	runGit(t, dir, "add", ".")
	runGit(t, dir, "commit", "-m", "initial "+name)
	return dir
}

// A workspace project leaves Sections and Commits zero-valued, so its review
// pane can only request the combined scope. A file an agent just created is
// untracked, and git diff never reports one — the group came back empty and the
// Files inspector sat on "Loading diff..." against successful responses.
func TestGetWorkspaceDiffsIncludesUntrackedChildRepoFile(t *testing.T) {
	root := newWorkspaceRepo(t)
	rootBase := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	alpha := workspaceChildRepo(t, root, "alpha", "package alpha\n")
	alphaBase := strings.TrimSpace(runGit(t, alpha, "rev-parse", "HEAD"))
	beta := workspaceChildRepo(t, root, "beta", "package beta\n")
	betaBase := strings.TrimSpace(runGit(t, beta, "rev-parse", "HEAD"))
	writeWorkspaceFile(t, alpha, "workspace-test.txt", "alpha workspace test\n")
	writeWorkspaceFile(t, beta, "service.go", "package beta\n\nfunc Added() {}\n")

	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "ws-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: root, BaseSHA: rootBase},
		{SessionID: "ws-1", RepoName: "alpha", WorktreePath: alpha, BaseSHA: alphaBase},
		{SessionID: "ws-1", RepoName: "beta", WorktreePath: beta, BaseSHA: betaBase},
	}
	svc := &Service{store: st}
	files, err := svc.ListWorkspaceFiles(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetWorkspaceDiffs(context.Background(), "ws-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffCombined, Paths: []string{"alpha/workspace-test.txt", "beta/service.go"},
		ContextLines: 3, WorkspaceVersion: files.WorkspaceVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	patches := map[string]string{}
	for _, group := range got.Groups {
		if len(group.Errors) > 0 || len(group.Deferred) > 0 {
			t.Fatalf("group %q = errors:%#v deferred:%#v", group.Repository, group.Errors, group.Deferred)
		}
		patches[group.Repository] = group.Patch
	}
	// The renderer keys its diffs by "<repository>/<path>", so an empty patch
	// leaves the file with nothing to render at all.
	if !strings.Contains(patches["alpha"], "+++ b/workspace-test.txt") || !strings.Contains(patches["alpha"], "+alpha workspace test") {
		t.Fatalf("untracked child repo patch = %q", patches["alpha"])
	}
	if !strings.Contains(patches["beta"], "+func Added() {}") {
		t.Fatalf("tracked child repo patch = %q", patches["beta"])
	}
}

func TestGetWorkspaceDiffsCombinesUntrackedAndTrackedPathsInOneRepo(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "goodbye\n")
	writeWorkspaceFile(t, repo, "notes.txt", "new notes\n")
	svc := workspaceReviewService(t, repo)
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}

	got, err := svc.GetWorkspaceDiffs(context.Background(), "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffCombined, Paths: []string{"README.md", "notes.txt"}, ContextLines: 3,
		WorkspaceVersion: files.WorkspaceVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Groups) != 1 {
		t.Fatalf("groups = %#v", got.Groups)
	}
	patch := got.Groups[0].Patch
	if !strings.Contains(patch, "+goodbye") || !strings.Contains(patch, "+new notes") {
		t.Fatalf("patch did not contain both the tracked and untracked change:\n%s", patch)
	}
}

func workspaceSummaryPaths(files []WorkspaceFileSummary) []string {
	paths := make([]string, 0, len(files))
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	return paths
}

func TestUncommittedScopeDiffsHeadToWorktreeIncludingUntracked(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged line\n")
	runGit(t, repo, "add", "README.md")
	writeWorkspaceFile(t, repo, "src/app.go", "package main\n\n// unstaged line\n")
	writeWorkspaceFile(t, repo, "notes.txt", "new notes\n")
	svc := workspaceReviewService(t, repo)
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := workspaceSummaryPaths(files.Sections.Uncommitted); strings.Join(got, ",") != "README.md,notes.txt,src/app.go" {
		t.Fatalf("uncommitted section = %v, want the staged, untracked and unstaged files", got)
	}

	diffs, err := svc.GetWorkspaceDiffs(context.Background(), "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffUncommitted, Paths: []string{"README.md", "src/app.go", "notes.txt"}, ContextLines: 3,
		WorkspaceVersion: files.WorkspaceVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs.Groups) != 1 || len(diffs.Groups[0].Errors) > 0 {
		t.Fatalf("groups = %#v", diffs.Groups)
	}
	patch := diffs.Groups[0].Patch
	for _, want := range []string{"+staged line", "+// unstaged line", "+new notes"} {
		if !strings.Contains(patch, want) {
			t.Fatalf("uncommitted patch is missing %q:\n%s", want, patch)
		}
	}

	detail, err := svc.GetWorkspaceFile(context.Background(), "ao-1", "README.md", WorkspaceFileSectionUncommitted)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(detail.Diff, "+staged line") {
		t.Fatalf("uncommitted file diff = %q, want the staged change against HEAD", detail.Diff)
	}
}

func TestUncommittedSectionCountsPartiallyStagedFileOnce(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged line\n")
	runGit(t, repo, "add", "README.md")
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged line\nunstaged line\n")
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	svc := NewWithDeps(Deps{Store: st})

	manifest, err := svc.GetWorkspaceManifest(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Sections.Staged) != 1 || len(manifest.Sections.Unstaged) != 1 {
		t.Fatalf("staged = %+v, unstaged = %+v; want README.md in both", manifest.Sections.Staged, manifest.Sections.Unstaged)
	}
	uncommitted := manifest.Sections.Uncommitted
	if len(uncommitted) != 1 || uncommitted[0].Path != "README.md" || uncommitted[0].Status != WorkspaceFileModified || uncommitted[0].Additions != 2 {
		t.Fatalf("uncommitted = %+v, want README.md once with both added lines", uncommitted)
	}
	if uncommitted[0].FileFingerprint == "" || !uncommitted[0].Editable {
		t.Fatalf("uncommitted row was not finalized: %+v", uncommitted[0])
	}
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.WorkspaceVersion != files.WorkspaceVersion {
		t.Fatalf("manifest version = %q, files version = %q", manifest.WorkspaceVersion, files.WorkspaceVersion)
	}

	// Two snapshots that differ only in the Uncommitted section must not share
	// a version, or a fenced diff request could read a stale patch.
	before := finalizeWorkspaceManifest(WorkspaceManifest{SessionID: "ao-1", Sections: WorkspaceFileSections{Uncommitted: []WorkspaceFileSummary{{Path: "README.md", Status: WorkspaceFileModified, Additions: 1}}}})
	after := finalizeWorkspaceManifest(WorkspaceManifest{SessionID: "ao-1", Sections: WorkspaceFileSections{Uncommitted: []WorkspaceFileSummary{{Path: "README.md", Status: WorkspaceFileModified, Additions: 2}}}})
	if before.WorkspaceVersion == after.WorkspaceVersion {
		t.Fatal("manifest version ignored a change only to the uncommitted section")
	}
	beforeFiles := finalizeWorkspaceFiles(WorkspaceFiles{SessionID: "ao-1", Sections: WorkspaceFileSections{Uncommitted: []WorkspaceFileSummary{{Path: "README.md", Status: WorkspaceFileModified, Additions: 1}}}})
	afterFiles := finalizeWorkspaceFiles(WorkspaceFiles{SessionID: "ao-1", Sections: WorkspaceFileSections{Uncommitted: []WorkspaceFileSummary{{Path: "README.md", Status: WorkspaceFileModified, Additions: 2}}}})
	if beforeFiles.WorkspaceVersion == afterFiles.WorkspaceVersion {
		t.Fatal("files version ignored a change only to the uncommitted section")
	}
}

func TestUncommittedSectionOnUnbornHead(t *testing.T) {
	repo := t.TempDir()
	runGit(t, repo, "init")
	writeWorkspaceFile(t, repo, "staged.txt", "one\n")
	runGit(t, repo, "add", "staged.txt")
	writeWorkspaceFile(t, repo, "staged.txt", "one\ntwo\n")
	writeWorkspaceFile(t, repo, "notes.txt", "new\n")
	svc := workspaceReviewService(t, repo)

	manifest, err := svc.GetWorkspaceManifest(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if got := workspaceSummaryPaths(manifest.Sections.Uncommitted); strings.Join(got, ",") != "notes.txt,staged.txt" {
		t.Fatalf("uncommitted = %v, want the staged new file and the untracked file", got)
	}
	for _, file := range manifest.Sections.Uncommitted {
		if file.Status != WorkspaceFileAdded {
			t.Fatalf("uncommitted %s status = %q, want added against the empty tree", file.Path, file.Status)
		}
		if file.Path == "staged.txt" && file.Additions != 2 {
			t.Fatalf("staged.txt additions = %d, want 2 including the unstaged worktree edit", file.Additions)
		}
	}

	diffs, err := svc.GetWorkspaceDiffs(context.Background(), "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffUncommitted, Paths: []string{"staged.txt"}, ContextLines: 3, WorkspaceVersion: manifest.WorkspaceVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs.Groups) != 1 || !strings.Contains(diffs.Groups[0].Patch, "+one") || !strings.Contains(diffs.Groups[0].Patch, "+two") {
		t.Fatalf("unborn HEAD uncommitted diffs = %+v, want the whole worktree file added", diffs)
	}
}

func TestUnstagedScopeIncludesUntrackedFilesAsAdditions(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "src/app.go", "package main\n\n// unstaged line\n")
	writeWorkspaceFile(t, repo, "notes.txt", "new notes\n")
	svc := workspaceReviewService(t, repo)
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}

	diffs, err := svc.GetWorkspaceDiffs(context.Background(), "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffUnstaged, Paths: []string{"src/app.go", "notes.txt"}, ContextLines: 3,
		WorkspaceVersion: files.WorkspaceVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(diffs.Groups) != 1 {
		t.Fatalf("groups = %#v", diffs.Groups)
	}
	patch := diffs.Groups[0].Patch
	if !strings.Contains(patch, "+// unstaged line") || !strings.Contains(patch, "+++ b/notes.txt") || !strings.Contains(patch, "+new notes") {
		t.Fatalf("unstaged patch did not contain the tracked edit and the untracked addition:\n%s", patch)
	}

	before, err := svc.GetWorkspaceFileRevision(context.Background(), "ao-1", "notes.txt", WorkspaceDiffUnstaged, WorkspaceBlobBefore, files.WorkspaceVersion, "")
	if err != nil {
		t.Fatal(err)
	}
	if before.Exists {
		t.Fatalf("unstaged before side of an untracked file = %#v, want absent", before)
	}
}

func TestUncommittedRevisionSides(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged line\n")
	runGit(t, repo, "add", "README.md")
	writeWorkspaceFile(t, repo, "README.md", "hello\nstaged line\nunstaged line\n")
	writeWorkspaceFile(t, repo, "notes.txt", "new notes\n")
	svc := workspaceReviewService(t, repo)
	files, err := svc.ListWorkspaceFiles(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		path   string
		side   WorkspaceFileBlobSide
		exists bool
		want   string
	}{
		{name: "before is HEAD", path: "README.md", side: WorkspaceBlobBefore, exists: true, want: "hello\n"},
		{name: "after is worktree", path: "README.md", side: WorkspaceBlobAfter, exists: true, want: "hello\nstaged line\nunstaged line\n"},
		{name: "untracked has no before", path: "notes.txt", side: WorkspaceBlobBefore},
		{name: "untracked after is worktree", path: "notes.txt", side: WorkspaceBlobAfter, exists: true, want: "new notes\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// The versioned read takes the Git status path; the unversioned after
			// side takes the fast worktree path. Both must agree.
			for _, version := range []string{files.WorkspaceVersion, ""} {
				got, err := svc.GetWorkspaceFileRevision(context.Background(), "ao-1", tc.path, WorkspaceDiffUncommitted, tc.side, version, "")
				if err != nil {
					t.Fatal(err)
				}
				if got.Exists != tc.exists || got.Content != tc.want {
					t.Fatalf("version %q: revision = exists %v content %q, want exists %v content %q", version, got.Exists, got.Content, tc.exists, tc.want)
				}
			}
		})
	}
}

func TestWorkspaceProjectManifestListsUncommittedPerRepository(t *testing.T) {
	root := newWorkspaceRepo(t)
	rootBase := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	alpha := workspaceChildRepo(t, root, "alpha", "package alpha\n")
	alphaBase := strings.TrimSpace(runGit(t, alpha, "rev-parse", "HEAD"))
	beta := workspaceChildRepo(t, root, "beta", "package beta\n")
	betaBase := strings.TrimSpace(runGit(t, beta, "rev-parse", "HEAD"))
	writeWorkspaceFile(t, alpha, "workspace-test.txt", "alpha workspace test\n")
	writeWorkspaceFile(t, beta, "service.go", "package beta\n\nfunc Added() {}\n")
	writeWorkspaceFile(t, root, "README.md", "root change\n")

	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "ws-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: root, BaseSHA: rootBase},
		{SessionID: "ws-1", RepoName: "alpha", WorktreePath: alpha, BaseSHA: alphaBase},
		{SessionID: "ws-1", RepoName: "beta", WorktreePath: beta, BaseSHA: betaBase},
	}
	svc := NewWithDeps(Deps{Store: st})

	manifest, err := svc.GetWorkspaceManifest(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Degraded {
		t.Fatalf("manifest degraded: %s", manifest.DegradedCode)
	}
	if got := workspaceSummaryPaths(manifest.Sections.Uncommitted); strings.Join(got, ",") != "README.md,alpha/workspace-test.txt,beta/service.go" {
		t.Fatalf("uncommitted = %v, want each repository's changes under its prefix", got)
	}
	if len(manifest.Sections.Staged) != 0 || len(manifest.Sections.Unstaged) != 0 || len(manifest.Sections.Committed) != 0 {
		t.Fatalf("sections = %+v, want only uncommitted for a workspace project", manifest.Sections)
	}
	files, err := svc.ListWorkspaceFiles(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.WorkspaceVersion != files.WorkspaceVersion {
		t.Fatalf("manifest version = %q, files version = %q", manifest.WorkspaceVersion, files.WorkspaceVersion)
	}

	diffs, err := svc.GetWorkspaceDiffs(context.Background(), "ws-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffUncommitted, Paths: []string{"alpha/workspace-test.txt", "beta/service.go"}, ContextLines: 3,
		WorkspaceVersion: manifest.WorkspaceVersion,
	})
	if err != nil {
		t.Fatal(err)
	}
	patches := map[string]string{}
	for _, group := range diffs.Groups {
		patches[group.Repository] = group.Patch
	}
	if !strings.Contains(patches["alpha"], "+alpha workspace test") || !strings.Contains(patches["beta"], "+func Added() {}") {
		t.Fatalf("uncommitted workspace project patches = %#v", patches)
	}
}
