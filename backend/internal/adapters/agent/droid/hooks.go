package droid

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	droidSettingsDirName = ".factory"
	droidHooksFileName   = "hooks.json"

	droidHookCommandPrefix = "ao hooks droid "
	droidHookTimeout       = 30
)

var droidStartupMatcher = "startup"

var droidManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &droidStartupMatcher, Command: droidHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: droidHookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: droidHookCommandPrefix + "stop"},
	{Event: "Notification", Command: droidHookCommandPrefix + "notification"},
	{Event: "SessionEnd", Command: droidHookCommandPrefix + "session-end"},
}

var droidHooks = hooksjson.Manager{
	Label:         "droid",
	CommandPrefix: droidHookCommandPrefix,
	Timeout:       droidHookTimeout,
	Path:          droidHooksPath,
	Managed:       droidManagedHooks,
}

func droidHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, droidSettingsDirName, droidHooksFileName)
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return droidHooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return droidHooks.Uninstall(ctx, workspacePath)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return droidHooks.AreInstalled(ctx, workspacePath)
}
