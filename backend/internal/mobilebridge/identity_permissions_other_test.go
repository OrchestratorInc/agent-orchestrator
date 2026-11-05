//go:build !windows

package mobilebridge

import (
	"os"
	"testing"
)

// assertIdentityFilePrivate 保留真实 POSIX 0600 权限断言。
func assertIdentityFilePrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("identity file mode %o, want 600", info.Mode().Perm())
	}
}
