package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	ErrSteerUnsupported         = errors.New("chat driver cannot steer a running turn")
	ErrTurnNotSteerable         = errors.New("the running turn cannot take guidance")
	ErrSteerTextRequired        = errors.New("steer text is required")
	ErrTurnNotQueued            = errors.New("turn is not queued")
	ErrPromotionUncertain       = errors.New("queued turn promotion delivery is uncertain")
	ErrSteerDeliveryUncertain   = errors.New("steer delivery is uncertain")
	ErrSteerIdempotencyConflict = errors.New("steer idempotency handle belongs to different guidance")
	ErrSteerContentUnsupported  = errors.New("steer content is unsupported")
)

type SteerResult struct {
	ProviderTurnID string
	ActivityID     string
	Replayed       bool
}

type SteerOrSendResult struct {
	Steered   bool
	Duplicate bool
	Steer     SteerResult
	Turn      domain.ConversationTurn
}

func (s *Service) resolveSteerSender(ctx context.Context, msg ports.ChatUserMessage) ports.ChatUserMessage {
	if msg.SenderSessionID == "" || s.sessions == nil {
		return msg
	}
	record, found, err := s.sessions.GetSession(ctx, domain.SessionID(msg.SenderSessionID))
	if err != nil || !found {
		return msg
	}
	msg.SenderProjectID = string(record.ProjectID)
	msg.SenderDisplayName = strings.TrimSpace(record.DisplayName)
	return msg
}

type PromoteQueuedTurnResult struct {
	SourceTurnID   string
	ProviderTurnID string
	ActivityID     string
}

const maxSteerSummaryRunes = 120

func (s *Service) Steer(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
) (SteerResult, error) {
	if strings.TrimSpace(msg.Text) == "" {
		return SteerResult{}, ErrSteerTextRequired
	}
	msg = s.resolveSteerSender(ctx, msg)
	if msg.SenderSessionID != "" {
		msg.Origin = domain.MessageOriginAutomation
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return SteerResult{}, err
	}
	defer release()
	return controller.Steer(ctx, msg)
}

func (s *Service) RecoverSteer(ctx context.Context, id domain.SessionID, clientMessageID string) (SteerResult, error) {
	if clientMessageID == "" {
		return SteerResult{}, ErrSteerDeliveryUncertain
	}
	conversation, err := s.store.ConversationForSession(ctx, id)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: load conversation: %w", ErrSteerDeliveryUncertain, err)
	}
	delivery, found, err := s.store.SteerDelivery(ctx, conversation.ID, clientMessageID)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: load receipt: %w", ErrSteerDeliveryUncertain, err)
	}
	if !found {
		return SteerResult{}, ErrSteerDeliveryUncertain
	}
	return replaySteerDelivery(delivery, delivery.RequestJSON)
}

func (s *Service) SteerOrSend(
	ctx context.Context,
	id domain.SessionID,
	msg ports.ChatUserMessage,
	recoverOnly bool,
) (SteerOrSendResult, error) {
	if strings.TrimSpace(msg.Text) == "" && !recoverOnly {
		return SteerOrSendResult{}, ErrSteerTextRequired
	}
	if msg.ClientMessageID == "" {
		return SteerOrSendResult{}, ErrSteerDeliveryUncertain
	}
	msg = s.resolveSteerSender(ctx, msg)
	if msg.SenderSessionID != "" {
		msg.Origin = domain.MessageOriginAutomation
	}
	controller, release, err := s.workingController(ctx, id)
	if err != nil {
		return SteerOrSendResult{}, err
	}
	defer release()
	return controller.SteerOrSend(ctx, msg, recoverOnly)
}

func (s *Service) PromoteQueuedTurn(
	ctx context.Context,
	id domain.SessionID,
	turnID string,
) (PromoteQueuedTurnResult, error) {
	if _, err := s.requireChatSession(ctx, id); err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	controller, err := s.Controller(id)
	if err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	if _, ok := controller.steerer(); !ok {
		return PromoteQueuedTurnResult{}, ErrSteerUnsupported
	}
	return controller.PromoteQueuedTurn(ctx, turnID)
}

