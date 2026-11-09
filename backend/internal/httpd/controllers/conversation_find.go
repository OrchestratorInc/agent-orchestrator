package controllers

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type conversationFinder interface {
	FindConversation(context.Context, string, string, string, ...chatsvc.FindAnchor) (chatsvc.FindResult, error)
	FindConversationWindow(context.Context, string, int64) (chatsvc.Snapshot, error)
}

func (c *ConversationsController) findConversation(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(conversationFinder)
	if !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/conversations/{conversationId}/find")
		return
	}
	query := r.URL.Query().Get("query")
	if !utf8.ValidString(query) || utf8.RuneCountInString(query) > 512 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_FIND_QUERY_INVALID", "query must be valid text of at most 512 characters", nil)
		return
	}
	block, offset := 0, 0
	for key, target := range map[string]*int{"anchorBlock": &block, "anchorOffset": &offset} {
		if raw := r.URL.Query().Get(key); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 0 {
				envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_FIND_ANCHOR_INVALID", "anchor positions must be nonnegative integers", nil)
				return
			}
			*target = value
		}
	}
	var anchorSequence int64
	if raw := r.URL.Query().Get("anchorSequence"); raw != "" {
		var err error
		anchorSequence, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || anchorSequence < 0 {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_FIND_ANCHOR_INVALID", "anchor sequence must be a nonnegative integer", nil)
			return
		}
	}
	preferPrevious := false
	if raw := r.URL.Query().Get("fallbackToPrevious"); raw != "" {
		var err error
		preferPrevious, err = strconv.ParseBool(raw)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_FIND_ANCHOR_INVALID", "fallbackToPrevious must be a boolean", nil)
			return
		}
	}
	result, err := svc.FindConversation(r.Context(), chi.URLParam(r, "conversationId"), query, r.URL.Query().Get("cursor"), chatsvc.FindAnchor{MessageID: r.URL.Query().Get("anchorMessageId"), Block: block, Offset: offset, Sequence: anchorSequence, PreferPrevious: preferPrevious})
	if err != nil {
		writeConversationFindError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConversationFindResponse(result))
}

func writeConversationFindError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, chatsvc.ErrFindCursor) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "CHAT_FIND_STALE", err.Error(), nil)
		return
	}
	if errors.Is(err, chatsvc.ErrFindUnavailable) {
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "unsupported", "CHAT_FIND_UNAVAILABLE", err.Error(), nil)
		return
	}
	envelope.WriteError(w, r, err)
}

func (c *ConversationsController) findConversationWindow(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(conversationFinder)
	if !ok {
		apispec.NotImplemented(w, r, "GET", "/api/v1/conversations/{conversationId}/find-window")
		return
	}
	sequence, err := strconv.ParseInt(r.URL.Query().Get("sequence"), 10, 64)
	if err != nil || sequence <= 0 || sequence == 1<<63-1 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CHAT_FIND_SEQUENCE_INVALID", "sequence must be a positive message sequence", nil)
		return
	}
	snapshot, err := svc.FindConversationWindow(r.Context(), chi.URLParam(r, "conversationId"), sequence)
	if err != nil {
		writeConversationFindError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, conversationSnapshotResponse(snapshot))
}
