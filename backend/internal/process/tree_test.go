package process_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

// TestCommandTreeCancellation 核验真实父子孙进程、继承输出管道与旁路进程的取消边界。
func TestCommandTreeCancellation(t *testing.T) {
	dir := t.TempDir()
	unrelated := aoprocess.CommandContext(t.Context(), os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
	unrelated.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=unrelated", "AO_TREE_TEST_DIR="+dir)
	if err := unrelated.Start(); err != nil {
		t.Fatalf("start unrelated process: %v", err)
	}
	t.Cleanup(func() {
		_ = unrelated.Process.Kill()
		_ = unrelated.Wait()
	})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := aoprocess.CommandContext(ctx, os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
	cmd.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=parent", "AO_TREE_TEST_DIR="+dir)
	aoprocess.ConfigureTreeCancellation(cmd)
	cmd.WaitDelay = time.Second
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	done := make(chan error, 1)
	go func() { done <- cmd.Run() }()
	// Releasing the child also makes a leaked helper exit after a failed test.
	t.Cleanup(func() { _ = os.WriteFile(filepath.Join(dir, "release"), nil, 0o600) })
	ready := filepath.Join(dir, "ready")
	deadline := time.Now().Add(10 * time.Second)
	var pid int
	for {
		if data, err := os.ReadFile(ready); err == nil {
			if pid, err = strconv.Atoi(string(data)); err == nil && pid > 0 {
				if _, err := os.Stat(filepath.Join(dir, "unrelated-ready")); err == nil {
					break
				}
			}
		}
		select {
		case err := <-done:
			t.Fatalf("command stopped before child started: %v\n%s", err, output.String())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("child did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	middleData, err := os.ReadFile(filepath.Join(dir, "middle"))
	if err != nil {
		t.Fatalf("read middle process: %v", err)
	}
	middlePID, err := strconv.Atoi(string(middleData))
	if err != nil || middlePID <= 0 {
		t.Fatalf("middle process PID = %q: %v", middleData, err)
	}
	cancelledAt := time.Now()
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled command succeeded")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("cancellation did not return")
	}
	cancelElapsed := time.Since(cancelledAt)
	// Child exit notification can follow the parent's Wait returning.
	for _, descendantPID := range []int{middlePID, pid} {
		deadline = time.Now().Add(5 * time.Second)
		for processalive.Alive(descendantPID) {
			if time.Now().After(deadline) {
				t.Fatalf("descendant %d survived cancellation: %s", descendantPID, output.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	if !processalive.Alive(unrelated.Process.Pid) {
		t.Fatal("cancellation stopped an unrelated process")
	}
	if runtime.GOOS == "windows" && cancelElapsed > 1500*time.Millisecond {
		t.Fatalf("tree cancellation took %s, want at most 1.5s", cancelElapsed)
	}
	t.Logf("整树取消耗时 %s；子孙已退出，旁路进程仍存活", cancelElapsed)
}

// TestCommandTreeCancellationHelper 建立共享输出的父子孙链，旁路角色独立等待释放。
func TestCommandTreeCancellationHelper(t *testing.T) {
	role := os.Getenv("AO_TREE_TEST_ROLE")
	if role == "" {
		return
	}
	if role == "finished" {
		return
	}
	dir := os.Getenv("AO_TREE_TEST_DIR")
	if role == "exited-parent" {
		cmd := aoprocess.Command(os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
		cmd.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=child")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if role == "parent" || role == "middle" {
		childRole := "middle"
		if role == "middle" {
			childRole = "child"
			if err := os.WriteFile(filepath.Join(dir, "middle"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		cmd := aoprocess.Command(os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
		cmd.Env = append(os.Environ(), "AO_TREE_TEST_ROLE="+childRole)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		return
	}
	readyFile := "ready"
	if role == "unrelated" {
		readyFile = "unrelated-ready"
	}
	if err := os.WriteFile(filepath.Join(dir, readyFile), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
