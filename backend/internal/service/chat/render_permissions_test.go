//go:build !windows

package chat_test

import (
	"os"
	"testing"
)

// assertRenderArtifactPrivate 保留新 HTML 只允许用户读写的 POSIX 权限断言。
func assertRenderArtifactPrivate(t *testing.T, file string) {
	t.Helper()
	info, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
}
