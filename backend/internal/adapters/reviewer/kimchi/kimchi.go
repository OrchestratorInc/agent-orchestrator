package kimchi

import (
	"context"

	workeragent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimchi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/agentrestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const HostTrustWarning = "experimental host-trusted reviewer: Kimchi tool allow/deny rules are not OS isolation; shell commands and GitHub access retain host authority"

var reviewerAllowedTools = []string{
	"read",
	"grep",
	"glob",
	"bash(printf:*)",
	"bash(git diff:*)",
	"bash(git log:*)",
	"bash(git status:*)",
	"bash(gh:*)",
	"bash(ao review submit:*)",
}

var reviewerDisallowedTools = []string{
	"edit",
	"write",
	"bash(git push:*)",
	"bash(git commit:*)",
	"bash(git show:*)",
	"bash(gh pr merge:*)",
	"bash(gh api --method DELETE:*)",
	"bash(gh api --method PUT:*)",
	"bash(gh api --method PATCH:*)",
	"bash(gh gist:*)",
}

type Reviewer struct {
	agent ports.Agent
}

func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerKimchi
}

func (r *Reviewer) InvalidateBinaryResolution() {
	if invalidator, ok := r.agent.(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerRestorer = (*Reviewer)(nil)

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		Config:           inv.Config,
		SessionID:        inv.ReviewerID,
		WorkspacePath:    inv.WorkspacePath,
		Prompt:           inv.Prompt,
		SystemPrompt:     inv.SystemPrompt,
		SystemPromptFile: inv.SystemPromptFile,
		Permissions:      ports.PermissionModeAuto,
		AllowedTools:     reviewerAllowedTools,
		DisallowedTools:  reviewerDisallowedTools,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{Argv: argv}, nil
}

func (r *Reviewer) PreLaunch(ctx context.Context, inv ports.ReviewInvocation) error {
	return r.agent.GetAgentHooks(ctx, ports.WorkspaceHookConfig{WorkspacePath: inv.WorkspacePath})
}

func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	return agentrestore.Command(ctx, r.agent, inv, agentrestore.Options{
		Permissions:     ports.PermissionModeAuto,
		AllowedTools:    reviewerAllowedTools,
		DisallowedTools: reviewerDisallowedTools,
	})
}

func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 2}, nil
}
