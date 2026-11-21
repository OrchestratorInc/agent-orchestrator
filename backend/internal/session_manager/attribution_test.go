package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/attribution"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	browsersvc "github.com/aoagents/agent-orchestrator/backend/internal/service/browser"
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

// A retry of the same session that reuses its worktree after attribution was
// turned off clears the recorded trailer too, so recreating the worktree later
// does not bring the trailer back.
func TestRetryWithAttributionOffForgetsTheRecordedTrailer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	root := t.TempDir()
	repoDir := filepath.Join(root, "repo")
	wt := filepath.Join(root, "session", "s-1")
	branch := "orchestrator/s-1/root"
	attributionRepo(t, repoDir, wt, branch)
	beta := true
	m, st := attributionManager(t, root, &beta)
	project := domain.ProjectRecord{ID: "p"}
	m.applyCommitAttribution(ctx, project, "s-1", wt, attributionFromSettings)
	if got, _ := st.SessionCommitAttribution(ctx, "s-1"); got == "" {
		t.Fatal("first attempt recorded no trailer")
	}

	// A retry decides again before writing its prompt, as
	// retryFailedChatSpawn does, then sets the workspace up.
	beta = false
	m.recordSpawnAttributionFor(ctx, project, "s-1")
	m.applyCommitAttribution(ctx, project, "s-1", wt, attributionFromSettings)
	if got, _ := st.SessionCommitAttribution(ctx, "s-1"); got != "" {
		t.Fatalf("recorded choice after the retry = %q, want none", got)
	}
	runManagerGit(t, repoDir, "worktree", "remove", "--force", wt)
	runManagerGit(t, repoDir, "worktree", "add", "-q", wt, branch)
	m.applyCommitAttribution(ctx, project, "s-1", wt, attributionRecorded)
	if msg := commitMessage(t, wt); strings.Contains(msg, "Co-authored-by") {
		t.Fatalf("recreated worktree adds the trailer again:\n%s", msg)
	}
}

// A retry with attribution off clears the recorded trailer even when the
// earlier attempt recorded it but never configured a worktree.
func TestRetryWithAttributionOffForgetsATrailerRecordedBeforeAFailure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	beta := false
	m, st := attributionManager(t, root, &beta)
	if err := st.SetSessionCommitAttribution(ctx, "s-1", attribution.Bot.CoAuthor()); err != nil {
		t.Fatal(err)
	}
	// No hooks directory and no configured worktree: the first attempt
	// stopped right after recording its choice. The retry decides again
	// before its prompt, as retryFailedChatSpawn does.
	m.recordSpawnAttributionFor(ctx, domain.ProjectRecord{ID: "p"}, "s-1")
	m.applyCommitAttribution(ctx, domain.ProjectRecord{ID: "p"}, "s-1", filepath.Join(root, "missing"), attributionFromSettings)
	if got, _ := st.SessionCommitAttribution(ctx, "s-1"); got != "" {
		t.Fatalf("recorded choice after the retry = %q, want none", got)
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

// A session keeps the PR footer choice it started with when its prompt is
// rebuilt later (a relaunch, a restore, a switch between Chat and the
// terminal), whatever the settings say by then.
func TestPRFooterPromptKeepsTheSessionsChoiceAfterSettingsChange(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		betaAtStart   bool
		betaAtRebuild bool
	}{
		{"started with the footer, beta turned off", true, false},
		{"started without the footer, beta turned on", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, st, _, _ := newManager()
			project := st.projects["mer"]
			project.RepoOriginURL = "https://github.com/o/r"
			st.projects["mer"] = project
			beta := tc.betaAtStart
			m.SetCommitAttributionEnabled(func(context.Context) bool { return beta })
			prompt, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(prompt, attribution.PRFooter); got != tc.betaAtStart {
				t.Fatalf("footer at start = %v, want %v", got, tc.betaAtStart)
			}
			// Setting up the workspace records the choice the session started with.
			m.applyCommitAttribution(ctx, project, "mer-1", t.TempDir(), attributionFromSettings)

			beta = tc.betaAtRebuild
			for _, chat := range []bool{false, true} {
				prompt, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", chat)
				if err != nil {
					t.Fatal(err)
				}
				if got := strings.Contains(prompt, attribution.PRFooter); got != tc.betaAtStart {
					t.Fatalf("footer after the settings change (chat=%v) = %v, want the starting %v", chat, got, tc.betaAtStart)
				}
			}
			// A new session follows the settings as they are now.
			prompt, err = m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-2", "", false)
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(prompt, attribution.PRFooter); got != tc.betaAtRebuild {
				t.Fatalf("footer for a new session = %v, want %v", got, tc.betaAtRebuild)
			}
		})
	}
}

