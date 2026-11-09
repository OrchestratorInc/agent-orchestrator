//go:build !windows

package agent

import (
	"os"
	"testing"
)

// assertPrivatePath 保留 Unix 上的真实权限和对象类型断言。
func assertPrivatePath(t *testing.T, path string, directory bool) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o600)
	if directory {
		want = 0o700
	}
	if info.IsDir() != directory || info.Mode().Perm() != want || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("%s mode=%v, want private directory=%t", path, info.Mode(), directory)
	}
}

func privatePathTestRoot(t *testing.T) string { t.Helper(); return t.TempDir() }
