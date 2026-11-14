package datadirlock

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A second acquirer retries for the whole wait, then fails with ErrLocked
// naming the holder's PID. A different data dir is unaffected.
func TestAcquireWaitsThenFailsWhileHeld(t *testing.T) {
	dir := t.TempDir()
	held, err := Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	t.Cleanup(func() { _ = held.Release() })

	const wait = 300 * time.Millisecond
	start := time.Now()
	_, err = Acquire(context.Background(), dir, wait)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Acquire = %v, want ErrLocked", err)
	}
	if elapsed := time.Since(start); elapsed < wait {
		t.Fatalf("second Acquire gave up after %s, want at least %s", elapsed, wait)
	}
	if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("error should name holder pid %d: %v", os.Getpid(), err)
	}

	other, err := Acquire(context.Background(), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("Acquire on another data dir: %v", err)
	}
	_ = other.Release()
}

// Release hands the lock to a waiting acquirer; a second Release is a no-op.
func TestReleaseHandsLockToWaiter(t *testing.T) {
	dir := t.TempDir()
	held, err := Acquire(context.Background(), dir, 0)
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = held.Release()
	}()

	lock, err := Acquire(context.Background(), dir, 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire while the holder releases: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second Release should be a no-op: %v", err)
	}
}

// The OS drops the lock when the holder dies, SIGKILL included, so a crashed
// daemon never wedges its data dir.
func TestLockReleasedWhenHolderKilled(t *testing.T) {
	if dir := os.Getenv("AO_DATADIRLOCK_HOLDER"); dir != "" {
		lock, err := Acquire(context.Background(), dir, 0)
		if err != nil {
			fmt.Println("error:", err)
			os.Exit(1)
		}
		fmt.Println("locked")
		// Hold until killed; exit if the parent test dies and closes stdin.
		_, _ = io.Copy(io.Discard, os.Stdin)
		runtime.KeepAlive(lock) // an unreachable *os.File is closed by its finalizer
		os.Exit(0)
	}

	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=^TestLockReleasedWhenHolderKilled$")
	cmd.Env = append(os.Environ(), "AO_DATADIRLOCK_HOLDER="+dir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cmd.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start holder: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); strings.TrimSpace(line) != "locked" {
		t.Fatalf("holder reported %q (%v), want locked", line, err)
	}

	if _, err := Acquire(context.Background(), dir, 0); !errors.Is(err, ErrLocked) {
		t.Fatalf("Acquire while the holder is alive = %v, want ErrLocked", err)
	}
	if err := cmd.Process.Kill(); err != nil { // SIGKILL; TerminateProcess on Windows
		t.Fatalf("kill holder: %v", err)
	}
	lock, err := Acquire(context.Background(), dir, 5*time.Second)
	if err != nil {
		t.Fatalf("Acquire after the holder was killed: %v", err)
	}
	_ = lock.Release()
}

func TestAcquireRequiresDataDir(t *testing.T) {
	if _, err := Acquire(context.Background(), "", 0); err == nil {
		t.Fatal("expected error for empty data dir")
	}
}
