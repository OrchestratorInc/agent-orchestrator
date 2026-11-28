//go:build windows

package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

// stdioChildEnv names the report file when this test binary runs as the child
// of TestStartProcessGivesChildNulStdioWhenNoStreams.
const stdioChildEnv = "AO_TEST_STDIO_CHILD"

// TestStartProcessGivesChildNulStdioWhenNoStreams covers the Windows half of
// #6261. A nil *os.File reaches os/exec as InvalidHandle, which is also the
// current-process pseudo-handle, so the child's stdout and stderr used to be
// handles to the parent (ao.exe) instead of NUL.
//
// The child is this test binary: unlike on Unix, Go on Windows does not reopen
// its standard handles at startup, so it sees what it was given.
func TestStartProcessGivesChildNulStdioWhenNoStreams(t *testing.T) {
	report := filepath.Join(t.TempDir(), "handles")
	err := startProcess(processStartConfig{
		Path: os.Args[0],
		Args: []string{"-test.run=^TestStdioChildReport$"},
		Env:  append(os.Environ(), stdioChildEnv+"="+report),
	})
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}
	got := strings.TrimSpace(waitForFile(t, report))
	want := fmt.Sprintf("stdout=%d stderr=%d", windows.FILE_TYPE_CHAR, windows.FILE_TYPE_CHAR)
	if got != want {
		t.Fatalf("child std handles = %q, want %q (NUL); FILE_TYPE_UNKNOWN (%d) is a process handle", got, want, windows.FILE_TYPE_UNKNOWN)
	}
}

// TestStdioChildReport is the child half of
// TestStartProcessGivesChildNulStdioWhenNoStreams: it records the file type of
// its standard output and error handles.
func TestStdioChildReport(t *testing.T) {
	report := os.Getenv(stdioChildEnv)
	if report == "" {
		t.Skip("runs only as the child of TestStartProcessGivesChildNulStdioWhenNoStreams")
	}
	kind := func(which uint32) uint32 {
		handle, err := windows.GetStdHandle(which)
		if err != nil {
			t.Fatalf("GetStdHandle(%d): %v", which, err)
		}
		fileType, err := windows.GetFileType(handle)
		if err != nil && fileType != windows.FILE_TYPE_UNKNOWN {
			t.Fatalf("GetFileType: %v", err)
		}
		return fileType
	}
	line := fmt.Sprintf("stdout=%d stderr=%d\n", kind(windows.STD_OUTPUT_HANDLE), kind(windows.STD_ERROR_HANDLE))
	// Write to a temp name and rename, so the parent never reads a partial file.
	if err := os.WriteFile(report+".tmp", []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(report+".tmp", report); err != nil {
		t.Fatal(err)
	}
}
