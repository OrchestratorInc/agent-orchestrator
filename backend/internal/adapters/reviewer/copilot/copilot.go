package copilot

import (
	"context"

	workeragent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/copilot"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const availableTools = "bash,view,grep,glob"

var allowedTools = []string{
	"read",
	"shell(git diff:*)",
	"shell(git log:*)",
	"shell(git show:*)",
	"shell(git status:*)",
	"shell(printf:*)",
	"shell(gh api:*)",
	"shell(ao review submit:*)",
}

var deniedTools = []string{
	"write",
	"shell(git push:*)",
	"shell(git commit:*)",
	"shell(git checkout:*)",
	"shell(git reset:*)",
	"shell(git clean:*)",
	"shell(git apply:*)",
	"shell(git merge:*)",
	"shell(git rebase:*)",
	"shell(git cherry-pick:*)",
}

type agent interface {
	ports.Agent
	InstallAgentProfile(context.Context, ports.WorkspaceHookConfig) error
}

type Reviewer struct {
	agent agent
}

func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerCopilot
}

func (r *Reviewer) InvalidateBinaryResolution() {
	if invalidator, ok := r.agent.(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)

func (r *Reviewer) PreLaunch(ctx context.Context, inv ports.ReviewInvocation) error {
	return r.agent.InstallAgentProfile(ctx, ports.WorkspaceHookConfig{
		DataDir:          inv.DataDir,
		SessionID:        inv.ReviewerID,
		WorkspacePath:    inv.WorkspacePath,
		SystemPromptFile: inv.SystemPromptFile,
	})
}

func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		Config:           inv.Config,
		DataDir:          inv.DataDir,
		SessionID:        inv.ReviewerID,
		WorkspacePath:    inv.WorkspacePath,
		Prompt:           inv.Prompt,
		SystemPromptFile: inv.SystemPromptFile,
		Permissions:      ports.PermissionModeDefault,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}

	policy := []string{"--available-tools", availableTools}
	for _, tool := range allowedTools {
		policy = append(policy, "--allow-tool", tool)
	}
	for _, tool := range deniedTools {
		policy = append(policy, "--deny-tool", tool)
	}
	if inv.TaskPromptRoot != "" {
		policy = append(policy, "--add-dir", inv.TaskPromptRoot)
	}
	policy = append(policy,
		"--allow-url", "github.com",
		"--allow-url", "api.github.com",
		"--disable-builtin-mcps",
		"--no-ask-user",
	)
	return ports.ReviewCommandSpec{Argv: insertBeforeInteractive(argv, policy...)}, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 2}, nil
}

func insertBeforeInteractive(argv []string, extra ...string) []string {
	for i, arg := range argv {
		if arg == "--interactive" || arg == "-i" {
			out := make([]string, 0, len(argv)+len(extra))
			out = append(out, argv[:i]...)
			out = append(out, extra...)
			return append(out, argv[i:]...)
		}
	}
	return append(argv, extra...)
}
