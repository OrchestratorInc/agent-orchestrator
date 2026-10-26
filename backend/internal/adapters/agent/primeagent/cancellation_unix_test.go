//go:build !windows

package primeagent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestGetLaunchCommandReturnsCancellationAfterPromptFileRead(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "system-prompt")
	makeFIFO(t, fifo)
	ctx, cancel := context.WithCancel(context.Background())
	writerReady, releaseWriter := startFIFOWriter(t, fifo, "standing instructions")
	dataDir := t.TempDir()

	result := make(chan error, 1)
	go func() {
		_, err := (&Plugin{resolvedBinary: "prime-agent", extensionFlag: map[string]bool{"prime-agent": true}}).GetLaunchCommand(ctx, ports.LaunchConfig{
			DataDir:          dataDir,
			SystemPromptFile: fifo,
		})
		result <- err
	}()
	waitForFIFOReader(t, writerReady)
	cancel()
	close(releaseWriter)
	if err := waitForResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetLaunchCommand error = %v, want context.Canceled", err)
	}
}

func TestGetAgentHooksReturnsCancellationAfterExistingExtensionRead(t *testing.T) {
	dataDir := t.TempDir()
	path, err := extensionPath(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	makeFIFO(t, path)
	ctx, cancel := context.WithCancel(context.Background())
	writerReady, releaseWriter := startFIFOWriter(t, path, primeAgentExtensionSource)

	result := make(chan error, 1)
	go func() {
		result <- New().GetAgentHooks(ctx, ports.WorkspaceHookConfig{DataDir: dataDir})
	}()
	waitForFIFOReader(t, writerReady)
	cancel()
	close(releaseWriter)
	if err := waitForResult(t, result); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetAgentHooks error = %v, want context.Canceled", err)
	}
}

func makeFIFO(t *testing.T, path string) {
	t.Helper()
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
}

func startFIFOWriter(t *testing.T, path, contents string) (<-chan struct{}, chan<- struct{}) {
	t.Helper()
	ready := make(chan struct{})
	release := make(chan struct{})
	go func() {
		file, err := os.OpenFile(path, os.O_WRONLY, 0o600) //nolint:gosec // test-owned FIFO
		if err != nil {
			t.Errorf("open FIFO writer: %v", err)
			close(ready)
			return
		}
		defer file.Close()
		close(ready)
		<-release
		if _, err := file.WriteString(contents); err != nil {
			t.Errorf("write FIFO: %v", err)
		}
	}()
	return ready, release
}

func waitForFIFOReader(t *testing.T, ready <-chan struct{}) {
	t.Helper()
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for FIFO reader")
	}
}

func waitForResult(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for adapter result")
		return nil
	}
}
