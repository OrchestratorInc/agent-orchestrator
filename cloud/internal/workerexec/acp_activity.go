package workerexec

import (
	"encoding/json"
	"fmt"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// acpToolState is what the worker remembers of one ACP tool call. A
// tool_call_update carries only the fields that changed, but every chat
// activity it becomes is a full snapshot: the Chat timeline merges activities
// by id and replaces the summary on each one.
type acpToolState struct {
	title     string
	kind      acp.ToolKind
	status    acp.ToolCallStatus
	content   []acp.ToolCallContent
	rawInput  any
	rawOutput any
	meta      map[string]any
}

// acpToolActivity projects an ACP tool call onto the activity kinds the Chat
// timeline renders, the way local Chat does (activityKindFromTool in the
// desktop's ACP driver). Every field is bounded: each update is stored as an
// event.
func acpToolActivity(id string, tool *acpToolState) *worker.ChatActivity {
	kind := "mcp_tool"
	switch tool.kind {
	case acp.ToolKindExecute:
		kind = "command"
	case acp.ToolKindEdit, acp.ToolKindDelete, acp.ToolKindMove:
		kind = "file_change"
	case acp.ToolKindThink:
		kind = "reasoning"
	}
	status := "running"
	switch tool.status {
	case acp.ToolCallStatusCompleted:
		status = "completed"
	case acp.ToolCallStatusFailed:
		status = "failed"
	}
	output := acpToolOutput(tool)
	detail := map[string]any{"protocol": "acp", "toolKind": string(tool.kind)}
	if input := boundedACPValue(tool.rawInput); input != nil && kind == "mcp_tool" {
		// A tool row shows what it was given (a command row shows its command,
		// a file change its files).
		detail["arguments"] = input
	}
	if name := acpClaudeToolName(tool.meta); name != "" {
		detail["toolName"] = boundedCodexField(name)
	}
	summary := strings.TrimSpace(tool.title)
	switch kind {
	case "command":
		if command := acpCommand(tool.rawInput); command != "" {
			command = boundedACPCommand(command)
			detail["command"] = command
			detail["rawCommand"] = command
			if summary == "" {
				summary = command
			}
		}
		if output != "" {
			detail["output"] = output
			detail["outputSource"] = "aggregate"
		}
		if summary == "" {
			summary = "Ran a command"
		}
	case "file_change":
		files := acpFiles(tool.content)
		detail["files"] = files
		detail["filesTruncated"] = acpDiffCount(tool.content) > len(files)
		if summary == "" {
			summary = "Edited files"
		}
	case "reasoning":
		if output != "" {
			detail["text"] = output
		}
		if summary == "" {
			summary = "Reasoning"
		}
	default:
		if output != "" {
			detail["result"] = output
		}
		if summary == "" {
			summary = "Used a tool"
		}
	}
	return fitACPActivity(&worker.ChatActivity{ID: id, Kind: kind, Status: status, Summary: boundedCodexField(summary), Detail: detail})
}

// maxACPActivityBytes keeps one activity well inside the control plane's limit
// on a worker event (maxWorkerOutput + maxWorkerControlBody, 24 KiB).
const maxACPActivityBytes = 20 << 10

// fitACPActivity drops the largest optional fields until the activity fits,
// so an outsized tool result never turns into a rejected event.
func fitACPActivity(activity *worker.ChatActivity) *worker.ChatActivity {
	for _, field := range []string{"arguments", "result", "output", "text", "files"} {
		raw, err := json.Marshal(activity)
		if err != nil || len(raw) <= maxACPActivityBytes {
			return activity
		}
		if _, ok := activity.Detail[field]; ok {
			delete(activity.Detail, field)
			activity.Detail[field+"Truncated"] = true
		}
	}
	return activity
}

func boundedACPCommand(command string) string {
	if len(command) > 2<<10 {
		return command[:2<<10]
	}
	return command
}

// acpToolOutput is the tool's text result: its content blocks, else its raw
// output, keeping the end (where a command's result is) within the bound.
func acpToolOutput(tool *acpToolState) string {
	var parts []string
	for _, item := range tool.content {
		if item.Content != nil && item.Content.Content.Text != nil {
			parts = append(parts, unfencedACPText(item.Content.Content.Text.Text))
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" && tool.rawOutput != nil {
		switch value := tool.rawOutput.(type) {
		case string:
			text = value
		default:
			if raw, err := json.Marshal(value); err == nil {
				text = string(raw)
			}
		}
	}
	if len(text) > 8<<10 {
		text = text[len(text)-8<<10:]
	}
	return text
}

// unfencedACPText drops the markdown fence Claude wraps tool output in
// ("```console" ... "```"): the row already shows it as preformatted text.
func unfencedACPText(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "```") || !strings.HasSuffix(trimmed, "```") || len(trimmed) < 6 {
		return text
	}
	body := strings.TrimSuffix(trimmed, "```")
	newline := strings.IndexByte(body, '\n')
	if newline < 0 {
		return text
	}
	return strings.TrimSuffix(body[newline+1:], "\n")
}

func acpCommand(rawInput any) string {
	input, _ := rawInput.(map[string]any)
	command, _ := input["command"].(string)
	return strings.TrimSpace(command)
}

func acpClaudeToolName(meta map[string]any) string {
	claude, _ := meta["claudeCode"].(map[string]any)
	name, _ := claude["toolName"].(string)
	return strings.TrimSpace(name)
}

func acpDiffCount(content []acp.ToolCallContent) int {
	count := 0
	for _, item := range content {
		if item.Diff != nil {
			count++
		}
	}
	return count
}

// acpFiles is up to 8 changed files, each with its line counts and a bounded
// whole-file patch built from the old and new text ACP reports.
func acpFiles(content []acp.ToolCallContent) []map[string]any {
	files := make([]map[string]any, 0, min(acpDiffCount(content), 8))
	for _, item := range content {
		if item.Diff == nil || item.Diff.Path == "" || len(files) >= 8 {
			continue
		}
		oldText := ""
		if item.Diff.OldText != nil {
			oldText = *item.Diff.OldText
		}
		status := "modified"
		switch {
		case item.Diff.OldText == nil:
			status = "added"
		case item.Diff.NewText == "":
			status = "deleted"
		}
		additions, deletions := acpLineChanges(oldText, item.Diff.NewText)
		patch := acpPatch(oldText, item.Diff.NewText)
		file := map[string]any{"path": boundedCodexField(item.Diff.Path), "status": status,
			"additions": additions, "deletions": deletions, "patch": boundedCodexPatch(patch)}
		if len(patch) > 512 {
			file["patchTruncated"] = true
		}
		files = append(files, file)
	}
	return files
}

// acpLineChanges counts lines added and removed between two texts, ignoring
// order: enough for the row's +N -M without a full diff.
func acpLineChanges(oldText, newText string) (int, int) {
	remaining := map[string]int{}
	for _, line := range acpLines(oldText) {
		remaining[line]++
	}
	additions := 0
	for _, line := range acpLines(newText) {
		if remaining[line] > 0 {
			remaining[line]--
		} else {
			additions++
		}
	}
	deletions := 0
	for _, count := range remaining {
		deletions += count
	}
	return additions, deletions
}

func acpLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}

// acpPatch is one hunk replacing the old text with the new, the shape the
// Chat diff view renders (it has no file header: the row names the file).
func acpPatch(oldText, newText string) string {
	oldLines, newLines := acpLines(oldText), acpLines(newText)
	var patch strings.Builder
	fmt.Fprintf(&patch, "@@ -%s +%s @@\n", acpHunkRange(len(oldLines)), acpHunkRange(len(newLines)))
	for _, line := range oldLines {
		patch.WriteString("-" + line + "\n")
	}
	for _, line := range newLines {
		patch.WriteString("+" + line + "\n")
	}
	return patch.String()
}

func acpHunkRange(lines int) string {
	if lines == 0 {
		return "0,0"
	}
	return fmt.Sprintf("1,%d", lines)
}

func boundedACPValue(value any) any {
	if value == nil {
		return nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	return boundedCodexJSON(raw)
}
