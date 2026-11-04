package controllers

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type fileReferenceResolver interface {
	ResolveFileReference(context.Context, domain.SessionID, ports.ChatFileReference) (sessionsvc.FileReferenceResolution, error)
}

func (c *SessionsController) resolveFileReference(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.Svc.(fileReferenceResolver)
	if !ok {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/conversation/file-references/resolve")
		return
	}
	var req ConversationFileReferenceRequest
	if !decodeConversationBody(w, r, &req) {
		return
	}
	result, err := svc.ResolveFileReference(r.Context(), sessionID(r), ports.ChatFileReference(req))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, ResolveFileReferenceResponse(result))
}
