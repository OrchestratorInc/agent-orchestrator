//go:build !windows

package processalive

import (
	"os/exec"
	"testing"
	"time"
)

func TestAliveReportsZombieAsDead(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	defer func() { _ = cmd.Wait() }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if isZombie(cmd.Process.Pid) {
			if Alive(cmd.Process.Pid) {
				t.Fatal("Alive returned true for zombie process")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("child did not become a zombie before timeout")
}

func TestExitedOnlyOnceTheProcessIsReaped(t *testing.T) {
	cmd := exec.Command("sh", "-c", "exit 0")
	if err := cmd.Start(); err != nil {
		t.Fatalf("start child: %v", err)
	}
	pid := cmd.Process.Pid
	deadline := time.Now().Add(5 * time.Second)
	for !isZombie(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// A zombie still exists for signal 0, as for Node's process.kill(pid, 0).
	if Exited(pid) {
		t.Fatal("Exited returned true for a zombie")
	}
	_ = cmd.Wait()
	if !Exited(pid) {
		t.Fatal("Exited returned false for a reaped process")
	}
	if Exited(0) {
		t.Fatal("Exited returned true for pid 0")
	}
}
