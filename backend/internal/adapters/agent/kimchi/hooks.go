package kimchi

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	settingsDirName  = ".kimchi"
	settingsFileName = "hooks.local.json"

	hookCommandPrefix = "ao hooks kimchi "
	hookTimeout       = 30
)

var startupMatcher = "startup"

var managedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &startupMatcher, Command: hookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: hookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: hookCommandPrefix + "stop"},
	{Event: "Notification", Command: hookCommandPrefix + "notification"},
	{Event: "SessionEnd", Command: hookCommandPrefix + "session-end"},
	{Event: "PreToolUse", Command: hookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: hookCommandPrefix + "post-tool-use"},
	{Event: "PostToolUseFail", Command: hookCommandPrefix + "post-tool-use-failure"},
}

var kimchiHooks = hooksjson.Manager{
	Label:         "kimchi",
	CommandPrefix: hookCommandPrefix,
	Timeout:       hookTimeout,
	Path:          settingsPath,
	Managed:       managedHooks,
}

func settingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, settingsDirName, settingsFileName)
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return kimchiHooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return kimchiHooks.Uninstall(ctx, workspacePath)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return kimchiHooks.AreInstalled(ctx, workspacePath)
}
