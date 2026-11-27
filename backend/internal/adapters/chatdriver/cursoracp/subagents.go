package cursoracp

import (
	"strings"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// Cursor subagents, filed the way the chat reads Claude's.
//
// `cursor-agent acp` (as of 2026.10.16) launches a subagent with its Task tool,
// sent as a tool call titled "Task: <description>" with rawInput
// {_toolName: "task", prompt, description, subagentType}. With the subagents
// extension negotiated, it announces each child and streams its work under the
// child's session (acp/spawned_subagents.go). A foreground Task completes when
// its child does; a background one completes at once with rawOutput
// {isBackground: true}. The Task's result carries no report, and on completion
// Cursor also sends a cursor/task request with the task's details, which AO
// acknowledges: the subagent updates already carry them.

const (
	cursorTaskTool = "task"
	// taskLaunchTool is the name the renderer lists as a launch.
	taskLaunchTool = "Task"
	taskMethod     = "cursor/task"
)

func spawnedSubagents() *acpdriver.SpawnedSubagents {
	return &acpdriver.SpawnedSubagents{Launch: subagentLaunch}
}

func subagentLaunch(call acpdriver.RecordedCall) (acpdriver.RecordedLaunch, bool) {
	input, ok := call.RawInput.(map[string]any)
	if !ok {
		return acpdriver.RecordedLaunch{}, false
	}
	if name, _ := input["_toolName"].(string); name != cursorTaskTool {
		return acpdriver.RecordedLaunch{}, false
	}
	prompt, _ := input["prompt"].(string)
	title, _ := input["description"].(string)
	if strings.TrimSpace(title) == "" {
		title = strings.TrimSpace(strings.TrimPrefix(call.Title, "Task:"))
	}
	agentType, _ := input["subagentType"].(string)
	background := false
	if output, ok := call.RawOutput.(map[string]any); ok {
		background, _ = output["isBackground"].(bool)
	}
	return acpdriver.RecordedLaunch{
		ToolName: taskLaunchTool, Title: strings.TrimSpace(title), Prompt: prompt,
		AgentType: shownAgentType(agentType), Foreground: !background,
	}, true
}

// shownAgentType hides Cursor's general-purpose default, as the chat hides
// Claude's: only a specialized agent's type says something.
func shownAgentType(agentType string) string {
	agentType = strings.TrimSpace(agentType)
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(agentType))
	if normalized == "generalpurpose" || normalized == "default" {
		return ""
	}
	return agentType
}
