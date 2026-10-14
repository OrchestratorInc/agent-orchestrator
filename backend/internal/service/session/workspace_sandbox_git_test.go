package session

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// plantNestedWorkspaceRepo does what a sandboxed agent can do inside its
// worktree: commit a nested repository as a gitlink whose own config carries
// a clean filter and an fsmonitor command, then leave a modified file in it.
// It returns the marker path those commands append to.
func plantNestedWorkspaceRepo(t *testing.T, repo string) string {
	t.Helper()
	marker := filepath.Join(t.TempDir(), "marker")
	sub := filepath.Join(repo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, sub, "init")
	runGit(t, sub, "config", "user.email", "agent@example.com")
	runGit(t, sub, "config", "user.name", "Agent")
	writeWorkspaceFile(t, sub, "f.txt", "one\n")
	writeWorkspaceFile(t, sub, ".gitattributes", "*.txt filter=evil\n")
	runGit(t, sub, "add", "-A")
	runGit(t, sub, "commit", "-m", "nested")
	runGit(t, sub, "config", "filter.evil.clean", "sh -c 'echo clean >> \""+marker+"\"; cat'")
	runGit(t, sub, "config", "core.fsmonitor", "sh -c 'echo fsmonitor >> \""+marker+"\"; exit 1' --")
	runGit(t, repo, "add", "sub")
	runGit(t, repo, "commit", "-m", "add nested repo")
	writeWorkspaceFile(t, sub, "f.txt", "two\n")
	writeWorkspaceFile(t, repo, "README.md", "hello\nchanged\n")
	return marker
}

func nestedRepoService(t *testing.T, mode domain.SandboxMode) (*Service, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("planted filter commands use a POSIX shell")
	}
	repo := newWorkspaceRepo(t)
	marker := plantNestedWorkspaceRepo(t, repo)
	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Path: repo, Config: domain.ProjectConfig{Sandbox: domain.SandboxConfig{Mode: mode, Image: "node:22"}}}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "proj", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	return NewWithDeps(Deps{Store: st}), marker
}

// exerciseChangesPanel runs the reads the changes panel makes when a user
// opens a session: the file list, its status and diff, one file's detail and
// the review diff.
func exerciseChangesPanel(t *testing.T, svc *Service) {
	t.Helper()
	ctx := context.Background()
	files, err := svc.ListWorkspaceFiles(ctx, "ao-1")
	if err != nil {
		t.Fatalf("list files: %v", err)
	}
	if _, err := svc.GetWorkspaceManifest(ctx, "ao-1"); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	if _, err := svc.GetWorkspaceFile(ctx, "ao-1", "README.md", ""); err != nil {
		t.Fatalf("file detail: %v", err)
	}
	if _, err := svc.GetWorkspaceDiffs(ctx, "ao-1", WorkspaceDiffInput{
		Scope: WorkspaceDiffCombined, Paths: []string{"README.md", "sub"}, ContextLines: 3, WorkspaceVersion: files.WorkspaceVersion,
	}); err != nil {
		t.Fatalf("diffs: %v", err)
	}
	if _, err := svc.ListWorkspaceTree(ctx, "ao-1", ""); err != nil {
		t.Fatalf("tree: %v", err)
	}
}

func TestSandboxedChangesPanelDoesNotRunNestedRepoCommands(t *testing.T) {
	for _, mode := range []domain.SandboxMode{domain.SandboxModePerAgent, domain.SandboxModeShared} {
		t.Run(string(mode), func(t *testing.T) {
			svc, marker := nestedRepoService(t, mode)
			exerciseChangesPanel(t, svc)
			if data, err := os.ReadFile(marker); err == nil {
				t.Fatalf("host git ran a command planted in a nested repository: %q", data)
			}
		})
	}
}

// The daemon injects one predicate (sandbox state dir or config) so a
// session that ran sandboxed stays hardened after the config is turned off.
func TestChangesPanelUsesInjectedSandboxPredicate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("planted filter commands use a POSIX shell")
	}
	repo := newWorkspaceRepo(t)
	marker := plantNestedWorkspaceRepo(t, repo)
	st := newFakeStore()
	st.projects["proj"] = domain.ProjectRecord{ID: "proj", Path: repo}
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "proj", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	var asked []domain.SessionID
	svc := NewWithDeps(Deps{Store: st, SandboxedSession: func(_ context.Context, rec domain.SessionRecord) bool {
		asked = append(asked, rec.ID)
		return true
	}})
	exerciseChangesPanel(t, svc)
	if data, err := os.ReadFile(marker); err == nil {
		t.Fatalf("host git ran a command planted in a nested repository: %q", data)
	}
	if len(asked) == 0 {
		t.Fatal("the injected predicate was never consulted")
	}
}

// A session whose project can't be found fails closed, the same as the
// daemon's probe.
func TestChangesPanelMissingProjectIsHardened(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("planted filter commands use a POSIX shell")
	}
	repo := newWorkspaceRepo(t)
	marker := plantNestedWorkspaceRepo(t, repo)
	st := newFakeStore()
	st.sessions["ao-1"] = domain.SessionRecord{ID: "ao-1", ProjectID: "gone", Kind: domain.KindWorker, Metadata: domain.SessionMetadata{WorkspacePath: repo}}
	exerciseChangesPanel(t, NewWithDeps(Deps{Store: st}))
	if data, err := os.ReadFile(marker); err == nil {
		t.Fatalf("host git ran a command planted in a nested repository: %q", data)
	}
}

func TestUnsandboxedChangesPanelKeepsSubmoduleRecursion(t *testing.T) {
	svc, marker := nestedRepoService(t, domain.SandboxModeOff)
	exerciseChangesPanel(t, svc)
	// Host sessions keep plain git, which recurses into the nested repository.
	// That is also what proves the fixture is live for the sandboxed test.
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("fixture inert: unhardened host git did not run the planted filter: %v", err)
	}
}
