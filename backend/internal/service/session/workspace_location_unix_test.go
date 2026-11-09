//go:build !windows

package session

import (
	"os"
	"testing"
)

// unreadableWorkspace 只撤销本用例目录的读取权限，清理前恢复。
func unreadableWorkspace(t *testing.T) string {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	path := t.TempDir()
	t.Cleanup(func() {
		if err := os.Chmod(path, 0o700); err != nil {
			t.Error(err)
		}
	})
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	return path
}
