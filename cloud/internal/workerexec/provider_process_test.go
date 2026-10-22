package workerexec

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestProviderCancellationStopsDescendantHoldingPipes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	process := exec.CommandContext(ctx, "sh", "-c", "sleep 30 & echo ready; wait")
	configureProviderProcess(process)
	stdout, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	process.Stderr = io.Discard
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stopProviderProcess(process) }()
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "ready\n" {
		t.Fatalf("child did not start: %q, %v", line, err)
	}
	cancel()
	done := make(chan error, 1)
	go func() { done <- process.Wait() }()
	select {
	case <-done:
	case <-time.After(4 * time.Second):
		t.Fatal("provider process and descendant kept the pipes open after cancellation")
	}
}

// os/exec copies an agent's stderr into the buffer from its own goroutine
// while a failing turn reads it (boundedError(stderr.String())). The copy must
// take the buffer's lock: run under -race, an unlocked copy fails this test.
func TestLockedBufferHoldsItsLockWhileExecCopiesStderr(t *testing.T) {
	process := exec.Command("sh", "-c", `i=0; while [ $i -lt 200 ]; do echo stderr-line >&2; i=$((i+1)); done`)
	var stderr lockedBuffer
	process.Stderr = &stderr
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan error, 1)
	go func() { waited <- process.Wait() }()
	for {
		_ = stderr.String()
		select {
		case err := <-waited:
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Count(stderr.String(), "stderr-line"); got != 200 {
				t.Fatalf("stderr kept %d lines, want 200", got)
			}
			return
		default:
		}
	}
}
