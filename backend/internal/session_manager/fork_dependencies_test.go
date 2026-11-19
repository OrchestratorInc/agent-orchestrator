package sessionmanager

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func writeTestFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestForkClonesDependencies(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(src, "node_modules", "left-pad", "index.js"), "pad")
	writeTestFile(t, filepath.Join(src, "packages", "app", "node_modules", "react", "index.js"), "react")
	writeTestFile(t, filepath.Join(src, ".venv", "bin", "python"), "py")
	writeTestFile(t, filepath.Join(src, "node_modules", "nested", "node_modules", "deep.js"), "deep")
	writeTestFile(t, filepath.Join(src, "src", "main.go"), "package main")
	if err := os.MkdirAll(filepath.Join(dst, "packages", "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	cloned, err := cloneForkDependencies(context.Background(), src, dst)
	if errors.Is(err, errCloneUnsupported) {
		t.Skip("copy-on-write clones are not supported here")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"node_modules/left-pad/index.js", "packages/app/node_modules/react/index.js", ".venv/bin/python", "node_modules/nested/node_modules/deep.js"} {
		if _, err := os.Stat(filepath.Join(dst, rel)); err != nil {
			t.Fatalf("%s not cloned: %v", rel, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dst, "src", "main.go")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("only dependency folders are cloned; tracked files come from git")
	}
	if len(cloned) != 3 {
		t.Fatalf("cloned = %v", cloned)
	}
	// The clone is independent of the original.
	writeTestFile(t, filepath.Join(dst, "node_modules", "left-pad", "index.js"), "changed")
	if b, _ := os.ReadFile(filepath.Join(src, "node_modules", "left-pad", "index.js")); string(b) != "pad" {
		t.Fatalf("source changed: %q", b)
	}
}

func TestForkKeepsExistingDependencies(t *testing.T) {
	src, dst := t.TempDir(), t.TempDir()
	writeTestFile(t, filepath.Join(src, "node_modules", "a.js"), "source")
	writeTestFile(t, filepath.Join(dst, "node_modules", "a.js"), "fork")
	if _, err := cloneForkDependencies(context.Background(), src, dst); err != nil && !errors.Is(err, errCloneUnsupported) {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dst, "node_modules", "a.js")); string(b) != "fork" {
		t.Fatalf("an existing folder was replaced: %q", b)
	}
}

func TestForkSetupRunsWithAOFork(t *testing.T) {
	m, _, _, _ := newManager()
	ws := t.TempDir()
	project := domain.ProjectRecord{ID: "mer", Path: t.TempDir()}
	project.Config.PostCreate = []string{`printf '%s' "$AO_FORK" > fork-flag`}
	m.markForkWorkspace(ws)
	if err := m.provisionWorkspace(context.Background(), project, ws); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(ws, "fork-flag"))
	if strings.TrimSpace(string(b)) != "1" {
		t.Fatalf("AO_FORK = %q", b)
	}
	plain := t.TempDir()
	if err := m.provisionWorkspace(context.Background(), project, plain); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(plain, "fork-flag")); strings.TrimSpace(string(b)) != "" {
		t.Fatalf("a normal session got AO_FORK=%q", b)
	}
}