func (c *Controller) steerer() (ports.ChatSteerer, bool) {
	steerer, ok := c.conv.(ports.ChatSteerer)
	return steerer, ok && c.Capabilities().Has(ports.ChatCapabilitySteer)
}

func (c *Controller) PromoteQueuedTurn(
	ctx context.Context,
	turnID string,
) (PromoteQueuedTurnResult, error) {
	steerer, ok := c.steerer()
	if !ok {
		return PromoteQueuedTurnResult{}, ErrSteerUnsupported
	}
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if c.handoffActive() {
		return PromoteQueuedTurnResult{}, ErrControllerHandoff
	}

	source, err := c.store.TurnByID(ctx, turnID)
	if err != nil {
		return PromoteQueuedTurnResult{}, err
	}
	if source.ConversationID != c.conversation.ID {
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", domain.ErrNoConversationTurn, turnID)
	}
	if source.State != domain.TurnStateQueued {
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", ErrTurnNotQueued, turnID)
	}

	target, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		return PromoteQueuedTurnResult{}, ErrNoActiveTurn
	}
	queued, err := c.store.ReserveQueuedTurnForPromotion(ctx, c.conversation.ID, turnID, c.now())
	if err != nil {
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s: %w", ErrTurnNotQueued, turnID, err)
	}
	release := func() {
		if releaseErr := c.store.ReleaseQueuedTurnPromotion(
			context.WithoutCancel(ctx), c.conversation.ID, turnID); releaseErr != nil {
			c.log.Error("failed to release queued turn promotion", "turn", turnID, "error", releaseErr)
		}
	}
	if queued.Origin != domain.MessageOriginHuman && !queued.AuthoredByUser {
		release()
		return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %s", ErrTurnNotQueued, turnID)
	}
	settleUncertain := func(cause error) error {
		settleCtx := context.WithoutCancel(ctx)
		if settleErr := c.store.SettleTurnByID(
			settleCtx, turnID, domain.TurnStateFailed, ErrPromotionUncertain.Error(), c.now()); settleErr != nil {
			c.log.Error("failed to settle uncertain queued turn promotion",
				"turn", turnID, "cause", cause, "error", settleErr)
		}
		return fmt.Errorf("%w: %w", ErrPromotionUncertain, cause)
	}

	var content []ports.ChatContent
	if queued.DeliveryContentJSON != "" {
		if err := json.Unmarshal([]byte(queued.DeliveryContentJSON), &content); err != nil {
			release()
			return PromoteQueuedTurnResult{}, fmt.Errorf("decode queued chat content: %w", err)
		}
	}
	msg := ports.ChatUserMessage{
		Text: queued.Text, Content: content, Origin: queued.Origin,
		AuthoredByUser: queued.AuthoredByUser, ClientMessageID: queued.ClientMessageID,
	}
	ref, err := steerer.Steer(ctx, target, excerptDeliveryMessage(msg))
	if err != nil {
		classified := classify(err)
		switch {
		case errors.Is(err, ports.ErrChatNoSteerableTurn):
			release()
			return PromoteQueuedTurnResult{}, ErrNoActiveTurn
		case errors.Is(err, ports.ErrChatTurnNotSteerable):
			release()
			return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %w", ErrTurnNotSteerable, err)
		case errors.Is(err, ports.ErrChatSteerContentUnsupported):
			release()
			return PromoteQueuedTurnResult{}, fmt.Errorf("%w: %w", ErrSteerContentUnsupported, err)
		case errors.Is(classified, ErrProviderRefused):
			release()
			return PromoteQueuedTurnResult{}, classified
		default:
			return PromoteQueuedTurnResult{}, settleUncertain(
				fmt.Errorf("steer queued turn %s: %w", turnID, err))
		}
	}
	landed := ref.ProviderTurnID
	if landed == "" {
		landed = target
	}
	activityID := c.newID()
	activity, err := makeSteerActivity(activityID, msg, turnID, steerProviderInput{
		Text: msg.Text, Content: msg.Content, SourceTurnIDs: []string{turnID},
	})
	if err != nil {
		return PromoteQueuedTurnResult{}, settleUncertain(err)
	}
	if err := c.store.CompleteQueuedTurnPromotion(
		ctx, c.conversation.ID, turnID, landed, activity, c.now()); err != nil {
		return PromoteQueuedTurnResult{}, settleUncertain(err)
	}
	return PromoteQueuedTurnResult{
		SourceTurnID: turnID, ProviderTurnID: landed, ActivityID: activityID,
	}, nil
}

