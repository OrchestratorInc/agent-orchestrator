package controllers_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// linkArtifactFileOutside 用无需管理员权限的真实目录 junction 验证越界拒绝。
// 先证明链接确实到达外部普通文件，再交给生产 HTTP 文件路由检查。
func linkArtifactFileOutside(t *testing.T, dir, outside string) string {
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
