package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The frames below follow what `cursor-agent acp` 2026.10.16 sends with the
// subagents extension negotiated (src/acp/session-update-presenter.ts and its
// subagent publisher, read from the installed bundle): the Task launch on the
// parent's session, subagent_spawned and subagent_state_update naming the
// child's session and the launch's tool call under _meta.cursor, and the
// child's own work under the child's session id.

type lockedWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lockedWriter) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(b)
}

// rawSpawn is fakeSpawn with a way to write a frame the SDK cannot encode,
// such as an update type it does not know.
func rawSpawn(agent *fakeAgent) (spawnFunc, func(t *testing.T, frame any)) {
	var writer *lockedWriter
	spawn := func(Launch, string) (*process, error) {
		clientToAgentR, clientToAgentW := io.Pipe()
		agentToClientR, agentToClientW := io.Pipe()
		writer = &lockedWriter{w: agentToClientW}
		agent.conn = acpsdk.NewAgentSideConnection(agent, writer, clientToAgentR)
		var once sync.Once
		return &process{
			stdin: clientToAgentW, stdout: agentToClientR,
			stop: func() error {
				once.Do(func() {
					_ = clientToAgentW.Close()
					_ = clientToAgentR.Close()
					_ = agentToClientW.Close()
					_ = agentToClientR.Close()
				})
				return nil
			},
		}, nil
	}
	raw := func(t *testing.T, frame any) {
		t.Helper()
		encoded, err := json.Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write(append(encoded, '\n')); err != nil {
			t.Fatalf("write raw frame: %v", err)
		}
	}
	return spawn, raw
}

func cursorLikeLaunch(call RecordedCall) (RecordedLaunch, bool) {
	input, _ := call.RawInput.(map[string]any)
	if input == nil || input["_toolName"] != "task" {
		return RecordedLaunch{}, false
	}
	return RecordedLaunch{ToolName: "Task", Title: input["description"].(string), Prompt: input["prompt"].(string),
		AgentType: input["subagentType"].(string), Foreground: true}, true
}

func subagentNotice(session, kind, child, state string) map[string]any {
	update := map[string]any{
		"sessionUpdate": kind, "subagentSessionId": child,
		"_meta": map[string]any{"cursor": map[string]any{"toolCallId": "task-1", "agentId": child}},
	}
	if kind == subagentSpawned {
		update["name"], update["task"], update["capabilities"] = "explore", "Read notes.txt", map[string]any{}
	} else {
		update["state"] = state
	}
	return map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": session, "update": update}}
}

