package aider

import (
	"context"

	workeraider "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/aider"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

func New() *Reviewer { return &Reviewer{resolveBinary: workeraider.ResolveAiderBinary} }

func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerAider }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerReusePolicy = (*Reviewer)(nil)

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	argv := []string{
		binary,
		"--chat-mode", "ask",
		"--dry-run",
		"--no-auto-commits",
		"--no-dirty-commits",
		"--no-gitignore",
		"--no-check-update",
		"--no-stream",
		"--no-pretty",
	}
	if inv.SystemPromptFile != "" {
		argv = append(argv, "--read", inv.SystemPromptFile)
	}
	if inv.TaskPromptFile != "" {
		argv = append(argv, "--read", inv.TaskPromptFile)
	}
	message := ""
	if inv.TaskPromptFile != "" {
		message = "Follow the Orchestrator reviewer role and review task loaded as read-only context."
	}
	return ports.ReviewCommandSpec{Argv: argv, InitialMessage: message}, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

func (*Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

func (*Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 2}, nil
}

func (*Reviewer) ReviewProcessReusable() bool { return false }
