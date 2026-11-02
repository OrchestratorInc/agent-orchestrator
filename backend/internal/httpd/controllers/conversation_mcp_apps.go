package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mcpapp"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type mcpAppService interface {
	MCPAppSnapshot(context.Context, string, string) (chatsvc.MCPAppSnapshot, error)
	MCPAppDocument(context.Context, string, string) (mcpapp.Document, error)
	MCPAppAction(context.Context, string, string, chatsvc.MCPAppRequest) (chatsvc.MCPAppResponse, error)
}

func (c *ConversationsController) mcpApp(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(mcpAppService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	result, err := svc.MCPAppSnapshot(r.Context(), chi.URLParam(r, "conversationId"), chi.URLParam(r, "activityId"))
	if err != nil {
		writeMCPAppError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MCPAppSnapshotResponse(result))
}

func (c *ConversationsController) mcpAppDocument(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(mcpAppService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	doc, err := svc.MCPAppDocument(r.Context(), chi.URLParam(r, "conversationId"), chi.URLParam(r, "activityId"))
	if err != nil {
		writeMCPAppError(w, r, err)
		return
	}
	w.Header().Set("Content-Security-Policy", doc.Policy())
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Permissions-Policy", doc.PermissionsPolicy())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(doc.HTML))
}

func (c *ConversationsController) mcpAppAction(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(mcpAppService)
	if !ok {
		apispec.NotImplemented(w, r, r.Method, r.URL.Path)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 300<<10)
	var req MCPAppActionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "validation", "MCP_APP_INVALID", "invalid app request", nil)
		return
	}
	result, err := svc.MCPAppAction(r.Context(), chi.URLParam(r, "conversationId"), chi.URLParam(r, "activityId"), chatsvc.MCPAppRequest(req))
	if err != nil {
		writeMCPAppError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MCPAppActionResponse(result))
}

func writeMCPAppError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, chatsvc.ErrMCPApp) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "MCP_APP_UNAVAILABLE", err.Error(), nil)
		return
	}
	writeConversationError(w, r, err)
}
