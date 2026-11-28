package goose

import (
	"context"
	"path/filepath"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

const (
	// Goose auto-discovers any plugin dir containing a hooks/hooks.json at
	// startup; unlike Codex there is no separate feature flag to toggle, so
	// installing the file is sufficient.
	gooseHooksRootDirName = ".agents"
	goosePluginsDirName   = "plugins"
	goosePluginName       = "ao"
	gooseHooksSubDirName  = "hooks"
	gooseHooksFileName    = "hooks.json"

	// gooseHookCommandPrefix identifies the hook commands AO owns, so install
	// skips duplicates and uninstall recognizes AO entries by prefix.
	gooseHookCommandPrefix = "ao hooks goose "
	gooseHookTimeout       = 30
)

// gooseManagedHooks is the source of truth for the hooks AO installs. Goose
// groups every hook under the nil matcher. Goose has no permission/approval
// lifecycle event yet, so AO installs only the session/prompt/stop signals.
var gooseManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Command: gooseHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: gooseHookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: gooseHookCommandPrefix + "stop"},
}

// gooseHooks manages AO's hooks in the workspace-local
// .agents/plugins/ao/hooks/hooks.json file.
var gooseHooks = hooksjson.Manager{
	Label:         "goose",
	CommandPrefix: gooseHookCommandPrefix,
	Timeout:       gooseHookTimeout,
	Path:          gooseHooksPath,
	Managed:       gooseManagedHooks,
}

func gooseHooksPath(workspacePath string) string {
	return filepath.Join(workspacePath, gooseHooksRootDirName, goosePluginsDirName, goosePluginName, gooseHooksSubDirName, gooseHooksFileName)
}

// GetAgentHooks installs AO's Goose hooks, preserving user-defined hooks.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return gooseHooks.Install(ctx, cfg.WorkspacePath)
}

// UninstallHooks removes AO's Goose hooks, leaving user-defined hooks untouched.
func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return gooseHooks.Uninstall(ctx, workspacePath)
}

// AreHooksInstalled reports whether any AO Goose hook is present.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return gooseHooks.AreInstalled(ctx, workspacePath)
}
