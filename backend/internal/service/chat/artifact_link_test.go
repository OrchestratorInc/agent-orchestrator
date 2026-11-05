//go:build !windows

package chat_test

import (
	"os"
	"path/filepath"
	"testing"
)

// linkArtifactOutside 在 artifact 目录内创建指向外部 HTML 的真实文件链接。
func linkArtifactOutside(t *testing.T, dir, outside string) string {
	t.Helper()
	if err := os.Symlink(outside, filepath.Join(dir, "escape.html")); err != nil {
		t.Fatal(err)
	}
	return "escape.html"
}
