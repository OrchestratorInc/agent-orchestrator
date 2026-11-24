package controllers

import (
	"net/http"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/envelope"
)

// stageAttachments writes files into a live session's worktree and answers with
// the paths the caller should name in the message it sends next.
//
// Two steps rather than one field on the send body, because the transport is the
// path and not the bytes: a conversation carries text, and a file reaches the
// agent by being a file the agent can open. Staging separately also means a write
// that fails is reported before any message claiming to carry an attachment is
// recorded — the timeline never shows an attachment that is not on disk.
//
// The count and blocked-type rules are the spawn ones, unchanged: this is the
// same operation against a session that already exists. A large file arrives
// as an uploadId (see uploadAttachment), not as inline bytes.
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
		writeAttachmentWriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, StageSessionAttachmentsResponse{
		SessionID: id,
		Paths:     paths,
	})
}
