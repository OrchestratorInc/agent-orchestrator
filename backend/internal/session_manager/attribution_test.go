package sessionmanager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/attribution"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func attributionWorktree(t *testing.T, root, name string) string {
	t.Helper()
	repo := filepath.Join(root, name+"-repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	runManagerGit(t, repo, "init", "-q", "-b", "main")
	runManagerGit(t, repo, "-c", "user.name=Dev", "-c", "user.email=dev@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	runManagerGit(t, repo, "config", "extensions.worktreeConfig", "true")
	wt := filepath.Join(root, "session", name)
	runManagerGit(t, repo, "worktree", "add", "-q", "-b", "orchestrator/"+name+"/root", wt)
	return wt
}

func worktreeConfig(t *testing.T, dir, key string) string {
	t.Helper()
	out, _ := exec.Command("git", "-C", dir, "config", "--worktree", "--get", key).Output()
	return strings.TrimSpace(string(out))
}

func TestApplyCommitAttributionFollowsBetaAndProjectToggle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name      string
		beta      bool
		disable   bool
		wantHooks bool
	}{
		{"beta off changes nothing", false, false, false},
		{"beta on configures trailer", true, false, true},
		{"project turned the trailer off", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			wt := attributionWorktree(t, root, "s-1")
			beta := tc.beta
			m, _ := attributionManager(t, root, &beta)
			project := domain.ProjectRecord{ID: "p", Config: domain.ProjectConfig{Attribution: domain.AttributionConfig{DisableCommitTrailer: tc.disable}}}
			m.applyCommitAttribution(ctx, project, domain.SessionID(filepath.Base(wt)), wt, attributionFromSettings)
			hooks := worktreeConfig(t, wt, "core.hooksPath")
			coAuthor := worktreeConfig(t, wt, attribution.ConfigCoAuthor)
			if tc.wantHooks {
				if filepath.Dir(hooks) != attribution.HooksDir(m.dataDir) || coAuthor != attribution.Bot.CoAuthor() {
					t.Fatalf("hooksPath=%q coauthor=%q", hooks, coAuthor)
				}
				return
			}
			if hooks != "" || coAuthor != "" {
				t.Fatalf("attribution configured while off: hooksPath=%q coauthor=%q", hooks, coAuthor)
			}
			if _, err := os.Stat(attribution.HooksDir(m.dataDir)); !os.IsNotExist(err) {
				t.Fatalf("hooks dir written while off: %v", err)
			}
		})
	}
}

// attributionRepo makes a repository whose linked worktree for branch is
// created at path, the way session workspaces are.
func attributionRepo(t *testing.T, repoDir, path, branch string) {
	t.Helper()
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	runManagerGit(t, repoDir, "init", "-q", "-b", "main")
	runManagerGit(t, repoDir, "-c", "user.name=Dev", "-c", "user.email=dev@example.com", "commit", "-q", "--allow-empty", "-m", "init")
	runManagerGit(t, repoDir, "config", "extensions.worktreeConfig", "true")
	runManagerGit(t, repoDir, "worktree", "add", "-q", "-b", branch, path)
}

func commitMessage(t *testing.T, dir string) string {
	t.Helper()
	cmd := exec.Command("git", "-C", dir, "-c", "user.name=Dev", "-c", "user.email=dev@example.com", "commit", "-q", "--allow-empty", "-m", "feat: work")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("commit in %s: %v\n%s", dir, err, out)
	}
	out, err := exec.Command("git", "-C", dir, "log", "-1", "--format=%B").Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func attributionManager(t *testing.T, root string, beta *bool) (*Manager, *fakeStore) {
	t.Helper()
	m, st, _, _ := newManager()
	m.dataDir = filepath.Join(root, "data")
	m.SetCommitAttributionEnabled(func(context.Context) bool { return *beta })
	return m, st
}

