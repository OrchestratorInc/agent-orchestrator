package cursor

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	workeragent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/cursor"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Reviewer struct {
	agent agent
}

func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

type agent interface {
	ports.Agent
	InstallWorkspaceTrust(context.Context, ports.WorkspaceHookConfig) error
}

func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerCursor
}

func (r *Reviewer) InvalidateBinaryResolution() {
	if invalidator, ok := r.agent.(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)

func (r *Reviewer) PreLaunch(ctx context.Context, inv ports.ReviewInvocation) error {
	if err := installReviewerConfig(ctx, inv); err != nil {
		return err
	}
	if err := applyReviewerMCPDisable(ctx, inv); err != nil {
		return err
	}
	return r.agent.InstallWorkspaceTrust(ctx, ports.WorkspaceHookConfig{
		DataDir:       inv.DataDir,
		Env:           reviewerEnv(inv),
		SessionID:     inv.ReviewerID,
		WorkspacePath: inv.WorkspacePath,
	})
}

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	prompt := cursorPrompt(inv)
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		Config:        inv.Config,
		SessionID:     inv.ReviewerID,
		WorkspacePath: inv.WorkspacePath,
		Prompt:        prompt,
		Permissions:   ports.PermissionModeDefault,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	flags := []string{"--trust"}
	if strings.TrimSpace(inv.TaskPromptRoot) != "" {
		flags = append(flags, "--add-dir", inv.TaskPromptRoot)
	}
	return ports.ReviewCommandSpec{
		Argv: insertBeforePrompt(argv, flags...),
		Env:  reviewerEnv(inv),
	}, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

func cursorPrompt(inv ports.ReviewInvocation) string {
	if inv.SystemPromptFile != "" {
		return fmt.Sprintf(
			"Read and follow the Orchestrator reviewer role in `%s`, then %s",
			filepath.ToSlash(inv.SystemPromptFile),
			strings.TrimSpace(inv.Prompt),
		)
	}
	return strings.TrimSpace(inv.Prompt)
}

func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 2}, nil
}

func insertBeforePrompt(argv []string, extra ...string) []string {
	for i, arg := range argv {
		if arg == "--" {
			out := make([]string, 0, len(argv)+len(extra))
			out = append(out, argv[:i]...)
			out = append(out, extra...)
			return append(out, argv[i:]...)
		}
	}
	return append(argv, extra...)
}