type durableSteerRefusal struct {
	kind           domain.ConversationSteerRejectionKind
	cause          error
	reserveContext string
	persistContext string
}

func (c *Controller) rejectSteerBeforeDispatch(
	ctx context.Context,
	clientMessageID, requestJSON string,
	refusal durableSteerRefusal,
) (SteerResult, error) {
	if clientMessageID == "" {
		return SteerResult{}, refusal.cause
	}
	delivery, created, err := c.store.ReserveSteerDelivery(
		ctx, c.conversation.ID, clientMessageID, requestJSON, c.now())
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: %s: %w",
			ErrSteerDeliveryUncertain, refusal.reserveContext, err)
	}
	if !created {
		return replaySteerDelivery(delivery, requestJSON)
	}
	if err := c.store.RejectSteerDelivery(
		context.WithoutCancel(ctx), c.conversation.ID, clientMessageID,
		refusal.kind, refusal.cause.Error(), c.now()); err != nil {
		return SteerResult{}, fmt.Errorf("%w: %s: %w",
			ErrSteerDeliveryUncertain, refusal.persistContext, err)
	}
	return SteerResult{}, refusal.cause
}

func (c *Controller) Steer(ctx context.Context, msg ports.ChatUserMessage) (SteerResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	return c.steerLocked(ctx, msg)
}

