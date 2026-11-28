package acp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
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
	return childSendMessageCall(id, recipient, message, acpsdk.ToolCallStatusCompleted)
}

func childSendMessageCall(id, recipient, message string, status acpsdk.ToolCallStatus) acpsdk.SessionUpdate {
	return acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: acpsdk.ToolCallId(id), Title: "Run send_message",
		Kind: acpsdk.ToolKindOther, Status: status,
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
	if read.ProviderItemID != conv.childItemID("child-1", "call-read") || read.ProviderTurnID != "" ||
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
	if childDetail(t, handback)["parentProviderItemId"] != conv.childItemID("child-2", "call-inner") {
		t.Fatalf("grandchild hand-back = %s", handback.Detail)
	}
}

// A grandchild can stream while its parent, idle after a reattach, sends
// nothing: the chain of launches back to the conversation is looked up at once.
func TestChildSessionGrandchildStreamingBeforeItsParentIsFiledUnderItsLaunch(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-earlier", "child-2")
	records.record("child-2", "call-inner", "child-3")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	childSessionUpdate(t, agent, "child-3", childRead("call-read"))
	if read := nextChildActivity(t, conv); childDetail(t, read)["parentProviderItemId"] != conv.childItemID("child-2", "call-inner") {
		t.Fatalf("grandchild step = %#v %s", read, read.Detail)
	}
	childSessionUpdate(t, agent, "child-2", childRead("call-read-2"))
	if read := nextChildActivity(t, conv); childDetail(t, read)["parentProviderItemId"] != "call-earlier" {
		t.Fatalf("child step = %#v %s", read, read.Detail)
	}
	records.mu.Lock()
	defer records.mu.Unlock()
	if records.lookedUp["child-2"] != 1 || records.lookedUp["child-3"] != 1 {
		t.Fatalf("looked up %v", records.lookedUp)
	}
}

func TestChildSessionUnknownSessionIsLookedUpOnce(t *testing.T) {
	records := newChildSessionRecords()
	// A child of a session that is not ours leads elsewhere.
	records.record("someone-else", "call-other", "their-child")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	for range 2 {
		for _, session := range []string{"someone-else", "their-child"} {
			// The driver still refuses an update for a session that is not its own.
			_ = agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
				SessionId: acpsdk.SessionId(session), Update: childRead("call-read"),
			})
		}
	}
	noChildActivity(t, conv)
	records.mu.Lock()
	defer records.mu.Unlock()
	if records.lookedUp["someone-else"] != 1 || records.lookedUp["their-child"] != 1 {
		t.Fatalf("looked up %v", records.lookedUp)
	}
}

// A report that fails is no hand-back, so it shows as the failed call it is.
func TestChildSessionFailedReportShowsAsAFailedStep(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch", "child-1")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	childSessionUpdate(t, agent, "child-1", childSendMessageCall("call-send", childTestParent, "alpha", acpsdk.ToolCallStatusInProgress))
	noChildActivity(t, conv)
	failed := acpsdk.ToolCallStatusFailed
	childSessionUpdate(t, agent, "child-1", acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		SessionUpdate: "tool_call_update", ToolCallId: "call-send", Status: &failed,
	}})
	step := nextChildActivity(t, conv)
	d := childDetail(t, step)
	if step.ProviderItemID != conv.childItemID("child-1", "call-send") || step.ActivityStatus != domain.ActivityStatusFailed ||
		d["parentProviderItemId"] != "call-launch" || d["providerToolName"] == childSessionHandbackTool {
		t.Fatalf("failed report = %#v %s", step, step.Detail)
	}
	noChildActivity(t, conv)
}