func TestSpawnedSubagentFilesItsWorkOnTheTurnUnderItsLaunch(t *testing.T) {
	agent := &fakeAgent{}
	spawn, raw := rawSpawn(agent)
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		parent := string(params.SessionId)
		send := func(session string, update acpsdk.SessionUpdate) {
			if err := agent.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: acpsdk.SessionId(session), Update: update}); err != nil {
				t.Errorf("session/update: %v", err)
			}
		}
		send(parent, acpsdk.StartToolCall("task-1", "Task: Read notes",
			acpsdk.WithStartKind(acpsdk.ToolKindOther), acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
			acpsdk.WithStartRawInput(map[string]any{"_toolName": "task", "description": "Read notes",
				"prompt": "Read notes.txt and report its second line.", "subagentType": "explore"})))
		raw(t, subagentNotice(parent, subagentSpawned, "child-1", ""))
		// The child's own work, under its own session; Cursor gives its
		// messages no id.
		send("child-1", acpsdk.UpdateAgentMessageText("Reading."))
		send("child-1", acpsdk.StartToolCall("read-1", "Read notes.txt",
			acpsdk.WithStartKind(acpsdk.ToolKindRead), acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress)))
		send("child-1", acpsdk.UpdateToolCall("read-1", acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted)))
		send("child-1", acpsdk.UpdateAgentMessageText("The second line "))
		send("child-1", acpsdk.UpdateAgentMessageText("says kiwi."))
		raw(t, subagentNotice(parent, subagentStateUpdate, "child-1", "completed"))
		send(parent, acpsdk.UpdateToolCall("task-1", acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
			acpsdk.WithUpdateRawOutput(map[string]any{"durationMs": 1200, "isBackground": false})))
		send(parent, acpsdk.UpdateAgentMessageText("It says kiwi."))
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	driver := New(Config{
		Harness:          domain.HarnessCursor,
		Capabilities:     ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityTools: true},
		Probe:            func(context.Context) error { return nil },
		Launch:           func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SpawnedSubagents: &SpawnedSubagents{Launch: cursorLikeLaunch},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(spawn)
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer opened.Close()
	conv := opened.(*conversation)

	agent.mu.Lock()
	optedIn := agent.initParams.ClientCapabilities.Meta["subagents"]
	agent.mu.Unlock()
	if optedIn != true {
		t.Fatalf("initialize must opt into the subagents extension, got %v", optedIn)
	}

	startTurn(t, opened, "Read notes.txt with a subagent.")
	seen := collectUntil(t, opened.Events(), func(e ports.ChatEvent) bool { return e.Kind == ports.ChatEventTurnCompleted })
	last := seen[len(seen)-1]
	if last.TurnState == domain.TurnStateFailed {
		t.Fatalf("the turn must complete, got %s", last.TurnState)
	}
	turn := last.ProviderTurnID
	launchItem := conv.providerItemID("task-1")
	detailOf := func(e ports.ChatEvent) map[string]any {
		var detail map[string]any
		_ = json.Unmarshal(e.Detail, &detail)
		return detail
	}
	var launchDetail map[string]any
	var responses []string
	var step, handback ports.ChatEvent
	for _, e := range seen {
		detail := detailOf(e)
		switch {
		case e.ProviderItemID == launchItem && e.Kind == ports.ChatEventActivityCompleted:
			launchDetail = detail
		case e.Summary == subagentResponseSummary:
			if detail["parentProviderItemId"] != launchItem || e.ProviderTurnID != turn {
				t.Fatalf("child message filed wrong: %+v %v", e, detail)
			}
			responses = append(responses, e.ProviderItemID+"="+e.Text)
		case e.Summary == "Read notes.txt" && e.Kind == ports.ChatEventActivityCompleted:
			step = e
		case e.Summary == subagentHandbackTool:
			handback = e
		}
	}
	if launchDetail["providerToolName"] != "Task" || launchDetail["subagentType"] != "explore" {
		t.Fatalf("launch detail = %v", launchDetail)
	}
	if _, background := launchDetail["background"]; background {
		t.Fatal("a foreground Task completes with its child; it is not a background launch")
	}
	if detail := detailOf(step); detail["parentProviderItemId"] != launchItem || step.ProviderTurnID != turn {
		t.Fatalf("child step = %+v %v", step, detail)
	}
	if len(responses) < 2 {
		t.Fatalf("want the child's two messages, split by its tool call, got %v", responses)
	}
	finalText := responses[len(responses)-1]
	if got := finalText[len(finalText)-len("The second line says kiwi."):]; got != "The second line says kiwi." {
		t.Fatalf("the second message must accumulate its chunks, got %q", finalText)
	}
	if handback.ActivityStatus != domain.ActivityStatusCompleted || handback.ProviderTurnID != turn {
		t.Fatalf("hand-back = %+v", handback)
	}
	detail := detailOf(handback)
	if input, _ := detail["input"].(map[string]any); input["message"] != "The second line says kiwi." || detail["parentProviderItemId"] != launchItem {
		t.Fatalf("hand-back detail = %v; want the child's last message as its report", detail)
	}
}

func TestSpawnedSubagentEndSaysHowTheChildEnded(t *testing.T) {
	for state, want := range map[string]RecordedEndStatus{
		"completed": RecordedCompleted, "failed": RecordedFailed, "disconnected": RecordedFailed, "cancelled": RecordedCancelled,
	} {
		got, ended := spawnedEndStatus(state)
		if !ended || got != want {
			t.Errorf("%s = %s %v, want %s", state, got, ended, want)
		}
	}
	for _, state := range []string{"running", "background", ""} {
		if _, ended := spawnedEndStatus(state); ended {
			t.Errorf("%q must not end the run", state)
		}
	}
}

func TestSpawnedSubagentIgnoresAnotherSessionsChildren(t *testing.T) {
	conv, _ := recordTestConversation("turn-1", 4, nil)
	conv.sessionID = "parent"
	conv.spawnedSubagents = &SpawnedSubagents{Launch: cursorLikeLaunch}
	notice := func(session, kind, child, state string) json.RawMessage {
		frame := subagentNotice(session, kind, child, state)
		encoded, _ := json.Marshal(frame["params"])
		return encoded
	}
	if !conv.spawnedSubagentNotification(notice("someone-else", subagentSpawned, "child-x", "")) {
		t.Fatal("the extension's update types are always handled")
	}
	if conv.spawned["child-x"] != nil {
		t.Fatal("a child another session announced must not be followed")
	}
	if !conv.spawnedSubagentNotification(notice("parent", subagentSpawned, "child-1", "")) || conv.spawned["child-1"] == nil {
		t.Fatal("this session's child must be followed")
	}
	// A child the child starts files under the child's own tool call.
	if !conv.spawnedSubagentNotification(notice("child-1", subagentSpawned, "grandchild", "")) {
		t.Fatal("a nested spawn is handled")
	}
	if got, want := conv.spawned["grandchild"].launch, "subagent-tool:"+lengthPrefixedTuple("child-1", "task-1"); got != want {
		t.Fatalf("nested launch = %q, want %q", got, want)
	}
	if conv.spawnedSubagentNotification(json.RawMessage(`{"sessionId":"parent","update":{"sessionUpdate":"agent_message_chunk"}}`)) {
		t.Fatal("an ordinary update is not the extension's")
	}
}
