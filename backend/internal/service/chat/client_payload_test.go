package chat

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
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
