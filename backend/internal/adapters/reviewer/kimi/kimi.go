package kimi

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	workerkimi "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const skillsDirectoryName = "kimi-reviewer-skills"

const HostTrustWarning = "experimental host-trusted reviewer: Kimi has no OS isolation; terminal users can invoke shell mode, change Plan mode, open an external editor, or alter configuration"

type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
}

func New() *Reviewer {
	return &Reviewer{resolveBinary: workerkimi.ResolveKimiBinary}
}

func (*Reviewer) Harness() domain.ReviewerHarness { return domain.ReviewerKimi }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerPromptReadinessProvider = (*Reviewer)(nil)

func (*Reviewer) ReviewPromptReadinessHints(ctx context.Context) (ports.PromptReadinessHints, error) {
	return workerkimi.New().PromptReadinessHints(ctx, ports.LaunchConfig{})
}

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if !filepath.IsAbs(binary) {
		return ports.ReviewCommandSpec{}, errors.New("kimi reviewer: resolved binary must be absolute")
	}
	if strings.TrimSpace(inv.TaskPromptRoot) == "" {
		return ports.ReviewCommandSpec{Argv: []string{binary, "--plan", "--auto"}}, nil
	}
	if strings.TrimSpace(inv.SystemPromptFile) == "" {
		return ports.ReviewCommandSpec{}, errors.New("kimi reviewer: system prompt file is required")
	}
	skillsDir := filepath.Join(inv.TaskPromptRoot, skillsDirectoryName)
	if err := os.MkdirAll(skillsDir, 0o700); err != nil {
		return ports.ReviewCommandSpec{}, fmt.Errorf("kimi reviewer: create empty skills directory: %w", err)
	}
	message := fmt.Sprintf(
		"Read and follow the Orchestrator reviewer role in `%s`, then %s",
		filepath.ToSlash(inv.SystemPromptFile),
		strings.TrimSpace(inv.Prompt),
	)
	return ports.ReviewCommandSpec{
		Argv:           []string{binary, "--plan", "--auto", "--skills-dir", skillsDir},
		InitialMessage: message,
	}, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

func (*Reviewer) ReviewMessage(ctx context.Context, inv ports.ReviewInvocation) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return inv.Prompt, nil
}

func (*Reviewer) ReviewCancel(ctx context.Context) (ports.ReviewCancelSpec, error) {
	if err := ctx.Err(); err != nil {
		return ports.ReviewCancelSpec{}, err
	}
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 2}, nil
}