func (c *Controller) steerLocked(ctx context.Context, msg ports.ChatUserMessage) (SteerResult, error) {

	requestJSON, err := encodeSteerDeliveryRequest(msg)
	if err != nil {
		return SteerResult{}, err
	}
	if msg.ClientMessageID != "" {
		delivery, found, loadErr := c.store.SteerDelivery(
			ctx, c.conversation.ID, msg.ClientMessageID)
		if loadErr != nil {
			return SteerResult{}, fmt.Errorf("%w: load prior result: %w",
				ErrSteerDeliveryUncertain, loadErr)
		}
		if found {
			return replaySteerDelivery(delivery, requestJSON)
		}
	}
	if c.handoffActive() {
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedInterfaceTransition,
				cause:          ErrControllerHandoff,
				reserveContext: "reserve interface-transition refusal",
				persistContext: "persist interface-transition refusal",
			})
	}
	steerer, ok := c.steerer()
	if !ok {
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedUnsupported,
				cause:          ErrSteerUnsupported,
				reserveContext: "reserve unsupported result",
				persistContext: "persist unsupported result",
			})
	}

	turn, ok := c.awaitAcknowledgedTurn(ctx)
	if !ok {
		return c.rejectSteerBeforeDispatch(ctx, msg.ClientMessageID, requestJSON,
			durableSteerRefusal{
				kind:           domain.ConversationSteerRejectedNoActiveTurn,
				cause:          ErrNoActiveTurn,
				reserveContext: "reserve refusal",
				persistContext: "persist no-active-turn refusal",
			})
	}
	if msg.ClientMessageID != "" {
		delivery, created, reserveErr := c.store.ReserveSteerDelivery(
			ctx, c.conversation.ID, msg.ClientMessageID, requestJSON, c.now())
		if reserveErr != nil {
			return SteerResult{}, fmt.Errorf("%w: reserve delivery: %w",
				ErrSteerDeliveryUncertain, reserveErr)
		}
		if !created {
			return replaySteerDelivery(delivery, requestJSON)
		}
	}
	batch, err := c.store.ClaimQueuedBatch(ctx, c.conversation.ID, false, 0, c.now())
	if errors.Is(err, domain.ErrNoQueuedTurn) {
		batch = nil
	} else if err != nil {
		return SteerResult{}, fmt.Errorf("%w: claim pending messages: %w", ErrSteerDeliveryUncertain, err)
	}
	providerMessage := msg
	if len(batch) > 0 {
		providerMessage, err = c.queuedBatchInput(ctx, batch, &msg)
		if err != nil {
			if releaseErr := c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch)); releaseErr != nil {
				return SteerResult{}, errors.Join(err, releaseErr)
			}
			if msg.ClientMessageID != "" {
				if rejectErr := c.store.RejectSteerDelivery(context.WithoutCancel(ctx), c.conversation.ID,
					msg.ClientMessageID, domain.ConversationSteerRejectedContentUnsupported, err.Error(), c.now()); rejectErr != nil {
					return SteerResult{}, fmt.Errorf("%w: reject invalid batch: %w", ErrSteerDeliveryUncertain, rejectErr)
				}
			}
			return SteerResult{}, err
		}
	}

	ref, err := steerer.Steer(ctx, turn, providerMessage)
	if err != nil {
		kind, definitive, refused := classifySteerRejection(err)
		if definitive {
			if len(batch) > 0 {
				if releaseErr := c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch)); releaseErr != nil {
					return SteerResult{}, fmt.Errorf("%w: release refused batch: %w", ErrSteerDeliveryUncertain, releaseErr)
				}
			}
			if msg.ClientMessageID != "" {
				if rejectErr := c.store.RejectSteerDelivery(
					context.WithoutCancel(ctx), c.conversation.ID, msg.ClientMessageID,
					kind, refused.Error(), c.now()); rejectErr != nil {
					return SteerResult{}, fmt.Errorf("%w: persist provider refusal: %w",
						ErrSteerDeliveryUncertain, rejectErr)
				}
			}
			return SteerResult{}, refused
		}
		wrapped := classify(fmt.Errorf("steer turn %s: %w", turn, err))
		if len(batch) > 0 {
			if failErr := c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
				queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now()); failErr != nil {
				c.log.Error("failed to settle uncertain steered batch", "error", failErr)
			}
		}
		if msg.ClientMessageID != "" {
			return SteerResult{}, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, wrapped)
		}
		return SteerResult{}, wrapped
	}

	landed := ref.ProviderTurnID
	if landed == "" {
		landed = turn
	}
	providerInput := steerProviderInput{
		Text: providerMessage.Text, Content: providerMessage.Content,
		SourceTurnIDs: queuedBatchIDs(batch),
	}
	if len(batch) > 0 {
		settleCtx := context.WithoutCancel(ctx)
		target, err := c.store.TurnByProviderID(settleCtx, c.conversation.ID, landed)
		if err == nil && target.ProviderInputText == "" {
			err = c.store.RecordProviderInput(settleCtx, target.ID, providerMessage.Text)
			providerInput.StoredOnTurn = err == nil
		}
		if err != nil {
			if failErr := c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
				queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now()); failErr != nil {
				c.log.Error("failed to settle steered batch with unrecorded provider input", "error", failErr)
			}
			return SteerResult{ProviderTurnID: landed}, fmt.Errorf("%w: record steered batch provider input: %w", ErrSteerDeliveryUncertain, err)
		}
	}
	if msg.ClientMessageID == "" {
		activityID, recordErr := c.recordSteer(ctx, landed, msg, providerInput)
		if recordErr != nil {
			if len(batch) > 0 {
				_ = c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
					queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now())
			}
			return SteerResult{ProviderTurnID: landed}, recordErr
		}
		if len(batch) > 0 {
			if err := c.store.CompleteQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch), landed, activityID, c.now()); err != nil {
				_ = c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
					queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now())
				return SteerResult{ProviderTurnID: landed, ActivityID: activityID}, fmt.Errorf("%w: complete pending messages: %w", ErrSteerDeliveryUncertain, err)
			}
		}
		return SteerResult{ProviderTurnID: landed, ActivityID: activityID}, nil
	}
	activityID := c.newID()
	activity, err := makeSteerActivity(activityID, msg, "", providerInput)
	if err != nil {
		return SteerResult{}, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, err)
	}
	var completeErr error
	if len(batch) == 0 {
		completeErr = c.store.CompleteSteerDelivery(context.WithoutCancel(ctx), c.conversation.ID,
			msg.ClientMessageID, landed, activity, c.now())
	} else {
		completeErr = c.store.CompleteSteerBatchDelivery(context.WithoutCancel(ctx), c.conversation.ID,
			msg.ClientMessageID, landed, activity, queuedBatchIDs(batch), c.now())
	}
	if completeErr != nil {
		if len(batch) > 0 {
			_ = c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
				queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now())
		}
		return SteerResult{}, fmt.Errorf("%w: %w", ErrSteerDeliveryUncertain, completeErr)
	}
	return SteerResult{ProviderTurnID: landed, ActivityID: activityID}, nil
}

