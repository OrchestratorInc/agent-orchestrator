package claudecode

import (
	"context"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	claudeSettingsDirName   = ".claude"
	claudeSettingsFileName  = "settings.local.json"
	claudeHookCommandPrefix = "ao hooks claude-code "
	claudeHookTimeout       = 30
)

var claudeSessionStartMatcher = "startup|resume|clear|compact|fork"

var claudeManagedHooks = []hooksjson.HookSpec{
	{Event: "SessionStart", Matcher: &claudeSessionStartMatcher, Command: claudeHookCommandPrefix + "session-start"},
	{Event: "UserPromptSubmit", Command: claudeHookCommandPrefix + "user-prompt-submit"},
	{Event: "PreToolUse", Command: claudeHookCommandPrefix + "pre-tool-use"},
	{Event: "PostToolUse", Command: claudeHookCommandPrefix + "post-tool-use"},
	{Event: "PostToolUseFailure", Command: claudeHookCommandPrefix + "post-tool-use-failure"},
	{Event: "PermissionRequest", Command: claudeHookCommandPrefix + "permission-request"},
	{Event: "Stop", Command: claudeHookCommandPrefix + "stop"},
	{Event: "StopFailure", Command: claudeHookCommandPrefix + "stop-failure"},
	{Event: "Notification", Command: claudeHookCommandPrefix + "notification"},
	{Event: "SubagentStart", Command: claudeHookCommandPrefix + "subagent-start"},
	{Event: "SubagentStop", Command: claudeHookCommandPrefix + "subagent-stop"},
	{Event: "SessionEnd", Command: claudeHookCommandPrefix + "session-end"},
}

var claudeHooks = hooksjson.Manager{
	Label:                 "claude-code",
	CommandPrefix:         claudeHookCommandPrefix,
	LegacyCommandPrefixes: []string{"ao hooks continue "},
	Timeout:               claudeHookTimeout,
	Path:                  claudeSettingsPath,
	Managed:               claudeManagedHooks,
}

func claudeSettingsPath(workspacePath string) string {
	return filepath.Join(workspacePath, claudeSettingsDirName, claudeSettingsFileName)
}

func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return claudeHooks.Install(ctx, cfg.WorkspacePath)
}

func (p *Plugin) UninstallHooks(ctx context.Context, workspacePath string) error {
	return claudeHooks.Uninstall(ctx, workspacePath)
}

func (p *Plugin) AreHooksInstalled(ctx context.Context, workspacePath string) (bool, error) {
	return claudeHooks.AreInstalled(ctx, workspacePath)
}
