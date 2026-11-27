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

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// scriptedRecords plays an agent's records of one child: what the poll finds
// while the turn runs, then what lands only after the prompt has returned. The
// last batch goes only to the last read before the turn settles, the one read
// with a deadline, so the test fails if that read is missing.
type scriptedRecords struct {
	mu        sync.Mutex
	during    []RecordedUpdate
	after     []RecordedUpdate
	sentAfter bool
	polled    chan struct{}
	session   string
}

func (s *scriptedRecords) Read(ctx context.Context) ([]RecordedUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.during != nil {
		out := s.during
		s.during = nil
		close(s.polled)
		return out, nil
	}
	if _, last := ctx.Deadline(); last && ctx.Err() == nil && !s.sentAfter {
		s.sentAfter = true
		return s.after, nil
	}
	return nil, nil
}

func (s *scriptedRecords) Busy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !s.sentAfter
}

func TestRecordedSubagentFilesItsWorkOnTheTurnUnderItsLaunch(t *testing.T) {
	child := "child-1"
	call := func(id, title string) *acpsdk.SessionUpdate {
		return &acpsdk.SessionUpdate{ToolCall: &acpsdk.SessionUpdateToolCall{
			ToolCallId: acpsdk.ToolCallId(id), Title: title, Kind: acpsdk.ToolKindOther,
			Status: acpsdk.ToolCallStatusInProgress, RawInput: map[string]any{"command": "ls"},
		}}
	}
	done := acpsdk.ToolCallStatusCompleted
	said := "said-1"
	records := &scriptedRecords{
		polled: make(chan struct{}),
		during: []RecordedUpdate{
			{Launch: "launch-1", Child: child, Update: &acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
				MessageId: &said, Content: acpsdk.TextBlock("Looking at the cart."),
			}}},
			// The child's call id repeats the parent's: ids are scoped by child.
			{Launch: "launch-1", Child: child, Update: call("launch-1", "bash")},
		},
		after: []RecordedUpdate{
			{Launch: "launch-1", Child: child, Update: &acpsdk.SessionUpdate{ToolCallUpdate: &acpsdk.SessionToolCallUpdate{
				ToolCallId: "launch-1", Status: &done,
			}}},
			{Launch: "launch-1", Child: child, End: &RecordedEnd{ID: "run-1", Status: RecordedCompleted, Report: "The cart is fine."}},
		},
	}
	agent := &fakeAgent{}
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		send := func(update acpsdk.SessionUpdate) {
			_ = agent.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: params.SessionId, Update: update})
		}
		send(acpsdk.StartToolCall("launch-1", "subagent",
			acpsdk.WithStartKind(acpsdk.ToolKindOther), acpsdk.WithStartStatus(acpsdk.ToolCallStatusInProgress),
			acpsdk.WithStartRawInput(map[string]any{"description": "Audit the cart", "prompt": "Read-only. Audit the cart."})))
		send(acpsdk.UpdateToolCall("launch-1", acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
			acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{acpsdk.ToolContent(acpsdk.TextBlock("started subagent agent-1"))})))
		// The agent's prompt returns once the child is idle; its last records
		// land after.
		<-records.polled
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	driver := New(Config{
		Harness:      domain.HarnessDeepSeek,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityTools: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SubagentRecords: &SubagentRecords{
			Launch: func(call RecordedCall) (RecordedLaunch, bool) {
				input, _ := call.RawInput.(map[string]any)
				if call.Title != "subagent" || input == nil {
					return RecordedLaunch{}, false
				}
				return RecordedLaunch{ToolName: "subagent", Title: input["description"].(string), Prompt: input["prompt"].(string)}, true
			},
			Open: func(env map[string]string, workspace, session string) SubagentRecordReader {
				records.session = session
				return records
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	opened, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer opened.Close()
	conv := opened.(*conversation)
	events := opened.Events()
	startTurn(t, opened, "Audit the cart with a subagent.")
	seen := collectUntil(t, events, func(e ports.ChatEvent) bool { return e.Kind == ports.ChatEventTurnCompleted })
	turn := seen[len(seen)-1].ProviderTurnID

	if records.session != opened.ProviderConversationID() {
		t.Fatalf("records opened for session %q, want %q", records.session, opened.ProviderConversationID())
	}
	launchItem := conv.providerItemID("launch-1")
	detailOf := func(e ports.ChatEvent) map[string]any {
		var detail map[string]any
		_ = json.Unmarshal(e.Detail, &detail)
		return detail
	}
	var launchDetail, stepDetail, messageDetail, handbackDetail map[string]any
	var step, message, handback ports.ChatEvent
	for _, e := range seen {
		switch {
		case e.ProviderItemID == launchItem && e.Kind == ports.ChatEventActivityCompleted:
			launchDetail = detailOf(e)
		case e.Summary == "bash" && e.Kind == ports.ChatEventActivityCompleted:
			step, stepDetail = e, detailOf(e)
		case e.Summary == subagentResponseSummary:
			message, messageDetail = e, detailOf(e)
		case e.Summary == subagentHandbackTool:
			handback, handbackDetail = e, detailOf(e)
		}
	}
	if launchDetail["providerToolName"] != "subagent" || launchDetail["background"] != true {
		t.Fatalf("launch detail = %v; want the launch name and background", launchDetail)
	}
	if input, _ := launchDetail["input"].(map[string]any); input["description"] != "Audit the cart" || input["prompt"] != "Read-only. Audit the cart." {
		t.Fatalf("launch input = %v", launchDetail["input"])
	}
	if stepDetail == nil || stepDetail["parentProviderItemId"] != launchItem || step.ProviderTurnID != turn {
		t.Fatalf("child step = %+v %v; want it under the launch, on the turn", step, stepDetail)
	}
	if step.ProviderItemID == launchItem {
		t.Fatal("a child's call id must not take its launch's item")
	}
	if messageDetail["parentProviderItemId"] != launchItem || messageDetail["nestedAgent"] != true ||
		message.Text != "Looking at the cart." || message.ProviderTurnID != turn {
		t.Fatalf("child message = %+v %v", message, messageDetail)
	}
	if handbackDetail["parentProviderItemId"] != launchItem || handback.ActivityStatus != domain.ActivityStatusCompleted ||
		handback.ProviderTurnID != turn {
		t.Fatalf("hand-back = %+v %v", handback, handbackDetail)
	}
	if input, _ := handbackDetail["input"].(map[string]any); input["message"] != "The cart is fine." {
		t.Fatalf("hand-back report = %v", handbackDetail["input"])
	}
}

func TestRecordedHandbackSaysHowTheRunEnded(t *testing.T) {
	conv := newTestConversation()
	for status, want := range map[RecordedEndStatus]domain.ActivityStatus{
		RecordedCompleted: domain.ActivityStatusCompleted,
		RecordedFailed:    domain.ActivityStatusFailed,
		RecordedCancelled: domain.ActivityStatusCancelled,
	} {
		event := conv.recordedHandback("turn-1", "launch", "child", RecordedEnd{ID: "run-1", Status: status, Report: "why"})
		if event.ActivityStatus != want {
			t.Errorf("%s: status = %s, want %s", status, event.ActivityStatus, want)
		}
		second := conv.recordedHandback("turn-1", "launch", "child", RecordedEnd{ID: "run-2", Status: status})
		if second.ProviderItemID == event.ProviderItemID {
			t.Errorf("%s: each run must report under its own item", status)
		}
	}
}

// A turn that only sends an earlier child more work, as after a restart that
// lost the conversation's followers, still follows the records.
func TestRecordedSubagentIsFollowedWhenATurnResumesIt(t *testing.T) {
	read := make(chan struct{}, 8)
	opened := make(chan string, 1)
	agent := &fakeAgent{}
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		_ = agent.conn.SessionUpdate(ctx, acpsdk.SessionNotification{SessionId: params.SessionId,
			Update: acpsdk.StartToolCall("resume-1", "send_message", acpsdk.WithStartKind(acpsdk.ToolKindOther),
				acpsdk.WithStartRawInput(map[string]any{"agent": "agent-1", "message": "Check the totals too."}))})
		select {
		case <-read:
		case <-ctx.Done():
		}
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonEndTurn}, nil
	}
	reader := readerFunc(func() {
		select {
		case read <- struct{}{}:
		default:
		}
	})
	driver := New(Config{
		Harness:      domain.HarnessDeepSeek,
		Capabilities: ports.ChatCapabilities{ports.ChatCapabilityStreaming: true, ports.ChatCapabilityTools: true},
		Probe:        func(context.Context) error { return nil },
		Launch:       func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
		SubagentRecords: &SubagentRecords{
			Launch:  func(RecordedCall) (RecordedLaunch, bool) { return RecordedLaunch{}, false },
			Resumes: func(call RecordedCall) bool { return call.Title == "send_message" },
			Open: func(_ map[string]string, _ string, session string) SubagentRecordReader {
				opened <- session
				return reader
			},
		},
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	conv, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer conv.Close()
	startTurn(t, conv, "Ask agent-1 to check the totals.")
	collectUntil(t, conv.Events(), func(e ports.ChatEvent) bool { return e.Kind == ports.ChatEventTurnCompleted })
	select {
	case session := <-opened:
		if session != conv.ProviderConversationID() {
			t.Fatalf("opened for %q, want %q", session, conv.ProviderConversationID())
		}
	default:
		t.Fatal("a turn that resumes a child must open the records")
	}
}

type readerFunc func()

func (f readerFunc) Read(context.Context) ([]RecordedUpdate, error) {
	f()
	return nil, nil
}

func (readerFunc) Busy() bool { return false }

// listReader hands out one list of updates, then nothing.
type listReader struct {
	updates []RecordedUpdate
	busy    bool
}

func (r *listReader) Read(context.Context) ([]RecordedUpdate, error) {
	out := r.updates
	r.updates = nil
	return out, nil
}

func (r *listReader) Busy() bool { return r.busy }

func recordTestConversation(turn string, buffer int, reader SubagentRecordReader) (*conversation, *recordFollower) {
	conv := newTestConversation()
	conv.events = make(chan ports.ChatEvent, buffer)
	conv.closing = make(chan struct{})
	conv.activeTurn = turn
	follower := &recordFollower{reader: reader, readMu: make(chan struct{}, 1),
		tools: map[string]map[string]*toolState{}, said: map[string]map[string]string{}}
	conv.recordFollower = follower
	return conv, follower
}

func said(child, id, text string) RecordedUpdate {
	return RecordedUpdate{Launch: "launch", Child: child, Update: &acpsdk.SessionUpdate{AgentMessageChunk: &acpsdk.SessionUpdateAgentMessageChunk{
		MessageId: &id, Content: acpsdk.TextBlock(text),
	}}}
}

func TestRecordPollDoesNotStartWhileTheTurnSettles(t *testing.T) {
	conv, follower := recordTestConversation("turn-1", 4, &listReader{})
	conv.settlingTurn = "turn-1"
	conv.pollSubagentRecords()
	if follower.stop != nil {
		t.Fatal("a launch seen while the turn settles must leave reading to the last read")
	}
}

// A consumer that stops reading must not hold the turn's last read past its
// deadline, and what it could not take is delivered on the next read of the
// same turn, whole and once.
func TestRecordDeliveryGivesUpAndRedeliversInTheSameTurn(t *testing.T) {
	reader := &listReader{updates: []RecordedUpdate{said("child", "m1", "One."), said("child", "m2", "Two.")}}
	conv, follower := recordTestConversation("turn-1", 1, reader)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if busy, ok := conv.readSubagentRecords(ctx, follower); !ok || !busy {
		t.Fatalf("read = busy %v, ok %v; want busy with a backlog", busy, ok)
	}
	if len(follower.backlog) != 1 || follower.backlogTurn != "turn-1" {
		t.Fatalf("backlog = %d for %q", len(follower.backlog), follower.backlogTurn)
	}
	if first := <-conv.events; first.Text != "One." {
		t.Fatalf("first = %q", first.Text)
	}
	if busy, _ := conv.readSubagentRecords(context.Background(), follower); busy {
		t.Fatal("still busy after the backlog went out")
	}
	if second := <-conv.events; second.Text != "Two." {
		t.Fatalf("second = %q; the held update must go out whole", second.Text)
	}
}

func TestRecordBacklogIsNeverFiledOnALaterTurn(t *testing.T) {
	conv, follower := recordTestConversation("turn-2", 4, &listReader{})
	follower.backlog, follower.backlogTurn = []RecordedUpdate{said("child", "m1", "Late.")}, "turn-1"
	conv.readSubagentRecords(context.Background(), follower)
	if len(follower.backlog) != 0 || len(conv.events) != 0 {
		t.Fatalf("turn-1's backlog must be dropped, not filed on turn-2: %d left, %d sent", len(follower.backlog), len(conv.events))
	}
}

func TestRecordedRunEndReleasesItsMessages(t *testing.T) {
	reader := &listReader{updates: []RecordedUpdate{
		said("child", "m1", "Working."),
		{Launch: "launch", Child: "child", End: &RecordedEnd{ID: "run-1", Status: RecordedCompleted}},
	}}
	conv, follower := recordTestConversation("turn-1", 4, reader)
	conv.readSubagentRecords(context.Background(), follower)
	if len(follower.said) != 0 {
		t.Fatalf("an ended run's messages are still held: %v", follower.said)
	}
}
