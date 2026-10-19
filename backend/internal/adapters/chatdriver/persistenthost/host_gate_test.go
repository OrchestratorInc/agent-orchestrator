package persistenthost

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHostGateExclusiveAndReusable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.guard")
	release, err := acquireHostGate(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if secondRelease, err := acquireHostGate(path); !errors.Is(err, ErrHostExists) {
		if secondRelease != nil {
			secondRelease()
		}
		t.Fatalf("contending acquisition = %v, want ErrHostExists", err)
	}
	release()
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("released gate disappeared: %v", err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("release replaced the gate file")
	}
	nextRelease, err := acquireHostGate(path)
	if err != nil {
		t.Fatalf("acquire released gate: %v", err)
	}
	nextRelease()
}

func TestHostGatesDoNotBlockOtherSessions(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"first.guard", "second.guard"} {
		release, err := acquireHostGate(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
	}
}

func TestHostGatePreservesOpenError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "host.guard")
	if release, err := acquireHostGate(path); !errors.Is(err, os.ErrNotExist) || errors.Is(err, ErrHostExists) {
		if release != nil {
			release()
		}
		t.Fatalf("acquire missing parent = %v, want underlying not-exist error", err)
	}
}

func TestHostGateProcessHelper(t *testing.T) {
	path := os.Getenv("AO_CHAT_HOST_GATE_HELPER_PATH")
	if path == "" {
		return
	}
	release, err := acquireHostGate(path)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if _, err := fmt.Fprintln(os.Stdout, "locked"); err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
}

func TestHostGateReleasedWhenOwnerDies(t *testing.T) {
	path := filepath.Join(t.TempDir(), "host.guard")
	cmd := exec.Command(os.Args[0], "-test.run=^TestHostGateProcessHelper$")
	cmd.Env = append(os.Environ(), "AO_CHAT_HOST_GATE_HELPER_PATH="+path)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close() })
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	ready := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(stdout).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "locked\n" {
			t.Fatalf("gate owner output = %q", line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gate owner did not acquire the lock")
	}
	if release, err := acquireHostGate(path); !errors.Is(err, ErrHostExists) {
		if release != nil {
			release()
		}
		t.Fatalf("acquire live owner's gate = %v, want ErrHostExists", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	release, err := acquireHostGate(path)
	if err != nil {
		t.Fatalf("acquire exited owner's gate: %v", err)
	}
	release()
}
