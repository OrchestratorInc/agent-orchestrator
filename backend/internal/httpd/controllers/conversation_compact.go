package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

const activityPreviewBytes = 128

type conversationActivityReader interface {
	Activity(context.Context, domain.SessionID, string) (domain.ConversationActivity, error)
}

// activity returns exactly one row, never a message-counted conversation page.
func (c *ConversationsController) activity(w http.ResponseWriter, r *http.Request) {
	reader, ok := c.Svc.(conversationActivityReader)
	if !ok {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/conversation/activities/{activityId}")
		return
	}
	id, err := url.PathUnescape(chi.URLParam(r, "activityId"))
	if err != nil || id == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "CONVERSATION_ACTIVITY_INVALID", "activity identifier is invalid", nil)
		return
	}
	row, err := reader.Activity(r.Context(), domain.SessionID(chi.URLParam(r, "sessionId")), id)
	if errors.Is(err, domain.ErrConversationActivityNotFound) {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "CONVERSATION_ACTIVITY_NOT_FOUND", "activity is not in this conversation", nil)
		return
	}
	if err != nil {
		writeConversationError(w, r, err)
		return
	}
	response := conversationSnapshotResponse(chatsvc.Snapshot{Activities: []domain.ConversationActivity{row}})
	compactConversationDetails(&response, false, true)
	envelope.WriteJSON(w, http.StatusOK, response.Activities[0])
}

func compactConversationDetails(snapshot *ConversationSnapshotResponse, compact, dedupe bool) {
	if !compact && !dedupe {
		return
	}
	for i := range snapshot.Activities {
		row := &snapshot.Activities[i]
		// Interactive rows and inline system content must always remain whole.
		if row.Status == "running" || row.Status == "pending" {
			continue
		}
		switch row.ActivityKind {
		case "command", "mcp_tool", "file_change", "reasoning":
		default:
			continue
		}
		if dedupe {
			dedupeActivityDetail(row.ActivityKind, row.Detail)
		}
		if compact {
			_, row.DetailOmitted = previewDetail(row.Detail, false)
		}
	}
}

func dedupeActivityDetail(kind string, detail map[string]any) {
	drop, keep := "content", "output"
	if kind == "mcp_tool" {
		drop, keep = "output", "content"
	}
	if kind == "command" || kind == "mcp_tool" {
		removed, exists := detail[drop]
		retained, kept := detail[keep]
		if exists && kept && duplicateLongStrings(removed, retained) {
			delete(detail, drop)
			detail[drop+"Omitted"] = keep
		}
	}
	if raw, ok := detail["rawCommand"].(string); ok && raw != "" && raw == detail["command"] {
		delete(detail, "rawCommand")
		detail["rawCommandOmitted"] = "command"
	}
}

// Only sizeable duplicated bodies are omitted. Exact string membership avoids
// claiming two structured results are interchangeable because of a short label.
func duplicateLongStrings(removed, retained any) bool {
	kept := map[string]bool{}
	walkStrings(retained, func(s string) { kept[s] = true })
	count, matches := 0, true
	walkStrings(removed, func(s string) {
		if len(s) > activityPreviewBytes {
			count++
			if !kept[s] {
				matches = false
			}
		}
	})
	return count > 0 && matches
}
func walkStrings(value any, visit func(string)) {
	switch v := value.(type) {
	case string:
		visit(v)
	case map[string]any:
		for _, child := range v {
			walkStrings(child, visit)
		}
	case []any:
		for _, child := range v {
			walkStrings(child, visit)
		}
	}
}

func previewDetail(value any, tail bool) (any, bool) {
	omitted := false
	switch v := value.(type) {
	case string:
		if len(v) <= activityPreviewBytes {
			return v, false
		}
		if tail {
			start := len(v) - activityPreviewBytes
			for start < len(v) && !utf8.RuneStart(v[start]) {
				start++
			}
			return v[start:], true
		}
		end := activityPreviewBytes
		for end > 0 && !utf8.RuneStart(v[end]) {
			end--
		}
		return v[:end], true
	case map[string]any:
		for key, child := range v {
			next, changed := previewDetail(child, tail || strings.EqualFold(key, "progress"))
			v[key] = next
			omitted = omitted || changed
		}
	case []any:
		for i, child := range v {
			next, changed := previewDetail(child, tail)
			v[i] = next
			omitted = omitted || changed
		}
	}
	return value, omitted
}
