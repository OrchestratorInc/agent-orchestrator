package chat

import (
	"context"
	"fmt"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const MaxAdditionalInstructions = 16000

// additionalInstructionsStore owns session-wide text independently of provider settings.
type additionalInstructionsStore interface {
	GetSessionAdditionalInstructions(context.Context, domain.SessionID) (string, error)
	SetSessionAdditionalInstructions(context.Context, domain.SessionID, string) error
}

// AdditionalInstructions returns durable policy even when the provider is stopped.
func (s *Service) AdditionalInstructions(ctx context.Context, id domain.SessionID) (string, error) {
	store, ok := s.store.(additionalInstructionsStore)
	if !ok {
		return "", fmt.Errorf("additional instructions storage unavailable")
	}
	return store.GetSessionAdditionalInstructions(ctx, id)
}

// SetAdditionalInstructions changes only subsequent provider deliveries.
func (s *Service) SetAdditionalInstructions(ctx context.Context, id domain.SessionID, text string) error {
	if !utf8.ValidString(text) || utf8.RuneCountInString(text) > MaxAdditionalInstructions {
		return fmt.Errorf("additional instructions exceed %d characters or contain invalid Unicode", MaxAdditionalInstructions)
	}
	store, ok := s.store.(additionalInstructionsStore)
	if !ok {
		return fmt.Errorf("additional instructions storage unavailable")
	}
	return store.SetSessionAdditionalInstructions(ctx, id, text)
}

// withAdditionalInstructions runs only at provider dispatch, never when persisting a queue entry.
func (c *Controller) withAdditionalInstructions(ctx context.Context, msg ports.ChatUserMessage) (ports.ChatUserMessage, error) {
	store, ok := c.store.(additionalInstructionsStore)
	if !ok {
		return msg, nil
	}
	text, err := store.GetSessionAdditionalInstructions(ctx, c.sessionID)
	if err != nil {
		return msg, err
	}
	msg.Text = appendAdditionalInstructions(msg.Text, text)
	return msg, nil
}

// appendAdditionalInstructions is shared by main dispatch and frozen side-chat delivery.
func appendAdditionalInstructions(message, instructions string) string {
	if instructions == "" {
		return message
	}
	return message + "\n\n<ao-user-additional-instructions>\n" + instructions + "\n</ao-user-additional-instructions>"
}
