package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/nativeconfig"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestImportCopiesDirtyGitStateWithoutChangingSource(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(map[bool]string{false: "checkout", true: "linked-worktree"}[linked], func(t *testing.T) {
			ctx := context.Background()
			root := t.TempDir()
			source := filepath.Join(root, "source")
			destination := filepath.Join(root, "ao", "worktree")
			if err := os.MkdirAll(source, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
				t.Fatal(err)
			}
			git := func(path string, args ...string) []byte {
				t.Helper()
				out, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput()
				if err != nil {
					t.Fatalf("git %v: %v %s", args, err, out)
				}
				return out
			}
			write := func(name string, data []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(source, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			git(source, "init", "-b", "main")
			write("file", []byte("base\n"))
			write("deleted", []byte("remove\n"))
			write(".gitignore", []byte(".env\n.setup/\n"))
			git(source, "add", ".")
			git(source, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-m", "base")
			if linked {
				other := filepath.Join(root, "linked")
				git(source, "worktree", "add", "-b", "external", other)
				source = other
			}
			write("file", []byte("staged\n"))
			write("binary", []byte{0, 1, 2, 255})
			git(source, "add", "file", "binary")
			write("file", []byte("staged plus local\n"))
			write("binary", []byte{0, 3, 4, 255})
			write(".env", []byte("ignored local data\n"))
			write("new\nfile", []byte("untracked\n"))
			// A nested repository, such as Claude's agent worktree, is not part of this checkout.
			nested := filepath.Join(source, ".claude", "worktrees", "agent")
			if err := os.MkdirAll(nested, 0700); err != nil {
				t.Fatal(err)
			}
			git(nested, "init", "-b", "main")
			// A tracked submodule keeps its folder.
			git(source, "update-index", "--add", "--cacheinfo", "160000,"+strings.TrimSpace(string(git(source, "rev-parse", "HEAD")))+",sub")
			if err := os.MkdirAll(filepath.Join(source, "sub"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(source, "deleted")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(source, "file"), filepath.Join(source, "absolute-link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(source, "file"), 0755); err != nil {
				t.Fatal(err)
			}
			before := git(source, "status", "--porcelain=v1", "-z", "--untracked-files=all")
			staged := git(source, "diff", "--cached", "--binary", "--full-index")
			local := git(source, "diff", "--binary", "--full-index")
			git(source, "worktree", "add", "-b", "ao-test", destination, "HEAD")
			// Workspace setup may add ignored folders to the AO worktree before the copy.
			if err := os.MkdirAll(filepath.Join(destination, ".setup"), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(destination, ".setup", "tool"), []byte("from setup\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := copyImportedWorkspace(ctx, source, destination, nil, ""); err != nil {
				t.Fatal(err)
			}
			if kept, err := os.ReadFile(filepath.Join(destination, ".setup", "tool")); err != nil || string(kept) != "from setup\n" {
				t.Fatalf("the copy removed what workspace setup added: %q %v", kept, err)
			}
			if !bytes.Equal(staged, git(destination, "diff", "--cached", "--binary", "--full-index")) || !bytes.Equal(local, git(destination, "diff", "--binary", "--full-index")) {
				t.Fatal("staged or unstaged state changed")
			}
			if !bytes.Equal(before, git(source, "status", "--porcelain=v1", "-z", "--untracked-files=all")) || !bytes.Equal(staged, git(source, "diff", "--cached", "--binary", "--full-index")) {
				t.Fatal("source changed")
			}
			for _, name := range []string{".env", ".claude"} {
				if _, err := os.Lstat(filepath.Join(destination, name)); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("%s copied; ignored files and nested repositories stay behind", name)
				}
			}
			for _, name := range []string{"new\nfile", "binary", "file"} {
				a, _ := os.ReadFile(filepath.Join(source, name))
				b, err := os.ReadFile(filepath.Join(destination, name))
				if err != nil || !bytes.Equal(a, b) {
					t.Fatalf("%q not copied: %v", name, err)
				}
			}
			if target, err := os.Readlink(filepath.Join(destination, "link")); err != nil || target != "file" {
				t.Fatalf("symlink: %q %v", target, err)
			}
			if _, err := os.Stat(filepath.Join(destination, "deleted")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("tracked deletion lost")
			}
			if err := os.WriteFile(filepath.Join(destination, "absolute-link"), []byte("AO edit"), 0600); err != nil {
				t.Fatal(err)
			}
			original, _ := os.ReadFile(filepath.Join(source, "file"))
			copied, _ := os.ReadFile(filepath.Join(destination, "file"))
			if string(original) != "staged plus local\n" || string(copied) != "AO edit" {
				t.Fatal("absolute link wrote into the original checkout")
			}
		})
	}
}

func TestImportedIdleHistoryDoesNotRestartUntilResume(t *testing.T) {
	launcher := &recordingLauncher{startErr: ports.ErrChatResumeFailed}
	m, st, _ := newChatManager(launcher)
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: "/already-copied", Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/original-provider-home", Transferred: true}}
	st.sessions[rec.ID] = rec
	if err := m.reconcileLive(context.Background(), rec); err != nil || len(launcher.started) != 0 {
		t.Fatalf("restart launched import: %v", err)
	}
	if m.SessionStatusReadiness(rec) != "ready" {
		t.Fatal("idle import is not ready to read")
	}
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("resume failure: %v", err)
	}
	if !st.sessions[rec.ID].NeedsImportResume() {
		t.Fatal("failed resume adopted history")
	}
	launcher.startErr = nil
	result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.NeedsImportResume() || result.Mode != RestoreModeNative {
		t.Fatalf("not native adoption: %v", result)
	}
	cfg := launcher.started[len(launcher.started)-1]
	if cfg.ProviderConversationID != "thread-1" || cfg.HistoryMode != ports.ChatHistoryRequired || cfg.FreshIfProviderConversationMissing || cfg.Env["CLAUDE_CONFIG_DIR"] != "/original-provider-home" {
		t.Fatalf("native identity lost: %#v", cfg)
	}
	if len(launcher.turns) != 0 || len(launcher.queued) != 0 {
		t.Fatal("resume sent a fabricated initial prompt")
	}
}

