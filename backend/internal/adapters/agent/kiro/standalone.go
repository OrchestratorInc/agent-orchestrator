package kiro

import (
	"context"
	"encoding/json"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// PrepareStandaloneWorkspace creates the custom agent selected by the normal
// launch command without installing daemon-dependent lifecycle callbacks.
// The caller owns this private workspace; no user-level Kiro config is changed.
func (p *Plugin) PrepareStandaloneWorkspace(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeKiroHooks(kiroAgentPath(cfg.WorkspacePath), map[string]json.RawMessage{}, map[string]json.RawMessage{}, cfg.SystemPrompt, cfg.SystemPromptFile, cfg.Config)
}
