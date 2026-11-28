package chat

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestClientPayloadHashPreservesLegacyEmptySenderHash(t *testing.T) {
	msg := ports.ChatUserMessage{ClientMessageID: "c1", Text: "hi"}
	got, err := clientPayloadHash(msg)
	if err != nil {
		t.Fatalf("clientPayloadHash: %v", err)
	}

	legacyPayload, err := json.Marshal(struct {
		Text           string
		Content        []ports.ChatContent
		Origin         domain.MessageOrigin
		AuthoredByUser bool
		Settings       ports.ChatTurnSettings
	}{msg.Text, nil, normalizeOrigin(msg.Origin), msg.AuthoredByUser, msg.Settings})
	if err != nil {
		t.Fatalf("marshal legacy payload: %v", err)
	}
	want := fmt.Sprintf("%x", sha256.Sum256(legacyPayload))
	if got != want {
		t.Fatalf("empty sender hash = %s, want legacy %s", got, want)
	}

	withSender, err := clientPayloadHash(ports.ChatUserMessage{
		ClientMessageID: "c1", Text: "hi", SenderSessionID: "worker-1",
	})
	if err != nil {
		t.Fatalf("clientPayloadHash with sender: %v", err)
	}
	if withSender == got {
		t.Fatal("sender identity did not change the payload hash")
	}
}

func TestClientPayloadHashDistinguishesContinuation(t *testing.T) {
	ordinary := ports.ChatUserMessage{ClientMessageID: "c1", Text: "Continue from where you stopped."}
	continued := ordinary
	continued.Continuation = true
	first, err := clientPayloadHash(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	second, err := clientPayloadHash(continued)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("continuation must change the client payload hash")
	}
	stored := domain.ConversationMessage{Text: ordinary.Text, Origin: domain.MessageOriginHuman}
	if legacyMessageMatches(stored, continued) {
		t.Fatal("ordinary message must not match a continuation")
	}
	stored.Continuation = true
	if !legacyMessageMatches(stored, continued) {
		t.Fatal("matching continuation must remain idempotent")
	}
}

func TestClientPayloadFileFormattingPreservesLegacyRetryIdentity(t *testing.T) {
	ref := testFileReference()
	msg := ports.ChatUserMessage{ClientMessageID: "receipt", Text: "question", Content: []ports.ChatContent{{Type: "file_reference", FileReference: &ref}}}
	legacy, err := clientPayloadHash(msg)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(msg.Content)
	stored := domain.ConversationMessage{Text: msg.Text, Origin: domain.MessageOriginHuman, DeliveryContentJSON: string(raw)}
	msg.Content[0].Text = FileReferenceQuotedPath
	current, err := clientPayloadHash(msg)
	if err != nil {
		t.Fatal(err)
	}
	if current != legacy || !legacyMessageMatches(stored, msg) {
		t.Fatal("prompt format changed a frozen client receipt")
	}
	if msg.Content[0].Text != FileReferenceQuotedPath {
		t.Fatal("hashing mutated the saved dispatch format")
	}
	ref.Lines[0].Text = "different code"
	changed, err := clientPayloadHash(msg)
	if err != nil {
		t.Fatal(err)
	}
	if changed == legacy || legacyMessageMatches(stored, msg) {
		t.Fatal("changed captured code reused a receipt")
	}
}

// A request accepted before images carried their staged path, or sent with the
// bytes inline, must still match its retry: only the path differs.
func TestStagedImagePathDoesNotChangeReceipts(t *testing.T) {
	inline := []ports.ChatContent{{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png"}}
	staged := []ports.ChatContent{{Type: "image", Data: "aW1hZ2U=", MIMEType: "image/png", URI: ".ao/attachments/attachment-a.png"}}

	before, err := clientPayloadHash(ports.ChatUserMessage{ClientMessageID: "c1", Text: "look", Content: inline})
	if err != nil {
		t.Fatal(err)
	}
	after, err := clientPayloadHash(ports.ChatUserMessage{ClientMessageID: "c1", Text: "look", Content: staged})
	if err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatal("send receipt changed with the staged path")
	}

	oldEdit, err := queuedEditDelivery("turn-1", QueuedMessageEdit{ClientMessageID: "e1", Text: "look", Content: inline})
	if err != nil {
		t.Fatal(err)
	}
	newEdit, err := queuedEditDelivery("turn-1", QueuedMessageEdit{ClientMessageID: "e1", Text: "look", Content: staged})
	if err != nil {
		t.Fatal(err)
	}
	if oldEdit.RequestHash != newEdit.RequestHash {
		t.Fatal("queued edit receipt changed with the staged path")
	}

	saved, err := encodeSteerDeliveryRequest(ports.ChatUserMessage{Text: "look", Content: inline})
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := encodeSteerDeliveryRequest(ports.ChatUserMessage{Text: "look", Content: staged})
	if err != nil {
		t.Fatal(err)
	}
	accepted := domain.ConversationSteerDelivery{RequestJSON: saved, State: domain.ConversationSteerAccepted}
	if _, err := replaySteerDelivery(accepted, fresh); errors.Is(err, ErrSteerIdempotencyConflict) {
		t.Fatal("steer receipt conflicts with its retry over the staged path")
	}
	other, err := encodeSteerDeliveryRequest(ports.ChatUserMessage{Text: "look", Content: []ports.ChatContent{
		{Type: "image", Data: "b3RoZXI=", MIMEType: "image/png", URI: ".ao/attachments/attachment-a.png"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := replaySteerDelivery(accepted, other); !errors.Is(err, ErrSteerIdempotencyConflict) {
		t.Fatalf("a different image replayed as the same steer: %v", err)
	}

	// The saved request keeps the path, which commits the draft's lease, and the
	// caller's content is left as it was.
	if !strings.Contains(fresh, "attachment-a.png") || staged[0].URI == "" {
		t.Fatal("the staged path was dropped from the request itself")
	}
}
