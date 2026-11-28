package qwen

import (
	"context"
	"path/filepath"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const (
	qwenSettingsDirName  = ".qwen"
	qwenSettingsFileName = "settings.json"

	// qwenHookCommandPrefix identifies the hook commands AO owns, so install
	// skips duplicates and uninstall recognizes AO entries by prefix.
	qwenHookCommandPrefix = "ao hooks qwen "

	// qwenHookTimeout is in milliseconds: Qwen Code (a gemini-cli fork) measures
	// hook timeouts in ms, unlike Claude/Codex which use seconds.
	qwenHookTimeout = 30000
)

// qwenSessionStartMatcher is referenced by pointer so SessionStart serializes
// with the Qwen-documented startup and resume source matcher.
var qwenSessionStartMatcher = "startup|resume"

// qwenManagedHooks is the source of truth for the hooks AO installs:
// SessionStart (under the startup/resume source matcher), UserPromptSubmit,
// PermissionRequest, and Stop.
var qwenManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &qwenSessionStartMatcher, Command: qwenHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: qwenHookCommandPrefix + "user-prompt-submit"},
	{Event: "PermissionRequest", Command: qwenHookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: qwenHookCommandPrefix + "stop"},
}

// qwenHooks manages AO's hooks in the workspace-local .qwen/settings.json file.
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

// GetAgentHooks installs AO's Qwen Code hooks, preserving user-defined hooks and unrelated settings.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return qwenHooks.Install(ctx, cfg.WorkspacePath)
}

// UninstallHooks removes AO's Qwen Code hooks, leaving user-defined hooks untouched.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return qwenHooks.Uninstall(ctx, workspacePath)
}

// AreHooksInstalled reports whether any AO Qwen Code hook is present.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return qwenHooks.AreInstalled(ctx, workspacePath)
}
