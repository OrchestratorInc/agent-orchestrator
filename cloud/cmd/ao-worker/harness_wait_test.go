package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workertransport"
)

// A template that installs the harness after the worker starts (the Coder
// agent reports ready before its own bootstrap finishes) still gets its agent:
// the worker keeps checking until the binary appears.
func TestWaitForHarnessFindsALateInstall(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin)
	restore := setHarnessWait(t, 10*time.Millisecond, 5*time.Second)
	defer restore()
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\n"), 0o755)
	}()
	if err := waitForHarness(context.Background(), slog.New(slog.NewTextHandler(io.Discard, nil)), "claude-code"); err != nil {
		t.Fatalf("waitForHarness = %v, want the late install found", err)
	}
}

// A binary that never appears keeps the existing unavailable error, and an
// unknown harness is reported at once.
func TestWaitForHarnessGivesUp(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	restore := setHarnessWait(t, 10*time.Millisecond, 60*time.Millisecond)
	defer restore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	started := time.Now()
	if err := waitForHarness(context.Background(), logger, "claude-code"); err == nil {
		t.Fatal("waitForHarness found a missing binary")
	}
	if time.Since(started) < 50*time.Millisecond {
		t.Fatal("waitForHarness gave up before its limit")
	}
	started = time.Now()
	if err := waitForHarness(context.Background(), logger, "no-such-harness"); err == nil || time.Since(started) > 50*time.Millisecond {
		t.Fatalf("unknown harness: err = %v after %s", err, time.Since(started))
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitForHarness(ctx, logger, "claude-code"); err == nil {
		t.Fatal("waitForHarness ignored a cancelled context")
	}
}

func setHarnessWait(t *testing.T, interval, limit time.Duration) func() {
	t.Helper()
	previousInterval, previousLimit := harnessWaitInterval, harnessWaitLimit
	harnessWaitInterval, harnessWaitLimit = interval, limit
	return func() { harnessWaitInterval, harnessWaitLimit = previousInterval, previousLimit }
}

// idleTransportControl is a control plane with no work for the transport.
type idleTransportControl struct{}

func (idleTransportControl) ClaimTransport(context.Context) (*worker.TransportRequest, error) {
	return nil, nil
}
func (idleTransportControl) ClaimTurn(context.Context) (*worker.Turn, error) { return nil, nil }
func (idleTransportControl) WaitForWork(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}
func (idleTransportControl) CompleteTurn(context.Context, string, int, bool) error     { return nil }
func (idleTransportControl) FailTurn(context.Context, string, int, string) error       { return nil }
func (idleTransportControl) CompleteTransport(context.Context, string, int, any) error { return nil }
func (idleTransportControl) FailTransport(context.Context, string, int, string, string) error {
	return nil
}
func (idleTransportControl) PublishTerminalOutput(context.Context, string, int64, []byte) error {
	return nil
}
func (idleTransportControl) PublishTerminalExit(context.Context, string, int, bool) error { return nil }
func (idleTransportControl) AgentSessionID(context.Context) (string, error)               { return "", nil }
func (idleTransportControl) EnsureAgentTerminal(context.Context) (worker.AgentTerminalResponse, error) {
	return worker.AgentTerminalResponse{}, errors.New("no agent terminal")
}

// A terminal session whose harness never appears reports the failure, so the
// start loop shows it as the session's startup error with a Retry instead of
// leaving a task no agent runs keeping the workspace awake. A session in Chat
// leaves it to its turns.
func TestStartInteractiveAgentReportsAHarnessThatNeverAppears(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	restore := setHarnessWait(t, 10*time.Millisecond, 50*time.Millisecond)
	defer restore()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	rehydrated := make(chan struct{})
	close(rehydrated)
	for _, iface := range []string{workertransport.InterfaceTUI, workertransport.InterfaceChat} {
		t.Run(iface, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			started := make(chan error, 1)
			supervisor := &workertransport.Supervisor{
				Control: idleTransportControl{}, Workspace: t.TempDir(), Logger: logger,
				Started: started, InitialInterface: iface, StartupTask: true,
				ChatRunner: blockingChatRunner{},
			}
			done := make(chan error, 1)
			go func() { done <- supervisor.Run(ctx) }()
			defer func() { cancel(); <-done }()
			if err := <-started; err != nil {
				t.Fatal(err)
			}
			bootstrap := worker.BootstrapResponse{Launch: worker.LaunchContext{Harness: "claude-code", Interface: iface}}
			err := startInteractiveAgent(ctx, logger, nil, bootstrap, t.TempDir(), t.TempDir(), "", "", "", supervisor, rehydrated)
			if iface == workertransport.InterfaceTUI && (err == nil || !strings.Contains(err.Error(), "harness unavailable")) {
				t.Fatalf("startInteractiveAgent = %v, want the missing harness reported", err)
			}
			if iface == workertransport.InterfaceChat && err != nil {
				t.Fatalf("startInteractiveAgent in Chat = %v, want nil", err)
			}
		})
	}
}

// blockingChatRunner is a Chat controller that takes no turn until stopped.
type blockingChatRunner struct{}

func (blockingChatRunner) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
