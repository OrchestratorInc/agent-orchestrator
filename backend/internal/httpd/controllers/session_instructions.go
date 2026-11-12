package controllers

import (
	"context"
	"net/http"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
	"github.com/go-chi/chi/v5"
)

// SessionAdditionalInstructionsPayload contains user text, never a provider system prompt.
type SessionAdditionalInstructionsPayload struct {
	Text string `json:"text"`
}

type sessionInstructionsService interface {
	AdditionalInstructions(context.Context, domain.SessionID) (string, error)
	SetAdditionalInstructions(context.Context, domain.SessionID, string) error
}

// additionalInstructions exposes durable settings independently of provider readiness.
func (c *ConversationsController) additionalInstructions(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(sessionInstructionsService)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "unsupported", "CHAT_INSTRUCTIONS_UNAVAILABLE", "Additional instructions unavailable", nil)
		return
	}
	id := domain.SessionID(chi.URLParam(r, "sessionId"))
	if r.Method == http.MethodPatch {
		var req SessionAdditionalInstructionsPayload
		if !decodeConversationBody(w, r, &req) {
			return
		}
		if !utf8.ValidString(req.Text) || utf8.RuneCountInString(req.Text) > chatsvc.MaxAdditionalInstructions {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_INSTRUCTIONS_INVALID", "Additional instructions must contain at most 16000 Unicode characters", nil)
			return
		}
		if err := svc.SetAdditionalInstructions(r.Context(), id, req.Text); err != nil {
			writeConversationError(w, r, err)
			return
		}
		envelope.WriteJSON(w, http.StatusOK, req)
		return
	}
	text, err := svc.AdditionalInstructions(r.Context(), id)
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionAdditionalInstructionsPayload{Text: text})
}
