package acp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// startOpenCode opens a conversation for harness on a fake agent, inside "turn-1".
func startOpenCode(t *testing.T, harness domain.AgentHarness) (*fakeAgent, *conversation) {
	t.Helper()
	agent := &fakeAgent{}
	driver := New(Config{
		Harness:      harness,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = opened.Close() })
	_ = nextEvent(t, opened.Events())
	conv := opened.(*conversation)
	setActiveTurn(conv, "turn-1")
	return agent, conv
}

func setActiveTurn(conv *conversation, turn string) {
	conv.mu.Lock()
	conv.activeTurn = turn
	conv.mu.Unlock()
}

func sendUpdate(t *testing.T, agent *fakeAgent, conv *conversation, update acpsdk.SessionUpdate) {
	t.Helper()
	if err := agent.conn.SessionUpdate(context.Background(), acpsdk.SessionNotification{
		SessionId: acpsdk.SessionId(conv.ProviderConversationID()), Update: update,
	}); err != nil {
		t.Fatalf("session update: %v", err)
	}
}

// sendChild writes one opencode/session/child_update as OpenCode 2 does: a plain
// notification, whose method the SDK's extension helper would refuse.
func sendChild(t *testing.T, agent *fakeAgent, conv *conversation, child string, payload map[string]any) {
	t.Helper()
	payload["rootSessionId"] = conv.ProviderConversationID()
	payload["childSessionId"] = child
	payload["parentSessionId"] = conv.ProviderConversationID()
	payload["depth"] = 1
	payload["title"] = "Find tests"
	frame, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": openCodeChildUpdateMethod, "params": payload})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := agent.wire.Write(append(frame, '\n')); err != nil {
		t.Fatalf("child update: %v", err)
	}
}

func childToolCall(id, title string, status acpsdk.ToolCallStatus) map[string]any {
	return map[string]any{
		"sessionUpdate": "tool_call", "toolCallId": id, "title": title, "kind": "read", "status": status,
		"rawInput": map[string]any{"path": "go.mod"},
	}
}

// nextActivity is the next activity event, past any controller or usage event.
func nextActivity(t *testing.T, conv *conversation) ports.ChatEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
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

func detailOf(t *testing.T, event ports.ChatEvent) map[string]any {
	t.Helper()
	detail := map[string]any{}
	if len(event.Detail) > 0 {
		if err := json.Unmarshal(event.Detail, &detail); err != nil {
			t.Fatalf("detail of %#v: %v", event, err)
		}
	}
	return detail
}

// launchSubagent sends OpenCode 2's subagent call up to the point where it runs,
// and returns the launch's running event.
func launchSubagent(t *testing.T, agent *fakeAgent, conv *conversation, id string) ports.ChatEvent {
	t.Helper()
	sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: acpsdk.ToolCallId(id), Title: "subagent",
		Kind: acpsdk.ToolKindThink, Status: acpsdk.ToolCallStatusPending, RawInput: map[string]any{},
	}})
	_ = nextActivity(t, conv)
	running := acpsdk.ToolCallStatusInProgress
	sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		SessionUpdate: "tool_call_update", ToolCallId: acpsdk.ToolCallId(id), Status: &running,
		RawInput: map[string]any{"agent": "explore", "description": "Find tests", "prompt": "Find the Go tests"},
	}})
	return nextActivity(t, conv)
}

// completeLaunch answers the subagent call the way OpenCode 2 does: at once,
// naming the child it queued.
func completeLaunch(t *testing.T, agent *fakeAgent, conv *conversation, id, child string) ports.ChatEvent {
	t.Helper()
	done := acpsdk.ToolCallStatusCompleted
	sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
		SessionUpdate: "tool_call_update", ToolCallId: acpsdk.ToolCallId(id), Status: &done,
		Content:   []acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("The subagent is working in the background"))},
		RawOutput: map[string]any{"metadata": map[string]any{"sessionID": child, "status": "running"}},
	}})
	return nextActivity(t, conv)
}

