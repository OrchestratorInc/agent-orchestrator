package crush

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	workercrush "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/crush"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const HostTrustWarning = "experimental user-approved reviewer: Crush uses normal permission prompts; Orchestrator deliberately does not enable --yolo"

type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

func New() *Reviewer { return &Reviewer{resolveBinary: workercrush.ResolveCrushBinary} }

func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerCrush }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerPromptReadinessProvider = (*Reviewer)(nil)

func (*Reviewer) ReviewPromptReadinessHints(ctx context.Context) (ports.PromptReadinessHints, error) {
	return workercrush.New().PromptReadinessHints(ctx, ports.LaunchConfig{})
}

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if strings.TrimSpace(inv.TaskPromptRoot) == "" {
		return ports.ReviewCommandSpec{Argv: []string{binary}}, nil
	}
	message := fmt.Sprintf("Read and follow the Orchestrator reviewer role in `%s`, then %s", filepath.ToSlash(inv.SystemPromptFile), strings.TrimSpace(inv.Prompt))
	return ports.ReviewCommandSpec{Argv: []string{binary, "--cwd", inv.WorkspacePath}, InitialMessage: message}, nil
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
