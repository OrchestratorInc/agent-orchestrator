package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

const steerPath = "/api/v1/sessions/{sessionId}/conversation/steer"
const steerOrSendPath = "/api/v1/sessions/{sessionId}/conversation/steer-or-send"
const queuedTurnSteerPath = "/api/v1/sessions/{sessionId}/conversation/turns/{turnId}/steer"

type steerOrSendService interface {
	SteerOrSend(context.Context, domain.SessionID, ports.ChatUserMessage, bool) (chatsvc.SteerOrSendResult, error)
}

type PromoteQueuedTurnResponse struct {
	SourceTurnID   string `json:"sourceTurnId"`
	ProviderTurnID string `json:"providerTurnId"`
	ActivityID     string `json:"activityId"`
}

func (c *ConversationsController) steerOrSend(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(steerOrSendService)
	if !ok {
		apispec.NotImplemented(w, r, "POST", steerOrSendPath)
		return
	}
	var req SteerConversationRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	content, attachmentErr := conversationContent(SendConversationMessageRequest{
		Attachments: req.Attachments,
	})
	if attachmentErr != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			attachmentErr.code, attachmentErr.message, nil)
		return
	}
	result, err := svc.SteerOrSend(
		r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")),
		ports.ChatUserMessage{
			Text: req.Text, Content: content, ClientMessageID: req.ClientMessageID,
			SenderSessionID: req.SenderSessionID, Origin: domain.MessageOriginHuman,
		},
		req.RecoverOnly,
	)
	if err != nil {
		writeSteerError(w, r, err)
		return
	}
	if !req.RecoverOnly && !result.Duplicate && result.Steered {
		recordUserMessage(r.Context(), c.UserMessages, domain.SessionID(chi.URLParam(r, "sessionId")), req.SenderSessionID, true)
	}
	response := SteerOrSendConversationResponse{Duplicate: result.Duplicate}
	if result.Steered {
		response.Outcome = "steered"
		response.ProviderTurnID = result.Steer.ProviderTurnID
		response.ActivityID = result.Steer.ActivityID
	} else {
		response.Outcome = "sent"
		response.TurnID = result.Turn.ID
		response.ProviderTurnID = result.Turn.ProviderTurnID
		response.State = result.Turn.State
	}
	envelope.WriteJSON(w, http.StatusAccepted, response)
}

func (c *ConversationsController) steer(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", steerPath)
		return
	}
	var req SteerConversationRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.RecoverOnly {
		result, err := c.Svc.RecoverSteer(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), req.ClientMessageID)
		if err != nil {
			writeSteerError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusAccepted, SteerConversationResponse{
			ProviderTurnID: result.ProviderTurnID, ActivityID: result.ActivityID,
		})
		return
	}
	content, attachmentErr := conversationContent(SendConversationMessageRequest{
		Attachments: req.Attachments,
	})
	if attachmentErr != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			attachmentErr.code, attachmentErr.message, nil)
		return
	}

	result, err := c.Svc.Steer(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")),
		ports.ChatUserMessage{
			Text:            req.Text,
			Content:         content,
			ClientMessageID: req.ClientMessageID,
			SenderSessionID: req.SenderSessionID,
			Origin:          domain.MessageOriginHuman,
		})
	if err != nil {
		writeSteerError(w, r, err)
		return
	}
	if !result.Replayed {
		recordUserMessage(r.Context(), c.UserMessages, domain.SessionID(chi.URLParam(r, "sessionId")), req.SenderSessionID, true)
	}
	envelope.WriteJSON(w, http.StatusAccepted, SteerConversationResponse{
		ProviderTurnID: result.ProviderTurnID,
		ActivityID:     result.ActivityID,
	})
}

func (c *ConversationsController) promoteQueuedTurn(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", queuedTurnSteerPath)
		return
	}
	result, err := c.Svc.PromoteQueuedTurn(
		r.Context(),
		domain.SessionID(chi.URLParam(r, "sessionId")),
		chi.URLParam(r, "turnId"),
	)
	if err != nil {
		writeQueuedTurnSteerError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusAccepted, PromoteQueuedTurnResponse{
		SourceTurnID: result.SourceTurnID, ProviderTurnID: result.ProviderTurnID,
		ActivityID: result.ActivityID,
	})
}

func writeQueuedTurnSteerError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, chatsvc.ErrTurnNotQueued):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_NOT_QUEUED", "that message is no longer queued", nil)
	case errors.Is(err, chatsvc.ErrPromotionUncertain):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_PROMOTION_UNCERTAIN",
			"the provider may have received this guidance; it will not be queued again automatically", nil)
	default:
		writeSteerError(w, r, err)
	}
}

func writeSteerError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, chatsvc.ErrSteerTextRequired):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_STEER_TEXT_REQUIRED", "steer text is required", nil)

	case errors.Is(err, chatsvc.ErrSteerUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_STEER_UNSUPPORTED",
			"this agent cannot take guidance while it is working", nil)

	case errors.Is(err, chatsvc.ErrTurnNotSteerable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_TURN_NOT_STEERABLE", err.Error(), nil)

	case errors.Is(err, chatsvc.ErrSteerContentUnsupported):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation",
			"CHAT_UNSUPPORTED_STEER_CONTENT",
			"this agent cannot steer every attachment in that message", nil)

	case errors.Is(err, chatsvc.ErrSteerDeliveryUncertain):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_STEER_UNCERTAIN",
			"the provider may have received this guidance; retry only with the same recovery action", nil)

	case errors.Is(err, chatsvc.ErrSteerIdempotencyConflict):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_STEER_IDEMPOTENCY_CONFLICT",
			"this delivery handle belongs to different guidance", nil)

	case errors.Is(err, chatsvc.ErrNoActiveTurn):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict",
			"CHAT_NO_ACTIVE_TURN",
			"there is no turn in flight to steer; send this as a message instead", nil)

	default:
		writeConversationError(w, r, err)
	}
}
