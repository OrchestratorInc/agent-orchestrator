package grok

import (
	"context"
	"fmt"
	"os"
	"strings"

	workergrok "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/grok"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const HostTrustWarning = "experimental user-approved reviewer: Grok uses its normal permission prompts because Orchestrator cannot enforce a read-only sandbox"

type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

func New() *Reviewer { return &Reviewer{resolveBinary: workergrok.ResolveGrokBinary} }

func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerGrok }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if strings.TrimSpace(inv.TaskPromptRoot) == "" {
		return ports.ReviewCommandSpec{Argv: []string{binary}}, nil
	}
	rules, err := os.ReadFile(inv.SystemPromptFile) //nolint:gosec // AO-owned prompt path
	if err != nil {
		return ports.ReviewCommandSpec{}, fmt.Errorf("grok reviewer: read system prompt: %w", err)
	}
	return ports.ReviewCommandSpec{Argv: []string{binary, "--no-auto-update", "--rules", string(rules), "--", inv.Prompt}}, nil
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