func (c *Controller) SteerOrSend(
	ctx context.Context,
	msg ports.ChatUserMessage,
	recoverOnly bool,
) (SteerOrSendResult, error) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	if existing, found, err := c.store.ConversationMessageByClientID(
		ctx, c.conversation.ID, msg.ClientMessageID,
	); err != nil {
		return SteerOrSendResult{}, fmt.Errorf("recover sent message: %w", err)
	} else if found {
		if !recoverOnly {
			hash, err := clientPayloadHash(msg)
			if err != nil {
				return SteerOrSendResult{}, err
			}
			if existing.ClientPayloadHash != "" {
				if existing.ClientPayloadHash != hash {
					return SteerOrSendResult{}, domain.ErrClientMessageConflict
				}
			} else if !legacyMessageMatches(existing, msg) {
				return SteerOrSendResult{}, domain.ErrClientMessageConflict
			}
		}
		turn, err := c.store.TurnByID(ctx, existing.TurnID)
		if err != nil {
			return SteerOrSendResult{}, fmt.Errorf("recover sent turn: %w", err)
		}
		return SteerOrSendResult{Duplicate: true, Turn: turn}, nil
	}

	requestJSON, err := encodeSteerDeliveryRequest(msg)
	if err != nil {
		return SteerOrSendResult{}, err
	}
	if delivery, found, err := c.store.SteerDelivery(
		ctx, c.conversation.ID, msg.ClientMessageID,
	); err != nil {
		return SteerOrSendResult{}, fmt.Errorf("%w: load prior result: %w", ErrSteerDeliveryUncertain, err)
	} else if found {
		replayRequest := requestJSON
		if recoverOnly {
			replayRequest = delivery.RequestJSON
		}
		steered, replayErr := replaySteerDelivery(delivery, replayRequest)
		if replayErr == nil {
			return SteerOrSendResult{Steered: true, Duplicate: true, Steer: steered}, nil
		}
		if !errors.Is(replayErr, ErrNoActiveTurn) || recoverOnly {
			return SteerOrSendResult{}, replayErr
		}
	}
	if recoverOnly {
		return SteerOrSendResult{}, ErrSteerDeliveryUncertain
	}

	if _, active := c.awaitAcknowledgedTurn(ctx); active {
		steered, err := c.steerLocked(ctx, msg)
		if err == nil {
			return SteerOrSendResult{Steered: true, Steer: steered}, nil
		}
		if !errors.Is(err, ErrNoActiveTurn) {
			return SteerOrSendResult{}, err
		}
	}

	batch, batchErr := c.store.ClaimQueuedBatch(ctx, c.conversation.ID, false, 0, c.now())
	if errors.Is(batchErr, domain.ErrNoQueuedTurn) {
		batch = nil
	} else if batchErr != nil {
		return SteerOrSendResult{}, batchErr
	}
	var override *ports.ChatUserMessage
	if len(batch) > 0 {
		combined, composeErr := c.queuedBatchInput(ctx, batch, &msg)
		if composeErr != nil {
			_ = c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch))
			return SteerOrSendResult{}, composeErr
		}
		override = &combined
	}
	turn, err := c.sendLocked(ctx, msg, false, override, queuedBatchIDs(batch))
	if err != nil {
		if len(batch) > 0 {
			if failErr := c.store.FailQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID,
				queuedBatchIDs(batch), ErrSteerDeliveryUncertain.Error(), c.now()); failErr != nil {
				c.log.Error("failed to settle fallback batch", "error", failErr)
			}
		}
		return SteerOrSendResult{}, err
	}
	if turn.ID == "" && len(batch) > 0 {
		if err := c.store.ReleaseQueuedBatch(context.WithoutCancel(ctx), c.conversation.ID, queuedBatchIDs(batch)); err != nil {
			return SteerOrSendResult{}, fmt.Errorf("release batch after duplicate send: %w", err)
		}
	}
	return SteerOrSendResult{Turn: turn}, nil
}

