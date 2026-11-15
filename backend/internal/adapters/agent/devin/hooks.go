package devin

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
)

const (
	devinConfigDirName     = ".devin"
	devinConfigFileName    = "config.local.json"
	devinHookCommandPrefix = "ao hooks devin "
	devinHookTimeout       = 30
)

var devinManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Command: devinHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: devinHookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: devinHookCommandPrefix + "stop"},
	{Event: "SessionEnd", Command: devinHookCommandPrefix + "session-end"},
}

var devinHooks = hooksjson.Manager{
	Label:         "devin",
	CommandPrefix: devinHookCommandPrefix,
	Timeout:       devinHookTimeout,
	Path:          devinConfigPath,
	Managed:       devinManagedHooks,
}

func devinConfigPath(workspacePath string) string {
	return filepath.Join(workspacePath, devinConfigDirName, devinConfigFileName)
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return devinHooks.Uninstall(ctx, workspacePath)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return devinHooks.AreInstalled(ctx, workspacePath)
}
