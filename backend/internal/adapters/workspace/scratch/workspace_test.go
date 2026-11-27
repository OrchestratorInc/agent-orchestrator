package scratch_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/workspace/scratch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestWorkspaceCreatesBranchlessPerSessionDirectories(t *testing.T) {
	root := t.TempDir()
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("resolve temp root: %v", err)
	}
	ws, err := scratch.New(scratch.Options{ManagedRoot: root})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	worker, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "scratch",
		SessionID: "scratch-1",
		Kind:      domain.KindWorker,
	})
	if err != nil {
		t.Fatalf("Create worker: %v", err)
	}
	if want := filepath.Join(physicalRoot, "scratch", "workers", "scratch-1"); worker.Path != want {
		t.Fatalf("worker path = %q, want %q", worker.Path, want)
	}
	if worker.Branch != "" {
		t.Fatalf("worker branch = %q, want empty", worker.Branch)
	}
	if info, err := os.Stat(worker.Path); err != nil || !info.IsDir() {
		t.Fatalf("worker dir stat = %#v, %v; want directory", info, err)
	}

	orchestrator, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "scratch",
		SessionID: "scratch-2",
		Kind:      domain.KindOrchestrator,
	})
	if err != nil {
		t.Fatalf("Create orchestrator: %v", err)
	}
	if want := filepath.Join(physicalRoot, "scratch", "orchestrators", "scratch-2"); orchestrator.Path != want {
		t.Fatalf("orchestrator path = %q, want %q", orchestrator.Path, want)
	}
	if orchestrator.Branch != "" {
		t.Fatalf("orchestrator branch = %q, want empty", orchestrator.Branch)
	}
}

