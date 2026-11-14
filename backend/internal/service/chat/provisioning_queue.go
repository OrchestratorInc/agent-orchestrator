package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var ErrNotProvisioning = domain.ErrSessionNotProvisioning

func (s *Service) QueueUserMessage(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
) (domain.ConversationTurn, error) {
	record, err := s.requireChatSession(ctx, id)
	if err != nil {
		return domain.ConversationTurn{}, err
	}
	return s.queueWithoutController(ctx, record, msg, false)
}

func (s *Service) queueWithoutController(
	ctx context.Context,
	record domain.SessionRecord,
	msg ports.ChatUserMessage,
	behindAccountMove bool,
) (domain.ConversationTurn, error) {
	if msg.ClientPayloadHash == "" {
		var err error
		msg.ClientPayloadHash, err = clientPayloadHash(msg)
		if err != nil {
			return domain.ConversationTurn{}, err
		}
	}
	if !behindAccountMove && !record.ProvisionState.IsProvisioning() && record.HibernatedAt == nil {
		return domain.ConversationTurn{}, ErrNotProvisioning
	}
	conversation, err := s.ensureConversation(ctx, record)
	if err != nil {
		return domain.ConversationTurn{}, err
	}

	msg, err = prepareContinuation(ctx, s.store, s.reader, conversation.ID, msg)
	if err != nil {
		return domain.ConversationTurn{}, err
	}

	now := s.now()
	turnID := s.newID()
	deliveryContent := ""
	if len(msg.Content) > 0 {
		encoded, marshalErr := json.Marshal(msg.Content)
		if marshalErr != nil {
			return domain.ConversationTurn{}, fmt.Errorf("encode chat delivery content: %w", marshalErr)
		}
		deliveryContent = string(encoded)
	}
	if len(msg.Excerpts) > 0 {
		if err := validateExcerptReferences(msg.Excerpts, conversation.ID); err != nil {
			return domain.ConversationTurn{}, err
		}
		queuedContent := append([]ports.ChatContent(nil), msg.Content...)
		for _, excerpt := range msg.Excerpts {
			queuedContent = append(queuedContent, ports.ChatContent{Type: "excerpt", Excerpt: &ports.ChatExcerptContext{
				Reference: excerpt, SelectedText: strings.TrimSpace(excerpt.Text),
			}})
		}
		encoded, marshalErr := json.Marshal(queuedContent)
		if marshalErr != nil {
			return domain.ConversationTurn{}, fmt.Errorf("encode queued excerpts: %w", marshalErr)
		}
		deliveryContent = string(encoded)
	}
	message := domain.ConversationMessage{
		ID:                  s.newID(),
		Text:                msg.Text,
		Origin:              normalizeOrigin(msg.Origin),
		ClientMessageID:     msg.ClientMessageID,
		ClientPayloadHash:   msg.ClientPayloadHash,
		DeliveryContentJSON: deliveryContent,
		SenderSessionID:     msg.SenderSessionID,
		SenderProjectID:     msg.SenderProjectID,
		SenderDisplayName:   msg.SenderDisplayName,
		AuthoredByUser:      msg.AuthoredByUser,
		SpawnOpeningPrompt:  msg.SpawnOpeningPrompt,
		Continuation:        msg.Continuation,
		InteractionAt:       msg.InteractionAt,
	}
	created, err := s.store.AppendUserMessage(ctx, conversation.ID, record.ID, "", message, turnID, now)
	if errors.Is(err, domain.ErrConversationRebound) {
		conversation, err = s.ensureConversation(ctx, record)
		if err == nil {
			err = validateExcerptReferences(msg.Excerpts, conversation.ID)
		}
		if err == nil {
			created, err = s.store.AppendUserMessage(ctx, conversation.ID, record.ID, "", message, turnID, now)
		}
	}
	if err != nil {
		return domain.ConversationTurn{}, fmt.Errorf("queue message for %s: %w", record.ID, err)
	}
	if !created {
		return domain.ConversationTurn{}, nil
	}
	turn := domain.ConversationTurn{
		ID:                 turnID,
		ConversationID:     conversation.ID,
		HandledBySessionID: record.ID,
		State:              domain.TurnStateQueued,
		RequestedAt:        now,
	}
	if controller, controllerErr := s.Controller(record.ID); controllerErr == nil && controller.State() != ports.ChatControllerStopped {
		_ = controller.drainOne(ctx)
	}
	return turn, nil
}

func (s *Service) ensureConversation(
	ctx context.Context,
	record domain.SessionRecord,
) (domain.ConversationRecord, error) {
	conversation, err := s.store.ConversationForSession(ctx, record.ID)
	if err == nil {
		return conversation, nil
	}
	if !errors.Is(err, domain.ErrNoConversation) {
		return domain.ConversationRecord{}, fmt.Errorf("read conversation for %s: %w", record.ID, err)
	}
	conversation, err = s.store.CreateConversation(
		ctx, s.newID(), domain.ConversationScopeSession, record.ProjectID, record.ID, s.now())
	if err != nil {
		return domain.ConversationRecord{}, fmt.Errorf("open conversation for %s: %w", record.ID, err)
	}
	return conversation, nil
}

func (s *Service) DrainQueued(ctx context.Context, id domain.SessionID) error {
	controller, err := s.Controller(id)
	if err != nil {
		return err
	}
	return controller.drainOne(ctx)
}