// A later attachment finds a child still streaming without the messages it
// sent before, so its count restarts; the names must not.
func TestChildSessionAdoptedAgainNamesItsMessagesApart(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch", "child-1")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	childSessionUpdate(t, agent, "child-1", childSays("one"))
	first := nextChildActivity(t, conv)
	later, laterConv, _ := startWithChildSessions(t, records.sessions())
	childSessionUpdate(t, later, "child-1", childSays("two"))
	second := nextChildActivity(t, laterConv)
	if second.Text != "two" || first.ProviderItemID == second.ProviderItemID {
		t.Fatalf("messages = %q %q, %q", first.ProviderItemID, second.ProviderItemID, second.Text)
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

// ACP keeps a tool call id unique only within its session, so two children can
// use the same one; each keeps its own call.
func TestChildSessionToolCallsKeepApartAcrossSessions(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch-a", "child-a")
	records.record(childTestParent, "call-launch-b", "child-b")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	start := func(session, path string) ports.ChatEvent {
		childSessionUpdate(t, agent, session, acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			SessionUpdate: "tool_call", ToolCallId: "call-1", Title: "File " + path,
			Kind: acpsdk.ToolKindRead, Status: acpsdk.ToolCallStatusInProgress,
			RawInput: map[string]any{"Path": path},
		}})
		return nextChildActivity(t, conv)
	}
	a, b := start("child-a", "a.txt"), start("child-b", "b.txt")
	if a.ProviderItemID == b.ProviderItemID {
		t.Fatalf("both children's call-1 are %q", a.ProviderItemID)
	}
	done := acpsdk.ToolCallStatusCompleted
	childSessionUpdate(t, agent, "child-a", acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		SessionUpdate: "tool_call_update", ToolCallId: "call-1", Status: &done,
	}})
	finished := nextChildActivity(t, conv)
	d := childDetail(t, finished)
	input, _ := d["input"].(map[string]any)
	if finished.ProviderItemID != a.ProviderItemID || d["parentProviderItemId"] != "call-launch-a" ||
		input["Path"] != "a.txt" || finished.ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("child-a's completion = %#v %s", finished, finished.Detail)
	}
}

// A child an earlier attachment loaded can stream while the launch that
// started it is looked up. Its history was shown then, and a replay now would
// run into what it says live, so it is not loaded again.
func TestChildSessionFoundStreamingWhileItsLaunchIsLookedUpIsNotLoaded(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch", "child-1")
	sessions := records.sessions()
	var agent *fakeAgent
	var conv *conversation
	lookup := sessions.LaunchOf
	var calls atomic.Int32
	sessions.LaunchOf = func(env map[string]string, child string) (string, string, bool) {
		if calls.Add(1) == 1 {
			// The finder's lookup: the child speaks before it returns.
			_ = agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
				SessionId: "child-1", Update: childSays("live"),
			})
			for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
				conv.mu.Lock()
				_, known := conv.childStateLocked().launchOf["child-1"]
				conv.mu.Unlock()
				if known {
					break
				}
			}
		}
		return lookup(env, child)
	}
	agent, conv, _ = startWithChildSessions(t, sessions)
	childSessionUpdate(t, agent, childTestParent, invokeSubagentCall("call-launch", acpsdk.ToolCallStatusCompleted))
	_ = nextChildActivity(t, conv)
	if said := nextChildActivity(t, conv); said.Text != "live" {
		t.Fatalf("live message = %#v", said)
	}
	time.Sleep(2 * childSessionFindEvery)
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loadCalls != 0 {
		t.Fatalf("loaded the child %d times", agent.loadCalls)
	}
}

// heldAdoption holds the first launch lookup, an adoption's, until release,
// and fails it when fail is set. lookedAgain closes on the next lookup but
// one: the finder's second, which it makes only when it found the adoption
// under way and kept looking.
type heldAdoption struct {
	adopting, lookedAgain chan struct{}
	release               func()
}

func holdAdoptionLookup(t *testing.T, sessions *ChildSessions, fail bool) *heldAdoption {
	t.Helper()
	released := make(chan struct{})
	h := &heldAdoption{
		adopting: make(chan struct{}), lookedAgain: make(chan struct{}),
		release: sync.OnceFunc(func() { close(released) }),
	}
	t.Cleanup(h.release)
	lookup := sessions.LaunchOf
	var calls atomic.Int32
	sessions.LaunchOf = func(env map[string]string, child string) (string, string, bool) {
		switch calls.Add(1) {
		case 1:
			close(h.adopting)
			<-released
			if fail {
				return "", "", false
			}
		case 3:
			close(h.lookedAgain)
		}
		return lookup(env, child)
	}
	return h
}

