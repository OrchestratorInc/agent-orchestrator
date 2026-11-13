package chat_test

import (
	"context"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

func TestProviderInputPreservesAutomationAuthorship(t *testing.T) {
	for _, tc := range []struct {
		name     string
		origin   domain.MessageOrigin
		authored bool
		want     string
	}{
		{"automation", domain.MessageOriginAutomation, false, "The user did not write it."},
		{"relayed user", domain.MessageOriginAutomation, true, "The user wrote this message."},
		{"direct user", domain.MessageOriginHuman, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			turn, err := h.svc.Send(context.Background(), testSession, ports.ChatUserMessage{Text: "Check the PR", Origin: tc.origin, AuthoredByUser: tc.authored})
			if err != nil {
				t.Fatal(err)
			}
			got := h.conv.sentTexts()[0]
			if tc.want == "" {
				if got != "Check the PR" {
					t.Fatalf("direct input changed: %q", got)
				}
			} else if !strings.Contains(got, tc.want) || !strings.HasSuffix(got, "\n\nCheck the PR") {
				t.Fatalf("authorship missing from provider input: %q", got)
			}
			stored, err := h.st.TurnByID(context.Background(), turn.ID)
			if err != nil || stored.ProviderInputText != got {
				t.Fatalf("stored input differs: %+v %v", stored, err)
			}
			snapshot, err := h.st.LoadConversationSnapshot(context.Background(), h.ctrl.ConversationID())
			if err != nil {
				t.Fatal(err)
			}
			for _, message := range snapshot.Messages {
				if message.Role == domain.MessageRoleUser && (message.Text != "Check the PR" || message.Origin != tc.origin || message.AuthoredByUser != tc.authored) {
					t.Fatalf("transcript attribution changed: %+v", message)
				}
			}
		})
	}
}

func TestQueuedAutomationPreservesAuthorship(t *testing.T) {
	for _, batched := range []bool{false, true} {
		t.Run(map[bool]string{false: "single", true: "batch"}[batched], func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "active", Origin: domain.MessageOriginHuman}); err != nil {
				t.Fatal(err)
			}
			for i, authored := range []bool{true, false} {
				if i == 1 && !batched {
					break
				}
				if _, err := h.svc.Send(ctx, testSession, ports.ChatUserMessage{Text: "Check the PR", Origin: domain.MessageOriginAutomation, AuthoredByUser: authored}); err != nil {
					t.Fatal(err)
				}
			}
			h.conv.emit(ports.ChatEvent{Kind: ports.ChatEventTurnCompleted, ProviderTurnID: "provider-turn-1", TurnState: domain.TurnStateCompleted})
			h.awaitSnapshot(t, func(store.ConversationSnapshot) bool { return len(h.conv.sentTexts()) == 2 })
			text := h.conv.sentTexts()[1]
			if !batched {
				if !strings.Contains(text, "The user wrote this message.") {
					t.Fatalf("queued authorship lost: %q", text)
				}
				return
			}
			items := parseDeliveredBatch(t, text)
			if len(items) != 2 || !items[0].AuthoredByUser || items[1].AuthoredByUser {
				t.Fatalf("batch authorship lost: %+v", items)
			}
		})
	}
}
