package systemexec

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestProbeHonorsCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (Adapter{}).Probe(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Probe error = %v, want context canceled", err)
	}
}

// TestRunInstallClosesStdinAndSetsControlledEnvironment 用平台原生脚本验证关闭输入和子进程环境覆盖。
func TestRunInstallClosesStdinAndSetsControlledEnvironment(t *testing.T) {
	t.Parallel()
	argv := []string{"sh", "-c", `if IFS= read -r value; then echo "stdin:$value"; else echo "stdin:eof"; fi; printf 'ci:%s noninteractive:%s' "$CI" "$NONINTERACTIVE"`}
	if runtime.GOOS == "windows" {
		script := filepath.Join(t.TempDir(), "stdin.ps1")
		body := `$value = [Console]::ReadLine(); if ($null -eq $value) { [Console]::WriteLine('stdin:eof') } else { [Console]::WriteLine('stdin:{0}', $value) }; [Console]::Write('ci:{0} noninteractive:{1}', $env:CI, $env:NONINTERACTIVE)`
		if err := os.WriteFile(script, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		argv = []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", script}
	}
	var output bytes.Buffer
	err := (Adapter{}).RunInstall(context.Background(), ports.InstallCommand{
		Argv: argv,
		Env:  []string{"CI=1", "NONINTERACTIVE=1"},
	}, &output, &output)
	if err != nil {
		t.Fatalf("RunInstall: %v", err)
	}
	got := output.String()
	if !strings.Contains(got, "stdin:eof") || !strings.Contains(got, "ci:1 noninteractive:1") {
		t.Fatalf("output = %q, want closed stdin and controlled env", got)
	}
}

func TestPathWritableUsesEffectiveFilesystemPermissions(t *testing.T) {
	t.Parallel()
	writable := t.TempDir()
	got, err := pathWritable(context.Background(), writable)
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatalf("PathWritable(%q) = false, want true", writable)
	}
	got, err = pathWritable(context.Background(), writable+"/missing/child")
	if err != nil {
		t.Fatal(err)
	}
	if !got {
		t.Fatalf("PathWritable should accept a missing destination below a writable ancestor")
	}
}

// installerScriptFixture 为固定离线脚本选择平台原生解释器，Windows 无需安装 POSIX shell。
func installerScriptFixture(posix, powerShell string) (string, []string) {
	if runtime.GOOS == "windows" {
		return powerShell, []string{"powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"}
	}
	return posix, []string{"sh"}
}
