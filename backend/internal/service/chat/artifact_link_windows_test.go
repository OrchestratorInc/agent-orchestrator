package chat_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// linkArtifactOutside 用真实 junction 覆盖 os.Root 的目录越界链接拒绝，
// 不依赖 Windows 创建文件 symlink 所需的系统权限。
func linkArtifactOutside(t *testing.T, dir, outside string) string {
	t.Helper()
	link := filepath.Join(dir, "escape")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "powershell.exe", "-NoProfile", "-NonInteractive", "-Command",
		`$ErrorActionPreference = "Stop"; New-Item -ItemType Junction -Path $env:AO_TEST_ARTIFACT_LINK -Target $env:AO_TEST_ARTIFACT_TARGET | Out-Null`)
	cmd.Env = append(os.Environ(), "AO_TEST_ARTIFACT_LINK="+link, "AO_TEST_ARTIFACT_TARGET="+filepath.Dir(outside))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("create artifact junction: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if err := os.Remove(link); err != nil {
			t.Errorf("remove artifact junction: %v", err)
		}
	})
	rel := "escape/" + filepath.Base(outside)
	linked, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	target, err := os.Stat(outside)
	if err != nil {
		t.Fatal(err)
	}
	if !linked.Mode().IsRegular() || !os.SameFile(linked, target) {
		t.Fatal("artifact junction does not reach the outside HTML file")
	}
	return rel
}
