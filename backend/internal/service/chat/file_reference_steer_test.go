package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestFileOnlyLegacyReceiptRetryKeepsItsQueuedPrompt(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	n := 1
	ref := ports.ChatFileReference{ID: "capture", SessionID: string(testSession), Path: "a.ts", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Side: "file", Line: 1, EndLine: 1, Lines: []ports.ChatFileReferenceLine{{Kind: "context", NewNo: &n, Text: "frozen code"}}}
	msg := ports.ChatUserMessage{ClientMessageID: "legacy-file-only", Text: "question", Content: []ports.ChatContent{{Type: "file_reference", FileReference: &ref}}}
	queued, err := h.svc.Send(ctx, testSession, msg)
	if err != nil {
		t.Fatal(err)
	}
	msg.Content[0].Text = chatsvc.FileReferenceQuotedPath
	duplicate, err := h.svc.Send(ctx, testSession, msg)
	if err != nil || duplicate.ID != "" {
		t.Fatalf("legacy retry duplicated/conflicted: %+v %v", duplicate, err)
	}
	if _, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); err != nil {
		t.Fatal(err)
	}
	// Promotion dispatches the queued message as a real send, so the frozen
	// prompt arrives through the provider's ordinary turn input.
	sent := provider.sentTexts()
	prompt := sent[len(sent)-1]
	if !strings.Contains(prompt, "a.ts#L1-L1") || strings.Contains(prompt, `"a.ts"#L1-L1`) {
		t.Fatal("retry reformatted the frozen queued prompt")
	}
	ref.Lines[0].Text = "changed code"
	if _, err = h.svc.Send(ctx, testSession, msg); !errors.Is(err, domain.ErrClientMessageConflict) {
		t.Fatalf("changed capture reused receipt: %v", err)
	}
}

func TestPromoteQueuedFileCaptureFormatsOnceAndRetainsProvenance(t *testing.T) {
	// interruptCounting settles the cut immediately: promotion is
	// cut-and-dispatch, and this test cares about the composed prompt the
	// promoted queued message dispatches with.
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()

	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	n := 1
	ref := ports.ChatFileReference{ID: "captured", Order: 2, SessionID: string(testSession), Path: "a.ts", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Side: "file", Line: 1, EndLine: 1, Lines: []ports.ChatFileReferenceLine{{Kind: "context", NewNo: &n, OldNo: &n, Text: "QUEUE_CAPTURE_MARKER"}}}
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "question", Origin: domain.MessageOriginHuman, ClientMessageID: "queued-capture", Content: []ports.ChatContent{
		{Type: "file_reference", FileReference: &ref},
	}})
	if err != nil {
		t.Fatal(err)
	}
	// The queued item keeps its durable provenance until it dispatches.
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil || next.TurnID != queued.ID {
		t.Fatalf("lost queued capture: %+v %v", next, err)
	}
	var durable []ports.ChatContent
	if err := json.Unmarshal([]byte(next.DeliveryContentJSON), &durable); err != nil || len(durable) != 1 || durable[0].FileReference.ID != ref.ID {
		t.Fatalf("lost provenance: %+v %v", durable, err)
	}
	if _, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); err != nil {
		t.Fatal(err)
	}
	// The promoted message dispatches as a real send; its composed prompt keeps
	// the capture formatted exactly once.
	sent := provider.sentMessages()
	if len(sent) != 2 {
		t.Fatalf("provider dispatches = %d, want the original turn plus the promoted one", len(sent))
	}
	last := sent[len(sent)-1]
	if strings.Count(last.Text, "QUEUE_CAPTURE_MARKER") != 1 || strings.Count(last.Text, "a.ts#L1-L1") != 1 {
		t.Fatalf("provider prompt: %q", last.Text)
	}
}
