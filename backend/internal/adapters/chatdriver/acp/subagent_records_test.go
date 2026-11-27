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

// scriptedRecords plays an agent's records of one child: what the poll finds
// while the turn runs, then what lands only after the prompt has returned.
type scriptedRecords struct {
	mu        sync.Mutex
	during    []RecordedUpdate
	after     []RecordedUpdate
	promptEnd bool
	sentAfter bool
	polled    chan struct{}
	launched  map[string]bool
	session   string
}

func (s *scriptedRecords) Read(_ context.Context, launched map[string]bool) ([]RecordedUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.launched = launched
	if s.during != nil {
		out := s.during
		s.during = nil
		close(s.polled)
		return out, nil
	}
	if s.promptEnd && !s.sentAfter {
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
		records.mu.Lock()
		records.promptEnd = true
		records.mu.Unlock()
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
	if !records.launched["launch-1"] {
		t.Fatalf("the launch seen on this connection must be passed to Read, got %v", records.launched)
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
	read := make(chan map[string]bool, 8)
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
	reader := readerFunc(func(launched map[string]bool) {
		select {
		case read <- launched:
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

type readerFunc func(launched map[string]bool)

func (f readerFunc) Read(_ context.Context, launched map[string]bool) ([]RecordedUpdate, error) {
	f(launched)
	return nil, nil
}

func (readerFunc) Busy() bool { return false }
