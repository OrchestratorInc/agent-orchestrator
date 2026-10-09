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
