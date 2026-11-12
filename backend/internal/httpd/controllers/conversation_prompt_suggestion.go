package controllers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type promptSuggestionService interface {
	PromptSuggestion(context.Context, domain.SessionID, string) (string, error)
}

// draftPromptSuggestionService is the optional draft-aware form. A service that
// only offers next-message suggestions still serves the empty-draft case.
type draftPromptSuggestionService interface {
	PromptDraftSuggestion(context.Context, domain.SessionID, string, string) (string, error)
}

func (c *ConversationsController) promptSuggestion(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(promptSuggestionService)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/{sessionId}/conversation/prompt-suggestion")
		return
	}
	var req ConversationPromptSuggestionRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	if req.TurnID == "" || len(req.TurnID) > 128 || strings.TrimSpace(req.TurnID) != req.TurnID {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_TURN_ID_INVALID", "turnId must be a nonempty identifier of at most 128 bytes", nil)
		return
	}
	// The draft is the composer's text. It is bounded before the provider is
	// asked, so an oversized draft is a validation error rather than a slow
	// round trip that discards its own answer.
	if utf8.RuneCountInString(req.Draft) > chatsvc.PromptSuggestionDraftLimit {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_PROMPT_DRAFT_TOO_LONG",
			fmt.Sprintf("draft must be at most %d characters", chatsvc.PromptSuggestionDraftLimit), nil)
		return
	}
	var prompt string
	var err error
	if strings.TrimSpace(req.Draft) != "" {
		// A draft can only be polished by a service that accepts one. Falling back
		// to PromptSuggestion here would answer with a next-message suggestion the
		// caller never asked for, about text it never sent: a wrong answer is worse
		// than a clear refusal, so an unsupported draft is reported, not degraded.
		draftSvc, ok := c.Svc.(draftPromptSuggestionService)
		if !ok {
			envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "CHAT_PROMPT_DRAFT_UNSUPPORTED",
				"This daemon cannot polish a draft; retry with an empty draft to get a next-message suggestion", nil)
			return
		}
		prompt, err = draftSvc.PromptDraftSuggestion(r.Context(), sessionID(r), req.TurnID, req.Draft)
	} else {
		prompt, err = svc.PromptSuggestion(r.Context(), sessionID(r), req.TurnID)
	}
	if err != nil {
		if r.Context().Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
			return
		}
		if errors.Is(err, chatsvc.ErrPromptSuggestionBusy) {
			w.Header().Set("Retry-After", "1")
			envelope.WriteAPIError(w, r, http.StatusTooManyRequests, "too_many_requests", "CHAT_PROMPT_SUGGESTION_BUSY", "Prompt suggestion capacity is busy; retry shortly", nil)
			return
		}
		writeConversationError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConversationPromptSuggestionResponse{TurnID: req.TurnID, Prompt: prompt})
}