func TestApplyCommitAttributionConfiguresWorkspaceRootAndChildren(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	root := t.TempDir()
	session := filepath.Join(root, "session")
	// A workspace project's root is a worktree of the root repository, and the
	// child repositories' worktrees sit inside it.
	attributionRepo(t, filepath.Join(root, "root-repo"), session, "orchestrator/ws-1/root")
	api := filepath.Join(session, "api")
	web := filepath.Join(session, "web")
	attributionRepo(t, filepath.Join(root, "api-repo"), api, "orchestrator/ws-1/api")
	attributionRepo(t, filepath.Join(root, "web-repo"), web, "orchestrator/ws-1/web")
	// A person's own checkout nested in the tree is not a session worktree.
	own := filepath.Join(session, "own")
	if err := os.MkdirAll(own, 0o755); err != nil {
		t.Fatal(err)
	}
	runManagerGit(t, own, "init", "-q")

	beta := true
	m, st := attributionManager(t, root, &beta)
	project := domain.ProjectRecord{ID: "ws", Kind: domain.ProjectKindWorkspace}
	st.workspaceRepo["ws"] = []domain.WorkspaceRepoRecord{
		{ProjectID: "ws", Name: "api", RelativePath: "api"},
		{ProjectID: "ws", Name: "web", RelativePath: "web"},
		{ProjectID: "ws", Name: "own", RelativePath: "own"},
	}
	if got := m.sessionGitWorktrees(ctx, project, session); len(got) != 3 || got[0] != session || got[1] != api || got[2] != web {
		t.Fatalf("worktrees = %v", got)
	}
	m.applyCommitAttribution(ctx, project, domain.SessionID(filepath.Base(session)), session, attributionFromSettings)
	for _, dir := range []string{session, api, web} {
		if msg := commitMessage(t, dir); !strings.Contains(msg, attribution.Bot.Trailer()) {
			t.Fatalf("commit in %s has no trailer:\n%s", dir, msg)
		}
	}
	if hooks := worktreeConfig(t, own, "core.hooksPath"); hooks != "" {
		t.Fatalf("person's checkout configured: %q", hooks)
	}
}

func TestApplyCommitAttributionUndoesReusedWorktreeWhenOff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		off  func(beta *bool, project *domain.ProjectRecord)
	}{
		{"beta turned off", func(beta *bool, _ *domain.ProjectRecord) { *beta = false }},
		{"project trailer turned off", func(_ *bool, p *domain.ProjectRecord) { p.Config.Attribution.DisableCommitTrailer = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			// The orchestrator worktree lives at a fixed path and is reused by
			// the next orchestrator session.
			wt := filepath.Join(root, "orchestrator", "p-orchestrator")
			attributionRepo(t, filepath.Join(root, "repo"), wt, "orchestrator/p-orchestrator")
			beta := true
			m, st := attributionManager(t, root, &beta)
			project := domain.ProjectRecord{ID: "p"}
			m.applyCommitAttribution(ctx, project, "first", wt, attributionFromSettings)
			if msg := commitMessage(t, wt); !strings.Contains(msg, attribution.Bot.Trailer()) {
				t.Fatalf("first session has no trailer:\n%s", msg)
			}

			tc.off(&beta, &project)
			m.applyCommitAttribution(ctx, project, "next", wt, attributionFromSettings)
			if hooks, co := worktreeConfig(t, wt, "core.hooksPath"), worktreeConfig(t, wt, attribution.ConfigCoAuthor); hooks != "" || co != "" {
				t.Fatalf("reused worktree still configured: hooksPath=%q coauthor=%q", hooks, co)
			}
			if msg := commitMessage(t, wt); strings.Contains(msg, "Co-authored-by") {
				t.Fatalf("next session still adds the trailer:\n%s", msg)
			}
			if got, _ := st.SessionCommitAttribution(ctx, "next"); got != "" {
				t.Fatalf("recorded choice = %q, want none", got)
			}
		})
	}
}

