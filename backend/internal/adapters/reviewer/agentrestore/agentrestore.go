package agentrestore

import (
	"context"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type Options struct {
	Permissions     ports.PermissionMode
	AllowedTools    []string
	DisallowedTools []string
}

func Command(ctx context.Context, agent ports.Agent, inv ports.ReviewInvocation, opts Options) (ports.ReviewCommandSpec, bool, error) {
	agentSessionID := strings.TrimSpace(inv.AgentSessionID)
	prompt := ""
	if strings.TrimSpace(inv.RunID) != "" {
		prompt = inv.Prompt
	}
	metadata := map[string]string{}
	if agentSessionID != "" {
		metadata[ports.MetadataKeyAgentSessionID] = agentSessionID
	}
	argv, ok, err := agent.GetRestoreCommand(ctx, ports.RestoreConfig{
		Config: inv.Config,
		Session: ports.SessionRef{
			ID:            inv.ReviewerID,
			WorkspacePath: inv.WorkspacePath,
			Metadata:      metadata,
		},
		Kind:             domain.KindWorker,
		DataDir:          inv.DataDir,
		Permissions:      opts.Permissions,
		AllowedTools:     opts.AllowedTools,
		DisallowedTools:  opts.DisallowedTools,
		Prompt:           prompt,
		SystemPrompt:     inv.SystemPrompt,
		SystemPromptFile: inv.SystemPromptFile,
	})
	if err != nil || !ok {
		return ports.ReviewCommandSpec{}, ok, err
	}
	return ports.ReviewCommandSpec{
		Argv:           argv,
		AgentSessionID: strings.TrimSpace(inv.AgentSessionID),
		NativeResumed:  true,
	}, true, nil
}
