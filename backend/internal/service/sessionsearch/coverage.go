package sessionsearch

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// What a tool call, a tool result and reasoning contribute to the index, the
// same for every source (AO Chat, Claude Code, Codex, OpenCode):
//
//   - A tool call is its name and one argument: the first of toolArgumentKeys
//     that holds text, cut to toolArgumentChars. That is what a person
//     remembers about a call: the command, the file or the pattern.
//   - A tool result keeps its first toolOutputChars. Logs and file dumps
//     would be most of the index while adding little that is searched.
//   - Reasoning is not indexed. It restates the turn's reply and plan.
//
// Messages, plans, titles and file paths are not cut by these rules.
const (
	toolArgumentChars = 2000
	toolOutputChars   = 3072
)

// toolArgumentKeys are the arguments a tool call is remembered by, in order.
var toolArgumentKeys = []string{"command", "cmd", "file_path", "path", "pattern", "query", "description"}

// toolArgument is a tool call's argument: the first of toolArgumentKeys that
// holds text in an object input, or the input itself when it is text. A JSON
// string that holds an object (Codex function arguments) is read as the object.
func toolArgument(input json.RawMessage) string {
	input = json.RawMessage(strings.TrimSpace(string(input)))
	if len(input) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(input, &text) == nil {
		trimmed := strings.TrimSpace(text)
		if strings.HasPrefix(trimmed, "{") {
			if argument := toolArgument(json.RawMessage(trimmed)); argument != "" {
				return argument
			}
			return ""
		}
		return trimmed
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return ""
	}
	for _, key := range toolArgumentKeys {
		raw, ok := fields[key]
		if !ok {
			continue
		}
		var value string
		if json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
			return value
		}
		// Codex records a command as its argv.
		var argv []string
		if json.Unmarshal(raw, &argv) == nil && len(argv) > 0 {
			if command := commandFromArgv(argv); strings.TrimSpace(command) != "" {
				return command
			}
		}
	}
	return ""
}

// commandFromArgv renders a recorded argv: the script after a shell's -c or
// -lc is what the agent wrote, so only that is kept.
func commandFromArgv(argv []string) string {
	if len(argv) >= 3 && (argv[len(argv)-2] == "-lc" || argv[len(argv)-2] == "-c") {
		return argv[len(argv)-1]
	}
	return strings.Join(argv, " ")
}

// toolCallText renders a tool call as "name: argument", with the argument cut
// to toolArgumentChars; either part alone when the other is empty.
func toolCallText(name, argument string) string {
	name = strings.TrimSpace(name)
	argument = firstChars(strings.TrimSpace(argument), toolArgumentChars)
	switch {
	case name != "" && argument != "":
		return name + ": " + argument
	case name != "":
		return name
	default:
		return argument
	}
}

// toolOutputText keeps the first toolOutputChars of a tool's output.
func toolOutputText(output string) string {
	return firstChars(strings.TrimSpace(output), toolOutputChars)
}

// toolText is one tool row: its call, then the start of its output.
func toolText(name, argument, output string) string {
	return joinSections(toolCallText(name, argument), toolOutputText(output))
}

// joinSections joins the non-empty sections with a blank line.
func joinSections(sections ...string) string {
	var out strings.Builder
	for _, section := range sections {
		section = strings.TrimSpace(section)
		if section == "" {
			continue
		}
		if out.Len() > 0 {
			out.WriteString("\n\n")
		}
		out.WriteString(section)
	}
	return out.String()
}

// firstChars keeps the first limit characters of value, never splitting one.
func firstChars(value string, limit int) string {
	if utf8.RuneCountInString(value) <= limit {
		return value
	}
	count := 0
	for index := range value {
		if count == limit {
			return value[:index]
		}
		count++
	}
	return value
}

// chatTool is what an AO Chat tool activity's detail records about the call.
type chatTool struct {
	name     string
	argument string
	output   string
}

// activityTool reads a tool activity's detail: the provider's tool name, the
// remembered argument of its input (or of the detail itself, where a command
// is recorded at the top level), and the output the detail carries when the
// activity's own output column is empty (MCP tools and file changes).
func activityTool(detail string) chatTool {
	var fields struct {
		ProviderToolName string          `json:"providerToolName"`
		Server           string          `json:"server"`
		ToolName         string          `json:"toolName"`
		Tool             string          `json:"tool"`
		Input            json.RawMessage `json:"input"`
		Arguments        json.RawMessage `json:"arguments"`
		Output           json.RawMessage `json:"output"`
		Result           json.RawMessage `json:"result"`
	}
	if json.Unmarshal([]byte(detail), &fields) != nil {
		return chatTool{}
	}
	name := fields.ProviderToolName
	if name == "" {
		tool := fields.ToolName
		if tool == "" {
			tool = fields.Tool
		}
		if tool != "" && fields.Server != "" {
			tool = fields.Server + "." + tool
		}
		name = tool
	}
	argument := toolArgument(fields.Input)
	if argument == "" {
		argument = toolArgument(fields.Arguments)
	}
	if argument == "" {
		argument = toolArgument(json.RawMessage(detail))
	}
	output := toolResultText(fields.Output)
	if strings.TrimSpace(output) == "" {
		output = toolResultText(fields.Result)
	}
	return chatTool{name: name, argument: argument, output: output}
}

// toolResultText is the text of a tool result: a string as is, or the text of
// each part of a content array ({"type":"text","text":...}). An object with a
// content array (an MCP result) is read through it. Anything else falls back
// to its string values.
func toolResultText(raw json.RawMessage) string {
	raw = json.RawMessage(strings.TrimSpace(string(raw)))
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var parts []json.RawMessage
	if json.Unmarshal(raw, &parts) == nil {
		var out []string
		for _, part := range parts {
			var value string
			if json.Unmarshal(part, &value) == nil {
				out = append(out, value)
				continue
			}
			var item struct {
				Text string `json:"text"`
			}
			if json.Unmarshal(part, &item) == nil && item.Text != "" {
				out = append(out, item.Text)
			}
		}
		return strings.Join(out, "\n")
	}
	var object struct {
		Text    string          `json:"text"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &object) == nil {
		if object.Text != "" {
			return object.Text
		}
		if len(object.Content) > 0 {
			return toolResultText(object.Content)
		}
	}
	return rawJSONText(raw)
}
