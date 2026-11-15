package cursor

import (
	"encoding/json"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const EnvPermissionMode = "AO_PERMISSION_MODE"

type PermissionDecision struct {
	Permission     string
	State          domain.ActivityState
	ReportActivity bool
}

func DeriveActivityState(event string, _ []byte) (domain.ActivityState, bool) {
	switch event {
	case "session-start", "user-prompt-submit":
		return domain.ActivityActive, true
	case "stop":
		return domain.ActivityIdle, true
	case "after-shell-execution", "after-mcp-execution", "post-tool-use", "post-tool-use-failure":
		return domain.ActivityActive, true
	case "before-shell-execution", "before-mcp-execution":
		return "", false
	default:
		return "", false
	}
}

func EvaluatePermission(mode ports.PermissionMode, _ string, _ []byte) PermissionDecision {
	if permissionRequired(mode) {
		return PermissionDecision{
			Permission:     "ask",
			State:          domain.ActivityBlocked,
			ReportActivity: true,
		}
	}
	return PermissionDecision{
		Permission:     "allow",
		State:          domain.ActivityActive,
		ReportActivity: true,
	}
}

func permissionRequired(mode ports.PermissionMode) bool {
	switch ports.NormalizePermissionMode(mode) {
	case ports.PermissionModeBypassPermissions, ports.PermissionModeAuto:
		return false
	default:
		return true
	}
}

func HookToolName(event string, payload []byte) string {
	var p struct {
		Command  string `json:"command"`
		ToolName string `json:"tool_name"`
	}
	_ = json.Unmarshal(payload, &p)
	switch event {
	case "before-shell-execution", "after-shell-execution":
		return capHookField(p.Command)
	default:
		return capHookField(p.ToolName)
	}
}

func TerminalFailureCorrelation(payload []byte) (event, toolName string, ok bool) {
	var p struct {
		ToolName    string `json:"tool_name"`
		FailureType string `json:"failure_type"`
		IsInterrupt bool   `json:"is_interrupt"`
		ToolInput   struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(payload, &p); err != nil || !terminalFailure(p.FailureType, p.IsInterrupt) {
		return "", "", false
	}
	switch {
	case p.ToolName == "Shell":
		command := capHookField(p.ToolInput.Command)
		return "cursor-shell-terminal-failure", command, command != ""
	case strings.HasPrefix(p.ToolName, "MCP:"):
		name := capHookField(strings.TrimPrefix(p.ToolName, "MCP:"))
		return "cursor-mcp-terminal-failure", name, name != ""
	default:
		return "", "", false
	}
}

func terminalFailure(failureType string, isInterrupt bool) bool {
	if isInterrupt {
		return true
	}
	switch failureType {
	case "permission_denied", "error", "timeout":
		return true
	default:
		return false
	}
}

func capHookField(value string) string {
	value = strings.TrimSpace(value)
	const maxLen = 256
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen]
}
