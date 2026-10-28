package workerexec

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func testCodex(home string, onPath map[string]string, versions map[string]string, install func(context.Context, string, string) error) *claudeCode {
	code := testClaudeCode(home, onPath, versions, install)
	code.spec = codexSpec
	return code
}

func TestCodexKeepsAWorkspaceInstallOfThePinnedRelease(t *testing.T) {
	workspace := filepath.Join(t.TempDir(), "usr", "bin", "codex")
	writeExecutable(t, workspace)
	codex := testCodex(t.TempDir(),
		map[string]string{"codex": workspace, "npm": "/usr/bin/npm"},
		map[string]string{workspace: CodexVersion},
		func(context.Context, string, string) error {
			t.Fatal("must not install over the pinned release")
			return nil
		})
	if path := codex.ensure(context.Background()); path != workspace {
		t.Fatalf("ensure = %q, want the workspace install", path)
	}
}

// Codex's app-server protocol changes between releases, so a template's newer
// release is not used either: the worker installs the pinned one beside it.
func TestCodexInstallsThePinnedReleaseOverANewerWorkspaceInstall(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".ao", "tools", "bin", "codex")
	versions := map[string]string{"/usr/bin/codex": "0.164.0"}
	var installed string
	codex := testCodex(home,
		map[string]string{"codex": "/usr/bin/codex", "npm": "/usr/bin/npm"},
		versions,
		func(_ context.Context, npm, prefix string) error {
			installed = prefix
			writeExecutable(t, local)
			versions[local] = CodexVersion
			return nil
		})
	if path := codex.ensure(context.Background()); path != local {
		t.Fatalf("ensure = %q, want the pinned %q", path, local)
	}
	if installed != filepath.Join(home, ".ao", "tools") {
		t.Fatalf("installed into %q, want the worker's own prefix", installed)
	}
}

// A template that installs its tools in the background has no Codex when the
// worker starts; the worker installs its own rather than waiting for it.
func TestCodexInstallsWhenTheWorkspaceHasNone(t *testing.T) {
	home := t.TempDir()
	local := filepath.Join(home, ".ao", "tools", "bin", "codex")
	versions := map[string]string{}
	codex := testCodex(home,
		map[string]string{"npm": "/usr/bin/npm"},
		versions,
		func(context.Context, string, string) error {
			writeExecutable(t, local)
			versions[local] = CodexVersion
			return nil
		})
	if path := codex.ensure(context.Background()); path != local {
		t.Fatalf("ensure = %q, want the worker's install %q", path, local)
	}
	if codex.current() != local {
		t.Fatalf("current = %q, want the worker's install", codex.current())
	}
}

// Without npm the worker cannot install, so it launches whatever the workspace has.
func TestCodexFallsBackToTheWorkspaceInstallWithoutNPM(t *testing.T) {
	codex := testCodex(t.TempDir(),
		map[string]string{"codex": "/usr/bin/codex"},
		map[string]string{"/usr/bin/codex": "0.164.0"},
		func(context.Context, string, string) error {
			t.Fatal("must not install without npm")
			return nil
		})
	if path := codex.ensure(context.Background()); path != "codex" {
		t.Fatalf("ensure = %q, want the bare name on PATH", path)
	}
}

func TestCodexVersionOutputParses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\necho 'codex-cli 0.147.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	version, err := claudeCodeExecutableVersion(context.Background(), path)
	if err != nil || version != "0.147.0" {
		t.Fatalf("version = %q, %v; want 0.147.0", version, err)
	}
}

func TestEnsureHarnessBinaryCoversOnlyPinnedHarnesses(t *testing.T) {
	if _, ok := EnsureHarnessBinary(context.Background(), "cursor"); ok {
		t.Fatal("cursor is not pinned by the worker")
	}
	if PinnedHarnessReady("opencode") {
		t.Fatal("opencode is not pinned by the worker")
	}
}
