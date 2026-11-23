package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const childTestParent = "claude-session-1" // the fake agent's session id

// childSessionRecords plays the binding's view of what the agent recorded.
type childSessionRecords struct {
	mu       sync.Mutex
	children map[string][]string
	launches map[string][2]string // child -> parent session, launch id
	lookedUp map[string]int
}

func newChildSessionRecords() *childSessionRecords {
	return &childSessionRecords{children: map[string][]string{}, launches: map[string][2]string{}, lookedUp: map[string]int{}}
}

func (r *childSessionRecords) record(parent, launch, child string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.children[parent] = append(r.children[parent], child)
	r.launches[child] = [2]string{parent, launch}
}

func (r *childSessionRecords) sessions() *ChildSessions {
	return &ChildSessions{
		Launch: func(call ChildToolCall) (SubagentLaunch, bool) {
			input, _ := call.RawInput.(map[string]any)
			prompt, _ := input["Prompt"].(string)
			if call.Title != "Run invoke_subagent" || prompt == "" {
				return SubagentLaunch{}, false
			}
			return SubagentLaunch{ToolName: "invoke_subagent", Title: "File Reader", Prompt: prompt, AgentType: "research"}, true
		},
		Report: func(call ChildToolCall, parent string) (string, bool) {
			input, _ := call.RawInput.(map[string]any)
			if input["Recipient"] != parent {
				return "", false
			}
			message, _ := input["Message"].(string)
			return message, true
		},
		Children: func(_ map[string]string, session string) []string {
			r.mu.Lock()
			defer r.mu.Unlock()
			return append([]string(nil), r.children[session]...)
		},
		LaunchOf: func(_ map[string]string, child string) (string, string, bool) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.lookedUp[child]++
			launch, ok := r.launches[child]
			return launch[0], launch[1], ok
		},
	}
}

func startWithChildSessions(t *testing.T, sessions *ChildSessions) (*fakeAgent, *conversation, string) {
	t.Helper()
	agent := &fakeAgent{}
	driver := New(Config{
		Harness:       domain.HarnessAgy,
		Capabilities:  ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:         func(context.Context) error { return nil },
		Launch:        func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		ChildSessions: sessions,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	workspace := t.TempDir()
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: workspace})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	_ = nextEvent(t, opened.Events())
	conv := opened.(*conversation)
	conv.mu.Lock()
	conv.activeTurn = "turn-1"
	conv.mu.Unlock()
	return agent, conv, workspace
}

func childSessionUpdate(t *testing.T, agent *fakeAgent, session string, update acpsdk.SessionUpdate) {
	t.Helper()
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(session), Update: update,
	}); err != nil {
		t.Fatalf("session update: %v", err)
	}
}

func nextChildActivity(t *testing.T, conv *conversation) ports.ChatEvent {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-conv.Events():
			switch event.Kind {
			case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted, ports.ChatEventActivityText:
				return event
			}
		case <-deadline:
			t.Fatal("timed out waiting for an activity event")
			return ports.ChatEvent{}
		}
	}
}

func noChildActivity(t *testing.T, conv *conversation) {
	t.Helper()
	deadline := time.After(300 * time.Millisecond)
	for {
		select {
		case event := <-conv.Events():
			switch event.Kind {
			case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted, ports.ChatEventActivityText:
				t.Fatalf("unexpected activity %s %q %s", event.Kind, event.Summary, event.Detail)
			}
		case <-deadline:
			return
		}
	}
}

func childDetail(t *testing.T, event ports.ChatEvent) map[string]any {
	t.Helper()
	detail := map[string]any{}
	if len(event.Detail) > 0 {
		if err := json.Unmarshal(event.Detail, &detail); err != nil {
			t.Fatalf("detail of %#v: %v", event, err)
		}
	}
	return detail
}

func invokeSubagentCall(id string, status acpsdk.ToolCallStatus) acpsdk.SessionUpdate {
	return acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: acpsdk.ToolCallId(id), Title: "Run invoke_subagent",
		Kind: acpsdk.ToolKindOther, Status: status,
		RawInput: map[string]any{"Prompt": "Read a.txt and send me its contents.", "Role": "File Reader", "TypeName": "research"},
	}}
}

