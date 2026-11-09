package deepseekharness

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// TestMain 的子进程模式模拟 Desktop runtime，返回真实收到的参数和环境。
func TestMain(m *testing.M) {
	if os.Getenv("AO_DESKTOP_ARGV_HELPER") == "1" {
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Args []string
			Node string
		}{os.Args[1:], os.Getenv("ELECTRON_RUN_AS_NODE")})
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// TestDesktopLaunchPreservesRuntimeAndLongPrompt 从公开终端启动入口验证 Desktop 参数与环境不被 npm shim 转换吞掉。
func TestDesktopLaunchPreservesRuntimeAndLongPrompt(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Desktop With Spaces")
	binDir := filepath.Join(root, "resources", "runtime", "cli", "bin")
	if err := os.MkdirAll(binDir, 0o700); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(root, "DeepSeek Harness.exe")
	archive := filepath.Join(root, "resources", "app.asar")
	shim := filepath.Join(binDir, "dsh.cmd")
	for _, file := range []string{exe, archive} {
		if err := os.WriteFile(file, []byte("fixture"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	body := "@echo off\r\nsetlocal DisableDelayedExpansion\r\nset \"ELECTRON_RUN_AS_NODE=1\"\r\n\"%~dp0..\\..\\..\\..\\DeepSeek Harness.exe\" --expose-internals \"%~dp0..\\..\\..\\app.asar\\dsh\\node_modules\\@deepseek-ai\\dsh-desktop-host\\lib\\cli.js\" %*\r\nexit /b %errorlevel%\r\n"
	if err := os.WriteFile(shim, []byte(body), 0o700); err != nil {
		t.Fatal(err)
	}
	p := &Plugin{resolvedBinary: shim}
	prompt := strings.Repeat("long prompt <&|>\n", 700)
	argv, err := p.GetLaunchCommand(t.Context(), ports.LaunchConfig{Prompt: prompt})
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{"DSH_HOME": "existing-profile", "PATH": "unchanged"}
	command, err := p.PrepareCommand(t.Context(), argv, env)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{exe, "--expose-internals", filepath.Join(archive, "dsh", "node_modules", "@deepseek-ai", "dsh-desktop-host", "lib", "cli.js"), "--profile", "headless", prompt}
	if !reflect.DeepEqual(command, want) {
		t.Fatalf("command prefix = %v, expected %v; prompt preserved=%v", command[:len(command)-1], want[:len(want)-1], command[len(command)-1] == prompt)
	}
	if env["ELECTRON_RUN_AS_NODE"] != "1" || env["DSH_HOME"] != "existing-profile" || env["PATH"] != "unchanged" {
		t.Fatalf("env = %v", env)
	}
	if runtime.GOOS == "windows" {
		self, err := os.ReadFile(os.Args[0])
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(exe, self, 0o700); err != nil {
			t.Fatal(err)
		}
		child := process.CommandContext(t.Context(), command[0], command[1:]...)
		child.Env = append(os.Environ(), "AO_DESKTOP_ARGV_HELPER=1", "ELECTRON_RUN_AS_NODE="+env["ELECTRON_RUN_AS_NODE"])
		out, err := child.Output()
		if err != nil {
			t.Fatalf("start Desktop runtime with long prompt: %v", err)
		}
		var received struct {
			Args []string
			Node string
		}
		if err := json.Unmarshal(out, &received); err != nil || !reflect.DeepEqual(received.Args, command[1:]) || received.Node != "1" {
			t.Fatalf("Desktop process did not receive argv/environment intact: %v", err)
		}
	}
	if err := os.Remove(archive); err != nil {
		t.Fatal(err)
	}
	if _, err := p.PrepareCommand(t.Context(), argv, env); err == nil {
		t.Fatal("missing Desktop archive accepted")
	}
}
