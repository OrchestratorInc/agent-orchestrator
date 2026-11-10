package gitworktree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestWorkspaceAssetsSkipLockedSQLiteFamily 用任意目录名和真实字节锁验证索引整组跳过，普通资料与静态数据库仍复制。
func TestWorkspaceAssetsSkipLockedSQLiteFamily(t *testing.T) {
	root, destination := t.TempDir(), t.TempDir()
	asset := filepath.Join(root, "custom-search-tool")
	if err := os.Mkdir(asset, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"index":         "SQLite format 3\x00active index",
		"index-wal":     "pending writes",
		"index-shm":     "shared state",
		"index-journal": "journal state",
		"fixture.db":    "SQLite format 3\x00static fixture",
		"guide.md":      "search instructions",
		"notes":         "ordinary document with a companion",
		"notes-wal":     "ordinary document",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(asset, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	locked, err := os.OpenFile(filepath.Join(asset, "index-shm"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locked.Close() })
	if err := windows.LockFileEx(windows.Handle(locked.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &windows.Overlapped{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.ReadFile(locked.Name()); !errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		t.Fatalf("expected source byte lock, got %v", err)
	}
	if err := copyWorkspaceAssets(root, destination, []ports.WorkspaceProjectAssetConfig{{RelativePath: "custom-search-tool", SourcePath: asset}}); err != nil {
		t.Fatalf("copy assets with locked SQLite state: %v", err)
	}
	for _, name := range []string{"index", "index-wal", "index-shm", "index-journal"} {
		if _, err := os.Lstat(filepath.Join(destination, "custom-search-tool", name)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("live database member %q was copied: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(asset, name)); err != nil {
			t.Errorf("source database member %q lost: %v", name, err)
		}
	}
	for _, name := range []string{"fixture.db", "guide.md", "notes", "notes-wal"} {
		got, err := os.ReadFile(filepath.Join(destination, "custom-search-tool", name))
		if err != nil || string(got) != files[name] {
			t.Errorf("copied file %q = %q, %v", name, got, err)
		}
	}
}