func TestOpenCodeV2SubagentFilesChildWorkUnderItsLaunch(t *testing.T) {
	agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
	agent.mu.Lock()
	asked := agent.initParams.ClientCapabilities.Meta[openCodeChildUpdatesCapability]
	agent.mu.Unlock()
	if asked != true {
		t.Fatalf("initialize did not ask OpenCode 2 for child session updates: %#v", agent.initParams.ClientCapabilities.Meta)
	}

	running := launchSubagent(t, agent, conv, "call_launch")
	if detail := detailOf(t, running); detail["providerToolName"] != openCodeSubagentTool ||
		detail["background"] != true || detail["subagentType"] != "explore" {
		t.Fatalf("launch detail = %#v", detail)
	}

	// The child starts before the launch names it, so its work has to wait.
	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "status", "status": "created"})
	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update",
		"update": childToolCall("ses_child:call_read", "Find tests: read", "pending")})

	launched := completeLaunch(t, agent, conv, "call_launch", "ses_child")
	if launched.Kind != ports.ChatEventActivityCompleted || launched.ProviderItemID != "call_launch" {
		t.Fatalf("event after the launch completed = %#v, want the launch itself first", launched)
	}
	// The child's run opens under the launch, so the launch reads as working
	// even while the child has no step running.
	run := nextActivity(t, conv)
	if detail := detailOf(t, run); run.Kind != ports.ChatEventActivityStarted || run.ActivityStatus != domain.ActivityStatusRunning ||
		run.ProviderTurnID != "" || detail["providerToolName"] != subagentRunTool || detail["parentProviderItemId"] != "call_launch" {
		t.Fatalf("child run = %#v, detail %#v", run, detail)
	}
	step := nextActivity(t, conv)
	if step.Kind != ports.ChatEventActivityStarted || step.ProviderItemID != "ses_child:call_read" ||
		step.ProviderTurnID != "" || step.Summary != "read" {
		t.Fatalf("child step = %#v", step)
	}
	if detail := detailOf(t, step); detail["parentProviderItemId"] != "call_launch" {
		t.Fatalf("child step detail = %#v", detail)
	}

	// A child's steps carry no turn, so the parent's turn ending cannot settle
	// them; the child keeps working after it.
	setActiveTurn(conv, "")
	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update", "update": map[string]any{
		"sessionUpdate": "agent_message_chunk", "messageId": "msg_1",
		"content": map[string]any{"type": "text", "text": "Found 3 tests."},
	}})
	said := nextActivity(t, conv)
	if detail := detailOf(t, said); said.Kind != ports.ChatEventActivityStarted || said.ProviderTurnID != "" ||
		said.ActivityKind != domain.ActivityKindMCPTool || detail["nestedAgent"] != true ||
		detail["parentProviderItemId"] != "call_launch" || detail["providerToolName"] != nil {
		t.Fatalf("child message = %#v, detail %#v", said, detail)
	}
	if text := nextActivity(t, conv); text.Kind != ports.ChatEventActivityText || text.Delta != "Found 3 tests." {
		t.Fatalf("child message text = %#v", text)
	}
	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update", "update": map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": "ses_child:call_read", "status": "completed",
	}})
	if read := nextActivity(t, conv); read.Kind != ports.ChatEventActivityCompleted ||
		read.ProviderTurnID != "" || detailOf(t, read)["parentProviderItemId"] != "call_launch" {
		t.Fatalf("child step completion = %#v", read)
	}

	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "status", "status": "completed"})
	if finished := nextActivity(t, conv); finished.Kind != ports.ChatEventActivityCompleted ||
		finished.ProviderItemID != said.ProviderItemID || finished.Text != "Found 3 tests." {
		t.Fatalf("child message completion = %#v", finished)
	}
	if ended := nextActivity(t, conv); ended.Kind != ports.ChatEventActivityCompleted ||
		ended.ProviderItemID != run.ProviderItemID || ended.ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("child run end = %#v", ended)
	}
	handback := nextActivity(t, conv)
	detail := detailOf(t, handback)
	input, _ := detail["input"].(map[string]any)
	if handback.Kind != ports.ChatEventActivityCompleted || handback.ActivityStatus != domain.ActivityStatusCompleted ||
		handback.ProviderTurnID != "" || detail["providerToolName"] != subagentHandbackTool ||
		detail["parentProviderItemId"] != "call_launch" || input["message"] != "Found 3 tests." {
		t.Fatalf("hand-back = %#v, detail %#v", handback, detail)
	}
}