func TestImportPreservesNestedWorktreePointers(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "ao")
	git := func(path string, args ...string) []byte {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return out
	}
	for _, rel := range []string{".", "child"} {
		repo := filepath.Join(source, rel)
		if err := os.MkdirAll(repo, 0700); err != nil {
			t.Fatal(err)
		}
		git(repo, "init", "-b", "main")
		if err := os.WriteFile(filepath.Join(repo, "file"), []byte("base\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if rel == "." {
			if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("child/\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		git(repo, "add", ".")
		git(repo, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-m", "base")
		git(repo, "worktree", "add", "-b", "ao-import", filepath.Join(destination, rel), "HEAD")
		if err := os.WriteFile(filepath.Join(repo, "file"), []byte("staged\n"), 0600); err != nil {
			t.Fatal(err)
		}
		git(repo, "add", "file")
		if err := os.WriteFile(filepath.Join(repo, "file"), []byte("working\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	before := map[string][]byte{}
	for _, rel := range []string{".", "child"} {
		before[rel], _ = os.ReadFile(filepath.Join(destination, rel, ".git"))
	}
	if err := copyImportedWorkspace(context.Background(), source, destination, nil, ""); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".", "child"} {
		after, err := os.ReadFile(filepath.Join(destination, rel, ".git"))
		if err != nil || !bytes.Equal(before[rel], after) {
			t.Fatalf("%s Git pointer changed: %v", rel, err)
		}
		for _, args := range [][]string{{"diff", "--cached", "--binary"}, {"diff", "--binary"}} {
			if !bytes.Equal(git(filepath.Join(source, rel), args...), git(filepath.Join(destination, rel), args...)) {
				t.Fatalf("%s file/index state changed", rel)
			}
		}
	}
	externalChild := filepath.Join(root, "external-child")
	git(root, "clone", "--no-local", filepath.Join(source, "child"), externalChild)
	if err := os.MkdirAll(filepath.Join(externalChild, "unregistered", ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	// A retry starts again from the source state, over whatever an earlier attempt left.
	if err := copyImportedWorkspace(context.Background(), source, destination, map[string]string{"child": externalChild}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "child", "unregistered")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("copied an unregistered nested repository")
	}
	// A source that committed since the AO worktree was created is followed to
	// its new commit; the files are never copied onto the old base.
	git(source, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-m", "advance")
	if err := copyImportedWorkspace(context.Background(), source, destination, nil, ""); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{".", "child"} {
		if !bytes.Equal(git(filepath.Join(source, rel), "rev-parse", "HEAD"), git(filepath.Join(destination, rel), "rev-parse", "HEAD")) {
			t.Fatalf("%s did not follow the source HEAD", rel)
		}
		for _, args := range [][]string{{"diff", "--cached", "--binary"}, {"diff", "--binary"}} {
			if !bytes.Equal(git(filepath.Join(source, rel), args...), git(filepath.Join(destination, rel), args...)) {
				t.Fatalf("%s file/index state differs after following the source", rel)
			}
		}
	}
}

func TestImportedResumeReportsOwnershipRefusal(t *testing.T) {
	launcher := &recordingLauncher{startErr: errors.New("thread is already in use")}
	m, st, _ := newChatManager(launcher)
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: "/already-copied", Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider-home", Transferred: true, Prepared: true}}
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); !errors.Is(err, ErrSessionOpenElsewhere) {
		t.Fatalf("ownership refusal lost: %v", err)
	}
	if !st.sessions[rec.ID].NeedsImportResume() {
		t.Fatal("refused session was adopted")
	}
}

func TestImportedWorkspaceSetupRetriesWithoutRecopy(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	workspace := t.TempDir()
	project := st.projects["mer"]
	project.Config.PostCreate = []string{"exit 9"}
	st.projects["mer"] = project
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider-home", Transferred: true}}
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil {
		t.Fatal("failed setup was ignored")
	}
	if len(launcher.started) != 0 || st.sessions[rec.ID].Metadata.ImportSource.Prepared {
		t.Fatal("provider launched before workspace setup")
	}
	file := filepath.Join(workspace, "local-edit")
	if err := os.WriteFile(file, []byte("keep this edit"), 0600); err != nil {
		t.Fatal(err)
	}
	project.Config.PostCreate = nil
	st.projects["mer"] = project
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err != nil {
		t.Fatal(err)
	}
	if text, err := os.ReadFile(file); err != nil || string(text) != "keep this edit" {
		t.Fatal("retry lost AO edits")
	}
}

func TestImportedPendingSessionCanBeArchivedAndRestored(t *testing.T) {
	for _, withWorkspace := range []bool{false, true} {
		t.Run(map[bool]string{false: "unopened", true: "copied but not adopted"}[withWorkspace], func(t *testing.T) {
			launcher := &recordingLauncher{}
			m, st, _ := newChatManager(launcher)
			rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
			rec.Metadata = domain.SessionMetadata{ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider"}}
			if withWorkspace {
				rec.Metadata.WorkspacePath, rec.Metadata.Branch = "/already-copied", "ao/import"
				rec.Metadata.ImportSource.Transferred, rec.Metadata.ImportSource.Prepared = true, true
				m.workspace.(*fakeWorkspace).path = rec.Metadata.WorkspacePath
			}
			st.sessions[rec.ID] = rec
			if _, err := m.Kill(context.Background(), rec.ID); err != nil {
				t.Fatal(err)
			}
			if !st.sessions[rec.ID].IsTerminated {
				t.Fatal("session did not archive")
			}
			restored, err := m.RestoreWithMode(context.Background(), rec.ID)
			if err != nil {
				t.Fatal(err)
			}
			if restored.Session.IsTerminated || restored.Session.Activity.State != domain.ActivityIdle || !restored.Session.NeedsImportResume() || restored.Session.Metadata.WorkspacePath != rec.Metadata.WorkspacePath {
				t.Fatalf("restored import = %#v", restored.Session)
			}
			if len(launcher.started) != 0 {
				t.Fatal("restoring unopened history started a provider")
			}

		})
	}
}

type failingImportWorkspaceStore struct {
	*fakeStore
	calls, failAt int
	cancel        context.CancelFunc
}

func (s *failingImportWorkspaceStore) SetSessionImportWorkspace(ctx context.Context, id domain.SessionID, expected, next *domain.SessionImportSource, branch, path, repo string, now time.Time) (bool, error) {
	s.calls++
	if s.calls == s.failAt {
		if s.cancel != nil {
			s.cancel()
			return false, ctx.Err()
		}
		return false, errors.New("injected database write failure")
	}
	return s.fakeStore.SetSessionImportWorkspace(ctx, id, expected, next, branch, path, repo, now)
}

func TestImportedWorkspaceTransferRetriesAfterMetadataFailure(t *testing.T) {
	for _, test := range []struct {
		name   string
		failAt int
		cancel bool
	}{
		{"before copy", 1, false}, {"after copy", 2, false}, {"cancel after copy", 2, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			m, st, _, source := newGitTaskPreparationManager(t)
			subdir := filepath.Join(source, "subdir")
			if err := os.MkdirAll(subdir, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(subdir, "local"), []byte("source edit"), 0600); err != nil {
				t.Fatal(err)
			}
			rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
			rec.Metadata = domain.SessionMetadata{ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider", CWD: subdir}}
			st.sessions[rec.ID] = rec
			failing := &failingImportWorkspaceStore{fakeStore: st, failAt: test.failAt}
			m.store = failing
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if test.cancel {
				failing.cancel = cancel
			}
			if _, err := m.prepareImportedWorkspace(ctx, rec, st.projects["mer"]); err == nil {
				t.Fatal("injected failure ignored")
			}
			pending := st.sessions[rec.ID]
			if test.failAt == 2 {
				if pending.Metadata.WorkspacePath == "" {
					t.Fatal("copied workspace was not recorded")
				}
				if err := os.WriteFile(filepath.Join(pending.Metadata.WorkspacePath, "subdir", "local"), []byte("AO edit"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ready, err := m.prepareImportedWorkspace(context.Background(), pending, st.projects["mer"])
			if err != nil {
				t.Fatal(err)
			}
			if !ready.Metadata.ImportSource.Transferred || !ready.Metadata.ImportSource.Prepared || ready.Metadata.ImportSource.WorkingSubdir != "subdir" {
				t.Fatalf("transfer incomplete: %#v", ready.Metadata.ImportSource)
			}
			copied, err := os.ReadFile(filepath.Join(ready.Metadata.WorkspacePath, "subdir", "local"))
			if err != nil {
				t.Fatal(err)
			}
			want := "source edit"
			if test.failAt == 2 {
				want = "AO edit"
				if ready.Metadata.WorkspacePath != pending.Metadata.WorkspacePath {
					t.Fatal("retry replaced the workspace")
				}
			}
			if string(copied) != want {
				t.Fatalf("copied file = %q, want %q", copied, want)
			}
			if _, err := os.Stat(importCopyMarker(ready.Metadata.WorkspacePath, rec.ID)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("completion marker remains: %v", err)
			}
			original, _ := os.ReadFile(filepath.Join(source, "subdir", "local"))
			if string(original) != "source edit" {
				t.Fatal("source changed")
			}
		})
	}
}

func TestImportedWorkspaceAdoptsSeparateCloneWithUnpublishedCommit(t *testing.T) {
	m, st, _, root := newGitTaskPreparationManager(t)
	child := filepath.Join(root, "child")
	runManagerGit(t, root, "clone", newManagerGitRepo(t), child)
	runManagerGit(t, child, "remote", "set-url", "origin", "https://github.com/example/import-child.git")
	clone := filepath.Join(t.TempDir(), "clone")
	runManagerGit(t, root, "clone", "--no-local", child, clone)
	runManagerGit(t, clone, "remote", "set-url", "origin", "git@github.com:example/import-child.git")
	if err := os.WriteFile(filepath.Join(clone, "new-commit"), []byte("unpublished"), 0600); err != nil {
		t.Fatal(err)
	}
	runManagerGit(t, clone, "add", "new-commit")
	runManagerGit(t, clone, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-m", "unpublished")
	if err := os.WriteFile(filepath.Join(clone, "local-edit"), []byte("dirty clone"), 0600); err != nil {
		t.Fatal(err)
	}
	project := st.projects["mer"]
	project.Kind = domain.ProjectKindWorkspace
	st.projects["mer"] = project
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "child", RelativePath: "child", GitStatus: domain.GitStatusReady}}
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider", CWD: clone}}
	st.sessions[rec.ID] = rec
	ready, err := m.prepareImportedWorkspace(context.Background(), rec, project)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(ready.Metadata.WorkspacePath, "child")
	if got := runManagerGit(t, target, "rev-parse", "HEAD"); got != runManagerGit(t, clone, "rev-parse", "HEAD") {
		t.Fatal("unpublished source commit was lost")
	}
	for name, want := range map[string]string{"new-commit": "unpublished", "local-edit": "dirty clone"} {
		got, err := os.ReadFile(filepath.Join(target, name))
		if err != nil || string(got) != want {
			t.Fatalf("%s = %q: %v", name, got, err)
		}
	}
	if ready.Metadata.ImportSource.WorkingSubdir != "child" {
		t.Fatalf("working subdirectory = %q", ready.Metadata.ImportSource.WorkingSubdir)
	}
	if got := runManagerGit(t, target, "rev-parse", "--path-format=absolute", "--git-common-dir"); got != runManagerGit(t, child, "rev-parse", "--path-format=absolute", "--git-common-dir") {
		t.Fatal("workspace no longer belongs to the registered repository")
	}
}

type providerHomeAgent struct{ fakeAgent }

func (providerHomeAgent) NativeSessionConfigDir(_ context.Context, env map[string]string) (string, error) {
	return nativeconfig.Resolve(env, "CLAUDE_CONFIG_DIR", ".claude")
}

type providerHomeAgents struct{}

func (providerHomeAgents) Agent(domain.AgentHarness) (ports.Agent, bool) {
	return providerHomeAgent{}, true
}

// Claude Code keys its keychain login by CLAUDE_CONFIG_DIR: setting even the
// default path makes a keychain user look signed out on the first follow-up.
func TestImportedConfigEnvKeepsDefaultProviderLogin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	m := &Manager{agents: providerHomeAgents{}}
	rec := domain.SessionRecord{Harness: domain.HarnessClaudeCode}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: "native", ImportSource: &domain.SessionImportSource{NativeID: "native", ConfigDir: filepath.Join(home, ".claude")}}
	env := map[string]string{}
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if value, set := env["CLAUDE_CONFIG_DIR"]; set {
		t.Fatalf("default provider home was forced: %q", value)
	}
	// An AO account folder links the computer's history, so the conversation is
	// reachable there and the account's login stays.
	source := filepath.Join(home, "source-home")
	transcript := filepath.Join(source, "projects", "-work", "native.jsonl")
	account := filepath.Join(home, "account-home")
	if err := os.MkdirAll(filepath.Dir(transcript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(transcript, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(account, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(source, "projects"), filepath.Join(account, "projects")); err != nil {
		t.Fatal(err)
	}
	rec.Metadata.ImportSource.ConfigDir = source
	rec.Metadata.NativeTranscriptPath = transcript
	env = map[string]string{"CLAUDE_CONFIG_DIR": account, "CLAUDE_SECURESTORAGE_CONFIG_DIR": account}
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if env["CLAUDE_CONFIG_DIR"] != account {
		t.Fatalf("account folder replaced: %v", env)
	}
	// A home that cannot reach the conversation is pointed at its source.
	rec.Metadata.ImportSource.ConfigDir = "/other-provider-home"
	rec.Metadata.NativeTranscriptPath = "/other-provider-home/projects/-elsewhere/native.jsonl"
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if env["CLAUDE_CONFIG_DIR"] != "/other-provider-home" {
		t.Fatalf("env = %v", env)
	}
}

// Startup health treats a ready row without a workspace as an interrupted
// spawn and rolls it back; an idle import must survive every daemon restart.
func TestStartupHealthKeepsIdleImportedHistory(t *testing.T) {
	launcher := &recordingLauncher{}
	m, st, _ := newChatManager(launcher)
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}, ProvisionState: domain.SessionProvisionReady}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: "native", ImportSource: &domain.SessionImportSource{NativeID: "native", ConfigDir: "/provider-home"}}
	st.sessions[rec.ID] = rec
	m.reconcileLivePass(context.Background(), []domain.SessionRecord{rec})
	got, ok := st.sessions[rec.ID]
	if !ok || got.IsTerminated || !got.NeedsImportResume() || len(launcher.started) != 0 {
		t.Fatalf("startup changed imported history: ok=%v %#v launches=%d", ok, got, len(launcher.started))
	}
}

// Provider worktrees are often deleted once their branch merges; the imported
// conversation still continues natively from the project checkout.
func TestImportedSessionWhoseFolderIsGoneStartsFromProjectCheckout(t *testing.T) {
	m, st, _, root := newGitTaskPreparationManager(t)
	project := st.projects["mer"]
	gone := filepath.Join(t.TempDir(), "removed-worktree", "pkg")
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider", CWD: gone}}
	st.sessions[rec.ID] = rec
	ready, err := m.prepareImportedWorkspace(context.Background(), rec, project)
	if err != nil {
		t.Fatal(err)
	}
	source := ready.Metadata.ImportSource
	if !source.Transferred || source.WorkingSubdir != "" || ready.Metadata.WorkspacePath == "" {
		t.Fatalf("import = %+v workspace=%q", source, ready.Metadata.WorkspacePath)
	}
	if got, want := runManagerGit(t, ready.Metadata.WorkspacePath, "rev-parse", "HEAD"), runManagerGit(t, root, "rev-parse", "HEAD"); got != want {
		t.Fatalf("workspace HEAD = %s, want project HEAD %s", got, want)
	}
}

// A provider that holds the native conversation after adoption still gets the
// open-elsewhere explanation, not a generic resume failure.
func TestAdoptedResumeReportsOwnershipRefusal(t *testing.T) {
	launcher := &recordingLauncher{startErr: errors.New("thread 019a already has an active writer")}
	m, st, _ := newChatManager(launcher)
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: "/already-copied", Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider-home", Transferred: true, Prepared: true, Adopted: true}}
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); !errors.Is(err, ErrSessionOpenElsewhere) {
		t.Fatalf("ownership refusal after adoption lost: %v", err)
	}
}

// A conversation from a second clone lends its commit and files; the AO
// worktree and branch still belong to the registered repository.
func TestImportedCloneLeavesSourceRepositoryUntouched(t *testing.T) {
	m, st, _, root := newGitTaskPreparationManager(t)
	project := st.projects["mer"]
	clone := filepath.Join(t.TempDir(), "clone")
	runManagerGit(t, root, "clone", "--no-local", root, clone)
	// Clones of one repository share its origin; that is how a scan matched this one.
	runManagerGit(t, root, "remote", "add", "origin", "https://github.com/example/mer.git")
	runManagerGit(t, clone, "remote", "set-url", "origin", "git@github.com:example/mer.git")
	if err := os.WriteFile(filepath.Join(clone, "new-commit"), []byte("unpublished"), 0600); err != nil {
		t.Fatal(err)
	}
	runManagerGit(t, clone, "add", "new-commit")
	runManagerGit(t, clone, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-m", "unpublished")
	if err := os.WriteFile(filepath.Join(clone, "local-edit"), []byte("dirty clone"), 0600); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider", CWD: clone}}
	st.sessions[rec.ID] = rec
	ready, err := m.prepareImportedWorkspace(context.Background(), rec, project)
	if err != nil {
		t.Fatal(err)
	}
	workspace := ready.Metadata.WorkspacePath
	if got, want := runManagerGit(t, workspace, "rev-parse", "HEAD"), runManagerGit(t, clone, "rev-parse", "HEAD"); got != want {
		t.Fatalf("workspace HEAD = %s, want the clone's unpublished %s", got, want)
	}
	if got, err := os.ReadFile(filepath.Join(workspace, "local-edit")); err != nil || string(got) != "dirty clone" {
		t.Fatalf("local edit = %q: %v", got, err)
	}
	if got, want := runManagerGit(t, workspace, "rev-parse", "--path-format=absolute", "--git-common-dir"), runManagerGit(t, root, "rev-parse", "--path-format=absolute", "--git-common-dir"); got != want {
		t.Fatalf("workspace repository = %s, want registered %s", got, want)
	}
	if branches := runManagerGit(t, clone, "branch", "--list", "ao/*"); branches != "" {
		t.Fatalf("source clone gained branches %q", branches)
	}
	if worktrees := runManagerGit(t, clone, "worktree", "list", "--porcelain"); strings.Count(worktrees, "worktree ") != 1 {
		t.Fatalf("source clone gained worktrees:\n%s", worktrees)
	}
}

// The replay boundary is recorded once, at adoption. A controller rebuild reuses
// the launch callback and must not move it over later AO turns.
func TestImportReplayBoundaryIsFixedAtAdoption(t *testing.T) {
	const sessionID = domain.SessionID("mer-1")
	st := &historicalChatRestoreStore{transitionStore: newTransitionStore(), conversation: domain.ConversationRecord{
		ID: "conv-1", Scope: domain.ConversationScopeSession, ProjectID: "mer", SessionID: sessionID, LatestSequence: 7,
	}}
	st.projects["mer"] = domain.ProjectRecord{ID: "mer", Config: testRoleAgents()}
	rec := domain.SessionRecord{ID: sessionID, ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCodex, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: "/already-copied", Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider-home", Transferred: true, Prepared: true}}
	st.sessions[sessionID] = rec
	launcher := &recordingLauncher{}
	m := New(Deps{
		Runtime: &fakeRuntime{}, Agents: fakeAgents{}, Workspace: &fakeWorkspace{},
		Store: st, Messenger: &fakeMessenger{}, Chat: launcher,
		Lifecycle: &fakeLCM{store: st.fakeStore}, DataDir: "/ao-test-data",
	})
	if _, err := m.ResumeAgentWithMode(context.Background(), sessionID); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[sessionID].Metadata.ImportSource; got == nil || !got.Adopted || got.HistoryThrough != 7 {
		t.Fatalf("adopted import = %+v, want boundary 7", got)
	}
	st.conversation.LatestSequence = 12 // AO turns after adoption
	if _, err := launcher.started[0].ControllerReady(ChatStarted{ProviderConversationID: "thread-1", ControllerGeneration: "gen-2"}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions[sessionID].Metadata.ImportSource.HistoryThrough; got != 7 {
		t.Fatalf("rebuild moved the boundary to %d", got)
	}
}

// A repository nested inside the project folder, such as an ignored clone, is not
// the project's repository: its commit and files never enter an AO worktree.
func TestImportedChatInUnrelatedNestedRepositoryIsRefused(t *testing.T) {
	m, st, _, root := newGitTaskPreparationManager(t)
	nested := filepath.Join(root, "third_party", "foo")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	runManagerGit(t, nested, "init")
	runManagerGit(t, nested, "remote", "add", "origin", "https://github.com/acme/foo.git")
	runManagerGit(t, nested, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "foo")
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider", CWD: nested}}
	st.sessions[rec.ID] = rec
	if _, err := m.prepareImportedWorkspace(context.Background(), rec, st.projects["mer"]); err == nil || !strings.Contains(err.Error(), "not a checkout of this project's repository") {
		t.Fatalf("nested unrelated repository: %v", err)
	}
	if branches := runManagerGit(t, root, "branch", "--list", "ao/*"); branches != "" {
		t.Fatalf("created branches %q", branches)
	}
}

// A chat that ran in a separate clone of a workspace's root repository: the
// clone holds no child checkouts, so children start from the registered ones.
func TestImportedWorkspaceFromRootCloneStartsChildrenFromRegisteredCheckouts(t *testing.T) {
	m, st, _, root := newGitTaskPreparationManager(t)
	runManagerGit(t, root, "remote", "add", "origin", "https://github.com/example/workspace-root.git")
	child := filepath.Join(root, "child")
	runManagerGit(t, root, "clone", newManagerGitRepo(t), child)
	if err := os.WriteFile(filepath.Join(child, "registered-edit"), []byte("not part of the chat"), 0600); err != nil {
		t.Fatal(err)
	}
	clone := filepath.Join(t.TempDir(), "root-clone")
	runManagerGit(t, root, "clone", "--no-local", root, clone)
	runManagerGit(t, clone, "remote", "set-url", "origin", "git@github.com:example/workspace-root.git")
	if err := os.WriteFile(filepath.Join(clone, "root-edit"), []byte("from the clone"), 0600); err != nil {
		t.Fatal(err)
	}
	project := st.projects["mer"]
	project.Kind = domain.ProjectKindWorkspace
	st.projects["mer"] = project
	st.workspaceRepo["mer"] = []domain.WorkspaceRepoRecord{{Name: "child", RelativePath: "child", GitStatus: domain.GitStatusReady}}
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider", CWD: clone}}
	st.sessions[rec.ID] = rec
	ready, err := m.prepareImportedWorkspace(context.Background(), rec, project)
	if err != nil {
		t.Fatal(err)
	}
	workspace := ready.Metadata.WorkspacePath
	if got, err := os.ReadFile(filepath.Join(workspace, "root-edit")); err != nil || string(got) != "from the clone" {
		t.Fatalf("root edit = %q: %v", got, err)
	}
	target := filepath.Join(workspace, "child")
	if got, want := runManagerGit(t, target, "rev-parse", "HEAD"), runManagerGit(t, child, "rev-parse", "HEAD"); got != want {
		t.Fatalf("child HEAD = %s, want the registered %s", got, want)
	}
	if _, err := os.Lstat(filepath.Join(target, "registered-edit")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the registered child's own edits were copied into the session")
	}
}

// A retry starts from the source as it is now: an untracked file an earlier
// attempt copied, and the source has since deleted, does not stay behind.
func TestImportRetryDropsFilesTheSourceNoLongerHas(t *testing.T) {
	root := t.TempDir()
	source, destination := filepath.Join(root, "source"), filepath.Join(root, "ao")
	git := func(path string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	git(source, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(source, "tracked"), []byte("base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	git(source, "add", ".")
	git(source, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-m", "base")
	git(source, "worktree", "add", "-b", "ao-import", destination, "HEAD")
	if err := os.WriteFile(filepath.Join(source, "notes"), []byte("draft\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := copyImportedWorkspace(context.Background(), source, destination, nil, ""); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(destination, "setup-output"), []byte("made by setup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "notes")); err != nil {
		t.Fatal(err)
	}
	if err := copyImportedWorkspace(context.Background(), source, destination, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(destination, "notes")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a file the source deleted stayed from the earlier attempt")
	}
	if _, err := os.Lstat(filepath.Join(destination, "setup-output")); err != nil {
		t.Fatalf("a file the earlier attempt did not copy was removed: %v", err)
	}
}