// The spawn decides attribution once, before the first prompt: a settings
// change between the prompt and the workspace setup changes neither the
// recorded choice nor the worktree's trailer.
func TestSpawnAttributionIsDecidedOnceBeforeThePrompt(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	for _, start := range []bool{true, false} {
		t.Run(fmt.Sprintf("beta %v at spawn", start), func(t *testing.T) {
			root := t.TempDir()
			wt := filepath.Join(root, "session", "mer-1")
			attributionRepo(t, filepath.Join(root, "repo"), wt, "orchestrator/mer-1/root")
			beta := start
			m, st := attributionManager(t, root, &beta)
			project := st.projects["mer"]
			project.RepoOriginURL = "https://github.com/o/r"
			st.projects["mer"] = project

			_, systemPrompt, err := m.buildSpawnTexts(ctx, ports.SpawnConfig{Kind: domain.KindWorker, ProjectID: "mer", RequestedMode: domain.SessionModeChat}, "mer-1")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(systemPrompt, attribution.PRFooter); got != start {
				t.Fatalf("first prompt footer = %v, want %v", got, start)
			}
			// The setting flips while the workspace is still being set up.
			beta = !start
			m.applyCommitAttribution(ctx, project, "mer-1", wt, attributionFromSettings)

			if footer, recorded, _ := st.SessionPRFooter(ctx, "mer-1"); !recorded || footer != start {
				t.Fatalf("recorded footer = %v (recorded %v), want %v", footer, recorded, start)
			}
			wantCoAuthor := ""
			if start {
				wantCoAuthor = attribution.Bot.CoAuthor()
			}
			if got, _ := st.SessionCommitAttribution(ctx, "mer-1"); got != wantCoAuthor {
				t.Fatalf("recorded trailer = %q, want %q", got, wantCoAuthor)
			}
			if got := strings.Contains(commitMessage(t, wt), "Co-authored-by: "+attribution.Bot.CoAuthor()); got != start {
				t.Fatalf("commit trailer = %v, want %v", got, start)
			}
		})
	}
}

// When the footer cannot be settled at spawn (a workspace whose repositories
// cannot be listed), the spawn's trailer decision still holds through a
// settings change, and the footer follows the settings later.
func TestSpawnTrailerDecisionHoldsWhenTheFooterIsUnsettled(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	root := t.TempDir()
	wt := filepath.Join(root, "session", "mer-1")
	attributionRepo(t, filepath.Join(root, "repo"), wt, "orchestrator/mer-1/root")
	beta := true
	m, st := attributionManager(t, root, &beta)
	project := st.projects["mer"]
	project.Kind = domain.ProjectKindWorkspace
	project.RepoOriginURL = ""
	st.projects["mer"] = project
	// Only the spawn's footer lookup fails; the prompt's own lookup works.
	st.workspaceRepoErr = errors.New("database is locked")
	st.workspaceRepoErrOnce = true

	if _, _, err := m.buildSpawnTexts(ctx, ports.SpawnConfig{Kind: domain.KindWorker, ProjectID: "mer", RequestedMode: domain.SessionModeChat}, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if _, recorded, _ := st.SessionPRFooter(ctx, "mer-1"); recorded {
		t.Fatal("recorded a footer choice from a failed repository lookup")
	}
	beta = false
	m.applyCommitAttribution(ctx, project, "mer-1", wt, attributionFromSettings)
	if got, _ := st.SessionCommitAttribution(ctx, "mer-1"); got != attribution.Bot.CoAuthor() {
		t.Fatalf("recorded trailer after the settings change = %q, want the spawn's", got)
	}
	if !strings.Contains(commitMessage(t, wt), "Co-authored-by: "+attribution.Bot.CoAuthor()) {
		t.Fatal("worktree lost the spawn's trailer")
	}
}

// Workspace setup applies the spawn's own decision even when recording it
// failed: neither a footer-only row nor a trailer left by an earlier attempt
// can replace it.
func TestSpawnDecisionHoldsWhenRecordingTheTrailerFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	for _, tc := range []struct {
		name          string
		earlier       string // a trailer an earlier attempt recorded
		betaAtSpawn   bool
		wantInCommits bool
	}{
		{"first attempt, beta on, then turned off", "", true, true},
		{"retry after the beta was turned off", attribution.Bot.CoAuthor(), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			wt := filepath.Join(root, "session", "mer-1")
			attributionRepo(t, filepath.Join(root, "repo"), wt, "orchestrator/mer-1/root")
			beta := tc.betaAtSpawn
			m, st := attributionManager(t, root, &beta)
			project := st.projects["mer"]
			project.RepoOriginURL = "https://github.com/o/r"
			st.projects["mer"] = project
			if tc.earlier != "" {
				if err := st.SetSessionCommitAttribution(ctx, "mer-1", tc.earlier); err != nil {
					t.Fatal(err)
				}
			}
			st.commitAttributionErr = errors.New("disk I/O error")

			if _, _, err := m.buildSpawnTexts(ctx, ports.SpawnConfig{Kind: domain.KindWorker, ProjectID: "mer", RequestedMode: domain.SessionModeChat}, "mer-1"); err != nil {
				t.Fatal(err)
			}
			beta = !tc.betaAtSpawn
			m.applyCommitAttribution(ctx, project, "mer-1", wt, attributionFromSettings)
			if got := strings.Contains(commitMessage(t, wt), "Co-authored-by: "+attribution.Bot.CoAuthor()); got != tc.wantInCommits {
				t.Fatalf("commit trailer = %v, want the spawn's %v", got, tc.wantInCommits)
			}
		})
	}
}

