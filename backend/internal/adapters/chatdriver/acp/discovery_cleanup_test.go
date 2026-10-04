package acp

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"
)

func TestDiscoveryUsesImmediateCleanupWithoutPrompt(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "initialize failure"}[failed], func(t *testing.T) {
			agent := &fakeAgent{newConfig: []acpsdk.SessionConfigOption{selectConfigOption("model", "Model", "model", "first", "first", "second")}}
			if failed {
				agent.initErr = errors.New("initialize unavailable")
			}
			driver := New(Config{Launch: func(context.Context, LaunchConfig) (Launch, error) { return Launch{}, nil }}, slog.New(slog.DiscardHandler))
			var forced atomic.Bool
			driver.useTestProcess(func(launch Launch, dir string) (*process, error) {
				proc, err := fakeSpawn(agent)(launch, dir)
				if err != nil {
					return nil, err
				}
				stop := proc.stop
				proc.forceStopFunc = func() error { forced.Store(true); return stop() }
				proc.stop = func() error { t.Error("catalog used graceful chat shutdown"); return stop() }
				return proc, nil
			})
			_, err := driver.discoverConfigOptions(context.Background(), t.TempDir())
			if failed != (err != nil) {
				t.Fatalf("error = %v", err)
			}
			if !forced.Load() {
				t.Fatal("discovery did not force cleanup")
			}
			agent.mu.Lock()
			prompt := agent.promptParams.Prompt
			agent.mu.Unlock()
			if prompt != nil {
				t.Fatal("discovery submitted a prompt")
			}
		})
	}
}

func TestDiscoveryCancellationDoesNotWaitForGracefulExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	binary := filepath.Join(t.TempDir(), "silent-agent")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 30\n"), 0700); err != nil {
		t.Fatal(err)
	}
	driver := New(Config{Launch: func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: binary}, nil }}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := driver.discoverConfigOptions(ctx, t.TempDir()); err == nil {
		t.Fatal("cancelled discovery succeeded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("catalog cancellation waited for chat shutdown grace")
	}
}

func TestProcessForceStopIsReapedAndIdempotent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	proc, err := spawnAgent(Launch{Command: "sh", Args: []string{"-c", "exec sleep 30"}}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if err = proc.forceStop(); err != nil {
		t.Fatal(err)
	}
	if err = proc.forceStop(); err != nil {
		t.Fatal(err)
	}
	if err = proc.stop(); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("forced process retained graceful wait")
	}
}
