package agyacp

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// Antigravity subagents.
//
// `agy acp` launches a subagent with its invoke_subagent tool. The call
// completes as soon as the child starts and carries only its input (Prompt,
// Role, TypeName); nothing on the parent's session names the child or carries
// its work. The child is an agy session of its own, which session/load streams,
// and agy records under the chat data directory which launch started it:
//
//	<GEMINI_HOME>/antigravity-acp/brain/<session>/.system_generated/
//	  subagents/<child>.json   one per child the session started
//	  messages/<id>.json       messages the session received; a child's first
//	                           is its task, and sourceMetadata.tool names the
//	                           launching session and tool call
//
// A child reports with its send_message tool, addressed to the session that
// launched it. All of this was read from agy 1.5.15; the files are agy's own
// and undocumented, so anything unreadable leaves a launch without a child and
// the chat shows it with no report.

const (
	invokeSubagentTool = "invoke_subagent"
	sendMessageTool    = "send_message"
	// defaultSubagentType is the type that inherits the parent's setup, which
	// says nothing worth showing, as Claude's general-purpose does not.
	defaultSubagentType = "self"
	// maxChildMessages bounds the messages read to find a child's launch. Its
	// task is the first it receives.
	maxChildMessages = 64
)

// sessionIDPattern keeps a session id from naming a path outside the brain.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,127}$`)

func childSessions(geminiHome func(map[string]string) (string, error)) *acpdriver.ChildSessions {
	b := brain{home: geminiHome}
	return &acpdriver.ChildSessions{
		Launch:   subagentLaunch,
		Report:   subagentReport,
		Children: b.children,
		LaunchOf: b.launchOf,
	}
}

// subagentLaunch recognises invoke_subagent. agy names the tool only in a field
// ACP does not define, so it is known by its title or its input.
func subagentLaunch(call acpdriver.ChildToolCall) (acpdriver.SubagentLaunch, bool) {
	input, _ := call.RawInput.(map[string]any)
	prompt := stringField(input, "Prompt")
	if prompt == "" || (call.Title != "Run "+invokeSubagentTool && stringField(input, "TypeName") == "") {
		return acpdriver.SubagentLaunch{}, false
	}
	launch := acpdriver.SubagentLaunch{ToolName: invokeSubagentTool, Title: stringField(input, "Role"), Prompt: prompt}
	if agentType := stringField(input, "TypeName"); agentType != defaultSubagentType {
		launch.AgentType = agentType
	}
	return launch, true
}

// subagentReport recognises a child's send_message to the session that
// launched it. A message to anyone else is an ordinary step.
func subagentReport(call acpdriver.ChildToolCall, parentSession string) (string, bool) {
	input, _ := call.RawInput.(map[string]any)
	if parentSession == "" || stringField(input, "Recipient") != parentSession {
		return "", false
	}
	if call.Title != "Run "+sendMessageTool && stringField(input, "Message") == "" {
		return "", false
	}
	return stringField(input, "Message"), true
}

// brain reads agy's records of which session started which.
type brain struct {
	home func(map[string]string) (string, error)
}

func (b brain) dir(env map[string]string, session string) string {
	if !sessionIDPattern.MatchString(session) {
		return ""
	}
	home, err := b.home(env)
	if err != nil || home == "" {
		return ""
	}
	return filepath.Join(home, agy.ACPStorageDir, "brain", session, ".system_generated")
}

// children lists the sessions session started, from its subagents directory.
func (b brain) children(env map[string]string, session string) []string {
	dir := b.dir(env, session)
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(filepath.Join(dir, "subagents"))
	if err != nil {
		return nil
	}
	var out []string
	for _, entry := range entries {
		child, ok := strings.CutSuffix(entry.Name(), ".json")
		if ok && !entry.IsDir() && sessionIDPattern.MatchString(child) {
			out = append(out, child)
		}
	}
	return out
}

// launchOf reads the launch that started child from the task it received: the
// message whose source is an invoke_subagent call.
func (b brain) launchOf(env map[string]string, child string) (string, string, bool) {
	dir := b.dir(env, child)
	if dir == "" {
		return "", "", false
	}
	entries, err := os.ReadDir(filepath.Join(dir, "messages"))
	if err != nil {
		return "", "", false
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	read := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "read.json" {
			continue
		}
		read++
		if read > maxChildMessages {
			break
		}
		data, err := os.ReadFile(filepath.Join(dir, "messages", entry.Name())) //nolint:gosec // under the user's GEMINI_HOME, named by a validated session id.
		if err != nil {
			continue
		}
		var message struct {
			Recipient      string `json:"recipient"`
			SourceMetadata struct {
				Tool struct {
					ConversationID string `json:"conversationId"`
					ToolCall       struct {
						ID   string `json:"id"`
						Name string `json:"name"`
					} `json:"toolCall"`
				} `json:"tool"`
			} `json:"sourceMetadata"`
		}
		if json.Unmarshal(data, &message) != nil {
			continue
		}
		source := message.SourceMetadata.Tool
		if message.Recipient == child && source.ToolCall.Name == invokeSubagentTool &&
			source.ConversationID != "" && source.ToolCall.ID != "" {
			return source.ConversationID, source.ToolCall.ID, true
		}
	}
	return "", "", false
}

func stringField(input map[string]any, key string) string {
	value, _ := input[key].(string)
	return strings.TrimSpace(value)
}
