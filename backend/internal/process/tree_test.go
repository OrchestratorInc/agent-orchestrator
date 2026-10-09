package process

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

func TestCommandTreeCancellation(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	cmd := CommandContext(ctx, os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
	cmd.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=parent", "AO_TREE_TEST_DIR="+dir)
	ConfigureTreeCancellation(cmd)
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
				break
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
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled command succeeded")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("cancellation did not return")
	}
	// Child exit notification can follow the parent's Wait returning.
	deadline = time.Now().Add(5 * time.Second)
	for processalive.Alive(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("child %d survived cancellation: %s", pid, output.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCommandTreeCancellationHelper(t *testing.T) {
	role := os.Getenv("AO_TREE_TEST_ROLE")
	if role == "" {
		return
	}
	dir := os.Getenv("AO_TREE_TEST_DIR")
	if role == "parent" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestCommandTreeCancellationHelper$")
		cmd.Env = append(os.Environ(), "AO_TREE_TEST_ROLE=child")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := os.WriteFile(filepath.Join(dir, "ready"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "release")); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