func TestOpenCodeV2SubagentReportsHowTheChildEnded(t *testing.T) {
	for _, tc := range []struct {
		status  string
		error   map[string]any
		want    domain.ActivityStatus
		message string
	}{
		{status: "failed", error: map[string]any{"type": "provider", "message": "rate limited"},
			want: domain.ActivityStatusFailed, message: "rate limited"},
		{status: "interrupted", want: domain.ActivityStatusCancelled},
	} {
		t.Run(tc.status, func(t *testing.T) {
			agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
			_ = launchSubagent(t, agent, conv, "call_launch")
			_ = completeLaunch(t, agent, conv, "call_launch", "ses_child")
			ended := map[string]any{"type": "status", "status": tc.status}
			if tc.error != nil {
				ended["error"] = tc.error
			}
			sendChild(t, agent, conv, "ses_child", ended)
			handback := nextActivity(t, conv)
			detail := detailOf(t, handback)
			input, _ := detail["input"].(map[string]any)
			if handback.ActivityStatus != tc.want || detail["providerToolName"] != subagentHandbackTool ||
				detail["parentProviderItemId"] != "call_launch" {
				t.Fatalf("hand-back = %#v, detail %#v", handback, detail)
			}
			if got, _ := input["message"].(string); got != tc.message {
				t.Fatalf("hand-back message = %q, want %q", got, tc.message)
			}
		})
	}
}

// A child no launch could have started still shows, as OpenCode would have sent
// it without the capability.
func TestOpenCodeV2ChildWithNoLaunchStaysVisible(t *testing.T) {
	agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
	sendChild(t, agent, conv, "ses_orphan", map[string]any{"type": "update",
		"update": childToolCall("ses_orphan:call_read", "Find tests: read", "pending")})
	step := nextActivity(t, conv)
	if step.ProviderItemID != "ses_orphan:call_read" || step.ProviderTurnID != "turn-1" {
		t.Fatalf("unfiled child step = %#v", step)
	}
	if parent := detailOf(t, step)["parentProviderItemId"]; parent != nil {
		t.Fatalf("unfiled child step has parent %v", parent)
	}
}

func TestOpenCodeV1TaskReportsItsResult(t *testing.T) {
	agent, conv := startOpenCode(t, domain.HarnessOpenCode)
	if asked := agent.initParams.ClientCapabilities.Meta[openCodeChildUpdatesCapability]; asked != nil {
		t.Fatalf("OpenCode 1 was asked for child session updates")
	}
	// runTask sends a task call and its result, and returns the call's completion.
	runTask := func(id, state string) ports.ChatEvent {
		sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			SessionUpdate: "tool_call", ToolCallId: acpsdk.ToolCallId(id), Title: "Find tests",
			Kind: acpsdk.ToolKindThink, Status: acpsdk.ToolCallStatusInProgress,
			RawInput: map[string]any{"description": "Find tests", "prompt": "Find the Go tests", "subagent_type": "explore"},
		}})
		_ = nextActivity(t, conv)
		done := acpsdk.ToolCallStatusCompleted
		sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
			SessionUpdate: "tool_call_update", ToolCallId: acpsdk.ToolCallId(id), Status: &done,
			RawOutput: map[string]any{"output": `<task id="ses_child" state="` + state + `">` +
				"\n<task_result>\nFound 3 tests.\n</task_result>\n</task>"},
		}})
		return nextActivity(t, conv)
	}

	inline := runTask("call_inline", "completed")
	if inline.ProviderItemID != "call_inline" || detailOf(t, inline)["background"] != nil {
		t.Fatalf("inline task = %#v", inline)
	}
	handback := nextActivity(t, conv)
	detail := detailOf(t, handback)
	report, _ := detail["input"].(map[string]any)
	if detail["providerToolName"] != subagentHandbackTool || detail["parentProviderItemId"] != "call_inline" ||
		report["message"] != "Found 3 tests." || handback.ActivityStatus != domain.ActivityStatusCompleted {
		t.Fatalf("hand-back = %#v, detail %#v", handback, detail)
	}

	// A background task's result is a note to the model, not the child's report.
	background := runTask("call_background", "running")
	if background.ProviderItemID != "call_background" || detailOf(t, background)["background"] != true {
		t.Fatalf("background task = %#v", background)
	}
	select {
	case event := <-conv.Events():
		if event.Kind == ports.ChatEventActivityCompleted && detailOf(t, event)["providerToolName"] == subagentHandbackTool {
			t.Fatalf("background task handed back at launch: %#v", event)
		}
	case <-time.After(100 * time.Millisecond):
	}
}

