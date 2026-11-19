package project_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/project"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func mkdirWithFile(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte(filepath.Base(dir)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v (%s)", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func workspaceRepoNames(t *testing.T, store interface {
	ListWorkspaceRepos(context.Context, string) ([]domain.WorkspaceRepoRecord, error)
}, id string) []string {
	t.Helper()
	repos, err := store.ListWorkspaceRepos(context.Background(), id)
	if err != nil {
		t.Fatalf("list workspace repos: %v", err)
	}
	names := make([]string, 0, len(repos))
	for _, repo := range repos {
		names = append(names, repo.Name)
	}
	return names
}

// A plain parent becomes a repository Orchestrator owns: every non-repository
// folder stays out of it, and only ordinary folders are copied into sessions.
func TestManager_AddWorkspaceCopiesOnlyOrdinaryFoldersFromPlainParent(t *testing.T) {
	configureCommitter(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.New(store)
	parent := t.TempDir()
	gitRepoWithCommit(t, filepath.Join(parent, "app"))
	for _, dir := range []string{"notes", "node_modules", ".venv", "dist"} {
		mkdirWithFile(t, filepath.Join(parent, dir))
	}

	if _, err := m.Add(ctx, project.AddInput{Path: parent, ProjectID: ptr("ws"), AsWorkspace: true}); err != nil {
		t.Fatalf("Add workspace: %v", err)
	}
	if got, want := workspaceRepoNames(t, store, "ws"), []string{"app", "notes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("registered children = %v, want %v", got, want)
	}
	ignored, err := os.ReadFile(filepath.Join(parent, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/app/", "/notes/", "/node_modules/", "/.venv/", "/dist/"} {
		if !strings.Contains(string(ignored), want) {
			t.Fatalf(".gitignore missing %q:\n%s", want, ignored)
		}
	}
	if tracked := gitIn(t, parent, "ls-files"); strings.Contains(tracked, "file.txt") {
		t.Fatalf("root repository committed folder contents:\n%s", tracked)
	}
}

// In the user's own repository, Orchestrator lists only child repositories in
// .gitignore, and never copies tracked, ignored, dependency or dot-folders.
func TestManager_AddWorkspaceAdoptedParentIgnoresOnlyChildRepos(t *testing.T) {
	configureCommitter(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	m := project.New(store)
	parent := t.TempDir()
	gitRepoWithCommit(t, parent)
	mkdirWithFile(t, filepath.Join(parent, "src"))
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("/data/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, parent, "add", "src", ".gitignore")
	gitIn(t, parent, "commit", "-m", "add src and ignore data")
	for _, dir := range []string{"data", "notes", "node_modules", ".venv"} {
		mkdirWithFile(t, filepath.Join(parent, dir))
	}
	gitRepoWithCommit(t, filepath.Join(parent, "api"))

	if _, err := m.Add(ctx, project.AddInput{Path: parent, ProjectID: ptr("ws"), AsWorkspace: true}); err != nil {
		t.Fatalf("Add workspace: %v", err)
	}
	if got, want := workspaceRepoNames(t, store, "ws"), []string{"api", "notes"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("registered children = %v, want %v", got, want)
	}
	ignored, err := os.ReadFile(filepath.Join(parent, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(ignored), "/data/\n/api/\n"; got != want {
		t.Fatalf(".gitignore = %q, want %q", got, want)
	}
	if files := strings.TrimSpace(gitIn(t, parent, "show", "--name-only", "--format=", "HEAD")); files != ".gitignore" {
		t.Fatalf("Orchestrator commit touched %q, want only .gitignore", files)
	}
}

// Rows written by earlier releases are repaired once: folders sessions should
// never receive are dropped, while repositories and the folders Orchestrator
// itself ignored for copying are kept.
func TestService_RepairWorkspaceAssetsDropsFoldersSessionsNeverReceive(t *testing.T) {
	configureCommitter(t)
	t.Setenv("GIT_CEILING_DIRECTORIES", os.TempDir())
	ctx := context.Background()
	store, err := sqlitetest.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	parent := t.TempDir()
	gitRepoWithCommit(t, parent)
	mkdirWithFile(t, filepath.Join(parent, "src"))
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("/data/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, parent, "add", "src", ".gitignore")
	gitIn(t, parent, "commit", "-m", "add src and ignore data")
	// The commit an earlier release made, ignoring every child it registered.
	if err := os.WriteFile(filepath.Join(parent, ".gitignore"), []byte("/data/\n/api/\n/local/\n/notes/\n/src/\n/.venv/\n/node_modules/\nnode_modules/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, parent, "commit", "-m", "chore: configure AO workspace ignores", "--", ".gitignore")
	for _, dir := range []string{"data", "notes", "node_modules", ".venv"} {
		mkdirWithFile(t, filepath.Join(parent, dir))
	}
	gitRepoWithCommit(t, filepath.Join(parent, "api"))
	gitRepoWithCommitNoOrigin(t, filepath.Join(parent, "local"))

	now := time.Now()
	row := domain.ProjectRecord{ID: "ws", Path: parent, DisplayName: "ws", RegisteredAt: now, Kind: domain.ProjectKindWorkspace}
	var repos []domain.WorkspaceRepoRecord
	for _, name := range []string{".venv", "api", "data", "local", "node_modules", "notes", "src"} {
		status := domain.GitStatusNeedsInit
		if name == "api" {
			status = domain.GitStatusReady
		}
		repos = append(repos, domain.WorkspaceRepoRecord{ProjectID: "ws", Name: name, RelativePath: name, RegisteredAt: now, GitStatus: status})
	}
	if err := store.UpsertWorkspaceProject(ctx, row, repos); err != nil {
		t.Fatalf("seed workspace project: %v", err)
	}

	svc := project.New(store)
	want := []string{"api", "local", "notes"}
	for pass := 1; pass <= 2; pass++ {
		svc.RepairWorkspaceAssets(ctx)
		if got := workspaceRepoNames(t, store, "ws"); !reflect.DeepEqual(got, want) {
			t.Fatalf("pass %d: children = %v, want %v", pass, got, want)
		}
	}
}
