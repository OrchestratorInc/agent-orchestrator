package local

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestProbeRejectsRevisionDefaultDaemonInsteadOfOwnedBinary(t *testing.T) {
	f := fixture(t)
	f.ready["executablePath"] = filepath.Join(f.s.frontend, "daemon", "ao")
	if err := f.a.Probe(context.Background(), f.s.target); err == nil || !strings.Contains(err.Error(), "identity mismatch") {
		t.Fatalf("checkout daemon was admitted: %v", err)
	}
}

func TestUnsupportedRevisionFailsBeforeElectronAndCleansSetup(t *testing.T) {
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(home, ".ao", "dev", "agentic-target")
	checkout := filepath.Join(base, "checkout")
	if err := os.MkdirAll(filepath.Join(checkout, "frontend"), 0o700); err != nil {
		t.Fatal(err)
	}
	a := New()
	a.ops.home = func() (string, error) { return home, nil }
	a.ops.tmuxBinary = func(string) (string, error) { return "/fixture/tmux", nil }
	a.ops.prepare = func(_ context.Context, _, _, root string) error {
		if err := os.Mkdir(filepath.Join(root, "setup"), 0o700); err != nil {
			return err
		}
		return errors.New("unsupported_revision: original launch contract error")
	}
	a.ops.start = func(string, string, []string, *os.File) (int, error) {
		t.Fatal("unsupported revision launched Electron")
		return 0, nil
	}
	_, err = a.Start(context.Background(), ports.TestingTargetSpec{AttemptID: "unsupported", Generation: 1, CheckoutPath: checkout, CommitSHA: strings.Repeat("a", 40), Deadline: time.Now().Add(time.Minute)})
	if err == nil || err.Error() != "unsupported_revision: original launch contract error" {
		t.Fatalf("preflight cause lost: %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(base, "unsupported"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("private setup left behind: %v, %v", entries, err)
	}
}

func TestOwnedDaemonCommandHandlesQuotedPaths(t *testing.T) {
	if got := daemonCommand("/owned path/it's ao"); got != "exec '/owned path/it'\"'\"'s ao' daemon" {
		t.Fatalf("daemon override = %q", got)
	}
}

func TestOwnedBuildPreservesCheckoutAndCompilerError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("local macOS target uses POSIX tools")
	}
	for _, compilerFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "module ignores workspace", true: "compiler diagnostic"}[compilerFailure], func(t *testing.T) {
			root := t.TempDir()
			checkout := filepath.Join(root, "checkout")
			frontend := filepath.Join(checkout, "frontend")
			for _, dir := range []string{"attempt", "checkout/backend/cmd/ao", "checkout/cloud"} {
				if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			files := map[string]string{
				"go.work":                "go 1.21\nuse (\n./backend\n./cloud\n)\n",
				"go.work.sum":            "example.org/unused v1.0.0/go.mod h1:J7Y8YcW2NihsgmVo/mv3lAwl/skON4iLHjSsI+c5H38=\n",
				"backend/go.mod":         "module example.org/backend\ngo 1.21\n",
				"backend/go.sum":         "",
				"backend/cmd/ao/main.go": "package main\nfunc main() {}\n",
				"cloud/go.mod":           "invalid workspace sibling\n",
			}
			for name, data := range files {
				if err := os.WriteFile(filepath.Join(checkout, name), []byte(data), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			t.Setenv("GOWORK", filepath.Join(checkout, "go.work"))
			if compilerFailure {
				tool := filepath.Join(root, "go")
				if err := os.WriteFile(tool, []byte("#!/bin/sh\necho 'compiler diagnostic from pinned revision' >&2\nexit 19\n"), 0o700); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
			}
			executable := filepath.Join(root, "attempt", "daemon", "ao")
			err := buildOwnedDaemon(t.Context(), frontend, executable)
			if compilerFailure {
				if err == nil || !strings.Contains(err.Error(), "compiler diagnostic from pinned revision") || !strings.Contains(err.Error(), "exit status 19") {
					t.Fatalf("compiler cause was lost: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if _, err := os.Stat(executable); err != nil {
				t.Fatal(err)
			}
			for name, expected := range files {
				data, err := os.ReadFile(filepath.Join(checkout, name))
				if err != nil || string(data) != expected {
					t.Fatalf("build modified %s: %v", name, err)
				}
			}
		})
	}
}