// The spawn reads the beta once: a toggle between the trailer and the footer
// decisions cannot split them.
func TestSpawnAttributionReadsTheBetaOnce(t *testing.T) {
	ctx := context.Background()
	m, st, _, _ := newManager()
	project := st.projects["mer"]
	project.RepoOriginURL = "https://github.com/o/r"
	st.projects["mer"] = project
	reads := 0
	// On for the first read only, as if turned off right after it.
	m.SetCommitAttributionEnabled(func(context.Context) bool { reads++; return reads == 1 })
	if got := m.recordSpawnAttributionFor(ctx, project, "mer-1"); got != attribution.Bot.CoAuthor() {
		t.Fatalf("trailer = %q, want the bot", got)
	}
	if footer, recorded, _ := st.SessionPRFooter(ctx, "mer-1"); !recorded || !footer {
		t.Fatalf("footer = %v (recorded %v), want true like the trailer", footer, recorded)
	}
	if reads != 1 {
		t.Fatalf("beta read %d times, want once", reads)
	}
}

// A spawn that fails before its workspace setup leaves no pending decision
// behind, whether it runs synchronously or in the background.
func TestFailedSpawnForgetsItsAttributionDecision(t *testing.T) {
	ctx := context.Background()
	t.Run("synchronous", func(t *testing.T) {
		m, _, _, ws := newManager()
		m.SetCommitAttributionEnabled(func(context.Context) bool { return true })
		ws.createErr = errors.New("worktree add failed")
		if _, _, _, err := m.Spawn(ctx, ports.SpawnConfig{ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode}); err == nil {
			t.Fatal("spawn succeeded, want the worktree failure")
		}
		if n := len(m.spawnAttribution); n != 0 {
			t.Fatalf("%d pending decisions after the failed spawn, want none", n)
		}
	})
	t.Run("asynchronous chat", func(t *testing.T) {
		m, _, _ := newChatManager(&recordingLauncher{})
		m.browserCapabilities = browsersvc.NewAuthority()
		m.SetCommitAttributionEnabled(func(context.Context) bool { return true })
		deferred := deferredBackground(m)
		m.workspace.(*fakeWorkspace).createErr = errors.New("worktree add failed")
		if _, _, _, err := m.Spawn(ctx, asyncChatSpawnConfig("do the thing")); err != nil {
			t.Fatalf("spawn: %v", err)
		}
		if n := len(m.spawnAttribution); n != 0 {
			t.Fatalf("%d pending decisions once the spawn answered, want none (carried by the spawn)", n)
		}
		(*deferred)[0]()
		if n := len(m.spawnAttribution); n != 0 {
			t.Fatalf("%d pending decisions after the failed start, want none", n)
		}
	})
}