// findWhileAdopting runs the finder for the launch with the adoption held,
// releases the adoption once the finder has looked twice, and returns when
// the finder is done.
func (h *heldAdoption) findWhileAdopting(t *testing.T, conv *conversation) {
	t.Helper()
	<-h.adopting
	found := make(chan struct{})
	go func() {
		defer close(found)
		conv.findChildSession(childTestParent, "call-launch", "call-launch")
	}()
	select {
	case <-h.lookedAgain:
	case <-found:
		t.Fatal("the finder stopped looking while the adoption was under way")
	case <-time.After(3 * time.Second):
		t.Fatal("the finder did not look again while the adoption was under way")
	}
	h.release()
	select {
	case <-found:
	case <-time.After(3 * time.Second):
		t.Fatal("the finder did not stop once the adoption settled")
	}
}

// The finder can also look the child up while an adoption of it is under
// way, before the adoption files it: it leaves the child to the adoption.
func TestChildSessionAdoptionUnderWayIsNotLoadedByTheFinder(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch", "child-1")
	sessions := records.sessions()
	held := holdAdoptionLookup(t, sessions, false)
	agent, conv, _ := startWithChildSessions(t, sessions)
	childSessionUpdate(t, agent, "child-1", childSays("live"))
	held.findWhileAdopting(t, conv)
	if said := nextChildActivity(t, conv); said.Text != "live" {
		t.Fatalf("live message = %#v", said)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loadCalls != 0 {
		t.Fatalf("the finder loaded the child %d times", agent.loadCalls)
	}
}

// An adoption can fail where the finder succeeds, for instance while the
// agent's record of the launch cannot be read. The finder then files the
// child, and follows it live, as it has streamed here.
func TestChildSessionAdoptionThatFailsLeavesTheChildToTheFinder(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch", "child-1")
	sessions := records.sessions()
	held := holdAdoptionLookup(t, sessions, true)
	agent, conv, _ := startWithChildSessions(t, sessions)
	childSessionUpdate(t, agent, "child-1", childSays("lost"))
	held.findWhileAdopting(t, conv)
	childSessionUpdate(t, agent, "child-1", childRead("call-read"))
	read := nextChildActivity(t, conv)
	if read.ProviderItemID != conv.childItemID("child-1", "call-read") ||
		childDetail(t, read)["parentProviderItemId"] != "call-launch" {
		t.Fatalf("child step = %#v %s", read, read.Detail)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loadCalls != 0 {
		t.Fatalf("loaded a child that streams here %d times", agent.loadCalls)
	}
}

// A load that fails is tried again, and the replay names the child's messages
// as a first load does.
func TestChildSessionLoadThatFailsIsTriedAgain(t *testing.T) {
	records := newChildSessionRecords()
	records.record(childTestParent, "call-launch", "child-1")
	agent, conv, _ := startWithChildSessions(t, records.sessions())
	agent.mu.Lock()
	agent.failLoadFrom, agent.failLoadErr = 1, errors.New("busy")
	agent.loadUpdates = []acpsdk.SessionUpdate{childSays("one")}
	agent.mu.Unlock()
	childSessionUpdate(t, agent, childTestParent, invokeSubagentCall("call-launch", acpsdk.ToolCallStatusCompleted))
	_ = nextChildActivity(t, conv)
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		agent.mu.Lock()
		failed := agent.loadCalls >= 1
		if failed {
			agent.failLoadFrom = 0
		}
		agent.mu.Unlock()
		if failed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the child was never loaded")
		}
	}
	said := nextChildActivity(t, conv)
	if said.Text != "one" || said.ProviderItemID != "subagent-message:child-1:1" {
		t.Fatalf("replayed message = %#v", said)
	}
	agent.mu.Lock()
	defer agent.mu.Unlock()
	if agent.loadCalls != 2 {
		t.Fatalf("loaded %d times", agent.loadCalls)
	}
}
