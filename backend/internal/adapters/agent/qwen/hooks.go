package qwen

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	qwenSettingsDirName  = ".qwen"
	qwenSettingsFileName = "settings.json"

	qwenHookCommandPrefix = "ao hooks qwen "

	qwenHookTimeout = 30000
)

var qwenSessionStartMatcher = "startup|resume"

var qwenManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &qwenSessionStartMatcher, Command: qwenHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: qwenHookCommandPrefix + "user-prompt-submit"},
	{Event: "PermissionRequest", Command: qwenHookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: qwenHookCommandPrefix + "stop"},
}

var qwenHooks = hooksjson.Manager{
	Label:         "qwen",
	CommandPrefix: qwenHookCommandPrefix,
	Timeout:       qwenHookTimeout,
	Path:          qwenSettingsPath,
	Managed:       qwenManagedHooks,
}

func qwenSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, qwenSettingsDirName, qwenSettingsFileName)
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return qwenHooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return qwenHooks.Uninstall(ctx, workspacePath)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return qwenHooks.AreInstalled(ctx, workspacePath)
}
