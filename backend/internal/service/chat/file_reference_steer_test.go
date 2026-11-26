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
	calls := provider.steers()
	prompt := calls[len(calls)-1].msg.Text
	if !strings.Contains(prompt, "a.ts#L1-L1") || strings.Contains(prompt, `"a.ts"#L1-L1`) {
		t.Fatal("retry reformatted the frozen queued prompt")
	}
	ref.Lines[0].Text = "changed code"
	if _, err = h.svc.Send(ctx, testSession, msg); !errors.Is(err, domain.ErrClientMessageConflict) {
		t.Fatalf("changed capture reused receipt: %v", err)
	}
}

func TestPromoteQueuedFileCaptureFormatsOnceAndRetainsProvenance(t *testing.T) {
	h, provider := steerHarness(t)
	ctx := context.Background()
	n := 1
	ref := ports.ChatFileReference{ID: "captured", Order: 2, SessionID: string(testSession), Path: "a.ts", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Side: "file", Line: 1, EndLine: 1, Lines: []ports.ChatFileReferenceLine{{Kind: "context", NewNo: &n, OldNo: &n, Text: "QUEUE_CAPTURE_MARKER"}}}
	queued, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "question", Origin: domain.MessageOriginHuman, ClientMessageID: "queued-capture", Content: []ports.ChatContent{
		{Type: "file_reference", FileReference: &ref},
		{Type: "excerpt", Excerpt: &ports.ChatExcerptContext{Reference: ports.ChatExcerptReference{Order: 1, Text: "QUEUE_EXCERPT_MARKER"}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	provider.failWith(ports.ErrChatTurnNotSteerable)
	if _, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); !errors.Is(err, chatsvc.ErrTurnNotSteerable) {
		t.Fatalf("refusal: %v", err)
	}
	next, err := h.st.NextQueuedTurn(ctx, h.ctrl.ConversationID())
	if err != nil || next.TurnID != queued.ID {
		t.Fatalf("lost queued capture: %+v %v", next, err)
	}
	var durable []ports.ChatContent
	if err := json.Unmarshal([]byte(next.DeliveryContentJSON), &durable); err != nil || len(durable) != 2 || durable[0].FileReference.ID != ref.ID {
		t.Fatalf("lost provenance: %+v %v", durable, err)
	}
	provider.failWith(nil)
	if _, err = h.svc.PromoteQueuedTurn(ctx, testSession, queued.ID); err != nil {
		t.Fatal(err)
	}
	calls := provider.steers()
	last := calls[len(calls)-1].msg
	if len(last.Content) != 0 || strings.Count(last.Text, "QUEUE_CAPTURE_MARKER") != 1 || strings.Count(last.Text, "QUEUE_EXCERPT_MARKER") != 1 || strings.Index(last.Text, "QUEUE_EXCERPT_MARKER") > strings.Index(last.Text, "QUEUE_CAPTURE_MARKER") {
		t.Fatalf("provider prompt: %+v", last)
	}
}