// A launch whose turn settled before it named a child names nothing later, so
// what waits on it goes out instead of being held until the cap, and the turn's
// settlement closes it like the rest of its work.
func TestOpenCodeV2SettledTurnReleasesHeldChildWork(t *testing.T) {
	agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
	_ = launchSubagent(t, agent, conv, "call_launch")
	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update",
		"update": childToolCall("ses_child:call_read", "Find tests: read", "pending")})
	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update", "update": map[string]any{
		"sessionUpdate": "agent_message_chunk", "messageId": "msg_1",
		"content": map[string]any{"type": "text", "text": "Looking."},
	}})
	// The child updates are read off the wire before the turn settles.
	time.Sleep(50 * time.Millisecond)

	conv.settleOpenItems("turn-1", nil)
	var stepStarted, messageCompleted bool
	launchStatus := domain.ActivityStatus("")
	for quiet := time.After(300 * time.Millisecond); ; {
		select {
		case event := <-conv.Events():
			switch {
			case event.ProviderItemID == "ses_child:call_read" && event.Kind == ports.ChatEventActivityStarted:
				stepStarted = true
			case event.ProviderItemID == "msg_1" && event.Kind == ports.ChatEventMessageCompleted:
				messageCompleted = true
			case event.ProviderItemID == "call_launch" && event.Kind == ports.ChatEventActivityCompleted:
				launchStatus = event.ActivityStatus
			}
			continue
		case <-quiet:
		}
		break
	}
	if !stepStarted || launchStatus != domain.ActivityStatusFailed {
		t.Fatalf("after the turn settled: child step started=%v, launch status %q", stepStarted, launchStatus)
	}
	if !messageCompleted {
		t.Fatal("a child message released by the settling turn was never completed")
	}
}

// A follow-up joins its child's run, so when the child ends, the launch that
// started it gets the outcome as well as the follow-up.
func TestOpenCodeV2ChildReportsToEveryLaunchWaitingOnIt(t *testing.T) {
	agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
	_ = launchSubagent(t, agent, conv, "call_first")
	_ = completeLaunch(t, agent, conv, "call_first", "ses_child")
	sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: "call_second", Title: "subagent",
		Kind: acpsdk.ToolKindThink, Status: acpsdk.ToolCallStatusInProgress,
		RawInput: map[string]any{"agent": "explore", "description": "Find tests", "prompt": "And the e2e tests",
			"sessionID": "ses_child"},
	}})
	_ = nextActivity(t, conv)
	_ = completeLaunch(t, agent, conv, "call_second", "ses_child")

	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "status", "status": "failed",
		"error": map[string]any{"type": "provider", "message": "rate limited"}})
	reported := map[any]domain.ActivityStatus{}
	for range 2 {
		handback := nextActivity(t, conv)
		detail := detailOf(t, handback)
		if detail["providerToolName"] != subagentHandbackTool {
			t.Fatalf("event = %#v, want a hand-back", handback)
		}
		reported[detail["parentProviderItemId"]] = handback.ActivityStatus
	}
	if reported["call_first"] != domain.ActivityStatusFailed || reported["call_second"] != domain.ActivityStatusFailed {
		t.Fatalf("hand-backs by launch = %v, want both launches failed", reported)
	}
}

