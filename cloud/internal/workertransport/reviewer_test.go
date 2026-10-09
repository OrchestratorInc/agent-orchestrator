package workertransport

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/aoagents/agent-orchestrator/cloud/internal/workerexec"
)

func TestReviewTerminalResolvesIndependentCommand(t *testing.T) {
	supervisor := Supervisor{AgentCommand: workerexec.Command{Path: "/bin/sh", Args: []string{"-c", "printf worker"}, Env: map[string]string{"ROLE": "worker"}}}
	supervisor.ProjectReviewCommand = func(_ context.Context, input worker.TerminalCommand) (workerexec.Command, error) {
		if input.Reviewer.Harness != "claude-code" || input.ReviewRunID != "review" {
			t.Fatalf("review settings=%+v", input)
		}
		return workerexec.Command{Path: "/bin/sh", Args: []string{"-c", "printf reviewer"}, Env: map[string]string{"ROLE": "reviewer"}}, nil
	}
	command, cleanup, err := supervisor.terminalCommand(context.Background(), worker.TerminalCommand{Kind: "reviewer", ReviewRunID: "review", Reviewer: &domain.ProjectReviewer{Harness: "claude-code"}})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	output, err := command.Output()
	if err != nil || string(output) != "reviewer" || supervisor.AgentCommand.Args[1] != "printf worker" {
		t.Fatalf("review output=%q err=%v worker=%+v", output, err, supervisor.AgentCommand)
	}
	supervisor.ProjectReviewCommand = nil
	if _, _, err := supervisor.terminalCommand(context.Background(), worker.TerminalCommand{Kind: "reviewer"}); err == nil {
		t.Fatal("missing review configuration fell back to worker command")
	}
}

type reviewerOutputControl struct {
	supervisorControlStub
	started   chan struct{}
	release   chan struct{}
	exited    chan struct{}
	outputErr chan error
}

func (c *reviewerOutputControl) PublishTerminalOutput(ctx context.Context, _ string, _ int64, _ []byte) error {
	close(c.started)
	select {
	case <-c.release:
	case <-ctx.Done():
	}
	c.outputErr <- ctx.Err()
	return ctx.Err()
}
func (c *reviewerOutputControl) PublishTerminalExit(context.Context, string, int, bool) error {
	close(c.exited)
	return nil
}
func TestReviewerExitPreservesStartupErrorOutput(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := &reviewerOutputControl{started: make(chan struct{}), release: make(chan struct{}), exited: make(chan struct{}), outputErr: make(chan error, 1)}
	s := &Supervisor{Control: c, terminals: make(map[string]*terminalProcess), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.ProjectReviewCommand = func(context.Context, worker.TerminalCommand) (workerexec.Command, error) {
		return workerexec.Command{Path: "/bin/sh", Args: []string{"-c", "printf 'invalid reviewer argument'; exit 2"}}, nil
	}
	if err := s.openTerminal(ctx, worker.TerminalCommand{TerminalID: "reviewer", Kind: "reviewer", ReviewRunID: "run", Reviewer: &domain.ProjectReviewer{Harness: "codex"}}); err != nil {
		t.Fatal(err)
	}
	defer s.closeAllTerminals()
	select {
	case <-c.started:
	case <-time.After(5 * time.Second):
		t.Fatal("missing startup output")
	}
	select {
	case <-c.exited:
		t.Error("reviewer exit published before output was saved")
	case <-time.After(50 * time.Millisecond):
	}
	close(c.release)
	select {
	case err := <-c.outputErr:
		if err != nil {
			t.Errorf("output was canceled: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("output did not finish")
	}
	select {
	case <-c.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("reviewer exit not published")
	}
}
