//go:build !windows

package fsbrowser

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// symlink 在 POSIX 上创建真实符号链接，失败直接使测试失败。
func symlink(t *testing.T, target, path string) {
	t.Helper()
	require.NoError(t, os.Symlink(target, path))
}
