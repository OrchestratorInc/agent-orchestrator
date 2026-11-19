//go:build !windows

package gitworktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func physicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func writeAssetFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCopyWorkspaceAssetsSkipsEntriesItCannotCopySafely(t *testing.T) {
	root := physicalTempDir(t)
	dest := physicalTempDir(t)
	notes := filepath.Join(root, "notes")
	writeAssetFile(t, filepath.Join(notes, "keep.txt"), "keep")
	writeAssetFile(t, filepath.Join(notes, "node_modules", "pkg", "index.js"), "dependency")
	for name, target := range map[string]string{
		"inner":  "keep.txt",
		"abs":    "/etc/hosts",
		"escape": "../../outside",
		"broken": "missing.txt",
	} {
		if err := os.Symlink(target, filepath.Join(notes, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mkfifo(filepath.Join(notes, "pipe"), 0o644); err != nil {
		t.Fatal(err)
	}

	ws := &Workspace{}
	err := ws.copyWorkspaceAssets(context.Background(), root, dest, []ports.WorkspaceProjectAssetConfig{{RelativePath: "notes", SourcePath: notes}})
	if err != nil {
		t.Fatalf("copy assets: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dest, "notes", "keep.txt")); err != nil || string(got) != "keep" {
		t.Fatalf("keep.txt = %q, %v", got, err)
	}
	if target, err := os.Readlink(filepath.Join(dest, "notes", "inner")); err != nil || target != "keep.txt" {
		t.Fatalf("inner symlink = %q, %v", target, err)
	}
	for _, skipped := range []string{"abs", "escape", "broken", "pipe", "node_modules"} {
		if _, err := os.Lstat(filepath.Join(dest, "notes", skipped)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was copied (lstat err %v), want skipped", skipped, err)
		}
	}
}

func TestCopyWorkspaceAssetsFailsNamingAFolderOverTheSizeLimit(t *testing.T) {
	previous := workspaceAssetSizeLimit
	workspaceAssetSizeLimit = 8
	t.Cleanup(func() { workspaceAssetSizeLimit = previous })
	root := physicalTempDir(t)
	dest := physicalTempDir(t)
	data := filepath.Join(root, "data")
	writeAssetFile(t, filepath.Join(data, "big.bin"), "sixteen bytes!!!")

	ws := &Workspace{}
	err := ws.copyWorkspaceAssets(context.Background(), root, dest, []ports.WorkspaceProjectAssetConfig{{RelativePath: "data", SourcePath: data}})
	if !errors.Is(err, errWorkspaceAssetTooLarge) || !strings.Contains(err.Error(), `"data"`) {
		t.Fatalf("copy over the limit = %v, want a too-large error naming data", err)
	}
	if _, statErr := os.Lstat(filepath.Join(dest, "data")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("oversized folder was partly written: %v", statErr)
	}
}

func TestCopyWorkspaceAssetsStopsWhenCancelled(t *testing.T) {
	root := physicalTempDir(t)
	dest := physicalTempDir(t)
	notes := filepath.Join(root, "notes")
	writeAssetFile(t, filepath.Join(notes, "a.txt"), "a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ws := &Workspace{}
	err := ws.copyWorkspaceAssets(ctx, root, dest, []ports.WorkspaceProjectAssetConfig{{RelativePath: "notes", SourcePath: notes}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled copy = %v, want context.Canceled", err)
	}
}

func TestCopyWorkspaceAssetsSkipsDependencyDotAndMissingFolders(t *testing.T) {
	root := physicalTempDir(t)
	dest := physicalTempDir(t)
	venv := filepath.Join(root, ".venv")
	modules := filepath.Join(root, "node_modules")
	writeAssetFile(t, filepath.Join(venv, "pyvenv.cfg"), "home = /usr/bin")
	writeAssetFile(t, filepath.Join(modules, "pkg", "index.js"), "dependency")

	ws := &Workspace{}
	err := ws.copyWorkspaceAssets(context.Background(), root, dest, []ports.WorkspaceProjectAssetConfig{
		{RelativePath: ".venv", SourcePath: venv},
		{RelativePath: "node_modules", SourcePath: modules},
		{RelativePath: "gone", SourcePath: filepath.Join(root, "gone")},
	})
	if err != nil {
		t.Fatalf("copy assets: %v", err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("destination = %v, want nothing copied", entries)
	}
}
