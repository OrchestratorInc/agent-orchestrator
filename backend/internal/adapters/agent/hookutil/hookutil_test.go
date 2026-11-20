package hookutil

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

func TestEnsureWorkspaceGitignoreWritesSelfIgnoringFile(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".codex")
	if err := EnsureWorkspaceGitignore(dir, "hooks.json", "config.toml"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if !strings.Contains(content, GitignoreSentinel) {
		t.Fatalf("content missing sentinel: %q", content)
	}
	// Entries are anchored so only AO's files in THIS directory are ignored —
	// an agent's own files (even in the same dir) must keep counting as dirt.
	for _, want := range []string{"/.gitignore\n", "/hooks.json\n", "/config.toml\n"} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing entry %q: %q", want, content)
		}
	}
}

func TestEnsureWorkspaceGitignoreIsIdempotent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".codex")
	if err := EnsureWorkspaceGitignore(dir, "hooks.json"); err != nil {
		t.Fatalf("first ensure: %v", err)
	}
	first, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if err := EnsureWorkspaceGitignore(dir, "hooks.json"); err != nil {
		t.Fatalf("second ensure: %v", err)
	}
	second, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("rewrite changed content:\nfirst:  %q\nsecond: %q", first, second)
	}
}

func TestFileExistsIgnoresExecBit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-binary")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// FileExists must return true for a non-executable regular file — Cline
	// uses it as a do-not-clobber guard for existing user hook files.
	if !FileExists(path) {
		t.Fatalf("FileExists returned false for non-executable regular file")
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if !FileExists(path) {
		t.Fatalf("FileExists returned false for executable regular file")
	}
}

func TestIsExecutableFileChecksExecBit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("exec-bit check is skipped on Windows")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-binary")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// IsExecutableFile must return false for a non-executable regular file.
	if IsExecutableFile(path) {
		t.Fatalf("IsExecutableFile returned true for non-executable file")
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if !IsExecutableFile(path) {
		t.Fatalf("IsExecutableFile returned false for executable file")
	}
}

func TestEnsureWorkspaceGitignoreLeavesForeignFileUntouched(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".codex")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	foreign := "# user rules\n*.log\n"
	path := filepath.Join(dir, ".gitignore")
	if err := os.WriteFile(path, []byte(foreign), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := EnsureWorkspaceGitignore(dir, "hooks.json"); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(data) != foreign {
		t.Fatalf("foreign .gitignore was modified: %q", data)
	}
}

func TestBugTriageSkillInstallsCleanlyAndUninstalls(t *testing.T) {
	workspace := t.TempDir()
	if out, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	skillsDir := filepath.Join(workspace, ".harness", "skills")
	// The using-ao install owns this .gitignore and rewrites it with only its marker.
	if err := EnsureWorkspaceGitignore(skillsDir, ".using-ao.ao-managed"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := InstallBugTriageSkill(skillsDir); err != nil {
			t.Fatal(err)
		}
	}
	skill, err := os.ReadFile(filepath.Join(skillsDir, "bug-triage", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(skill, skillassets.BugTriageSkill()) {
		t.Fatal("installed skill differs from the embedded one")
	}
	// Untracked files here would make git worktree remove refuse at teardown.
	status, err := exec.Command("git", "-C", workspace, "status", "--porcelain", "--untracked-files=all").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v: %s", err, status)
	}
	if len(status) != 0 {
		t.Fatalf("install left untracked files:\n%s", status)
	}
	if err := UninstallBugTriageSkill(skillsDir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(skillsDir, "bug-triage")); !os.IsNotExist(err) {
		t.Fatalf("skill survived uninstall: %v", err)
	}
}

func TestBugTriageSkillRepairsAnInterruptedInstall(t *testing.T) {
	for name, marker := range map[string]bool{"marker without skill": true, "empty folder": false, "temp file only": false} {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			if out, err := exec.Command("git", "-C", workspace, "init", "-q").CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			// Pattern characters in the project path must not change which files match.
			skillsDir := filepath.Join(workspace, "notes[draft", "skills")
			skillDir := filepath.Join(skillsDir, "bug-triage")
			if err := os.MkdirAll(skillDir, 0o750); err != nil {
				t.Fatal(err)
			}
			if marker {
				if err := os.WriteFile(filepath.Join(skillDir, bugTriageMarkerFile), []byte(bugTriageSentinel+"\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if name == "temp file only" {
				if err := os.WriteFile(filepath.Join(skillDir, ".ao-tmp-123"), nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := InstallBugTriageSkill(skillsDir); err != nil {
				t.Fatal(err)
			}
			skill, err := os.ReadFile(filepath.Join(skillDir, "SKILL.md"))
			if err != nil || !bytes.Equal(skill, skillassets.BugTriageSkill()) {
				t.Fatalf("interrupted install not repaired: err=%v", err)
			}
			if managed, err := bugTriageManaged(skillDir); err != nil || !managed {
				t.Fatalf("repaired skill is not marked as AO's: managed=%v err=%v", managed, err)
			}
			// An interrupted write's temp file must not stay behind as dirt.
			status, err := exec.Command("git", "-C", workspace, "status", "--porcelain", "--untracked-files=all").CombinedOutput()
			if err != nil || len(status) != 0 {
				t.Fatalf("repair left untracked files: %v\n%s", err, status)
			}
		})
	}
}

func TestBugTriageSkillLeavesAProjectsOwnSkill(t *testing.T) {
	skillsDir := t.TempDir()
	own := filepath.Join(skillsDir, "bug-triage", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(own), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(own, []byte("project skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := InstallBugTriageSkill(skillsDir); err != nil {
		t.Fatalf("a project's own skill must not stop the session: %v", err)
	}
	if err := UninstallBugTriageSkill(skillsDir); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(own); err != nil || string(data) != "project skill" {
		t.Fatalf("project skill changed: data=%q err=%v", data, err)
	}
}
