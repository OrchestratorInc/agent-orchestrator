package sessionmanager

import (
	"context"
	"io"
	"log/slog"
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
			m := &Manager{dataDir: filepath.Join(root, "data"), logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
			beta := tc.beta
			m.SetCommitAttributionEnabled(func(context.Context) bool { return beta })
			project := domain.ProjectRecord{ID: "p", Config: domain.ProjectConfig{Attribution: domain.AttributionConfig{DisableCommitTrailer: tc.disable}}}
			m.applyCommitAttribution(ctx, project, wt)
			hooks := worktreeConfig(t, wt, "core.hooksPath")
			coAuthor := worktreeConfig(t, wt, attribution.ConfigCoAuthor)
			if tc.wantHooks {
				if hooks != attribution.HooksDir(m.dataDir) || coAuthor != attribution.Bot.CoAuthor() {
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

func TestSessionGitWorktreesFindsWorkspaceChildren(t *testing.T) {
	root := t.TempDir()
	a := attributionWorktree(t, root, "api")
	b := attributionWorktree(t, root, "web")
	// A person's own checkout (a .git directory) and dependency folders are skipped.
	if err := os.MkdirAll(filepath.Join(root, "session", "own", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "session", "node_modules", "dep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "session", "node_modules", "dep", ".git"), []byte("gitdir: x"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := sessionGitWorktrees(filepath.Join(root, "session"))
	if len(got) != 2 || got[0] != a || got[1] != b {
		t.Fatalf("worktrees = %v, want [%s %s]", got, a, b)
	}
	if one := sessionGitWorktrees(a); len(one) != 1 || one[0] != a {
		t.Fatalf("single worktree = %v", one)
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
