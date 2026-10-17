package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type batchInputError struct {
	turnID string
	cause  error
}

func (e batchInputError) Error() string { return fmt.Sprintf("queued turn %s: %v", e.turnID, e.cause) }
func (e batchInputError) Unwrap() error { return e.cause }

func invalidBatchTurn(err error) (string, bool) {
	var target batchInputError
	if errors.As(err, &target) {
		return target.turnID, true
	}
	return "", false
}

type batchInputItem struct {
	Kind              string               `json:"kind"`
	Text              string               `json:"text"`
	Origin            domain.MessageOrigin `json:"origin"`
	SenderSessionID   string               `json:"senderSessionId,omitempty"`
	SenderProjectID   string               `json:"senderProjectId,omitempty"`
	SenderDisplayName string               `json:"senderDisplayName,omitempty"`
	ContentStart      int                  `json:"contentStart,omitempty"`
	ContentCount      int                  `json:"contentCount,omitempty"`
}

// queuedBatchInput keeps message boundaries and source identity in the provider
// input while the persisted transcript retains one message per sender. AO sets
// no batch size or text limit; a provider refusal is surfaced in full.
func (c *Controller) queuedBatchInput(ctx context.Context, batch []domain.QueuedTurn, steer *ports.ChatUserMessage) (ports.ChatUserMessage, error) {
	items := make([]batchInputItem, 0, len(batch)+1)
	combined := ports.ChatUserMessage{}
	if len(batch) > 0 {
		combined.ClientMessageID = batch[0].ClientMessageID
		combined.Origin = batch[0].Origin
	}
	for _, queued := range batch {
		msg := ports.ChatUserMessage{
			Text: queued.Text, Origin: queued.Origin, ClientMessageID: queued.ClientMessageID,
			SenderSessionID: queued.SenderSessionID, SenderProjectID: queued.SenderProjectID,
			SenderDisplayName: queued.SenderDisplayName,
		}
		if queued.DeliveryContentJSON != "" {
			if err := json.Unmarshal([]byte(queued.DeliveryContentJSON), &msg.Content); err != nil {
				return ports.ChatUserMessage{}, batchInputError{queued.TurnID, err}
			}
		}
		for _, content := range msg.Content {
			if content.Type == "excerpt" && content.Excerpt != nil {
				msg.Excerpts = append(msg.Excerpts, content.Excerpt.Reference)
			}
		}
		if len(msg.Excerpts) > 0 {
			filtered := msg.Content[:0]
			for _, content := range msg.Content {
				if content.Type != "excerpt" {
					filtered = append(filtered, content)
				}
			}
			msg.Content = filtered
			if err := hydrateExcerptReferences(ctx, c, &msg); err != nil {
				return ports.ChatUserMessage{}, batchInputError{queued.TurnID, err}
			}
		}
		items = append(items, batchInputItem{
			Kind: "message", Text: msg.Text, Origin: msg.Origin,
			SenderSessionID: msg.SenderSessionID, SenderProjectID: msg.SenderProjectID,
			SenderDisplayName: msg.SenderDisplayName,
			ContentStart:      len(combined.Content), ContentCount: len(msg.Content),
		})
		combined.Content = append(combined.Content, msg.Content...)
		if len(batch) == 1 && steer == nil {
			return msg, nil
		}
	}
	if steer != nil {
		combined.ClientMessageID = steer.ClientMessageID
		combined.Origin = steer.Origin
		combined.Settings = steer.Settings
		items = append(items, batchInputItem{
			Kind: "steer", Text: steer.Text, Origin: steer.Origin,
			SenderSessionID: steer.SenderSessionID, SenderProjectID: steer.SenderProjectID,
			SenderDisplayName: steer.SenderDisplayName,
			ContentStart:      len(combined.Content), ContentCount: len(steer.Content),
		})
		combined.Content = append(combined.Content, steer.Content...)
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return ports.ChatUserMessage{}, fmt.Errorf("encode pending messages: %w", err)
	}
	combined.Text = "AO pending messages in enqueue order. Each JSON item is one complete message; kind steer identifies active-turn guidance. Content blocks follow the same item order, using contentStart and contentCount indices.\n" + string(encoded)
	return combined, nil
}

func queuedBatchIDs(batch []domain.QueuedTurn) []string {
	ids := make([]string, 0, len(batch))
	for _, queued := range batch {
		ids = append(ids, queued.TurnID)
	}
	return ids
}
