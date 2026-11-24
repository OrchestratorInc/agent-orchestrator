package chat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// clientPayloadHash binds a client message ID to the request as received,
// before AO may append worker reports or edit a queued message.
func clientPayloadHash(msg ports.ChatUserMessage) (string, error) {
	if msg.ClientMessageID == "" {
		return "", nil
	}
	content := clientPayloadContent(msg.Content)
	if len(content) == 0 {
		content = nil
	}
	payload, err := json.Marshal(struct {
		Excerpts        []ports.ChatExcerptReference `json:",omitempty"`
		Text            string
		Content         []ports.ChatContent
		Origin          domain.MessageOrigin
		SenderSessionID string `json:",omitempty"`
		AuthoredByUser  bool
		Continuation    bool `json:",omitempty"`
		Settings        ports.ChatTurnSettings
	}{msg.Excerpts, msg.Text, content, normalizeOrigin(msg.Origin), msg.SenderSessionID, msg.AuthoredByUser, msg.Continuation, msg.Settings})
	if err != nil {
		return "", fmt.Errorf("encode client message payload: %w", err)
	}
	return fmt.Sprintf("%x", sha256.Sum256(payload)), nil
}

// Prompt formatting is server-owned, so changing it must not change the identity
// of the client request. Copy only when normalizing, preserving durable content.
func clientPayloadContent(content []ports.ChatContent) []ports.ChatContent {
	for i, block := range content {
		if block.Type == "file_reference" && block.Text == FileReferenceQuotedPath {
			content = append([]ports.ChatContent(nil), content...)
			for j := i; j < len(content); j++ {
				if content[j].Type == "file_reference" && content[j].Text == FileReferenceQuotedPath {
					content[j].Text = ""
				}
			}
			break
		}
	}
	return content
}

func legacyMessageMatches(existing domain.ConversationMessage, msg ports.ChatUserMessage) bool {
	if existing.Text != msg.Text || existing.Origin != normalizeOrigin(msg.Origin) || existing.Continuation != msg.Continuation {
		return false
	}
	if len(msg.Content) == 0 {
		return existing.DeliveryContentJSON == ""
	}
	content, err := json.Marshal(clientPayloadContent(msg.Content))
	if err != nil {
		return false
	}
	if existing.DeliveryContentJSON == string(content) {
		return true
	}
	if !strings.Contains(existing.DeliveryContentJSON, `"text":"`+FileReferenceQuotedPath+`"`) {
		return false
	}
	var stored []ports.ChatContent
	if json.Unmarshal([]byte(existing.DeliveryContentJSON), &stored) != nil {
		return false
	}
	canonical, err := json.Marshal(clientPayloadContent(stored))
	return err == nil && bytes.Equal(canonical, content)
}

// sentMessageReceipt validates a retry without starting or contacting its provider.
func sentMessageReceipt(ctx context.Context, st Store, conversationID string, msg ports.ChatUserMessage, recoverOnly bool) (domain.ConversationTurn, bool, error) {
	if existing, found, err := st.ConversationMessageByClientID(
		ctx, conversationID, msg.ClientMessageID,
	); err != nil {
		return domain.ConversationTurn{}, false, fmt.Errorf("recover sent message: %w", err)
	} else if found {
		if !recoverOnly {
			hash, err := clientPayloadHash(msg)
			if err != nil {
				return domain.ConversationTurn{}, false, err
			}
			if existing.ClientPayloadHash != "" {
				if existing.ClientPayloadHash != hash {
					return domain.ConversationTurn{}, false, domain.ErrClientMessageConflict
				}
			} else if !legacyMessageMatches(existing, msg) {
				return domain.ConversationTurn{}, false, domain.ErrClientMessageConflict
			}
		}
		turn, err := st.TurnByID(ctx, existing.TurnID)
		if err != nil {
			return domain.ConversationTurn{}, false, fmt.Errorf("recover sent turn: %w", err)
		}
		return turn, true, nil
	}

	return domain.ConversationTurn{}, false, nil
}

func (s *Service) sentMessageRetry(ctx context.Context, id domain.SessionID, msg ports.ChatUserMessage) (bool, error) {
	if msg.ClientMessageID == "" {
		return false, nil
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return false, fmt.Errorf("recover sent conversation: %w", err)
	}
	_, found, err := sentMessageReceipt(ctx, s.store, conversation.ID, msg, false)
	return found, err
}
