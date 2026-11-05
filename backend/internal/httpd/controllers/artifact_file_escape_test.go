//go:build !windows

package controllers_test

import (
	"os"
	"path/filepath"
	"testing"
)

// linkArtifactFileOutside 用真实文件 symlink 验证外部文件的拒绝边界。
func linkArtifactFileOutside(t *testing.T, dir, outside string) string {
	t.Helper()
	if err := os.Symlink(outside, filepath.Join(dir, "escape.html")); err != nil {
		t.Fatal(err)
	}
	return "escape.html"
}