// A session with no recorded choice, from before choices were kept, follows
// the current settings, as it did before.
func TestPRFooterPromptFollowsSettingsWithoutARecordedChoice(t *testing.T) {
	ctx := context.Background()
	m, st, _, _ := newManager()
	project := st.projects["mer"]
	project.RepoOriginURL = "https://github.com/o/r"
	st.projects["mer"] = project
	if err := st.SetSessionCommitAttribution(ctx, "mer-1", attribution.Bot.CoAuthor()); err != nil {
		t.Fatal(err)
	}
	for _, beta := range []bool{true, false} {
		m.SetCommitAttributionEnabled(func(context.Context) bool { return beta })
		prompt, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(prompt, attribution.PRFooter); got != beta {
			t.Fatalf("beta %v: footer = %v", beta, got)
		}
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

// A workspace whose repositories cannot be listed records no footer choice,
// so the session's prompts follow the settings once the lookup works again.
func TestPRFooterChoiceIsNotRecordedWhenReposCannotBeListed(t *testing.T) {
	ctx := context.Background()
	m, st, _, _ := newManager()
	project := st.projects["mer"]
	project.Kind = domain.ProjectKindWorkspace
	project.RepoOriginURL = ""
	st.projects["mer"] = project
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{ProjectID: "mer", Name: "api", RelativePath: "api", RepoOriginURL: "https://github.com/o/api"}}
	m.SetCommitAttributionEnabled(func(context.Context) bool { return true })

	st.workspaceRepoErr = errors.New("database is locked")
	m.recordPRFooterChoice(ctx, project, "mer-1", true)
	if _, recorded, _ := st.SessionPRFooter(ctx, "mer-1"); recorded {
		t.Fatal("recorded a footer choice from a failed repository lookup")
	}
	st.workspaceRepoErr = nil
	prompt, err := m.buildSystemPrompt(ctx, domain.KindWorker, "mer", "mer-1", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(prompt, attribution.PRFooter) {
		t.Fatal("footer missing once the repository lookup works")
	}
	m.recordPRFooterChoice(ctx, project, "mer-1", true)
	if footer, recorded, _ := st.SessionPRFooter(ctx, "mer-1"); !recorded || !footer {
		t.Fatalf("after a working lookup: footer %v recorded %v; want true recorded", footer, recorded)
	}
}

// Project setup that points the worktree's core.hooksPath at the repository's
// own hooks (as a hook manager does) keeps the session's trailer, and the
// dispatcher still runs those hooks.
func TestSetupThatReplacesHooksPathKeepsAttribution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses Unix git hook scripts")
	}
	ctx := context.Background()
	root := t.TempDir()
	wt := filepath.Join(root, "session", "s-1")
	attributionRepo(t, filepath.Join(root, "repo"), wt, "orchestrator/s-1/root")
	beta := true
	m, _ := attributionManager(t, root, &beta)
	setup := `mkdir -p .githooks && ` +
		`printf '#!/bin/sh\necho ran >> "$(git rev-parse --git-common-dir)/repo-hook.log"\n' > .githooks/commit-msg && ` +
		`chmod +x .githooks/commit-msg && git config --worktree core.hooksPath .githooks`
	project := domain.ProjectRecord{ID: "p", Path: filepath.Join(root, "repo"), Config: domain.ProjectConfig{PostCreate: []string{setup}}}
	if err := m.provisionWorkspace(ctx, project, "s-1", wt); err != nil {
		t.Fatal(err)
	}
	if msg := commitMessage(t, wt); !strings.Contains(msg, "Co-authored-by: "+attribution.Bot.CoAuthor()) {
		t.Fatalf("commit after setup lost the trailer:\n%s", msg)
	}
	common, err := exec.Command("git", "-C", wt, "rev-parse", "--path-format=absolute", "--git-common-dir").Output()
	if err != nil {
		t.Fatal(err)
	}
	if log, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(common)), "repo-hook.log")); err != nil || !strings.Contains(string(log), "ran") {
		t.Fatalf("the repository's own commit-msg hook did not run: %v %q", err, log)
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
