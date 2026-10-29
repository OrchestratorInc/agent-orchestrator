package chat

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestFileCaptureNativeHistoryMatchesOpaqueIDs(t *testing.T) {
	for _, continuation := range []bool{false, true} {
		for _, text := range []string{"question", ""} {
			ref := testFileReference()
			ref.Order = 2
			content := []ports.ChatContent{
				{Type: "file_reference", FileReference: &ref},
				{Type: "excerpt", Excerpt: &ports.ChatExcerptContext{Reference: ports.ChatExcerptReference{Text: "EARLIER_EXCERPT", Order: 1}}},
			}
			if continuation {
				content = append(content, ports.ChatContent{Type: "text", Internal: true, Text: "CONTINUATION_CONTEXT"})
			}
			raw, _ := json.Marshal(content)
			message := domain.ConversationMessage{ID: "user", TurnID: "ao-turn", Role: domain.MessageRoleUser, Text: text, Continuation: continuation, DeliveryContentJSON: string(raw)}
			before := message.DeliveryContentJSON
			dispatched := excerptDeliveryMessage(ports.ChatUserMessage{Text: text, Content: content}).Text
			index := indexNativeHistoryTurns([]domain.ConversationTurn{{ID: "ao-turn", ProviderTurnID: "old-provider-id", State: domain.TurnStateCompleted}}, []domain.ConversationMessage{message}, nil)
			mapped, _ := index.mapReplay([]ports.ChatEvent{{Kind: ports.ChatEventUserMessageCompleted, ProviderTurnID: "different-provider-id", Text: dispatched}})
			if mapped["different-provider-id"] == nil || mapped["different-provider-id"].providerTurnID != "old-provider-id" {
				t.Fatalf("capture did not reconcile: continuation=%v text=%q", continuation, text)
			}
			if strings.Count(nativeHistoryMessageText(message), "await retry();") != 1 || message.DeliveryContentJSON != before {
				t.Fatal("native matching duplicated or mutated a capture")
			}
		}
	}
}

func TestNativeHistoryCaptureKeepsPlainAndMalformedFallbacks(t *testing.T) {
	for _, raw := range []string{"", "{", `[{"type":"image","data":"ignored"}]`} {
		message := domain.ConversationMessage{Role: domain.MessageRoleUser, Text: "plain", DeliveryContentJSON: raw}
		if nativeHistoryMessageText(message) != "plain" {
			t.Fatalf("changed plain history for %q", raw)
		}
	}
}
