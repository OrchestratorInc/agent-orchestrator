package session

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func listedPaths(t *testing.T, svc *Service, id domain.SessionID) ([]string, map[string]WorkspaceFileSummary, bool) {
	t.Helper()
	files, err := svc.ListWorkspaceFiles(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	byPath := map[string]WorkspaceFileSummary{}
	for _, file := range files.Files {
		byPath[file.Path] = file
		if file.Status != WorkspaceFileDeleted {
			paths = append(paths, file.Path)
		}
	}
	slices.Sort(paths)
	return paths, byPath, files.Truncated
}

func TestListWorkspaceFilePathsMatchesFullListWithoutDeletions(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "README.md", "changed\n")
	writeWorkspaceFile(t, repo, "notes/new.txt", "untracked\n")
	writeWorkspaceFile(t, repo, "node_modules/pkg/index.js", "ignored\n")
	runGit(t, repo, "rm", "-q", "src/app.go")
	svc := workspaceReviewService(t, repo)

	got, err := svc.ListWorkspaceFilePaths(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	want, _, truncated := listedPaths(t, svc, "ao-1")
	if !slices.Equal(got.Paths, want) || got.Truncated != truncated {
		t.Fatalf("paths = %v truncated=%v, want %v truncated=%v", got.Paths, got.Truncated, want, truncated)
	}
	if !slices.Contains(got.Paths, "notes/new.txt") || slices.Contains(got.Paths, "src/app.go") || slices.Contains(got.Paths, "node_modules/pkg/index.js") {
		t.Fatalf("paths = %v, want the untracked file and neither the deleted nor the ignored one", got.Paths)
	}
}

func TestListWorkspaceFilePathsListsPastTheFileBrowserCap(t *testing.T) {
	repo := newWorkspaceRepo(t)
	for i := 0; i < maxWorkspaceFiles+20; i++ {
		writeWorkspaceFile(t, repo, fmt.Sprintf("many/file-%05d.txt", i), "x\n")
	}
	writeWorkspaceFile(t, repo, "zz/late.txt", "x\n")
	svc := workspaceReviewService(t, repo)

	got, err := svc.ListWorkspaceFilePaths(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Truncated || len(got.Paths) != maxWorkspaceFiles+24 || !slices.Contains(got.Paths, "zz/late.txt") {
		t.Fatalf("truncated=%v len=%d, want every visible file", got.Truncated, len(got.Paths))
	}
	search, err := svc.SearchWorkspaceFiles(context.Background(), "ao-1", "zz/late", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.Truncated {
		t.Fatalf("search = %#v, want the file past the file browser cap", search)
	}
}

func TestWorkspaceSourcePathsKeepsChangedFilesWhenCapped(t *testing.T) {
	repo := newWorkspaceRepo(t)
	for i := 0; i < 20; i++ {
		writeWorkspaceFile(t, repo, fmt.Sprintf("many/file-%02d.txt", i), "x\n")
	}
	writeWorkspaceFile(t, repo, "zz/late.txt", "before\n")
	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-q", "-m", "many")
	writeWorkspaceFile(t, repo, "zz/late.txt", "after\n")
	svc := workspaceReviewService(t, repo)
	_, sources, _, err := svc.workspacePathSources(context.Background(), "ao-1")
	if err != nil {
		t.Fatal(err)
	}

	paths, truncated, err := svc.workspaceSourcePaths(context.Background(), "ao-1", sources, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !truncated || len(paths) != 10 || !slices.Contains(paths, "zz/late.txt") {
		t.Fatalf("paths = %v truncated=%v, want 10 paths that keep the changed file", paths, truncated)
	}
}

func TestListWorkspaceFilePathsPrefixesWorkspaceChildRepos(t *testing.T) {
	root := newWorkspaceRepo(t)
	rootBase := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	alpha := workspaceChildRepo(t, root, "alpha", "package alpha\n")
	alphaBase := strings.TrimSpace(runGit(t, alpha, "rev-parse", "HEAD"))
	writeWorkspaceFile(t, alpha, "service.go", "package alpha\n\nfunc Changed() {}\n")
	writeWorkspaceFile(t, alpha, "added.txt", "new\n")
	st := newFakeStore()
	st.projects["ws"] = domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.sessions["ws-1"] = domain.SessionRecord{ID: "ws-1", ProjectID: "ws", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	st.worktrees["ws-1"] = []domain.SessionWorktreeRecord{
		{SessionID: "ws-1", RepoName: domain.RootWorkspaceRepoName, WorktreePath: root, BaseSHA: rootBase},
		{SessionID: "ws-1", RepoName: "alpha", WorktreePath: alpha, BaseSHA: alphaBase},
	}
	svc := &Service{store: st}

	got, err := svc.ListWorkspaceFilePaths(context.Background(), "ws-1")
	if err != nil {
		t.Fatal(err)
	}
	want, byPath, _ := listedPaths(t, svc, "ws-1")
	if !slices.Equal(got.Paths, want) || !slices.Contains(got.Paths, "alpha/service.go") || !slices.Contains(got.Paths, "README.md") {
		t.Fatalf("paths = %v, want %v", got.Paths, want)
	}

	search, err := svc.SearchWorkspaceFiles(context.Background(), "ws-1", "alpha/", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 2 {
		t.Fatalf("search results = %#v, want both alpha files", search.Results)
	}
	for _, result := range search.Results {
		full := byPath[result.Path]
		if result.Status != full.Status || result.Size != full.Size || result.Binary != full.Binary || result.FileFingerprint != full.FileFingerprint {
			t.Fatalf("search result %#v differs from the full list row %#v", result, full)
		}
	}
}

func TestSearchWorkspaceFilesMatchesFullListDetails(t *testing.T) {
	repo := newWorkspaceRepo(t)
	writeWorkspaceFile(t, repo, "src/app.go", "package main\n\nfunc main() {}\n")
	writeWorkspaceFile(t, repo, "src/image.bin", "\x00\x01\x02")
	writeWorkspaceFile(t, repo, "src/new.go", "package main\n")
	svc := workspaceReviewService(t, repo)

	search, err := svc.SearchWorkspaceFiles(context.Background(), "ao-1", "src/", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	_, byPath, _ := listedPaths(t, svc, "ao-1")
	if len(search.Results) != 3 {
		t.Fatalf("search results = %#v, want three src files", search.Results)
	}
	for _, result := range search.Results {
		full := byPath[result.Path]
		if result.Status != full.Status || result.Size != full.Size || result.Binary != full.Binary || result.FileFingerprint != full.FileFingerprint {
			t.Fatalf("search result %#v differs from the full list row %#v", result, full)
		}
	}
}

func TestListWorkspaceFilePathsScratch(t *testing.T) {
	root := t.TempDir()
	writeWorkspaceFile(t, root, "a.txt", "a\n")
	writeWorkspaceFile(t, root, "nested/b.txt", "b\n")
	st := newFakeStore()
	st.projects["scratch"] = domain.ProjectRecord{ID: "scratch", Kind: domain.ProjectKindScratch}
	st.sessions["scratch-1"] = domain.SessionRecord{ID: "scratch-1", ProjectID: "scratch", Metadata: domain.SessionMetadata{WorkspacePath: root}}
	svc := &Service{store: st}

	got, err := svc.ListWorkspaceFilePaths(context.Background(), "scratch-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"a.txt", "nested/b.txt"}; !slices.Equal(got.Paths, want) {
		t.Fatalf("paths = %v, want %v", got.Paths, want)
	}
	search, err := svc.SearchWorkspaceFiles(context.Background(), "scratch-1", "b.txt", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Results) != 1 || search.Results[0].Path != "nested/b.txt" || search.Results[0].FileFingerprint == "" {
		t.Fatalf("scratch search = %#v", search.Results)
	}
}
