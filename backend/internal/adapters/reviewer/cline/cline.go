package cline

import (
	"context"
	"fmt"
	"os"
	"strings"

	workercline "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cline"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const HostTrustWarning = "experimental user-approved reviewer: Cline uses normal permission prompts; Orchestrator does not enable --auto-approve or --yolo"

type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

func New() *Reviewer { return &Reviewer{resolveBinary: workercline.ResolveClineBinary} }

func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerCline }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerPromptReadinessProvider = (*Reviewer)(nil)

func (*Reviewer) ReviewPromptReadinessHints(ctx context.Context) (ports.PromptReadinessHints, error) {
	return workercline.New().PromptReadinessHints(ctx, ports.LaunchConfig{})
}

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if strings.TrimSpace(inv.TaskPromptRoot) == "" {
		return ports.ReviewCommandSpec{Argv: []string{binary}}, nil
	}
	systemPrompt, err := os.ReadFile(inv.SystemPromptFile) //nolint:gosec // AO-owned prompt path
	if err != nil {
		return ports.ReviewCommandSpec{}, fmt.Errorf("cline reviewer: read system prompt: %w", err)
	}
	return ports.ReviewCommandSpec{Argv: []string{binary, "-s", string(systemPrompt)}, InitialMessage: inv.Prompt}, nil
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
