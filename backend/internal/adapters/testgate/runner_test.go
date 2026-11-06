package testgate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestM4GateProcess 是受信本地子进程入口，不连接 Provider；测试失败注入必须产生非零退出。
func TestM4GateProcess(t *testing.T) {
	if len(os.Args) < 2 {
		return
	}
	mode := os.Args[len(os.Args)-1]
	switch mode {
	case "gate-success":
		if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("AO_DATA_DIR") == "" {
			os.Exit(12)
		}
		fmt.Print(strings.Repeat("x", 1024))
		os.Exit(0)
	case "gate-fail":
		fmt.Print("PASS")
		os.Exit(7)
	case "gate-timeout":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
}

// TestM4GateRunnerRealExitAndBoundaries 删除 exit/超时/环境隔离/完整日志摘要中的任一判断会使本例变红。
func TestM4GateRunnerRealExitAndBoundaries(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "must-not-reach-child")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := domain.GateRule{ID: "unit", Level: 1, Required: true, Executable: exe, Args: []string{"-test.run=^TestM4GateProcess$", "--", "gate-success"}, Timeout: 5 * time.Second, MaxLogBytes: 64}
	if root, ok := os.LookupEnv("SystemRoot"); ok {
		r.Env = map[string]string{"SystemRoot": root}
	}
	workspace := t.TempDir()
	if cwd, err := os.Getwd(); err == nil && filepath.VolumeName(cwd) != filepath.VolumeName(exe) {
		workspace, err = os.MkdirTemp(cwd, ".gate-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	}
	got := Run(context.Background(), r, workspace)
	want := sha256.Sum256([]byte(strings.Repeat("x", 1024)))
	if got.State != "PASS" || got.ExitCode == nil || *got.ExitCode != 0 || !got.Truncated || len(got.Log) != 64 || got.LogDigest != hex.EncodeToString(want[:]) {
		t.Fatalf("success=%+v", got)
	}
	r.Args[len(r.Args)-1] = "gate-fail"
	got = Run(context.Background(), r, workspace)
	if got.State != "FAIL" || got.ExitCode == nil || *got.ExitCode != 7 {
		t.Fatalf("fake PASS=%+v", got)
	}
	r.Args[len(r.Args)-1] = "gate-timeout"
	r.Timeout = 200 * time.Millisecond
	got = Run(context.Background(), r, workspace)
	if (got.State != "FAIL" && got.State != "UNVERIFIED") || got.Reason != "timeout_or_cancelled" {
		t.Fatalf("timeout=%+v", got)
	}
	r.Executable = filepath.Join(workspace, "missing.exe")
	got = Run(context.Background(), r, workspace)
	if got.State != "BLOCKED" || got.Reason != "tool_unavailable" {
		t.Fatalf("missing=%+v", got)
	}
	r.Executable = exe
	r.Args = []string{"-test.run=^TestM4GateProcess$", "--", "gate-success"}
	r.Timeout = time.Second
	p := domain.GatePolicy{ID: "trusted", Revision: 1, Rules: []domain.GateRule{r}, ValidFor: time.Hour, MaxFixes: 2}
	a := domain.ArtifactSnapshot{Fingerprint: strings.Repeat("a", 64)}
	before, err := Fingerprint(a, p)
	if err != nil {
		t.Fatal(err)
	}
	p.Rules[0].Args = append(p.Rules[0].Args, "different")
	after, err := Fingerprint(a, p)
	if err != nil || before == after {
		t.Fatalf("changed argv reused fingerprint: %v", err)
	}
	p.Rules[0].Env = map[string]string{"OPENAI_API_KEY": "forbidden"}
	if ValidatePolicy(p) == nil {
		t.Fatal("secret environment accepted")
	}
}

// TestM4GateAcceptedPathsCannotSkipRequiredRule 删除路径规范化/平台折叠会错误跳过必需规则；组件边界仍不能扩大。
func TestM4GateAcceptedPathsCannotSkipRequiredRule(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	a := domain.ArtifactSnapshot{Git: domain.GitArtifact{Files: []domain.ArtifactFile{{Path: "src/check.go"}}}}
	r := domain.GateRule{ID: "security", Level: 3, Required: true, Executable: exe, Timeout: time.Second, MaxLogBytes: 64}
	paths := []string{"src/", "./src"}
	if runtime.GOOS == "windows" {
		paths = append(paths, "SRC")
	}
	for _, path := range paths {
		r.WhenPaths = []string{path}
		p := domain.GatePolicy{ID: "trusted", Revision: 1, Rules: []domain.GateRule{r}, ValidFor: time.Hour, MaxFixes: 2}
		if err := ValidatePolicy(p); err != nil {
			t.Fatal(err)
		}
		if reason := r.SkipReason(a); reason != "" {
			t.Fatalf("accepted scope %q skipped required rule: %s", path, reason)
		}
		a.Git.Files[0].Path = "src-other/check.go"
		if reason := r.SkipReason(a); reason != "paths_not_changed" {
			t.Fatalf("component boundary scope %q: %s", path, reason)
		}
		a.Git.Files[0].Path = "src/check.go"
	}
	r.Platforms = []string{"not-current-platform"}
	if reason := r.SkipReason(a); reason != "platform_not_applicable" {
		t.Fatal(reason)
	}
}
