package droid

import (
	"context"
	"path/filepath"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const (
	droidSettingsDirName = ".factory"
	droidHooksFileName   = "hooks.json"

	// droidHookCommandPrefix identifies the hook commands AO owns, so install
	// skips duplicates and uninstall recognizes AO entries by prefix.
	droidHookCommandPrefix = "ao hooks droid "
	droidHookTimeout       = 30
)

// droidStartupMatcher is referenced by pointer so SessionStart serializes with
// its "startup" source matcher.
var droidStartupMatcher = "startup"

// droidManagedHooks is the source of truth for the hooks AO installs:
// SessionStart (under the "startup" matcher), UserPromptSubmit, Stop,
// Notification, and SessionEnd.
var droidManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &droidStartupMatcher, Command: droidHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: droidHookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: droidHookCommandPrefix + "stop"},
	{Event: "Notification", Command: droidHookCommandPrefix + "notification"},
	{Event: "SessionEnd", Command: droidHookCommandPrefix + "session-end"},
}

// droidHooks manages AO's hooks in the workspace-local .factory/hooks.json file.
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

// GetAgentHooks installs AO's Droid hooks, preserving user-defined hooks.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return droidHooks.Install(ctx, cfg.WorkspacePath)
}

// UninstallHooks removes AO's Droid hooks, leaving user-defined hooks untouched.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return droidHooks.Uninstall(ctx, workspacePath)
}

// AreHooksInstalled reports whether any AO Droid hook is present.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return droidHooks.AreInstalled(ctx, workspacePath)
}
