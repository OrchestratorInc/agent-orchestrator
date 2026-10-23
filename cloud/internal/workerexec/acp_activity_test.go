package workerexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func liveACPClient(published *[]Output) *cloudACPClient {
	client := &cloudACPClient{publish: func(output Output) error {
		*published = append(*published, output)
		return nil
	}}
	client.live.Store(true)
	return client
}

func toolCallUpdate(update acp.SessionUpdate) acp.SessionNotification {
	return acp.SessionNotification{Update: update}
}

func TestACPToolCallsBecomeChatActivityWithTheirTitleKept(t *testing.T) {
	var published []Output
	client := liveACPClient(&published)
	ctx := context.Background()
	if err := client.SessionUpdate(ctx, toolCallUpdate(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
		ToolCallId: "tool-1", Title: "Run tests", Kind: acp.ToolKindExecute, Status: acp.ToolCallStatusPending,
		RawInput: map[string]any{"command": "go test ./..."},
	}})); err != nil {
		t.Fatal(err)
	}
	completed := acp.ToolCallStatusCompleted
	if err := client.SessionUpdate(ctx, toolCallUpdate(acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
		ToolCallId: "tool-1", Status: &completed,
		Content: []acp.ToolCallContent{{Content: &acp.ToolCallContentContent{Content: acp.TextBlock("ok  ./...")}}},
	}})); err != nil {
		t.Fatal(err)
	}
	if len(published) != 2 {
		t.Fatalf("published %d outputs, want 2", len(published))
	}
	first, last := published[0].Activity, published[1].Activity
	if first == nil || first.ID != "tool-1" || first.Kind != "command" || first.Status != "running" || first.Detail["command"] != "go test ./..." {
		t.Fatalf("tool call activity = %+v", first)
	}
	// The update carried only the status and output; the row keeps its title
	// and command, since the timeline replaces the summary on every snapshot.
	if last == nil || last.Status != "completed" || last.Summary != "Run tests" || last.Detail["command"] != "go test ./..." || last.Detail["output"] != "ok  ./..." {
		t.Fatalf("tool call update activity = %+v", last)
	}
}

func TestACPEditsBecomeFileChangesAndOtherToolsMCPRows(t *testing.T) {
	var published []Output
	client := liveACPClient(&published)
	oldText := "a\nb\n"
	if err := client.SessionUpdate(context.Background(), toolCallUpdate(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
		ToolCallId: "edit-1", Title: "Edit main.go", Kind: acp.ToolKindEdit, Status: acp.ToolCallStatusCompleted,
		Content: []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: "main.go", OldText: &oldText, NewText: "a\nc\nd\n"}}},
	}})); err != nil {
		t.Fatal(err)
	}
	if err := client.SessionUpdate(context.Background(), toolCallUpdate(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
		ToolCallId: "read-1", Title: "Read README.md", Kind: acp.ToolKindRead, Status: acp.ToolCallStatusCompleted,
		RawInput: map[string]any{"file_path": "README.md"},
	}})); err != nil {
		t.Fatal(err)
	}
	edit := published[0].Activity
	files, _ := edit.Detail["files"].([]map[string]any)
	if edit.Kind != "file_change" || len(files) != 1 || files[0]["path"] != "main.go" || files[0]["status"] != "modified" ||
		files[0]["additions"] != 2 || files[0]["deletions"] != 1 {
		t.Fatalf("edit activity = %+v", edit)
	}
	read := published[1].Activity
	if read.Kind != "mcp_tool" || read.Summary != "Read README.md" || read.Detail["arguments"] == nil {
		t.Fatalf("read activity = %+v", read)
	}
}

func TestACPThoughtsAreOneReasoningRowPerRun(t *testing.T) {
	var published []Output
	client := liveACPClient(&published)
	ctx := context.Background()
	thought := func(text string) {
		if err := client.SessionUpdate(ctx, toolCallUpdate(acp.SessionUpdate{AgentThoughtChunk: &acp.SessionUpdateAgentThoughtChunk{Content: acp.TextBlock(text)}})); err != nil {
			t.Fatal(err)
		}
	}
	thought("First, ")
	thought("read the code.")
	if err := client.SessionUpdate(ctx, toolCallUpdate(acp.SessionUpdate{AgentMessageChunk: &acp.SessionUpdateAgentMessageChunk{Content: acp.TextBlock("Done.")}})); err != nil {
		t.Fatal(err)
	}
	thought("Next run.")
	if len(published) != 4 {
		t.Fatalf("published %d outputs, want 4", len(published))
	}
	if published[0].Activity.ID != published[1].Activity.ID || published[0].Activity.Kind != "reasoning" ||
		published[1].Activity.Detail["textDelta"] != "read the code." {
		t.Fatalf("first run = %+v / %+v", published[0].Activity, published[1].Activity)
	}
	if published[2].Text != "Done." || published[3].Activity.ID == published[0].Activity.ID {
		t.Fatalf("a message must end the reasoning run: %+v", published)
	}
}

func TestACPActivityStaysInsideTheEventLimitAndNeverFailsTheTurn(t *testing.T) {
	var published []Output
	client := liveACPClient(&published)
	huge := strings.Repeat("x", 64<<10)
	if err := client.SessionUpdate(context.Background(), toolCallUpdate(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
		ToolCallId: "big-1", Title: "Fetch", Kind: acp.ToolKindFetch, Status: acp.ToolCallStatusCompleted,
		RawInput: map[string]any{"url": huge}, RawOutput: huge,
	}})); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(published[0].Activity)
	if len(raw) > maxACPActivityBytes {
		t.Fatalf("activity is %d bytes, over %d", len(raw), maxACPActivityBytes)
	}
	failing := &cloudACPClient{publish: func(output Output) error {
		if output.Activity != nil {
			return context.DeadlineExceeded
		}
		return nil
	}}
	failing.live.Store(true)
	if err := failing.SessionUpdate(context.Background(), toolCallUpdate(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
		ToolCallId: "tool-2", Kind: acp.ToolKindExecute, Status: acp.ToolCallStatusPending,
	}})); err != nil {
		t.Fatalf("a rejected activity failed the turn: %v", err)
	}
}

func TestACPReplayBeforeTheLiveTurnRecordsNoActivity(t *testing.T) {
	var published []Output
	client := liveACPClient(&published)
	client.live.Store(false)
	if err := client.SessionUpdate(context.Background(), toolCallUpdate(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
		ToolCallId: "old-1", Kind: acp.ToolKindExecute, Status: acp.ToolCallStatusCompleted,
	}})); err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 {
		t.Fatalf("replayed tool call published %d outputs", len(published))
	}
}
