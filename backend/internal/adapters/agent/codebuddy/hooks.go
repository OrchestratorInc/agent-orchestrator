package codebuddy

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var hooks = hooksjson.Manager{
	Label: "codebuddy", CommandPrefix: "ao hooks codebuddy ", Timeout: 10,
	Path: func(ws string) string { return filepath.Join(ws, ".codebuddy", "settings.local.json") },
	Managed: []hooksjson.HookSpec{
		{Event: "SessionStart", Command: "ao hooks codebuddy session-start"},
		{Event: "UserPromptSubmit", Command: "ao hooks codebuddy user-prompt-submit"},
		{Event: "PreToolUse", Command: "ao hooks codebuddy pre-tool-use"},
		{Event: "PostToolUse", Command: "ao hooks codebuddy post-tool-use"},
		{Event: "PostToolUseFailure", Command: "ao hooks codebuddy post-tool-use-failure"},
		{Event: "PermissionRequest", Command: "ao hooks codebuddy permission-request"},
		{Event: "Stop", Command: "ao hooks codebuddy stop"},
	},
}

func (*Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return hooks.Install(ctx, cfg.WorkspacePath)
}

func (*Plugin) UninstallHooks(ctx context.Context, ws string) error { return hooks.Uninstall(ctx, ws) }
func (*Plugin) AreHooksInstalled(ctx context.Context, ws string) (bool, error) {
	return hooks.AreInstalled(ctx, ws)
}