type steerDeliveryRequest struct {
	Text            string                  `json:"text"`
	Content         []ports.ChatContent     `json:"content,omitempty"`
	Origin          domain.MessageOrigin    `json:"origin"`
	SenderSessionID string                  `json:"senderSessionId,omitempty"`
	Settings        deliveryRequestSettings `json:"settings"`
}

func encodeSteerDeliveryRequest(msg ports.ChatUserMessage) (string, error) {
	encoded, err := json.Marshal(steerDeliveryRequest{
		Text: msg.Text, Content: msg.Content, Origin: normalizeOrigin(msg.Origin),
		SenderSessionID: msg.SenderSessionID,
		Settings: deliveryRequestSettings{
			Model: msg.Settings.Model, Effort: msg.Settings.Effort, Approval: msg.Settings.Approval,
		},
	})
	if err != nil {
		return "", fmt.Errorf("encode steer delivery request: %w", err)
	}
	return string(encoded), nil
}

func replaySteerDelivery(
	delivery domain.ConversationSteerDelivery,
	requestJSON string,
) (SteerResult, error) {
	if delivery.RequestJSON != requestJSON {
		return SteerResult{}, ErrSteerIdempotencyConflict
	}
	switch delivery.State {
	case domain.ConversationSteerAccepted:
		return SteerResult{
			ProviderTurnID: delivery.ProviderTurnID,
			ActivityID:     delivery.ActivityID,
			Replayed:       true,
		}, nil
	case domain.ConversationSteerRejected:
		return SteerResult{}, replaySteerRejection(delivery)
	case domain.ConversationSteerReserved:
		return SteerResult{}, ErrSteerDeliveryUncertain
	default:
		return SteerResult{}, fmt.Errorf("%w: invalid durable state %q",
			ErrSteerDeliveryUncertain, delivery.State)
	}
}

type storedSteerRejection struct {
	message string
	cause   error
}

func (e storedSteerRejection) Error() string { return e.message }
func (e storedSteerRejection) Unwrap() error { return e.cause }

func replaySteerRejection(delivery domain.ConversationSteerDelivery) error {
	var cause error
	switch delivery.RejectionKind {
	case domain.ConversationSteerRejectedNoActiveTurn:
		cause = ErrNoActiveTurn
	case domain.ConversationSteerRejectedUnsupported:
		cause = ErrSteerUnsupported
	case domain.ConversationSteerRejectedTurnNotSteerable:
		cause = ErrTurnNotSteerable
	case domain.ConversationSteerRejectedContentUnsupported:
		cause = ErrSteerContentUnsupported
	case domain.ConversationSteerRejectedByProvider:
		cause = ErrProviderRefused
	case domain.ConversationSteerRejectedInterfaceTransition:
		cause = ErrControllerHandoff
	default:
		return fmt.Errorf("%w: invalid durable rejection %q",
			ErrSteerDeliveryUncertain, delivery.RejectionKind)
	}
	return storedSteerRejection{message: delivery.RejectionMessage, cause: cause}
}