func TestWorkspaceCreatesStandaloneSessionDirectory(t *testing.T) {
	root := t.TempDir()
	physicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	ws, err := scratch.New(scratch.Options{ManagedRoot: root})
	if err != nil {
		t.Fatal(err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		SessionID: "standalone-1",
		Kind:      domain.KindWorker,
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(physicalRoot, "standalone", "sessions", "standalone-1"); info.Path != want {
		t.Fatalf("standalone path = %q, want %q", info.Path, want)
	}
	if info.ProjectID != "" || info.Branch != "" {
		t.Fatalf("standalone info = %#v", info)
	}
}

func TestWorkspaceDestroyPreservesNonEmptyDirectory(t *testing.T) {
	ws, err := scratch.New(scratch.Options{ManagedRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "scratch",
		SessionID: "scratch-1",
		Kind:      domain.KindWorker,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "notes.txt"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	err = ws.Destroy(context.Background(), info)
	if !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("Destroy err = %v, want ErrWorkspaceDirty", err)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "notes.txt")); err != nil {
		t.Fatalf("non-empty scratch dir was not preserved: %v", err)
	}

	if err := os.Remove(filepath.Join(info.Path, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatalf("Destroy empty: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty scratch dir still exists or stat failed differently: %v", err)
	}
}

func TestWorkspaceRejectsUnsafeSessionIDs(t *testing.T) {
	ws, err := scratch.New(scratch.Options{ManagedRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "scratch",
		SessionID: "../outside",
		Kind:      domain.KindWorker,
	})
	if err == nil {
		t.Fatal("Create unsafe session id succeeded, want error")
	}
}

// A path outside the managed root is never removed, and the refusal names that
// so cleanup can report it instead of a generic teardown failure.
func TestWorkspaceDestroyRefusesPathOutsideManagedRoot(t *testing.T) {
	ws, err := scratch.New(scratch.Options{ManagedRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	outside := t.TempDir()
	err = ws.Destroy(context.Background(), ports.WorkspaceInfo{Path: outside, ProjectID: "scratch", SessionID: "scratch-1"})
	if !errors.Is(err, ports.ErrWorkspaceUnsafePath) {
		t.Fatalf("Destroy outside root err = %v, want ports.ErrWorkspaceUnsafePath", err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory was touched: %v", err)
	}
}

// TestWorkspaceDestroyReclaimReportsAlreadyAbsentDirectory covers the scratch
// half of the cleanup accounting. os.Remove is deliberately tolerated on a
// missing path, so a scratch directory that was already gone tears down without
// error while releasing nothing. Counting that as reclaimed reports space that
// was never freed.
func TestWorkspaceDestroyReclaimReportsAlreadyAbsentDirectory(t *testing.T) {
	ws, err := scratch.New(scratch.Options{ManagedRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{
		ProjectID: "scratch",
		SessionID: "scratch-gone",
		Kind:      domain.KindWorker,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	reclaim, err := ws.DestroyReclaim(context.Background(), info)
	if err != nil {
		t.Fatalf("DestroyReclaim present: %v", err)
	}
	if reclaim != ports.WorkspaceReclaimRemoved {
		t.Fatalf("reclaim = %q, want %q", reclaim, ports.WorkspaceReclaimRemoved)
	}

	// Second pass: the directory is gone, so there is nothing left to release.
	reclaim, err = ws.DestroyReclaim(context.Background(), info)
	if err != nil {
		t.Fatalf("DestroyReclaim absent: %v", err)
	}
	if reclaim != ports.WorkspaceReclaimAlreadyAbsent {
		t.Fatalf("reclaim = %q, want %q", reclaim, ports.WorkspaceReclaimAlreadyAbsent)
	}
}

func createStandaloneScratch(t *testing.T) (*scratch.Workspace, ports.WorkspaceInfo) {
	t.Helper()
	ws, err := scratch.New(scratch.Options{ManagedRoot: t.TempDir()})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	info, err := ws.Create(context.Background(), ports.WorkspaceConfig{SessionID: "standalone-1", Kind: domain.KindWorker})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return ws, info
}

func writeScratchFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// A standalone session's folder holds the hook config AO installed at launch
// even when the agent wrote nothing. Archiving must not report that as dirty.
func TestWorkspaceDestroyRemovesDirectoryHoldingOnlyAOFiles(t *testing.T) {
	ws, info := createStandaloneScratch(t)
	if err := (&claudecode.Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: info.Path}); err != nil {
		t.Fatalf("install claude hooks: %v", err)
	}
	if err := hookutil.InstallBugTriageSkill(filepath.Join(info.Path, ".claude", "skills")); err != nil {
		t.Fatalf("install bug-triage skill: %v", err)
	}
	writeScratchFile(t, filepath.Join(info.Path, ".github", "agents", "ao-worker.agent.md"), hookutil.CopilotAgentProfileSentinel+"\nprofile\n")
	if err := os.MkdirAll(filepath.Join(info.Path, "empty", "nested"), 0o750); err != nil {
		t.Fatal(err)
	}

	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch dir holding only AO files still exists or stat failed differently: %v", err)
	}
}

func TestWorkspaceDestroyKeepsEverythingWhenUserWorkSitsBesideAOFiles(t *testing.T) {
	for name, userFile := range map[string]string{
		"top level":           "notes.md",
		"inside agent folder": filepath.Join(".claude", "notes.md"),
		"copilot lookalike":   filepath.Join(".github", "agents", "ao-mine.agent.md"),
	} {
		t.Run(name, func(t *testing.T) {
			ws, info := createStandaloneScratch(t)
			if err := (&claudecode.Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: info.Path}); err != nil {
				t.Fatalf("install claude hooks: %v", err)
			}
			writeScratchFile(t, filepath.Join(info.Path, userFile), "mine\n")

			if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
				t.Fatalf("Destroy err = %v, want ErrWorkspaceDirty", err)
			}
			for _, kept := range []string{userFile, filepath.Join(".claude", "settings.local.json")} {
				if _, err := os.Stat(filepath.Join(info.Path, kept)); err != nil {
					t.Fatalf("%s was not preserved: %v", kept, err)
				}
			}
		})
	}
}

// Only a .gitignore carrying AO's sentinel can claim files; one the user wrote
// does not make the files it names AO's to delete.
func TestWorkspaceDestroyIgnoresForeignGitignore(t *testing.T) {
	ws, info := createStandaloneScratch(t)
	writeScratchFile(t, filepath.Join(info.Path, ".claude", ".gitignore"), "/.gitignore\n/settings.local.json\n")
	writeScratchFile(t, filepath.Join(info.Path, ".claude", "settings.local.json"), "{}\n")

	if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("Destroy err = %v, want ErrWorkspaceDirty", err)
	}
	if _, err := os.Stat(filepath.Join(info.Path, ".claude", "settings.local.json")); err != nil {
		t.Fatalf("file named by a foreign .gitignore was not preserved: %v", err)
	}
}

// AO's .gitignore lists globs and whole directories (tau's per-session
// instructions and __pycache__/). Both are AO's, as git treats them in a
// project worktree, so neither keeps the folder.
func TestWorkspaceDestroyRemovesGlobAndDirectoryEntriesAOListed(t *testing.T) {
	ws, info := createStandaloneScratch(t)
	dir := filepath.Join(info.Path, ".tau")
	if err := hookutil.EnsureWorkspaceGitignore(dir, "ao-standing-instructions-*.md", "__pycache__/"); err != nil {
		t.Fatalf("EnsureWorkspaceGitignore: %v", err)
	}
	writeScratchFile(t, filepath.Join(dir, "ao-standing-instructions-abc.md"), "rules\n")
	writeScratchFile(t, filepath.Join(dir, "__pycache__", "ao_activity.cpython-312.pyc"), "bytecode\n")

	if err := ws.Destroy(context.Background(), info); err != nil {
		t.Fatalf("Destroy: %v", err)
	}
	if _, err := os.Stat(info.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch dir holding only AO-listed entries still exists or stat failed differently: %v", err)
	}
}

// A marker too large to read whole claims nothing, so a truncated last line
// can never be parsed as a shorter name that matches a user's file.
func TestWorkspaceDestroyIgnoresOversizedAOGitignore(t *testing.T) {
	ws, info := createStandaloneScratch(t)
	padding := strings.Repeat("/ao-padding-entry\n", 64*1024/len("/ao-padding-entry\n")+1)
	writeScratchFile(t, filepath.Join(info.Path, ".gitignore"), hookutil.GitignoreSentinel+"\n/.gitignore\n/notes.md\n"+padding)
	writeScratchFile(t, filepath.Join(info.Path, "notes.md"), "mine\n")

	if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
		t.Fatalf("Destroy err = %v, want ErrWorkspaceDirty", err)
	}
	if _, err := os.Stat(filepath.Join(info.Path, "notes.md")); err != nil {
		t.Fatalf("file named only by an oversized marker was not preserved: %v", err)
	}
}

// memcode keeps its native transcript under a directory its AO .gitignore
// lists. Restore reads it back, so archiving keeps the folder once it holds
// one, and still removes an empty transcript directory. A delete's teardown
// stages the transcript instead: the delete discards it once the session row
// is gone, or puts it back when the delete fails.
func TestWorkspaceDestroyKeepsRestorableAgentState(t *testing.T) {
	setup := func(t *testing.T) (*scratch.Workspace, ports.WorkspaceInfo, string) {
		ws, info := createStandaloneScratch(t)
		dir := filepath.Join(info.Path, ".memcode")
		if err := hookutil.EnsureWorkspaceGitignore(dir, "hooks.json", "sessions/"); err != nil {
			t.Fatalf("EnsureWorkspaceGitignore: %v", err)
		}
		writeScratchFile(t, filepath.Join(dir, "hooks.json"), "{}\n")
		if err := os.MkdirAll(filepath.Join(dir, "sessions", "native-1"), 0o750); err != nil {
			t.Fatal(err)
		}
		return ws, info, filepath.Join(dir, "sessions", "native-1", "messages.json")
	}
	// The managed root holds standalone/sessions/<id> and the staging root.
	stagingRoot := func(info ports.WorkspaceInfo) string {
		return filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(info.Path))), ".ao-staged-agent-state")
	}
	assertGone := func(t *testing.T, path, what string) {
		t.Helper()
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s still exists or stat failed differently: %v", what, err)
		}
	}
	deleteTeardown := func(t *testing.T, ws *scratch.Workspace, info ports.WorkspaceInfo) {
		t.Helper()
		info.ReleaseAgentState = true
		if err := ws.Destroy(context.Background(), info); err != nil {
			t.Fatalf("deleting Destroy: %v", err)
		}
		assertGone(t, info.Path, "scratch dir of a deleting teardown")
	}

	t.Run("archived with a transcript", func(t *testing.T) {
		ws, info, transcript := setup(t)
		writeScratchFile(t, transcript, "[]\n")
		if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
			t.Fatalf("Destroy err = %v, want ErrWorkspaceDirty", err)
		}
		if _, err := os.Stat(transcript); err != nil {
			t.Fatalf("memcode transcript was not preserved: %v", err)
		}
	})

	t.Run("deleted", func(t *testing.T) {
		ws, info, transcript := setup(t)
		writeScratchFile(t, transcript, "[]\n")
		deleteTeardown(t, ws, info)
		if err := ws.DiscardAgentState(context.Background(), info); err != nil {
			t.Fatalf("DiscardAgentState: %v", err)
		}
		assertGone(t, stagingRoot(info), "staging root after the discard")
	})

	t.Run("delete failed", func(t *testing.T) {
		ws, info, transcript := setup(t)
		writeScratchFile(t, transcript, "[]\n")
		deleteTeardown(t, ws, info)
		restored, err := ws.RestoreAgentState(context.Background(), info)
		if err != nil || !restored {
			t.Fatalf("RestoreAgentState = %v, %v; want the transcript put back", restored, err)
		}
		if data, err := os.ReadFile(transcript); err != nil || string(data) != "[]\n" {
			t.Fatalf("restored transcript = %q, %v", data, err)
		}
		assertGone(t, stagingRoot(info), "staging root after the restore")
		// The restored transcript is still AO's agent state: archiving keeps
		// the folder for it rather than treating it as user work or deleting it.
		if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
			t.Fatalf("archive after restore err = %v, want ErrWorkspaceDirty", err)
		}
		if _, err := os.Stat(transcript); err != nil {
			t.Fatalf("restored transcript was not preserved by the archive: %v", err)
		}
		info.ReleaseAgentState = true
		if err := ws.Destroy(context.Background(), info); err != nil {
			t.Fatalf("retried delete teardown: %v", err)
		}
	})

	t.Run("session restored after an interrupted delete", func(t *testing.T) {
		ws, info, transcript := setup(t)
		writeScratchFile(t, transcript, "[]\n")
		deleteTeardown(t, ws, info)
		if _, err := ws.Restore(context.Background(), ports.WorkspaceConfig{Path: info.Path, SessionID: info.SessionID}); err != nil {
			t.Fatalf("Restore: %v", err)
		}
		if _, err := os.Stat(transcript); err != nil {
			t.Fatalf("Restore did not put the staged transcript back: %v", err)
		}
	})

	t.Run("deleted with user work", func(t *testing.T) {
		ws, info, transcript := setup(t)
		writeScratchFile(t, transcript, "[]\n")
		writeScratchFile(t, filepath.Join(info.Path, "notes.md"), "mine\n")
		info.ReleaseAgentState = true
		if err := ws.Destroy(context.Background(), info); !errors.Is(err, ports.ErrWorkspaceDirty) {
			t.Fatalf("Destroy err = %v, want ErrWorkspaceDirty", err)
		}
		for _, kept := range []string{transcript, filepath.Join(info.Path, "notes.md")} {
			if _, err := os.Stat(kept); err != nil {
				t.Fatalf("%s was not preserved: %v", kept, err)
			}
		}
		assertGone(t, stagingRoot(info), "staging root of a refused teardown")
	})

	t.Run("empty", func(t *testing.T) {
		ws, info, _ := setup(t)
		if err := ws.Destroy(context.Background(), info); err != nil {
			t.Fatalf("Destroy: %v", err)
		}
		assertGone(t, info.Path, "scratch dir with an empty transcript directory")
	})
}

