package letta

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hooksjson"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const hookPrefix = "ao hooks letta-code "

var toolMatcher = "*"
var hooks = hooksjson.Manager{
	Label: "letta-code", CommandPrefix: hookPrefix,
	// Letta uses milliseconds, unlike Claude Code's seconds.
	Timeout: 10000,
	Path:    func(workspace string) string { return filepath.Join(workspace, ".letta", "settings.local.json") },
	Managed: []hooksjson.HookSpec{
		{Event: "SessionStart", Command: hookPrefix + "session-start", Quiet: true},
		{Event: "UserPromptSubmit", Command: hookPrefix + "user-prompt-submit", Quiet: true},
		{Event: "PreToolUse", Matcher: &toolMatcher, Command: hookPrefix + "pre-tool-use", Quiet: true},
		{Event: "PostToolUse", Matcher: &toolMatcher, Command: hookPrefix + "post-tool-use", Quiet: true},
		{Event: "PostToolUseFailure", Matcher: &toolMatcher, Command: hookPrefix + "post-tool-use-failure", Quiet: true},
		{Event: "PermissionRequest", Matcher: &toolMatcher, Command: hookPrefix + "permission-request", Quiet: true},
		{Event: "Stop", Command: hookPrefix + "stop", Quiet: true},
	},
}

// GetAgentHooks merges private workspace hooks and their sibling gitignore.
func (p *Plugin) GetAgentHooks(ctx context.Context, cfg ports.WorkspaceHookConfig) error {
	return hooks.Install(ctx, cfg.WorkspacePath)
}

// UninstallHooks removes only AO-owned hooks, retaining user configuration.
func (p *Plugin) UninstallHooks(ctx context.Context, workspace string) error {
	return hooks.Uninstall(ctx, workspace)
}

// AreHooksInstalled reports AO's local hook ownership.
func (p *Plugin) AreHooksInstalled(ctx context.Context, workspace string) (bool, error) {
	return hooks.AreInstalled(ctx, workspace)
}

// DeriveActivityState maps actual turn/approval events. SessionStart captures
// identity only: initialization is not evidence that an agent turn has settled.
func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "user-prompt-submit", "pre-tool-use", "post-tool-use", "post-tool-use-failure":
		return domain.ActivityActive, true
	case "permission-request":
		return domain.ActivityBlocked, true
	case "stop":
		return domain.ActivityIdle, true
	default:
		return "", false
	}
}

// NativeConversationID deliberately ignores the transient session_id and the
// agent identity: only a conv-* value is an exact conversation restore target.
func NativeConversationID(payload []byte) string {
	var data struct {
		ConversationID string `json:"conversation_id"`
	}
	if json.Unmarshal(payload, &data) != nil || !ValidConversationID(data.ConversationID) {
		return ""
	}
	return data.ConversationID
}

// NativeToolCallID preserves Letta's correlation ID for approval transitions.
func NativeToolCallID(payload []byte) string {
	var data struct {
		ToolCallID string `json:"tool_call_id"`
	}
	if json.Unmarshal(payload, &data) != nil {
		return ""
	}
	id := strings.TrimSpace(data.ToolCallID)
	if len(id) > 256 || domain.SanitizeControlChars(id) != id {
		return ""
	}
	return id
}
