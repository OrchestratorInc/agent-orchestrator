package reviewpolicy

import (
	"encoding/json"
	"errors"
	"strings"
)

// claudeToolKinds maps Claude Code's tool names onto policy kinds. Claude-
// compatible hook payloads (Claude Code, Kimchi) carry these names.
var claudeToolKinds = map[string]Kind{
	"Read": KindRead, "Grep": KindRead, "Glob": KindRead, "LS": KindRead, "NotebookRead": KindRead,
	"Bash": KindShell, "PowerShell": KindShell,
	"TodoWrite": KindBookkeeping, "TodoRead": KindBookkeeping, "TaskCreate": KindBookkeeping,
	"TaskUpdate": KindBookkeeping, "TaskList": KindBookkeeping, "TaskGet": KindBookkeeping,
	"ToolSearch": KindBookkeeping, "BashOutput": KindBookkeeping, "TaskOutput": KindBookkeeping,
	"Edit": KindWrite, "Write": KindWrite, "MultiEdit": KindWrite, "NotebookEdit": KindWrite,
	"Agent": KindSubagent, "Task": KindSubagent,
	"WebFetch": KindNetwork, "WebSearch": KindNetwork,
}

// ClaudeToolKind maps one Claude tool name onto a policy kind.
func ClaudeToolKind(tool string) Kind {
	if kind, ok := claudeToolKinds[tool]; ok {
		return kind
	}
	if strings.HasPrefix(tool, "mcp__") {
		return KindMCP
	}
	return KindUnknown
}

// DecodeClaudeHookCall reads the tool call from a Claude-compatible
// PreToolUse or PermissionRequest hook payload. A payload that cannot be read
// is an error so the caller can deny rather than guess.
func DecodeClaudeHookCall(payload []byte) (Call, error) {
	var p struct {
		ToolName  string `json:"tool_name"`
		ToolInput struct {
			Command string `json:"command"`
		} `json:"tool_input"`
	}
	if err := json.Unmarshal(payload, &p); err != nil {
		return Call{}, err
	}
	if strings.TrimSpace(p.ToolName) == "" {
		return Call{}, errors.New("hook payload names no tool")
	}
	return Call{Kind: ClaudeToolKind(p.ToolName), Tool: p.ToolName, Command: p.ToolInput.Command}, nil
}
