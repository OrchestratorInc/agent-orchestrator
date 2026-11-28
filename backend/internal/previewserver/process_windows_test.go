//go:build windows

package previewserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// An npm-shaped shim is unwrapped to its target, so preview argv never passes
// through cmd.exe quoting (issue #7212). The shim directory contains a space,
// which the unwrapped CreateProcess launch handles without any quoting.
func TestPreviewCommandUnwrapsNPMLikeShim(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "preview.js"), []byte("// entry"), 0o600); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(dir, "preview.cmd")
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

endLocal & goto #_undefined_# 2>NUL || title %COMSPEC% & "%_prog%"  "%dp0%\preview.js" %*
`
	if err := os.WriteFile(shim, []byte(shimContent), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := previewCommand(shim, "--port", "8080")
	assertPreviewProcessGroup(t, cmd)
	if cmd.Path != node {
		t.Fatalf("cmd.Path = %q, want the shim target %q (cmd.exe would re-parse the arguments)", cmd.Path, node)
	}
}

// A shim process cannot unwrap keeps its ComSpec /d /s /c launch, which carries
// a quoted shim path together with a quoted argument. The spaced shim name is
// load-bearing: Go's default batch launch would fail this exact case with
// `'...\preview' is not recognized`.
func TestPreviewCommandRunsWindowsBatchShim(t *testing.T) {
	dir := t.TempDir()
	shim := filepath.Join(dir, "preview helper.cmd")
	if err := os.WriteFile(shim, []byte("@echo off\r\necho %~1\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := previewCommand(shim, "argument with spaces")
	assertPreviewProcessGroup(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("run batch shim: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "argument with spaces" {
		t.Fatalf("output = %q, want batch argument preserved", got)
	}
}

func TestPreviewCommandConfiguresNativeProcessGroup(t *testing.T) {
	assertPreviewProcessGroup(t, previewCommand(os.Args[0]))
}

func assertPreviewProcessGroup(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr = nil, want Windows process attributes")
	}
	if got := cmd.SysProcAttr.CreationFlags; got&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NEW_PROCESS_GROUP", got)
	}
}
