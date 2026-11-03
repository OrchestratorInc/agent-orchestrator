package controllers

import (
	"context"
	"net/http"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// ConversationSearch finds chat messages for the command palette. Implemented
// by the SQLite store's full-text index.
type ConversationSearch interface {
	SearchConversationMessages(ctx context.Context, query string) ([]domain.ChatSearchHit, error)
}

func (c *ConversationsController) searchConversations(w http.ResponseWriter, r *http.Request) {
	if c.Search == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/conversations/search")
		return
	}
	query := strings.TrimSpace(r.URL.Query().Get("query"))
	if query == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "CONVERSATION_SEARCH_QUERY_REQUIRED", "query is required", nil)
		return
	}
	hits, err := c.Search.SearchConversationMessages(r.Context(), query)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	resp := ConversationSearchResponse{Results: make([]ConversationSearchHitResponse, 0, len(hits))}
	for _, hit := range hits {
		resp.Results = append(resp.Results, ConversationSearchHitResponse{
			MessageID: hit.MessageID,
			SessionID: hit.SessionID,
			ProjectID: hit.ProjectID,
			ReviewID:  hit.ReviewID,
			Title:     hit.Title,
			// The snippet may span lines; the palette shows one.
			Text: strings.Join(strings.Fields(hit.Snippet), " "),
		})
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}
