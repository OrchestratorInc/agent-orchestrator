package controllers

import (
	"context"
	"errors"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
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
	stage := c.Svc.StageAttachments
	if drafts, ok := c.Svc.(draftAttachmentService); ok && in.Draft {
		stage = drafts.StageDraftAttachments
	}
	paths, err := stage(r.Context(), id, attachments)
	if err != nil {
		writeAttachmentWriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, StageSessionAttachmentsResponse{
		SessionID: id,
		Paths:     paths,
	})
}

// draftAttachmentService is the optional draft-lease capability of the
// session service. Keeping it separate avoids widening every service fake.
type draftAttachmentService interface {
	StageDraftAttachments(ctx context.Context, id domain.SessionID, attachments []ports.SpawnAttachment) ([]string, error)
	UpdateAttachmentLeases(ctx context.Context, id domain.SessionID, release, renew []string) ([]string, error)
}

// updateAttachmentLeases releases files an unsent draft no longer holds and
// renews the ones it still does. Release is not a path-based delete: only a
// file still leased to a draft (staged with draft set, and named by no
// accepted message) is ever deleted, so the renderer cannot erase history.
func (c *SessionsController) updateAttachmentLeases(w http.ResponseWriter, r *http.Request) {
	drafts, ok := c.Svc.(draftAttachmentService)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/attachments/leases")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAttachmentLeaseBodyBytes)
	var in UpdateSessionAttachmentLeasesRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request",
			"INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if len(in.Release)+len(in.Renew) > maxAttachmentLeasePaths {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request",
			"TOO_MANY_PATHS", "too many attachment paths", nil)
		return
	}
	missing, err := drafts.UpdateAttachmentLeases(r.Context(), sessionID(r), in.Release, in.Renew)
	if err != nil {
		if errors.Is(err, ports.ErrSessionNotFound) {
			envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found",
				"SESSION_NOT_FOUND", "session not found", nil)
			return
		}
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal",
			"ATTACHMENT_LEASE_FAILED", "Attachment leases could not be updated", nil)
		return
	}
	if missing == nil {
		missing = []string{}
	}
	envelope.WriteJSON(w, http.StatusOK, UpdateSessionAttachmentLeasesResponse{Missing: missing})
}

const (
	// maxAttachmentLeasePaths bounds one lease update. A draft holds at most a
	// few attachments; a renderer reconciling several drafts stays well under.
	maxAttachmentLeasePaths     = 256
	maxAttachmentLeaseBodyBytes = 64 << 10
)
