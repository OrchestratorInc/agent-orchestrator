//go:build !windows

package pricing

import (
	"os"
	"testing"
)

// assertPrivateCachePath 保留非 Windows 平台实际生效的 POSIX 权限断言。
func assertPrivateCachePath(t *testing.T, path string, directory bool) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := os.FileMode(0o600)
	if directory {
		want = 0o700
	}
	if info.IsDir() != directory || info.Mode().Perm() != want {
		t.Fatalf("%s mode = %v, want directory=%t permissions=%o", path, info.Mode(), directory, want)
	}
}