func completeInvokeSubagent(id string) acpsdk.SessionUpdate {
	done := acpsdk.ToolCallStatusCompleted
	return acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		SessionUpdate: "tool_call_update", ToolCallId: acpsdk.ToolCallId(id), Status: &done,
	}}
}

func childRead(id string) acpsdk.SessionUpdate {
	return acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: acpsdk.ToolCallId(id), Title: "File a.txt",
		Kind: acpsdk.ToolKindRead, Status: acpsdk.ToolCallStatusCompleted,
		RawInput: map[string]any{"Path": "a.txt"},
	}}
}

func childSays(text string) acpsdk.SessionUpdate {
	return acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
		SessionUpdate: "agent_message_chunk", Content: acpsdk.TextBlock(text),
	}}
}

func childSendsMessage(id, recipient, message string) acpsdk.SessionUpdate {
	return acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: acpsdk.ToolCallId(id), Title: "Run send_message",
		Kind: acpsdk.ToolKindOther, Status: acpsdk.ToolCallStatusCompleted,
		RawInput: map[string]any{"Message": message, "Recipient": recipient},
	}}
}

func TestChildSessionFilesItsWorkUnderItsLaunch(t *testing.T) {
	records := newChildSessionRecords()
	agent, conv, workspace := startWithChildSessions(t, records.sessions())
	currentMode := acpsdk.SessionUpdate{CurrentModeUpdate: &acpsdk.SessionCurrentModeUpdate{
		SessionUpdate: "current_mode_update", CurrentModeId: "plan",
	}}
	agent.mu.Lock()
	agent.loadUpdates = []acpsdk.SessionUpdate{
		currentMode,
		childRead("call-read"),
		childSays("Reading "), childSays("it now."),
		childSendsMessage("call-send", childTestParent, "alpha"),
		childSays("Sent."),
	}
	agent.mu.Unlock()

	childSessionUpdate(t, agent, childTestParent, invokeSubagentCall("call-launch", acpsdk.ToolCallStatusInProgress))
	launch := nextChildActivity(t, conv)
	detail := childDetail(t, launch)
	input, _ := detail["input"].(map[string]any)
	if detail["providerToolName"] != "invoke_subagent" || detail["background"] != true ||
		detail["subagentType"] != "research" || input["description"] != "File Reader" ||
		input["prompt"] != "Read a.txt and send me its contents." {
		t.Fatalf("launch detail = %v", detail)
	}
	if launch.ProviderTurnID != "turn-1" {
		t.Fatalf("launch turn = %q", launch.ProviderTurnID)
	}

	// The agent records the child only once the launch has started it.
	records.record(childTestParent, "call-launch", "child-1")
	childSessionUpdate(t, agent, childTestParent, completeInvokeSubagent("call-launch"))
	if done := nextChildActivity(t, conv); done.ProviderItemID != "call-launch" || done.ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("launch completion = %#v", done)
	}

	read := nextChildActivity(t, conv)
	if read.ProviderItemID != "call-read" || read.ProviderTurnID != "" ||
		childDetail(t, read)["parentProviderItemId"] != "call-launch" {
		t.Fatalf("child step = %#v %s", read, read.Detail)
	}
	firstMessage := ""
	for _, want := range []string{"Reading", "Reading it now."} {
		said := nextChildActivity(t, conv)
		if firstMessage == "" {
			firstMessage = said.ProviderItemID
		}
		d := childDetail(t, said)
		if said.Summary != "Subagent response" || said.Text != want || said.ProviderTurnID != "" ||
			d["parentProviderItemId"] != "call-launch" || d["nestedAgent"] != true ||
			said.ActivityStatus != domain.ActivityStatusCompleted {
			t.Fatalf("child message = %#v %s, want text %q", said, said.Detail, want)
		}
	}
	handback := nextChildActivity(t, conv)
	hd := childDetail(t, handback)
	hInput, _ := hd["input"].(map[string]any)
	if hd["providerToolName"] != "SubagentHandback" || hd["parentProviderItemId"] != "call-launch" ||
		hInput["message"] != "alpha" || handback.ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("hand-back = %#v %s", handback, handback.Detail)
	}
	after := nextChildActivity(t, conv)
	if after.Text != "Sent." {
		t.Fatalf("message after the report = %#v", after)
	}
	if after.ProviderItemID == firstMessage {
		t.Fatal("the message after a tool call reused the earlier message's item")
	}

	agent.mu.Lock()
	params := agent.loadParams
	agent.mu.Unlock()
	if params.SessionId != "child-1" || params.Cwd != workspace || params.Meta[persistenthost.ACPChildSessionMetaKey] != true {
		t.Fatalf("child load = %+v", params)
	}
	noChildActivity(t, conv)
}

