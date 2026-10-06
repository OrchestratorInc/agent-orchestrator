package sessionmanager

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

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
			write(".gitignore", []byte(".env\n"))
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
			if err := os.Remove(filepath.Join(source, "deleted")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("file", filepath.Join(source, "link")); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(source, "file"), 0755); err != nil {
				t.Fatal(err)
			}
			before := git(source, "status", "--porcelain=v1", "-z", "--untracked-files=all")
			staged := git(source, "diff", "--cached", "--binary", "--full-index")
			local := git(source, "diff", "--binary", "--full-index")
			git(source, "worktree", "add", "-b", "ao-test", destination, "HEAD")
			if err := copyImportedWorkspace(ctx, source, destination, nil); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(staged, git(destination, "diff", "--cached", "--binary", "--full-index")) || !bytes.Equal(local, git(destination, "diff", "--binary", "--full-index")) {
				t.Fatal("staged or unstaged state changed")
			}
			if !bytes.Equal(before, git(source, "status", "--porcelain=v1", "-z", "--untracked-files=all")) || !bytes.Equal(staged, git(source, "diff", "--cached", "--binary", "--full-index")) {
				t.Fatal("source changed")
			}
			for _, name := range []string{".env", "new\nfile", "binary", "file"} {
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
		})
	}
}

func TestImportedIdleHistoryDoesNotRestartUntilResume(t *testing.T) {
	launcher := &recordingLauncher{startErr: ports.ErrChatResumeFailed}
	m, st, _ := newChatManager(launcher)
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: "/already-copied", Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/original-provider-home"}}
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
	if cfg.ProviderConversationID != "thread-1" || cfg.HistoryMode != ports.ChatHistoryRequired || cfg.Env["CLAUDE_CONFIG_DIR"] != "/original-provider-home" {
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
	if err := copyImportedWorkspace(context.Background(), source, destination, nil); err != nil {
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
	if err := copyImportedWorkspace(context.Background(), source, destination, map[string]string{"child": externalChild}); err == nil {
		t.Fatal("silently discarded nested Git metadata from the original linked checkout")
	}
	// A source HEAD advance must not copy files onto the old AO base.
	git(source, "-c", "user.name=AO test", "-c", "user.email=test@example.invalid", "commit", "-m", "advance")
	if err := copyImportedWorkspace(context.Background(), source, destination, nil); err == nil {
		t.Fatal("copied mismatched source HEAD")
	}
}

func TestImportedResumeReportsOwnershipRefusal(t *testing.T) {
	launcher := &recordingLauncher{startErr: errors.New("thread is already in use")}
	m, st, _ := newChatManager(launcher)
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{WorkspacePath: "/already-copied", Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider-home", Prepared: true}}
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
	rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/import", ProviderConversationID: "thread-1", ImportSource: &domain.SessionImportSource{NativeID: "thread-1", ConfigDir: "/provider-home"}}
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
