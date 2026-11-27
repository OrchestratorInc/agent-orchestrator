package cursoracp

import (
	"context"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// The Task call below is what `cursor-agent acp` 2026.10.16 sends
// (src/acp/tool-call-presentation.ts in the installed bundle).
func TestSubagentLaunchIsCursorsTaskTool(t *testing.T) {
	input := map[string]any{"_toolName": "task", "description": "Read notes", "prompt": "Read notes.txt.", "subagentType": "explore"}
	launch, ok := subagentLaunch(acpdriver.RecordedCall{Title: "Task: Read notes", RawInput: input})
	if !ok || launch != (acpdriver.RecordedLaunch{ToolName: "Task", Title: "Read notes", Prompt: "Read notes.txt.", AgentType: "explore", Foreground: true}) {
		t.Fatalf("Task = %+v, %v", launch, ok)
	}
	background, _ := subagentLaunch(acpdriver.RecordedCall{Title: "Task: Read notes", RawInput: input,
		RawOutput: map[string]any{"durationMs": 5, "isBackground": true}})
	if background.Foreground {
		t.Fatal("a background Task completes when its child starts")
	}
	untitled, _ := subagentLaunch(acpdriver.RecordedCall{Title: "Task: Audit the cart", RawInput: map[string]any{"_toolName": "task", "prompt": "Go."}})
	if untitled.Title != "Audit the cart" {
		t.Fatalf("title from the tool call's title = %q", untitled.Title)
	}
	for _, call := range []acpdriver.RecordedCall{
		{Title: "Read notes.txt", RawInput: map[string]any{"_toolName": "read"}},
		{Title: "Task: x", RawInput: "not an object"},
	} {
		if _, ok := subagentLaunch(call); ok {
			t.Fatalf("%+v must not be a launch", call)
		}
	}
}

func TestShownAgentTypeHidesTheDefault(t *testing.T) {
	for in, want := range map[string]string{"generalPurpose": "", "general-purpose": "", "default": "", "explore": "explore", "": ""} {
		if got := shownAgentType(in); got != want {
			t.Errorf("%q = %q, want %q", in, got, want)
		}
	}
}

func TestCursorTaskRequestIsAcknowledged(t *testing.T) {
	result, handled, err := handleExtension(context.Background(), nil, taskMethod,
		[]byte(`{"toolCallId":"task-1","description":"Read notes","agentId":"child-1","durationMs":12}`))
	if err != nil || !handled || result == nil {
		t.Fatalf("cursor/task = %v, %v, %v; want an empty success", result, handled, err)
	}
}

func TestCursorOptsIntoSubagents(t *testing.T) {
	if spawnedSubagents() == nil || spawnedSubagents().Launch == nil {
		t.Fatal("the Cursor binding must enable the subagents extension")
	}
}