// A follow-up launch reuses its child's session; what the child says for it
// is a message of its own, even when OpenCode gives it no id.
func TestOpenCodeV2FollowUpMessageIsItsOwn(t *testing.T) {
	agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
	say := func() ports.ChatEvent {
		sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update", "update": map[string]any{
			"sessionUpdate": "agent_message_chunk", "content": map[string]any{"type": "text", "text": "Done."},
		}})
		started := nextActivity(t, conv)
		_ = nextActivity(t, conv) // its text
		sendChild(t, agent, conv, "ses_child", map[string]any{"type": "status", "status": "completed"})
		_ = nextActivity(t, conv) // the message completes
		_ = nextActivity(t, conv) // the hand-back
		return started
	}

	_ = launchSubagent(t, agent, conv, "call_first")
	_ = completeLaunch(t, agent, conv, "call_first", "ses_child")
	first := say()

	running := acpsdk.ToolCallStatusInProgress
	sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
		SessionUpdate: "tool_call", ToolCallId: "call_second", Title: "subagent",
		Kind: acpsdk.ToolKindThink, Status: running,
		RawInput: map[string]any{"agent": "explore", "description": "Find tests", "prompt": "Now the e2e tests",
			"sessionID": "ses_child"},
	}})
	_ = nextActivity(t, conv)
	_ = completeLaunch(t, agent, conv, "call_second", "ses_child")
	second := say()

	if first.ProviderItemID == second.ProviderItemID {
		t.Fatalf("both launches' messages are item %q", first.ProviderItemID)
	}
	if parent := detailOf(t, second)["parentProviderItemId"]; parent != "call_second" {
		t.Fatalf("follow-up message parent = %v", parent)
	}
}

// A follow-up names its child in its input before OpenCode accepts it. The child
// stays with its launch until a follow-up is accepted, and a step it began under
// one launch ends under that launch.
func TestOpenCodeV2FollowUpTakesTheChildOnlyOnceAccepted(t *testing.T) {
	agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
	_ = launchSubagent(t, agent, conv, "call_first")
	_ = completeLaunch(t, agent, conv, "call_first", "ses_child")
	sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update",
		"update": childToolCall("ses_child:call_sleep", "Find tests: sleep", "in_progress")})
	_ = nextActivity(t, conv)

	followUp := func(id string, status acpsdk.ToolCallStatus) {
		t.Helper()
		sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			SessionUpdate: "tool_call", ToolCallId: acpsdk.ToolCallId(id), Title: "subagent",
			Kind: acpsdk.ToolKindThink, Status: acpsdk.ToolCallStatusInProgress,
			RawInput: map[string]any{"agent": "explore", "description": "Find tests", "prompt": "And the e2e tests",
				"sessionID": "ses_child"},
		}})
		_ = nextActivity(t, conv)
		sendUpdate(t, agent, conv, acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
			SessionUpdate: "tool_call_update", ToolCallId: acpsdk.ToolCallId(id), Status: &status,
			RawOutput: map[string]any{"metadata": map[string]any{"sessionID": "ses_child", "status": "running"}},
		}})
		_ = nextActivity(t, conv)
	}
	parentOf := func(update map[string]any) any {
		t.Helper()
		sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update", "update": update})
		return detailOf(t, nextActivity(t, conv))["parentProviderItemId"]
	}

	followUp("call_rejected", acpsdk.ToolCallStatusFailed)
	if parent := parentOf(childToolCall("ses_child:call_ls", "Find tests: ls", "pending")); parent != "call_first" {
		t.Fatalf("step after a rejected follow-up is under %v, want call_first", parent)
	}

	followUp("call_accepted", acpsdk.ToolCallStatusCompleted)
	if parent := parentOf(map[string]any{
		"sessionUpdate": "tool_call_update", "toolCallId": "ses_child:call_sleep", "status": "completed",
	}); parent != "call_first" {
		t.Fatalf("step begun before the follow-up ended under %v, want call_first", parent)
	}
	if parent := parentOf(childToolCall("ses_child:call_grep", "Find tests: grep", "pending")); parent != "call_accepted" {
		t.Fatalf("step after an accepted follow-up is under %v, want call_accepted", parent)
	}
}

// childEvents gathers what the conversation emits until it goes quiet.
func childEvents(t *testing.T, conv *conversation) []ports.ChatEvent {
	t.Helper()
	var events []ports.ChatEvent
	for {
		select {
		case event := <-conv.Events():
			events = append(events, event)
		case <-time.After(200 * time.Millisecond):
			return events
		}
	}
}

func handbacksIn(t *testing.T, events []ports.ChatEvent) []ports.ChatEvent {
	t.Helper()
	var out []ports.ChatEvent
	for _, event := range events {
		if detailOf(t, event)["providerToolName"] == subagentHandbackTool {
			out = append(out, event)
		}
	}
	return out
}