func TestRecreatedWorktreeKeepsTheSessionsChoice(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name        string
		betaAtStart bool
		want        bool
	}{
		{"started without, beta turned on later", false, false},
		{"started with, beta turned off later", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			repoDir := filepath.Join(root, "repo")
			wt := filepath.Join(root, "session", "s-1")
			branch := "orchestrator/s-1/root"
			attributionRepo(t, repoDir, wt, branch)
			// Another session turned attribution on once, so the hooks exist.
			other := filepath.Join(root, "session", "s-0")
			runManagerGit(t, repoDir, "worktree", "add", "-q", "-b", "orchestrator/s-0/root", other)
			beta := true
			m, _ := attributionManager(t, root, &beta)
			project := domain.ProjectRecord{ID: "p"}
			m.applyCommitAttribution(ctx, project, domain.SessionID(filepath.Base(other)), other, attributionFromSettings)

			beta = tc.betaAtStart
			m.applyCommitAttribution(ctx, project, domain.SessionID(filepath.Base(wt)), wt, attributionFromSettings)
			// The worktree is reclaimed and later recreated on the same branch.
			runManagerGit(t, repoDir, "worktree", "remove", "--force", wt)
			runManagerGit(t, repoDir, "worktree", "add", "-q", wt, branch)
			beta = !tc.betaAtStart
			m.applyCommitAttribution(ctx, project, domain.SessionID(filepath.Base(wt)), wt, attributionRecorded)
			if got := strings.Contains(commitMessage(t, wt), attribution.Bot.Trailer()); got != tc.want {
				t.Fatalf("trailer after recreate = %v, want %v", got, tc.want)
			}
			// The choice lives in Orchestrator's store, not the person's repository config.
			if out, _ := exec.Command("git", "-C", repoDir, "config", "--local", "--get-regexp", "(?i)orchestrator").Output(); len(out) != 0 {
				t.Fatalf("repository config holds attribution state:\n%s", out)
			}
		})
	}
}

func TestPRFooterPromptFollowsBetaProjectAndRemote(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		beta    bool
		disable bool
		repo    string
		want    bool
	}{
		{"beta off", false, false, "https://github.com/o/r", false},
		{"beta on", true, false, "https://github.com/o/r", true},
		{"project turned the footer off", true, true, "https://github.com/o/r", false},
		{"no remote, no PRs", true, false, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			project := st.projects["mer"]
			project.RepoOriginURL = tc.repo
			project.Config.Attribution.DisablePRFooter = tc.disable
			st.projects["mer"] = project
			beta := tc.beta
			m.SetCommitAttributionEnabled(func(context.Context) bool { return beta })
			prompt, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(prompt, attribution.PRFooter); got != tc.want {
				t.Fatalf("footer in prompt = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPRFooterPromptForWorkspaceChildRemotes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		origin string
		want   bool
	}{
		{"child repo has a remote", "https://github.com/o/api", true},
		{"no repo has a remote", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			project := st.projects["mer"]
			project.Kind = domain.ProjectKindWorkspace
			project.RepoOriginURL = ""
			st.projects["mer"] = project
			st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{ProjectID: "mer", Name: "api", RelativePath: "api", RepoOriginURL: tc.origin}}
			m.SetCommitAttributionEnabled(func(context.Context) bool { return true })
			if got := m.prFooterWanted(ctx, project); got != tc.want {
				t.Fatalf("prFooterWanted = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestProvisionWorkspaceDispatchesHookInstalledBySetup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	root := t.TempDir()
	wt := filepath.Join(root, "session", "s-1")
	attributionRepo(t, filepath.Join(root, "repo"), wt, "orchestrator/s-1/root")
	beta := true
	m, _ := attributionManager(t, root, &beta)
	// Project setup installs a reference-transaction hook, an on-demand name.
	install := `hooks="$(git rev-parse --path-format=absolute --git-common-dir)/hooks" && mkdir -p "$hooks" && ` +
		`printf '#!/bin/sh\ncat >/dev/null\necho ran >> "$(git rev-parse --path-format=absolute --git-common-dir)/ref-tx.log"\n' > "$hooks/reference-transaction" && ` +
		`chmod +x "$hooks/reference-transaction"`
	project := domain.ProjectRecord{ID: "p", Path: filepath.Join(root, "repo"), Config: domain.ProjectConfig{PostCreate: []string{install}}}
	if err := m.provisionWorkspace(ctx, project, "s-1", wt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(worktreeConfig(t, wt, "core.hooksPath"), "reference-transaction")); err != nil {
		t.Fatalf("no dispatcher for the hook setup installed: %v", err)
	}
	if msg := commitMessage(t, wt); !strings.Contains(msg, attribution.Bot.Trailer()) {
		t.Fatalf("no trailer:\n%s", msg)
	}
	if _, err := os.Stat(filepath.Join(root, "repo", ".git", "ref-tx.log")); err != nil {
		t.Fatalf("the hook setup installed did not run: %v", err)
	}
}
