//go:build !windows

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestStartProcessGivesChildOpenStdioWhenNoStreams covers #6261. openApp leaves
// Stdout and Stderr unset, and a nil *os.File stored in exec.Cmd's io.Writer
// fields used to reach the child as closed fds 1 and 2. The Linux AppImage
// runtime then put its keepalive pipe on those fds and spun at 100% CPU.
//
// The child must be a shell: a Go child reopens closed fds 0-2 on /dev/null at
// startup, which hides the bug.
func TestStartProcessGivesChildOpenStdioWhenNoStreams(t *testing.T) {
	report := filepath.Join(t.TempDir(), "fds")
	// Check every fd before opening the report, so the redirect cannot fill a
	// closed slot. Write to a temp name and rename so the poll never reads a
	// partial file.
	script := `s=""
for fd in 0 1 2; do
  if [ -e /dev/fd/$fd ]; then s="$s $fd=open"; else s="$s $fd=closed"; fi
done
printf '%s\n' "$s" > "$1.tmp" && mv "$1.tmp" "$1"`

	err := startProcess(processStartConfig{
		Path: "/bin/sh",
		Args: []string{"-c", script, "sh", report},
		Env:  os.Environ(),
	})
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}

	got := strings.TrimSpace(waitForFile(t, report))
	if want := "0=open 1=open 2=open"; got != want {
		t.Fatalf("child stdio = %q, want %q", got, want)
	}
}

func TestStartProcessRoutesOutputToProvidedStreams(t *testing.T) {
	dir := t.TempDir()
	outPath := filepath.Join(dir, "out")
	errPath := filepath.Join(dir, "err")
	stdout, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stdout.Close()
	stderr, err := os.Create(errPath)
	if err != nil {
		t.Fatal(err)
	}
	defer stderr.Close()

	err = startProcess(processStartConfig{
		Path:   "/bin/sh",
		Args:   []string{"-c", "echo to-stdout; echo to-stderr >&2"},
		Env:    os.Environ(),
		Stdout: stdout,
		Stderr: stderr,
	})
	if err != nil {
		t.Fatalf("startProcess: %v", err)
	}

	if got := strings.TrimSpace(waitForFile(t, outPath)); got != "to-stdout" {
		t.Fatalf("stdout = %q, want to-stdout", got)
	}
	if got := strings.TrimSpace(waitForFile(t, errPath)); got != "to-stderr" {
		t.Fatalf("stderr = %q, want to-stderr", got)
	}
}

// waitForFile polls until path has content, because startProcess returns as
// soon as the detached child starts.
func waitForFile(t *testing.T, path string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil && len(data) > 0 {
			return string(data)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (last error: %v)", path, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
