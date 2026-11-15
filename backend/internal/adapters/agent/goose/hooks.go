package goose

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	gooseHooksRootDirName = ".agents"
	goosePluginsDirName   = "plugins"
	goosePluginName       = "ao"
	gooseHooksSubDirName  = "hooks"
	gooseHooksFileName    = "hooks.json"

	gooseHookCommandPrefix = "ao hooks goose "
	gooseHookTimeout       = 30
)

var gooseManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Command: gooseHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: gooseHookCommandPrefix + "user-prompt-submit"},
	{Event: "Stop", Command: gooseHookCommandPrefix + "stop"},
}

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

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return gooseHooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return gooseHooks.Uninstall(ctx, workspacePath)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return gooseHooks.AreInstalled(ctx, workspacePath)
}
