package workertransport

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// gitCheckoutAt makes a committed checkout with a review base at path.
func gitCheckoutAt(t *testing.T, path, file string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	gitWorkspace(t, path, "init", "--quiet", "--initial-branch=main")
	gitWorkspace(t, path, "config", "user.name", "AO Test")
	gitWorkspace(t, path, "config", "user.email", "ao@example.test")
	writeWorkspaceFile(t, path, file, "base\n")
	gitWorkspace(t, path, "add", ".")
	gitWorkspace(t, path, "commit", "--quiet", "-m", "base")
	gitWorkspace(t, path, "update-ref", worker.WorkspaceReviewBaseRef, "HEAD")
}

func TestWorkspaceRequestsSelectTheRepositoryCheckout(t *testing.T) {
	root := t.TempDir()
	mainPath, siblingPath := filepath.Join(root, "alice"), filepath.Join(root, "julian")
	gitCheckoutAt(t, mainPath, "MAIN.md")
	gitCheckoutAt(t, siblingPath, "SIBLING.md")
	writeWorkspaceFile(t, siblingPath, "SIBLING.md", "changed\n")
	main, err := openWorkspace(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	supervisor := &Supervisor{
		Workspace: mainPath, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Repositories: []worker.RepoCheckout{
			{Name: "julian", Path: siblingPath, FullName: "acme/julian"},
			{Name: "missing", Path: filepath.Join(root, "missing"), FullName: "acme/missing"},
		},
	}
	defer supervisor.repositories.closeAll()
	dispatch := func(kind string, payload any) *reviewDispatchControl {
		control := &reviewDispatchControl{}
		supervisor.Control = control
		supervisor.handle(context.Background(), main, &worker.TransportRequest{ID: "r", Attempt: 1, Kind: kind, Payload: payload})
		return control
	}

	summary := dispatch("workspace.review.summary", map[string]any{}).completed.(worker.WorkspaceReviewResponse)
	if len(summary.Files) != 1 || summary.Files[0].Path != "MAIN.md" {
		t.Fatalf("main summary files = %+v", summary.Files)
	}
	if len(summary.Repositories) != 3 || !summary.Repositories[0].Main || summary.Repositories[1].Name != "julian" ||
		!summary.Repositories[1].Present || summary.Repositories[2].Present {
		t.Fatalf("summary repositories = %+v", summary.Repositories)
	}
	for _, selector := range []string{"julian", "acme/julian"} {
		sibling := dispatch("workspace.review.summary", map[string]any{"repository": selector}).completed.(worker.WorkspaceReviewResponse)
		if sibling.Summary.Files != 1 || len(sibling.Repositories) != 0 {
			t.Fatalf("sibling summary via %q = %+v", selector, sibling)
		}
		diffs := dispatch("workspace.review.diffs", map[string]any{
			"repository": selector, "scope": "combined", "paths": []string{"SIBLING.md"}, "contextLines": 3,
		}).completed.(worker.WorkspaceReviewDiffsResponse)
		if len(diffs.Groups) != 1 || diffs.Groups[0].Repository != "julian" || diffs.Groups[0].Patch == "" {
			t.Fatalf("sibling diffs = %+v", diffs)
		}
	}
	read := dispatch("workspace.read", map[string]any{"repository": "julian", "path": "SIBLING.md"})
	if read.failureCode != "" {
		t.Fatalf("sibling read failed: %s", read.failureCode)
	}
	if escape := dispatch("workspace.read", map[string]any{"repository": "julian", "path": "../alice/MAIN.md"}); escape.failureCode != "INVALID_WORKSPACE_PATH" {
		t.Fatalf("a sibling request escaped its checkout: %q", escape.failureCode)
	}
	for _, selector := range []string{"missing", "acme/other"} {
		if failed := dispatch("workspace.review.summary", map[string]any{"repository": selector}); failed.failureCode != "WORKSPACE_REPOSITORY_NOT_FOUND" {
			t.Fatalf("selector %q failure = %q", selector, failed.failureCode)
		}
	}
	listed := dispatch("workspace.repositories", nil).completed.(worker.WorkspaceRepositoriesResponse)
	if len(listed.Repositories) != 3 || listed.Repositories[0].Name != "alice" {
		t.Fatalf("listed repositories = %+v", listed)
	}

	single := &Supervisor{Workspace: mainPath, Logger: supervisor.Logger, Control: &reviewDispatchControl{}}
	single.handle(context.Background(), main, &worker.TransportRequest{ID: "r", Attempt: 1, Kind: "workspace.review.summary", Payload: map[string]any{}})
	if repositories := single.Control.(*reviewDispatchControl).completed.(worker.WorkspaceReviewResponse).Repositories; repositories != nil {
		t.Fatalf("a single-repo session listed repositories: %+v", repositories)
	}
}

// Freestyle and Coder check the main repository out into a fixed folder
// ("repository"); the switcher must show the repository's own name.
func TestMainRepositoryIsLabeledWithItsName(t *testing.T) {
	root := t.TempDir()
	mainPath := filepath.Join(root, "repository")
	supervisor := &Supervisor{
		Workspace:      mainPath,
		MainRepository: worker.SessionRepoCheckouts(mainPath, "https://github.com/Acme/Web-App.git", nil)[0],
		Repositories:   []worker.RepoCheckout{{Name: "api", Path: filepath.Join(root, "api"), FullName: "acme/api"}},
	}
	repositories := supervisor.workspaceRepositories()
	if len(repositories) != 2 {
		t.Fatalf("repositories = %+v", repositories)
	}
	main := repositories[0]
	if !main.Main || main.Name != "repository" || main.Label != "Web-App" || main.Repository != "acme/web-app" {
		t.Fatalf("main repository = %+v, want folder repository labeled Web-App (acme/web-app)", main)
	}
	if checkout, _, err := supervisor.workspaceFor(nil, map[string]any{"repository": "acme/web-app"}); err != nil || checkout != nil {
		t.Fatalf("selecting the main repository by owner/name = %v, %v; want the main workspace", checkout, err)
	}
	// Without a known URL the folder name stays the label.
	unknown := (&Supervisor{Workspace: mainPath, Repositories: supervisor.Repositories}).workspaceRepositories()
	if unknown[0].Label != "" || unknown[0].Name != "repository" {
		t.Fatalf("main repository without a URL = %+v", unknown[0])
	}
}