func TestChildSessionMessagesAreNamedApartAcrossToolCalls(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch", "child-1")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	childSessionUpdate(t, agent, "child-1", childSays("one"))
	first := nextChildActivity(t, conv)
	childSessionUpdate(t, agent, "child-1", childRead("call-read"))
	_ = nextChildActivity(t, conv)
	childSessionUpdate(t, agent, "child-1", childSays("two"))
	second := nextChildActivity(t, conv)
	if first.ProviderItemID == second.ProviderItemID || second.Text != "two" {
		t.Fatalf("messages = %q %q, %q", first.ProviderItemID, second.ProviderItemID, second.Text)
	}
}

// A child an earlier daemon attachment loaded keeps streaming through the
// persistent host; its updates are filed under the launch the agent recorded.
func TestChildSessionAlreadyStreamingIsFiledUnderItsLaunch(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-earlier", "child-2")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	childSessionUpdate(t, agent, "child-2", childRead("call-read"))
	read := nextChildActivity(t, conv)
	if childDetail(t, read)["parentProviderItemId"] != "call-earlier" || read.ProviderTurnID != "" {
		t.Fatalf("child step = %#v %s", read, read.Detail)
	}
	// Grandchildren of an adopted child are ours too.
	records.record("child-2", "call-inner", "child-3")
	childSessionUpdate(t, agent, "child-3", childSendsMessage("call-send", "child-2", "inner done"))
	handback := nextChildActivity(t, conv)
	if childDetail(t, handback)["parentProviderItemId"] != "call-inner" {
		t.Fatalf("grandchild hand-back = %s", handback.Detail)
	}
}

func TestChildSessionUnknownSessionIsLookedUpOnce(t *testing.T) {
	records := newChildSessionRecords()
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	for range 2 {
		// The driver still refuses an update for a session that is not its own.
		_ = agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
			SessionId: "someone-else", Update: childRead("call-read"),
		})
	}
	noChildActivity(t, conv)
	records.mu.Lock()
	defer records.mu.Unlock()
	if records.lookedUp["someone-else"] != 1 {
		t.Fatalf("looked up %d times", records.lookedUp["someone-else"])
	}
}

func TestChildSessionNotLoadedForALaunchThatFailed(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch", "child-1")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	childSessionUpdate(t, agent, childTestParent, invokeSubagentCall("call-launch", acpsdk.ToolCallStatusFailed))
	_ = nextChildActivity(t, conv)
	time.Sleep(2 * childSessionFindEvery)
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loadCalls != 0 {
		t.Fatalf("loaded %d sessions for a failed launch", agent.loadCalls)
	}
}

func TestChildSessionAToolCallWithoutLaunchShapeIsAPlainTool(t *testing.T) {
	agent, conv, _ := startWithChildSessions(t, newChildSessionRecords().sessions())
	childSessionUpdate(t, agent, childTestParent, childRead("call-read"))
	read := nextChildActivity(t, conv)
	d := childDetail(t, read)
	if _, ok := d["background"]; ok || d["providerToolName"] != nil || read.ProviderTurnID != "turn-1" {
		t.Fatalf("plain tool = %#v %s", read, read.Detail)
	}
}
