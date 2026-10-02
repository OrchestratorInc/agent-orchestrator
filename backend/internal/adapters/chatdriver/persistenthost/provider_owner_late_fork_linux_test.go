//go:build linux

package persistenthost

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestRemovalLateForkProviderHelper(t *testing.T) {
	role := os.Getenv("AO_OWNER_LATE_FORK_ROLE")
	if role == "" {
		return
	}
	dir := os.Getenv("AO_OWNER_LATE_FORK_DIR")
	if role == "parent" {
		if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			t.Fatal(err)
		}
		for {
			if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
				break
			}
			time.Sleep(time.Millisecond)
		}
		child := exec.Command(os.Args[0], "-test.run=^TestRemovalLateForkProviderHelper$")
		child.Env = append(os.Environ(), "AO_OWNER_LATE_FORK_ROLE=child")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "child"), []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			t.Fatal(err)
		}
		os.Exit(0)
	}
	for {
		time.Sleep(time.Hour)
	}
}

func TestRemovalOwnerLateForkCensus(t *testing.T) {
	for _, mode := range []string{"cold-stop", "normal-completion"} {
		t.Run(mode, func(t *testing.T) { testRemovalOwnerLateForkCensus(t, mode) })
	}
}

func testRemovalOwnerLateForkCensus(t *testing.T, mode string) {
	dir := t.TempDir()
	dataDir := t.TempDir()
	parent := exec.Command(os.Args[0], "-test.run=^TestRemovalLateForkProviderHelper$")
	parent.Env = append(os.Environ(), "AO_OWNER_LATE_FORK_ROLE=parent", "AO_OWNER_LATE_FORK_DIR="+dir)
	parent.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := parent.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Kill(-parent.Process.Pid, syscall.SIGKILL)
		_ = parent.Wait()
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late-fork leader did not become ready")
		}
		time.Sleep(time.Millisecond)
	}
	owner, err := beginProviderOwner(dataDir, "late-fork", strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	if err := captureProviderOwner(&owner, parent.Process.Pid); err != nil {
		t.Fatal(err)
	}
	if err := writeProviderOwner(dataDir, owner); err != nil {
		t.Fatal(err)
	}
	observed, resume := make(chan struct{}), make(chan struct{})
	var once sync.Once
	barrier := func(observedOwner providerOwner) {
		if observedOwner.Identity == owner.Identity {
			once.Do(func() { close(observed); <-resume })
		}
	}
	providerCensusBarrierForTest.Store(&barrier)
	t.Cleanup(func() { providerCensusBarrierForTest.Store(nil) })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		if mode == "normal-completion" {
			result <- finishProviderOwner(ctx, dataDir, owner)
		} else {
			result <- ShutdownExact(ctx, dataDir, owner.Session, owner.Identity)
		}
	}()
	select {
	case <-observed:
	case <-ctx.Done():
		close(resume)
		t.Fatal("teardown did not reach the census barrier")
	}
	if err := os.WriteFile(filepath.Join(dir, "release"), nil, 0o600); err != nil {
		close(resume)
		t.Fatal(err)
	}
	if err := parent.Wait(); err != nil {
		close(resume)
		t.Fatal(err)
	}
	close(resume)
	err = <-result
	data, readErr := os.ReadFile(filepath.Join(dir, "child"))
	if readErr != nil {
		t.Fatal("enumeration barrier did not release the late-fork child", readErr)
	}
	childPID, parseErr := strconv.Atoi(string(data))
	if parseErr != nil || !removalCrashProcessRunning(childPID) {
		t.Fatal("late-fork child was not held alive", parseErr)
	}
	if removalCrashProcessRunning(parent.Process.Pid) {
		t.Fatal("recorded leader did not exit before the census continued")
	}
	if err == nil {
		t.Error("teardown acknowledged an empty census while the unlisted child survives")
	}
	persisted, readErr := readProviderOwner(dataDir, owner.Session, owner.Identity)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if persisted.State == "stopped" {
		t.Error("stopped was persisted while the unlisted child survives")
	}
	if err := ShutdownExact(t.Context(), dataDir, owner.Session, owner.Identity); err == nil {
		t.Error("cold retry accepted stopped while the unlisted child survives")
	}
}