// A child can run again under the same launch: a background shell it started
// wakes it after it completed, or it resumes after an interrupt. Each run
// reports as an item of its own, so the second report never overwrites the
// first, and the conversation stays awake while either run is going.
func TestOpenCodeV2ChildReportsEachRunOnItsOwn(t *testing.T) {
	for _, first := range []struct {
		status string
		want   domain.ActivityStatus
	}{
		{status: "completed", want: domain.ActivityStatusCompleted},
		{status: "interrupted", want: domain.ActivityStatusCancelled},
	} {
		t.Run(first.status, func(t *testing.T) {
			agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
			_ = launchSubagent(t, agent, conv, "call_launch")
			_ = completeLaunch(t, agent, conv, "call_launch", "ses_child")
			setActiveTurn(conv, "")
			canHibernate := func() bool {
				t.Helper()
				ok, err := conv.CanHibernate(context.Background())
				if err != nil {
					t.Fatalf("CanHibernate: %v", err)
				}
				return ok
			}

			sendChild(t, agent, conv, "ses_child", map[string]any{"type": "status", "status": "running"})
			_ = childEvents(t, conv)
			if canHibernate() {
				t.Fatal("conversation can hibernate while its child runs")
			}
			sendChild(t, agent, conv, "ses_child", map[string]any{"type": "status", "status": first.status})
			firstRun := handbacksIn(t, childEvents(t, conv))
			if len(firstRun) != 1 || firstRun[0].ActivityStatus != first.want {
				t.Fatalf("first run's hand-backs = %#v", firstRun)
			}
			if !canHibernate() {
				t.Fatal("conversation cannot hibernate after its child's run ended")
			}

			sendChild(t, agent, conv, "ses_child", map[string]any{"type": "status", "status": "running"})
			sendChild(t, agent, conv, "ses_child", map[string]any{"type": "update",
				"update": childToolCall("ses_child:call_read", "Find tests: read", "completed")})
			_ = childEvents(t, conv)
			if canHibernate() {
				t.Fatal("conversation can hibernate while its child runs again")
			}
			sendChild(t, agent, conv, "ses_child", map[string]any{"type": "status", "status": "completed"})
			secondRun := handbacksIn(t, childEvents(t, conv))
			if len(secondRun) != 1 || secondRun[0].ActivityStatus != domain.ActivityStatusCompleted {
				t.Fatalf("second run's hand-backs = %#v", secondRun)
			}
			if secondRun[0].ProviderItemID == firstRun[0].ProviderItemID {
				t.Fatalf("both runs reported as item %q", firstRun[0].ProviderItemID)
			}
			if parent := detailOf(t, secondRun[0])["parentProviderItemId"]; parent != "call_launch" {
				t.Fatalf("second run reported to %v, want call_launch", parent)
			}
		})
	}
}

// Outside a turn, a child no launch is known for, as after a reattach that lost
// its launch, is dropped as before the capability: filed as an update of the
// parent's, its prose would read as the parent's reply.
func TestOpenCodeV2UnplacedChildOutsideATurnIsDropped(t *testing.T) {
	agent, conv := startOpenCode(t, domain.HarnessOpenCodeV2)
	setActiveTurn(conv, "")
	sendChild(t, agent, conv, "ses_orphan", map[string]any{"type": "update", "update": map[string]any{
		"sessionUpdate": "agent_message_chunk", "messageId": "msg_orphan",
		"content": map[string]any{"type": "text", "text": "Found 3 tests."},
	}})
	sendChild(t, agent, conv, "ses_orphan", map[string]any{"type": "update",
		"update": childToolCall("ses_orphan:call_read", "Find tests: read", "pending")})
	for _, event := range childEvents(t, conv) {
		switch event.Kind {
		case ports.ChatEventActivityStarted, ports.ChatEventActivityCompleted, ports.ChatEventActivityText,
			ports.ChatEventMessageDelta, ports.ChatEventMessageCompleted, ports.ChatEventProviderState:
			t.Fatalf("unplaced child update outside a turn emitted %#v", event)
		}
	}
}
