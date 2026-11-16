//go:build linux

package procmem

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"
)

func TestParseProcStatStart(t *testing.T) {
	// A command name with spaces and parentheses must not shift the fields.
	stat := "4242 (a (weird) name) S 1 4242 4242 0 -1 4194560 100 0 0 0 1 2 0 0 20 0 1 0 987654 1000 10 18446744073709551615"
	if got, ok := parseProcStatStart(stat); !ok || got != "987654" {
		t.Fatalf("start = %q, %v; want 987654", got, ok)
	}
	if _, ok := parseProcStatStart("4243 (gone) Z 1 4243 4243 0 -1 4194560 0 0 0 0 0 0 0 0 20 0 1 0 987655 0 0 0"); ok {
		t.Fatal("a zombie has a start time")
	}
}

// The kernel start time identifies a process exactly: it survives exec, and
// two processes started in the same second still differ.
func TestStartTimesFollowAProcessThroughExec(t *testing.T) {
	cmd := exec.Command("sh", "-c", "read _; exec sleep 600")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	before, err := StartTimes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	if before[pid] == "" {
		t.Fatalf("no start time for %d", pid)
	}
	_, _ = stdin.Write([]byte("go\n")) // the shell execs sleep
	waitForCommand(t, pid, "sleep")
	after, err := StartTimes(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if after[pid] != before[pid] {
		t.Fatalf("start time changed across exec: %q -> %q", before[pid], after[pid])
	}
	if before[os.Getpid()] == "" {
		t.Fatal("no start time for this process")
	}
}

func waitForCommand(t *testing.T, pid int, prefix string) {
	t.Helper()
	for range 250 {
		if comm, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/comm"); err == nil && len(comm) >= len(prefix) && string(comm[:len(prefix)]) == prefix {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("process %d never became %s", pid, prefix)
}
