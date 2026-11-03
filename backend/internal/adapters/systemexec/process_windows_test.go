//go:build windows

package systemexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// A shim process cannot unwrap keeps its ComSpec /d /s /c launch, which carries
// a quoted shim path together with a quoted argument. The spaced shim name is
// load-bearing: Go's default batch launch would fail this exact case with
// `'...\agent' is not recognized`.
func TestCommandContextRunsBatchShim(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "agent tool.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\necho %~1\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd, err := commandContext(context.Background(), shim, "argument with spaces")
	if err != nil {
		t.Fatal(err)
	}
	aoprocess.ConfigureTreeCancellation(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run batch shim: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "argument with spaces" {
		t.Fatalf("output = %q, want batch argument preserved", got)
	}
}

// An npm-shaped shim is unwrapped to its target, so its arguments never pass
// through cmd.exe quoting (issue #7212). The shim directory contains a space,
// which the unwrapped CreateProcess launch handles without any quoting.
func TestCommandContextUnwrapsNPMLikeShim(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "ao tools")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	node := filepath.Join(dir, "node.exe")
	content, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(node, content, 0o700); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(dir, "node_modules", "@openai", "codex", "bin", "codex.js")
	if err := os.MkdirAll(filepath.Dir(entry), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("// entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "codex.cmd")
	shimContent := `@ECHO off
GOTO start
:find_dp0
SET dp0=%~dp0
EXIT /b
:start
SETLOCAL
CALL :find_dp0

IF EXIST "%dp0%\node.exe" (
  SET "_prog=%dp0%\node.exe"
) ELSE (
  SET "_prog=node"
  SET PATHEXT=%PATHEXT:;.JS;=;%
)

endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\node_modules\@openai\codex\bin\codex.js" %*
`
	if err := os.WriteFile(shim, []byte(shimContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd, err := commandContext(context.Background(), shim, "-c", `hooks.SessionStart=[{hooks=[{type="command",command="& \"C:\\ao\\ao.exe\" hooks codex session-start",timeout=5}]}]`)
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Path != node {
		t.Fatalf("cmd.Path = %q, want the shim target %q (cmd.exe would re-parse the arguments)", cmd.Path, node)
	}
}

func TestMergeWindowsPathAddsRegistryEntriesWithoutDuplicates(t *testing.T) {
	got := mergeWindowsPath(
		`C:\Windows\System32;C:\Users\tester\AppData\Roaming\npm`,
		`C:\Program Files\Git\cmd;C:\WINDOWS\SYSTEM32`,
		`C:\Users\tester\AppData\Local\Microsoft\WinGet\Links`,
	)
	want := strings.Join([]string{
		`C:\Windows\System32`,
		`C:\Users\tester\AppData\Roaming\npm`,
		`C:\Program Files\Git\cmd`,
		`C:\Users\tester\AppData\Local\Microsoft\WinGet\Links`,
	}, ";")
	if got != want {
		t.Fatalf("mergeWindowsPath() = %q, want %q", got, want)
	}
}
