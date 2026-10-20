package triage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRunnerRelaysNativeChatAndWaitsForControllerReady(t *testing.T) {
	dir := t.TempDir()
	conv := fakeConversation()
	deps := configuredDeps()
	var runID string
	deps.StartAgent = func(_ context.Context, run Run) (AgentTransport, error) {
		runID = run.ID
		return AgentTransport{Conversation: conv}, nil
	}
	store, err := openRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv.ackHook = func(id string) error {
		if id != "approval-event" {
			return nil
		}
		saved, err := store.get(runID)
		if err != nil || saved.State != "needs_input" {
			return errors.New("native request was acknowledged before its run state committed")
		}
		return nil
	}
	var resolvedApproval ports.ChatDecision
	conv.resolveHook = func(id string, decision ports.ChatDecision) error {
		if id != "approval-1" {
			return errors.New("wrong approval request id")
		}
		resolvedApproval = decision
		return nil
	}
	var resolvedInput ports.ChatInputResponse
	conv.inputHook = func(id string, response ports.ChatInputResponse) error {
		if id != "input-1" {
			return errors.New("wrong input request id")
		}
		resolvedInput = response
		return nil
	}
	conv.deferredHook = func(turnID string) error {
		saved, err := store.get(runID)
		if err != nil || saved.ProviderTurnID != turnID {
			return errors.New("deferred turn started before its identity was committed")
		}
		return nil
	}
	c := serveForTest(t, dir, deps)
	c.send(startCommand("start", false))
	started := c.response("start").Run
	if started == nil {
		t.Fatal("start did not return a run")
	}
	c.wait(func(response Response) bool { return response.Run != nil && response.Run.Transport == "chat" })
	select {
	case message := <-conv.started:
		t.Fatalf("initial turn started before ControllerReady: %#v", message)
	default:
	}
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerReady}
	initial := receiveMessage(t, conv.started)
	if !strings.Contains(initial.Text, "triage.md") || initial.AuthoredByUser {
		t.Fatalf("initial native turn = %#v", initial)
	}
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventMessageDelta, ProviderTurnID: "turn-1", ProviderItemID: "assistant-1", Delta: "Investigating"}
	c.wait(func(response Response) bool {
		if response.Run == nil {
			return false
		}
		for _, message := range response.Run.Messages {
			if message.ID == "assistant-1" && message.Text == "Investigating" {
				return true
			}
		}
		return false
	})
	c.send(Command{ID: "reply-1", Type: "reply", RunID: started.ID, Text: "The issue began after restart."})
	if response := c.response("reply-1"); response.Type != "ack" {
		t.Fatalf("reply response = %#v", response)
	}
	select {
	case message := <-conv.started:
		t.Fatalf("queued reply sent before ControllerReady: %#v", message)
	default:
	}
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "turn-1", TurnState: domain.TurnStateCompleted}
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventControllerState, ControllerState: ports.ChatControllerReady}
	followup := receiveMessage(t, conv.started)
	if !followup.AuthoredByUser || followup.Text != "The issue began after restart." {
		t.Fatalf("queued follow-up = %#v", followup)
	}
	detail := json.RawMessage(`{"command":"run tests","target":"package"}`)
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventApprovalRequested, RequestID: "approval-1", ProviderEventID: "approval-event", Summary: "Run requested tests", Detail: detail,
		Decisions: []ports.ChatDecisionOption{{ID: "allow", Label: "Allow once", Kind: ports.ChatDecisionAllowOnce, Raw: []byte(`{"opaque_policy":"allow-once"}`)}, {ID: "reject", Label: "Reject", Kind: ports.ChatDecisionRejectOnce}}}
	pending := c.wait(func(response Response) bool { return response.Run != nil && response.Run.PendingInput != nil }).Run.PendingInput
	if pending.ID != "approval-1" || pending.Kind != "approval" || pending.Summary != "Run requested tests" {
		t.Fatalf("approval projection = %#v", pending)
	}
	var projected map[string]any
	if err := json.Unmarshal(pending.Detail, &projected); err != nil || projected["command"] != "run tests" || len(projected["decisions"].([]any)) != 2 {
		t.Fatalf("approval did not merge native decisions: %s, %v", pending.Detail, err)
	}
	c.send(Command{ID: "correction-during-approval", Type: "reply", RunID: started.ID, Text: "The package is frontend."})
	if response := c.response("correction-during-approval"); response.Type != "ack" {
		t.Fatalf("ordinary correction could not queue during native approval: %#v", response)
	}
	select {
	case message := <-conv.started:
		t.Fatalf("pending approval did not hold correction: %#v", message)
	default:
	}
	c.send(Command{ID: "approve", Type: "reply", RunID: started.ID, RequestID: "approval-1", DecisionID: "allow"})
	if response := c.response("approve"); response.Type != "ack" || resolvedApproval.ID != "allow" || string(resolvedApproval.Raw) != `{"opaque_policy":"allow-once"}` {
		t.Fatalf("approval answer = %#v, decision %#v", response, resolvedApproval)
	}
	input := &ports.ChatInputRequest{Mode: ports.ChatInputModeForm, Message: "Choose a target", URL: "https://provider.example/opaque-id", Schema: map[string]any{"type": "object", "properties": map[string]any{"target": map[string]any{"type": "string"}}}}
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventInputRequested, RequestID: "input-1", Input: input, Summary: "Choose a target"}
	c.wait(func(response Response) bool {
		return response.Run != nil && response.Run.PendingInput != nil && response.Run.PendingInput.ID == "input-1"
	})
	c.send(Command{ID: "answer", Type: "reply", RunID: started.ID, RequestID: "input-1", Action: ports.ChatInputActionAccept, Content: map[string]any{"target": "package"}})
	if response := c.response("answer"); response.Type != "ack" || resolvedInput.Action != ports.ChatInputActionAccept || resolvedInput.Content["target"] != "package" {
		t.Fatalf("input answer = %#v, response %#v", resolvedInput, response)
	}
	c.send(Command{ID: "cancel", Type: "cancel", RunID: started.ID})
	if response := c.response("cancel"); response.Type != "run" {
		t.Fatalf("cancel response = %#v", response)
	}
}