// A deleting teardown that fails after staging the transcript puts it back
// itself, so the session keeps a conversation it can restore.
func TestWorkspaceDestroyRestoresStagedAgentStateWhenTeardownFails(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs POSIX directory permissions that bind the test user")
	}
	ws, info := createStandaloneScratch(t)
	if err := (&claudecode.Plugin{}).GetAgentHooks(context.Background(), ports.WorkspaceHookConfig{WorkspacePath: info.Path}); err != nil {
		t.Fatalf("install claude hooks: %v", err)
	}
	memcode := filepath.Join(info.Path, ".memcode")
	if err := hookutil.EnsureWorkspaceGitignore(memcode, "sessions/"); err != nil {
		t.Fatalf("EnsureWorkspaceGitignore: %v", err)
	}
	transcript := filepath.Join(memcode, "sessions", "native-1", "messages.json")
	writeScratchFile(t, transcript, "[]\n")
	// AO's hook file can no longer be removed, so teardown fails after the
	// transcript was staged.
	locked := filepath.Join(info.Path, ".claude")
	if err := os.Chmod(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) })

	info.ReleaseAgentState = true
	if err := ws.Destroy(context.Background(), info); err == nil {
		t.Fatal("Destroy succeeded although an AO file could not be removed")
	}
	if data, err := os.ReadFile(transcript); err != nil || string(data) != "[]\n" {
		t.Fatalf("transcript after a failed teardown = %q, %v; want it put back", data, err)
	}
	if restored, err := ws.RestoreAgentState(context.Background(), info); err != nil || restored {
		t.Fatalf("RestoreAgentState = %v, %v; want nothing left staged", restored, err)
	}
}
