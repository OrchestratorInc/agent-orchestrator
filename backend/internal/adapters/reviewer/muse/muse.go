package muse

import (
	"context"

	workermuse "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/muse"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/agentrestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Reviewer struct {
	agent ports.Agent
}

func New() *Reviewer {
	return &Reviewer{agent: workermuse.New()}
}

func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerMuse
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
		DataDir:          inv.DataDir,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{Argv: addNoWriteFlag(argv, inv.Prompt != "")}, nil
}

func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, ok, err := agentrestore.Command(ctx, r.agent, inv, agentrestore.Options{Permissions: ports.PermissionModeAuto})
	if err != nil || !ok {
		return cmd, ok, err
	}
	cmd.Argv = addNoWriteFlag(cmd.Argv, false)
	return cmd, true, nil
}

func (*Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

func (*Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInput, Input: "\x1b"}, nil
}

func addNoWriteFlag(argv []string, hasPrompt bool) []string {
	return insertBeforeMuseOperand(argv, hasPrompt, "--disable-write")
}

func insertBeforeMuseOperand(argv []string, hasPrompt bool, extra ...string) []string {
	if len(argv) == 0 {
		return append([]string(nil), extra...)
	}
	insertAt := len(argv)
	for i, arg := range argv {
		if arg == "resume" || arg == "exec" || arg == "export" || arg == "trace" || arg == "skills" || arg == "sandbox" || arg == "session-message" || arg == "auth" || arg == "login" || arg == "logout" || arg == "init" {
			insertAt = i
			break
		}
	}
	if insertAt == len(argv) && hasPrompt {
		insertAt = len(argv) - 1
	}
	out := make([]string, 0, len(argv)+len(extra))
	out = append(out, argv[:insertAt]...)
	out = append(out, extra...)
	return append(out, argv[insertAt:]...)
}
