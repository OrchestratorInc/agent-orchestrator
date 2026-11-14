package chat

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

var ErrQueuedTurnTextRequired = errors.New("queued message text is required")

var ErrQueuedContentInvalid = errors.New("queued message attachments are invalid")

var ErrQueuedEditConflict = errors.New("queued message changed during editing")

type QueuedMessageEdit struct {
	ClientMessageID  string
	Text             string
	Content          []ports.ChatContent
	RetainedContent  *[]int
	ExpectedRevision *int64
}

var ErrInvalidQueuedTurnOrder = errors.New("invalid queued turn order")

func (s *Service) CancelQueuedTurn(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.CancelQueuedTurn(ctx, turnID)
}

func (s *Service) EditQueuedTurn(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
	edit QueuedMessageEdit,
) error {
	delivery, err := queuedEditDelivery(turnID, edit)
	if err != nil {
		return err
	}
	if delivery.ClientMessageID != "" {
		conversation, err := s.store.ConversationForSession(ctx, id)
		if err != nil {
			return err
		}
		hash, found, err := s.store.QueuedEditDelivery(ctx, conversation.ID, delivery.ClientMessageID)
		if err != nil {
			return err
		}
		if found {
			if hash != delivery.RequestHash {
				return store.ErrQueuedEditDeliveryConflict
			}
			return nil
		}
	}
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.EditQueuedTurn(ctx, turnID, edit)
}

func (s *Service) ReorderQueuedTurns(
	ctx context.Context,
	id domain.SessionID,
	turnIDs []string,
) error {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.ReorderQueuedTurns(ctx, turnIDs)
}

func (c *Controller) CancelQueuedTurn(ctx context.Context, turnID string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return ErrControllerHandoff
	}
	return c.store.CancelQueuedTurnByID(ctx, c.conversation.ID, turnID, c.now())
}

func (c *Controller) EditQueuedTurn(ctx context.Context, turnID string, edit QueuedMessageEdit) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	delivery, err := queuedEditDelivery(turnID, edit)
	if err != nil {
		return err
	}
	if delivery.ClientMessageID != "" {
		hash, found, err := c.store.QueuedEditDelivery(ctx, c.conversation.ID, delivery.ClientMessageID)
		if err != nil {
			return err
		}
		if found {
			if hash != delivery.RequestHash {
				return store.ErrQueuedEditDeliveryConflict
			}
			return nil
		}
	}
	if c.handoffActive() {
		return ErrControllerHandoff
	}
	message, err := c.store.QueuedTurnMessage(ctx, c.conversation.ID, turnID)
	if err != nil {
		return err
	}
	if edit.ExpectedRevision != nil && *edit.ExpectedRevision != message.Revision {
		return ErrQueuedEditConflict
	}
	var content []ports.ChatContent
	if message.DeliveryContentJSON != "" {
		if err := json.Unmarshal([]byte(message.DeliveryContentJSON), &content); err != nil {
			return ErrQueuedContentInvalid
		}
	}
	if edit.RetainedContent != nil {
		if edit.ExpectedRevision == nil {
			return ErrQueuedContentInvalid
		}
		retained := *edit.RetainedContent
		selected := make([]ports.ChatContent, 0, len(content))
		index, next := 0, 0
		for _, block := range content {
			if block.Type == "text" || ports.IsInternalReplayContent(block) {
				selected = append(selected, block)
				continue
			}
			if next < len(retained) && retained[next] == index {
				selected = append(selected, block)
				next++
			}
			index++
		}
		if next != len(retained) {
			return ErrQueuedContentInvalid
		}
		content = selected
	}
	content = append(content, edit.Content...)
	images, imageBytes := 0, 0
	for _, block := range content {
		if block.Type != "image" {
			continue
		}
		images++
		data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(block.Data))
		if err != nil || len(data) > 10<<20 {
			return ErrQueuedContentInvalid
		}
		imageBytes += len(data)
	}
	if images > 8 || imageBytes > 25*1024*1024 {
		return ErrQueuedContentInvalid
	}
	if strings.TrimSpace(edit.Text) == "" && len(content) == 0 {
		return ErrQueuedTurnTextRequired
	}
	encoded := ""
	if len(content) > 0 {
		data, err := json.Marshal(content)
		if err != nil {
			return err
		}
		encoded = string(data)
	}
	return c.store.UpdateQueuedTurnMessage(ctx, c.conversation.ID, turnID,
		edit.Text, encoded, message.Revision, c.now(), delivery)
}

func queuedEditDelivery(turnID string, edit QueuedMessageEdit) (domain.ConversationQueuedEditDelivery, error) {
	if edit.ClientMessageID == "" {
		return domain.ConversationQueuedEditDelivery{}, nil
	}
	data, err := json.Marshal(struct {
		TurnID string
		Edit   QueuedMessageEdit
	}{turnID, edit})
	if err != nil {
		return domain.ConversationQueuedEditDelivery{}, err
	}
	return domain.ConversationQueuedEditDelivery{ClientMessageID: edit.ClientMessageID, RequestHash: fmt.Sprintf("%x", sha256.Sum256(data))}, nil
}

func (c *Controller) ReorderQueuedTurns(ctx context.Context, turnIDs []string) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return ErrControllerHandoff
	}
	if err := c.store.ReorderQueuedTurns(ctx, c.conversation.ID, turnIDs); err != nil {
		if errors.Is(err, store.ErrInvalidQueuedTurnOrder) {
			return ErrInvalidQueuedTurnOrder
		}
		return err
	}
	return nil
}
