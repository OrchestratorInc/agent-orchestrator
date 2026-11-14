package chat_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

func TestFileOnlyLegacyReceiptRetryKeepsItsQueuedPrompt(t *testing.T) {
	provider := &interruptCounting{steerRecorder: newSteerRecorder()}
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()
	n := 1
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
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
	sent := provider.sentMessages()
	if len(sent) == 0 {
		t.Fatal("promoted turn never reached the provider")
	}
	prompt := sent[len(sent)-1].Text
	if !strings.Contains(prompt, "a.ts#L1-L1") || strings.Contains(prompt, `"a.ts"#L1-L1`) {
		t.Fatal("retry reformatted the frozen queued prompt")
	}
	ref.Lines[0].Text = "changed code"
	if _, err = h.svc.Send(ctx, testSession, msg); !errors.Is(err, domain.ErrClientMessageConflict) {
		t.Fatalf("changed capture reused receipt: %v", err)
	}
}

func TestPromoteQueuedFileCaptureFormatsOnceAndRetainsProvenance(t *testing.T) {
	restore := chatsvc.SetQueuedTurnSettleWait(50 * time.Millisecond)
	t.Cleanup(restore)
	provider := &interruptCounting{steerRecorder: newSteerRecorder(), settleOnInterrupt: true}
	provider.failInterrupt(errors.New("cancel refused"))
	h := newHarnessWithConversation(t, provider)
	ctx := context.Background()
	n := 1
	ref := ports.ChatFileReference{ID: "captured", Order: 2, SessionID: string(testSession), Path: "a.ts", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Side: "file", Line: 1, EndLine: 1, Lines: []ports.ChatFileReferenceLine{{Kind: "context", NewNo: &n, OldNo: &n, Text: "QUEUE_CAPTURE_MARKER"}}}
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "quote the long thing", ClientMessageID: "turn-1", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-1"})
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
	// The excerpt content of the original capture is dropped here: excerpts must
	// reference settled transcript text, and their retention is covered by the
	// excerpt validation and the batch input path on their own.
	if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{
		Text: "do the long thing", ClientMessageID: "turn-2", Origin: domain.MessageOriginHuman,
	}); err != nil {
		t.Fatal(err)
	}
	provider.emit(ports.ChatEvent{Kind: ports.ChatEventTurnStarted, ProviderTurnID: "provider-turn-2"})
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "question", Origin: domain.MessageOriginHuman, ClientMessageID: "queued-capture", Content: []ports.ChatContent{
		{Type: "file_reference", FileReference: &ref},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); err == nil {
		t.Fatal("promotion error = nil, want the interrupt refusal")
	}
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil || next.TurnID != queued.ID {
		t.Fatalf("lost queued capture: %+v %v", next, err)
	}
	var durable []ports.ChatContent
	if err := json.Unmarshal([]byte(next.DeliveryContentJSON), &durable); err != nil || len(durable) != 1 || durable[0].FileReference.ID != ref.ID {
		t.Fatalf("lost provenance: %+v %v", durable, err)
	}
	provider.failInterrupt(nil)
	if _, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); err != nil {
		t.Fatal(err)
	}
	// The refused request interrupted once and failed; the retry interrupts the
	// still-running turn again, and only then dispatches.
	calls := provider.interruptCalls()
	if len(calls) != 2 || calls[0] != "provider-turn-2" || calls[1] != "provider-turn-2" {
		t.Fatalf("provider interrupts = %v, want provider-turn-2 refused then accepted", calls)
	}
	// The dispatch rides the drain asynchronously; wait for the promoted turn.
	for i := 0; i < 100; i++ {
		snapshot, err := h.st.LoadConversationSnapshot(ctx, h.ctrl.ConversationID())
		if err != nil {
			t.Fatal(err)
		}
		done := false
		for _, turn := range snapshot.Turns {
			if turn.ID == queued.ID {
				if turn.State == domain.TurnStateRunning {
					done = true
				} else {
					t.Logf("capture turn state=%s provider=%q err=%q", turn.State, turn.ProviderTurnID, turn.ErrorMessage)
				}
			}
		}
		if done {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	sent := provider.sentMessages()
	if len(sent) < 2 {
		t.Fatalf("promoted turn never reached the provider: %d sent = %+v", len(sent), sent)
	}
	last := sent[len(sent)-1]
	// The frozen capture renders exactly once: the capture keeps its marker
	// line in the dispatched prompt, provenance intact.
	if strings.Count(last.Text, "QUEUE_CAPTURE_MARKER") != 1 {
		t.Fatalf("dispatched prompt %q, want the capture marker exactly once", last.Text)
	}
}
