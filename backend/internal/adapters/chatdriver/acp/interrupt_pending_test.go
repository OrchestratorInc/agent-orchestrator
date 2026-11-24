package acp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ACP prompt-turn cancellation: once the client sends session/cancel it must
// answer every pending session/request_permission with the cancelled outcome,
// rather than leaving the agent blocked until the approval timeout.
func TestACPInterruptCancelsPendingPermission(t *testing.T) {
	outcome := make(chan acpsdk.RequestPermissionOutcome, 1)
	agent := &fakeAgent{}
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		// A real agent keeps waiting for the client's answer after session/cancel,
		// so the request must not die with the SDK's prompt context.
		response, err := agent.conn.RequestPermission(context.WithoutCancel(ctx), acpsdk.RequestPermissionRequest{
			SessionId: params.SessionId,
			ToolCall: acpsdk.ToolCallUpdate{
				ToolCallId: "tool-1", Title: acpsdk.Ptr("Edit file"), Kind: acpsdk.Ptr(acpsdk.ToolKindEdit),
			},
			Options: []acpsdk.PermissionOption{
				{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
				{OptionId: "reject", Name: "Reject", Kind: acpsdk.PermissionOptionKindRejectOnce},
			},
		})
		if err != nil {
			return acpsdk.PromptResponse{}, err
		}
		outcome <- response.Outcome
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	}
	conversation, turnID := startInterruptPendingTurn(t, agent)
	defer conversation.Close()

	requestID := waitForEventKind(t, conversation.Events(), ports.ChatEventApprovalRequested).RequestID
	if err := conversation.Interrupt(context.Background(), turnID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	select {
	case got := <-outcome:
		if got.Cancelled == nil || got.Selected != nil {
			t.Fatalf("permission outcome = %#v, want cancelled", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending permission was not answered after session/cancel")
	}
	resolved := waitForEventKind(t, conversation.Events(), ports.ChatEventApprovalResolved)
	if resolved.RequestID != requestID {
		t.Fatalf("resolved request = %q, want %q", resolved.RequestID, requestID)
	}
	completed := waitForEventKind(t, conversation.Events(), ports.ChatEventTurnCompleted)
	if completed.TurnState != domain.TurnStateInterrupted {
		t.Fatalf("turn state = %q, want interrupted", completed.TurnState)
	}
	if err := conversation.ResolveRequest(context.Background(), requestID, ports.ChatDecision{ID: "allow"}); !errors.Is(err, ports.ErrChatRequestNotPending) {
		t.Fatalf("late ResolveRequest error = %v, want ErrChatRequestNotPending", err)
	}
}

func TestACPInterruptCancelsPendingElicitation(t *testing.T) {
	action := make(chan string, 1)
	agent := &fakeAgent{}
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		request := acpsdk.NewUnstableCreateElicitationRequestForm(acpsdk.UnstableElicitationSchema{
			Type:       acpsdk.UnstableElicitationSchemaTypeObject,
			Properties: map[string]any{"choice": map[string]any{"type": "string"}},
		})
		request.Form.Message = "Which branch?"
		response, err := agent.conn.UnstableCreateElicitation(context.WithoutCancel(ctx), request)
		if err != nil {
			return acpsdk.PromptResponse{}, err
		}
		switch {
		case response.Cancel != nil:
			action <- "cancel"
		case response.Accept != nil:
			action <- "accept"
		default:
			action <- "other"
		}
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	}
	conversation, turnID := startInterruptPendingTurn(t, agent)
	defer conversation.Close()

	requestID := waitForEventKind(t, conversation.Events(), ports.ChatEventInputRequested).RequestID
	if err := conversation.Interrupt(context.Background(), turnID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	select {
	case got := <-action:
		if got != "cancel" {
			t.Fatalf("elicitation action = %q, want cancel", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("pending elicitation was not answered after session/cancel")
	}
	resolved := waitForEventKind(t, conversation.Events(), ports.ChatEventInputResolved)
	if resolved.RequestID != requestID {
		t.Fatalf("resolved request = %q, want %q", resolved.RequestID, requestID)
	}
}

// A request the agent sends after session/cancel, while the stopped turn is
// still winding down, is answered cancelled at once instead of being parked.
func TestACPInterruptCancelsPermissionRequestedAfterCancel(t *testing.T) {
	started := make(chan struct{})
	outcome := make(chan acpsdk.RequestPermissionOutcome, 1)
	agent := &fakeAgent{}
	agent.customPrompt = func(ctx context.Context, params acpsdk.PromptRequest) (acpsdk.PromptResponse, error) {
		close(started)
		<-ctx.Done() // session/cancel arrived
		response, err := agent.conn.RequestPermission(context.WithoutCancel(ctx), acpsdk.RequestPermissionRequest{
			SessionId: params.SessionId,
			ToolCall:  acpsdk.ToolCallUpdate{ToolCallId: "tool-late", Title: acpsdk.Ptr("Edit file")},
			Options: []acpsdk.PermissionOption{
				{OptionId: "allow", Name: "Allow", Kind: acpsdk.PermissionOptionKindAllowOnce},
			},
		})
		if err != nil {
			return acpsdk.PromptResponse{}, err
		}
		outcome <- response.Outcome
		return acpsdk.PromptResponse{StopReason: acpsdk.StopReasonCancelled}, nil
	}
	conversation, turnID := startInterruptPendingTurn(t, agent)
	defer conversation.Close()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("prompt did not start")
	}
	if err := conversation.Interrupt(context.Background(), turnID); err != nil {
		t.Fatalf("Interrupt: %v", err)
	}
	select {
	case got := <-outcome:
		if got.Cancelled == nil || got.Selected != nil {
			t.Fatalf("permission outcome = %#v, want cancelled", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("permission requested after session/cancel was parked")
	}
	completed := waitForEventKind(t, conversation.Events(), ports.ChatEventTurnCompleted)
	if completed.TurnState != domain.TurnStateInterrupted {
		t.Fatalf("turn state = %q, want interrupted", completed.TurnState)
	}
}

func startInterruptPendingTurn(t *testing.T, agent *fakeAgent) (ports.ChatConversation, string) {
	t.Helper()
	driver := New(Config{
		Harness: domain.HarnessOpenCode,
		Capabilities: ports.ChatCapabilities{
			ports.ChatCapabilityStreaming: true, ports.ChatCapabilityApprovals: true,
			ports.ChatCapabilityInterrupt: true,
		},
		Probe:  func(context.Context) error { return nil },
		Launch: func(context.Context, LaunchConfig) (Launch, error) { return Launch{Command: "fake"}, nil },
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	driver.useTestProcess(fakeSpawn(agent))
	conversation, err := driver.Start(context.Background(), ports.ChatStartConfig{WorkspacePath: t.TempDir()})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	ref, err := conversation.SendTurn(context.Background(), ports.ChatUserMessage{Text: "edit"})
	if err != nil {
		conversation.Close()
		t.Fatalf("SendTurn: %v", err)
	}
	if err := conversation.(ports.ChatDeferredTurnStarter).StartDeferredTurn(ref.ProviderTurnID); err != nil {
		conversation.Close()
		t.Fatalf("StartDeferredTurn: %v", err)
	}
	return conversation, ref.ProviderTurnID
}

func waitForEventKind(t *testing.T, events <-chan ports.ChatEvent, kind ports.ChatEventKind) ports.ChatEvent {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("event stream closed before %s", kind)
			}
			if event.Kind == kind {
				return event
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", kind)
		}
	}
}