func TestTerminalReplayIsBoundedAndSequenceUsesByteOffsets(t *testing.T) {
	store, err := openRunStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	h := &host{deps: Dependencies{Now: time.Now}, store: store, out: &output}
	item := &investigation{host: h, id: "terminal-run", run: Run{ID: "terminal-run"}}
	frame := make([]byte, terminalFrameSize)
	for index := range frame {
		frame[index] = byte(index)
	}
	for index := 0; index < 5; index++ {
		item.emitTerminal(frame)
	}
	run := item.snapshot()
	replay, err := base64.StdEncoding.DecodeString(run.TerminalReplay)
	if err != nil || len(replay) != maxTerminalBytes || run.TerminalSequence != 5*terminalFrameSize {
		t.Fatalf("terminal replay length/cursor = %d/%d, err %v", len(replay), run.TerminalSequence, err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 5 {
		t.Fatalf("terminal frames = %d", len(lines))
	}
	for index, line := range lines {
		var response Response
		if err := json.Unmarshal([]byte(line), &response); err != nil || response.Type != "terminal-output" || response.Sequence != int64((index+1)*terminalFrameSize) {
			t.Fatalf("terminal frame %d = %#v, err %v", index, response, err)
		}
		chunk, err := base64.StdEncoding.DecodeString(response.Data)
		if response.Sequence-int64(len(chunk)) != int64(index*terminalFrameSize) {
			t.Fatalf("terminal frame has wrong byte interval: %d to %d", response.Sequence-int64(len(chunk)), response.Sequence)
		}
		if index == len(lines)-1 && response.Sequence != run.TerminalSequence {
			t.Fatal("last live frame and replay snapshot disagree about the byte cursor")
		}
		if err != nil || len(chunk) != terminalFrameSize {
			t.Fatalf("terminal data length = %d, err %v", len(chunk), err)
		}
	}
}

func receiveMessage(t *testing.T, messages <-chan ports.ChatUserMessage) ports.ChatUserMessage {
	t.Helper()
	select {
	case message := <-messages:
		return message
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for native chat turn")
		return ports.ChatUserMessage{}
	}
}

func TestCancelledStartupStopsLateNativeProvider(t *testing.T) {
	conv := fakeConversation()
	entered, release := make(chan struct{}), make(chan struct{})
	deps := configuredDeps()
	deps.StartAgent = func(context.Context, Run) (AgentTransport, error) {
		close(entered)
		<-release
		return AgentTransport{Conversation: conv}, nil
	}
	c := serveForTest(t, t.TempDir(), deps)
	c.send(startCommand("start", false))
	run := c.response("start").Run
	awaitSignal(t, entered)
	c.send(Command{ID: "cancel", Type: "cancel", RunID: run.ID})
	c.response("cancel")
	close(release)
	awaitSignal(t, conv.terminated)
}

func TestNativeProviderFailureIsCommittedBeforeAcknowledgement(t *testing.T) {
	dir := t.TempDir()
	store, err := openRunStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	conv := fakeConversation()
	deps := configuredDeps()
	var runID string
	deps.StartAgent = func(_ context.Context, run Run) (AgentTransport, error) {
		runID = run.ID
		return AgentTransport{Conversation: conv}, nil
	}
	acknowledged := make(chan struct{})
	conv.ackHook = func(id string) error {
		if id != "provider-error" {
			return nil
		}
		run, err := store.get(runID)
		if err != nil || run.State != "failed" || run.Error != "native provider failed" {
			return errors.New("failure event acknowledged before its manifest committed")
		}
		close(acknowledged)
		return nil
	}
	c := serveForTest(t, dir, deps)
	c.send(startCommand("start", false))
	c.response("start")
	c.wait(func(response Response) bool { return response.Run != nil && response.Run.Transport == "chat" })
	conv.events <- ports.ChatEvent{Kind: ports.ChatEventError, ProviderEventID: "provider-error", Err: errors.New("native provider failed")}
	awaitSignal(t, acknowledged)
	awaitSignal(t, conv.terminated)
}

func TestTerminalStartupFailureReachesRun(t *testing.T) {
	reader, writer := io.Pipe()
	defer writer.Close()
	startup := make(chan error, 1)
	startup <- errors.New("native prompt delivery failed")
	rt := &testHarnessRuntime{}
	terminal := &TerminalAgent{runtime: rt, stream: &testTerminalStream{ReadWriteCloser: struct {
		io.Reader
		io.Writer
		io.Closer
	}{reader, io.Discard, reader}}, startup: startup}
	deps := configuredDeps()
	deps.StartAgent = func(context.Context, Run) (AgentTransport, error) { return AgentTransport{Terminal: terminal}, nil }
	c := serveForTest(t, t.TempDir(), deps)
	c.send(startCommand("start", false))
	c.response("start")
	run := c.state("failed")
	if !strings.Contains(run.Error, "native prompt delivery failed") {
		t.Fatalf("startup failure lost: %#v", run)
	}
}

func TestTerminalEOFRequiresConfirmedChildExit(t *testing.T) {
	for _, test := range []struct {
		name  string
		alive bool
		err   error
		state string
	}{
		{"exited", false, nil, "completed"}, {"alive", true, nil, "failed"}, {"unknown", false, errors.New("probe unavailable"), "failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader, writer := io.Pipe()
			_ = writer.Close()
			rt := &testHarnessRuntime{alive: test.alive, probeErr: test.err}
			terminal := &TerminalAgent{runtime: rt, stream: &testTerminalStream{ReadWriteCloser: struct {
				io.Reader
				io.Writer
				io.Closer
			}{reader, io.Discard, reader}}}
			deps := configuredDeps()
			deps.StartAgent = func(context.Context, Run) (AgentTransport, error) { return AgentTransport{Terminal: terminal}, nil }
			c := serveForTest(t, t.TempDir(), deps)
			c.send(startCommand("start", false))
			c.response("start")
			run := c.state(test.state)
			if test.state == "failed" && !strings.Contains(run.Error, "disconnected") {
				t.Fatalf("unknown/live child treated as exited: %#v", run)
			}
		})
	}
}
