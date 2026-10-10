//go:build windows

package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestCommandContextHidesConsoleWindow(t *testing.T) {
	cmd := CommandContext(context.Background(), "git", "--version")
	if cmd.SysProcAttr == nil {
		t.Fatal("SysProcAttr = nil, want hidden Windows process attributes")
	}
	if got := cmd.SysProcAttr.CreationFlags; got&windows.CREATE_NO_WINDOW == 0 {
		t.Fatalf("CreationFlags = %#x, want CREATE_NO_WINDOW", got)
	}
	if !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow = false, want true")
	}
}

func TestCommandContextRunsBatchFileWithoutConsole(t *testing.T) {
	script := filepath.Join(t.TempDir(), "console-probe.cmd")
	contents := "@echo off\r\n\"%~1\" -test.run=TestHiddenConsoleHelper\r\n"
	if err := os.WriteFile(script, []byte(contents), 0o600); err != nil {
		t.Fatalf("write batch probe: %v", err)
	}

	cmd := CommandContext(t.Context(), script, os.Args[0])
	cmd.Env = append(os.Environ(), "AO_HIDDEN_CONSOLE_HELPER=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run batch probe: %v\n%s", err, output)
	}
}

func TestHiddenConsoleHelper(t *testing.T) {
	if os.Getenv("AO_HIDDEN_CONSOLE_HELPER") != "1" {
		return
	}
	getConsoleWindow := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow")
	window, _, callErr := getConsoleWindow.Call()
	if window != 0 {
		fmt.Fprintf(os.Stderr, "helper inherited console window %#x: %v\n", window, callErr)
		os.Exit(1)
	}
}
