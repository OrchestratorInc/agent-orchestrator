package autohand

import (
	"context"
	"strings"

	workerautohand "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/autohand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const HostTrustWarning = "experimental user-approved reviewer: Autohand uses normal permission prompts; Orchestrator does not enable --yes or --unrestricted"

type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

func New() *Reviewer { return &Reviewer{resolveBinary: workerautohand.ResolveAutohandBinary} }

func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerAutohand }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	env := map[string]string{workerautohand.NoBrowserEnv: "1"}
	if strings.TrimSpace(inv.TaskPromptRoot) == "" {
		return ports.ReviewCommandSpec{Argv: []string{binary}, Env: env}, nil
	}
	argv := []string{binary, "--path", inv.WorkspacePath, "--sys-prompt", inv.SystemPromptFile, "--", inv.Prompt}
	return ports.ReviewCommandSpec{Argv: argv, Env: env}, nil
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
