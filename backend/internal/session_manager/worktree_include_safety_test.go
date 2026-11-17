package sessionmanager

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestCopyWorktreeIncludeRejectsEscapingDestination(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows symlink creation requires a host privilege outside this unit test")
	}
	project := newManagerGitRepo(t)
	writeTestFile(t, filepath.Join(project, ".gitignore"), "config/\n")
	runManagerGit(t, project, "add", ".gitignore")
	runManagerGit(t, project, "commit", "-m", "ignore private configuration")
	writeTestFile(t, filepath.Join(project, ".worktreeinclude"), "config/\n")
	writeTestFile(t, filepath.Join(project, "config", "secrets.json"), "private configuration")
	workspace, adapter := newIncludedWorktree(t, project)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "config")); err != nil {
		t.Fatal(err)
	}
	if err := copyWorktreeInclude(context.Background(), domain.ProjectRecord{Path: project}, workspace, adapter); err == nil {
		t.Error("copy through an escaping directory symlink succeeded")
	}
	if _, err := os.Lstat(filepath.Join(outside, "secrets.json")); !os.IsNotExist(err) {
		t.Fatalf("file appeared outside the workspace: %v", err)
	}
}

type worktreeIncludeReader func([]byte) (int, error)

func (r worktreeIncludeReader) Read(p []byte) (int, error) { return r(p) }

func TestWorktreeIncludeCopyPublishesCompleteFiles(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "complete"
		if fail {
			name = "interrupted read"
		}
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			target, err := os.OpenRoot(workspace)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = target.Close() }()
			readErr := errors.New("interrupted source")
			src := worktreeIncludeReader(func(p []byte) (int, error) {
				if _, err := target.Lstat(".env"); !os.IsNotExist(err) {
					t.Error("final file is visible before copying finishes")
				}
				if fail {
					return copy(p, "partial"), readErr
				}
				return copy(p, "complete"), io.EOF
			})
			err = publishWorktreeIncludeFile(target, ".env", src, 0o600, ".ao-include-test")
			if fail {
				if !errors.Is(err, readErr) {
					t.Fatalf("copy error = %v, want source error", err)
				}
				if entries, err := os.ReadDir(workspace); err != nil || len(entries) != 0 {
					t.Fatalf("failed copy left files: %v, %v", entries, err)
				}
				// A retry must copy the full file rather than accept a partial one.
				err = publishWorktreeIncludeFile(target, ".env", strings.NewReader("complete"), 0o600, ".ao-include-test")
			}
			if err != nil {
				t.Fatal(err)
			}
			if got, err := target.ReadFile(".env"); err != nil || string(got) != "complete" {
				t.Fatalf("published file = %q, %v", got, err)
			}
			if entries, err := os.ReadDir(workspace); err != nil || len(entries) != 1 || entries[0].Name() != ".env" {
				t.Fatalf("temporary copy was not removed: %v, %v", entries, err)
			}
		})
	}
}

func TestWorktreeIncludeCopyKeepsFileCreatedDuringCopy(t *testing.T) {
	target, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	src := worktreeIncludeReader(func(p []byte) (int, error) {
		if err := target.WriteFile(".env", []byte("session edit"), 0o600); err != nil {
			t.Fatal(err)
		}
		return copy(p, "source"), io.EOF
	})
	if err := publishWorktreeIncludeFile(target, ".env", src, 0o600, ".ao-include-test"); err != nil {
		t.Fatal(err)
	}
	if got, err := target.ReadFile(".env"); err != nil || string(got) != "session edit" {
		t.Fatalf("workspace file was overwritten: %q, %v", got, err)
	}
}

func TestCopyNewRegularFileReportsSourceError(t *testing.T) {
	source, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	target, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = target.Close() }()
	if err := copyNewRegularFile(context.Background(), source, target, "missing", ".ao-include-test"); !os.IsNotExist(err) {
		t.Fatalf("source error = %v, want missing file", err)
	}
}