func classifySteerRejection(
	err error,
) (domain.ConversationSteerRejectionKind, bool, error) {
	switch {
	case errors.Is(err, ports.ErrChatNoSteerableTurn):
		return domain.ConversationSteerRejectedNoActiveTurn, true, ErrNoActiveTurn
	case errors.Is(err, ports.ErrChatTurnNotSteerable):
		return domain.ConversationSteerRejectedTurnNotSteerable, true,
			fmt.Errorf("%w: %w", ErrTurnNotSteerable, err)
	case errors.Is(err, ports.ErrChatSteerContentUnsupported):
		return domain.ConversationSteerRejectedContentUnsupported, true,
			fmt.Errorf("%w: %w", ErrSteerContentUnsupported, err)
	}
	classified := classify(err)
	if errors.Is(classified, ErrProviderRefused) {
		return domain.ConversationSteerRejectedByProvider, true, classified
	}
	return "", false, classified
}

func (c *Controller) recordSteer(
	ctx context.Context,
	providerTurnID string,
	msg ports.ChatUserMessage,
	providerInput steerProviderInput,
) (string, error) {
	id := c.newID()
	activity, err := makeSteerActivity(id, msg, "", providerInput)
	if err != nil {
		return "", err
	}
	if err := c.store.UpsertActivity(ctx, c.conversation.ID, providerTurnID,
		activity, c.now()); err != nil {
		return "", fmt.Errorf("record steer on turn %s: %w", providerTurnID, err)
	}
	return id, nil
}

type steerProviderInput struct {
	Text          string
	Content       []ports.ChatContent
	SourceTurnIDs []string
	StoredOnTurn  bool
}

func makeSteerActivity(
	id string,
	msg ports.ChatUserMessage,
	sourceTurnID string,
	providerInputs ...steerProviderInput,
) (domain.ConversationActivity, error) {
	detail := map[string]any{
		"event":  "steer",
		"text":   msg.Text,
		"origin": string(normalizeOrigin(msg.Origin)),
	}
	if msg.SenderSessionID != "" {
		detail["senderSessionId"] = msg.SenderSessionID
		if msg.SenderProjectID != "" {
			detail["senderProjectId"] = msg.SenderProjectID
		}
		if msg.SenderDisplayName != "" {
			detail["senderDisplayName"] = msg.SenderDisplayName
		}
	}
	if msg.ClientMessageID != "" {
		detail["clientMessageId"] = msg.ClientMessageID
	}
	if sourceTurnID != "" {
		detail["sourceTurnId"] = sourceTurnID
	}
	if len(providerInputs) > 0 {
		detail["providerInputCaptured"] = true
		detail["providerInputText"] = providerInputs[0].Text
		detail["providerInputContent"] = providerInputs[0].Content
		detail["providerInputSourceTurnIds"] = providerInputs[0].SourceTurnIDs
		detail["providerInputStoredOnTurn"] = providerInputs[0].StoredOnTurn
	}
	if len(msg.Content) > 0 {
		detail["content"] = msg.Content
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return domain.ConversationActivity{}, fmt.Errorf("encode steer detail: %w", err)
	}
	return domain.ConversationActivity{
		ID:             id,
		Kind:           domain.ActivityKindSystem,
		Status:         domain.ActivityStatusCompleted,
		Summary:        steerSummary(msg.Text),
		Detail:         encoded,
		ProviderItemID: steerItemID(msg.ClientMessageID),
	}, nil
}

func steerItemID(clientMessageID string) string {
	if clientMessageID == "" {
		return ""
	}
	return "steer:" + clientMessageID
}

func steerSummary(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(collapsed) <= maxSteerSummaryRunes {
		return collapsed
	}
	runes := []rune(collapsed)
	return strings.TrimRight(string(runes[:maxSteerSummaryRunes]), " ") + "…"
}
