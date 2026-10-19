//go:build windows

package previewserver

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPreviewProcessStartTimeKeepsLegacyFormat(t *testing.T) {
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(windows.CurrentProcess(), &creation, &exit, &kernel, &user); err != nil {
		t.Fatalf("read legacy process start time: %v", err)
	}
	ticks := uint64(creation.HighDateTime)<<32 | uint64(creation.LowDateTime)
	if ticks == 0 {
		t.Fatal("GetProcessTimes returned no process start time")
	}
	want := strconv.FormatUint(ticks, 10)
	if got := previewProcessStartTime(os.Getpid()); got != want {
		t.Fatalf("process start time = %q, want legacy FILETIME %q", got, want)
	}
}

func TestPreviewProcessStartTimeRejectsInvalidPID(t *testing.T) {
	for _, pid := range []int{-1, 0} {
		if got := previewProcessStartTime(pid); got != "" {
			t.Fatalf("process start time for pid %d = %q, want empty identity", pid, got)
		}
	}
}

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
