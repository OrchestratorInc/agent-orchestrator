package controllers

import (
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

func (c *SessionsController) stageAttachments(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/attachments")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxSpawnBodyBytes)

	var in StageSessionAttachmentsRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request",
			"INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if len(in.Attachments) == 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request",
			"ATTACHMENTS_REQUIRED", "attachments is required", nil)
		return
	}
	attachments, attachErr := decodeSpawnAttachments(in.Attachments)
	if attachErr != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request",
			attachErr.code, attachErr.message, nil)
		return
	}

	id := sessionID(r)
	paths, err := c.Svc.StageAttachments(r.Context(), id, attachments)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, StageSessionAttachmentsResponse{
		SessionID: id,
		Paths:     paths,
	})
}
