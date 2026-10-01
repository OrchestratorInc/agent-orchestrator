package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/aoagents/agent-orchestrator/cloud/internal/attachments"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
	"github.com/go-chi/chi/v5"
	"net/http"
)

var (
	errUploadIncomplete = errors.New("attachment upload incomplete")
	errInvalidImage     = errors.New("invalid attachment image")
	errFinalizeStorage  = errors.New("attachment finalization failed")
)

type AttachmentStore interface {
	PrepareAttachment(context.Context, domain.Principal, string, string, domain.PrepareAttachment) (domain.Attachment, error)
	GetAttachment(context.Context, domain.Principal, string, string) (domain.Attachment, error)
	FinalizeAttachment(context.Context, domain.Principal, string, string, func(context.Context, domain.Attachment) error) (domain.Attachment, error)
	WorkerAttachments(context.Context, string, string, string, int64) ([]attachments.Metadata, error)
	LeaseAttachments(context.Context, domain.Principal, string, string, []string) ([]attachments.Metadata, error)
	RetainAttachments(context.Context, domain.Principal, string, string, []string, string, int64) error
}

func (s *Server) attachmentStore(w http.ResponseWriter, r *http.Request) (AttachmentStore, bool) {
	store, ok := s.store.(AttachmentStore)
	if !ok || s.attachmentStorage == nil {
		writeError(w, r, 503, "attachments_unavailable", "Image attachments are not enabled.")
		return nil, false
	}
	return store, true
}
func uploadKey(id string) string    { return "upload-" + id }
func canonicalKey(id string) string { return "image-" + id }
func (s *Server) prepareAttachment(w http.ResponseWriter, r *http.Request) {
	store, ok := s.attachmentStore(w, r)
	if !ok {
		return
	}
	org := chi.URLParam(r, "orgId")
	key, err := idempotencyKey(r)
	var input domain.PrepareAttachment
	if err != nil || requireUUID(org, "orgId") != nil || decodeJSON(w, r, &input) != nil || requireUUID(input.ProjectID, "projectId") != nil || (input.SessionID != "" && requireUUID(input.SessionID, "sessionId") != nil) || attachments.ValidateMetadata(input.Metadata) != nil {
		writeError(w, r, 422, "validation_error", "A project, raster image, size and SHA-256 checksum are required.")
		return
	}
	a, err := store.PrepareAttachment(r.Context(), principalFrom(r), org, key, input)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if a.Status == "expired" {
		writeError(w, r, 410, "attachment_expired", "The unreferenced upload expired.")
		return
	}
	grant, err := s.attachmentStorage.Upload(r.Context(), uploadKey(a.ID), a.Metadata)
	if err != nil {
		writeError(w, r, 503, "storage_unavailable", "The upload grant could not be created.")
		return
	}
	writeJSON(w, 201, map[string]any{"attachment": a, "upload": grant})
}
func (s *Server) completeAttachment(w http.ResponseWriter, r *http.Request) {
	store, ok := s.attachmentStore(w, r)
	if !ok {
		return
	}
	org, id := chi.URLParam(r, "orgId"), chi.URLParam(r, "attachmentId")
	if requireUUID(org, "orgId") != nil || requireUUID(id, "attachmentId") != nil {
		writeError(w, r, 400, "invalid_request", "Invalid attachment ID.")
		return
	}
	a, err := store.FinalizeAttachment(r.Context(), principalFrom(r), org, id, func(ctx context.Context, a domain.Attachment) error {
		object, err := s.attachmentStorage.Open(ctx, uploadKey(id))
		if err != nil {
			return errUploadIncomplete
		}
		data, err := attachments.Verify(object, a.Metadata)
		object.Close()
		if err != nil {
			return errInvalidImage
		}
		if err := s.attachmentStorage.PutVerified(ctx, canonicalKey(id), a.Metadata, data); err != nil {
			return errFinalizeStorage
		}
		return nil
	})
	switch {
	case errors.Is(err, errUploadIncomplete):
		writeError(w, r, 409, "upload_incomplete", "Upload the image before completing it.")
	case errors.Is(err, errInvalidImage):
		writeError(w, r, 422, "invalid_image", "The stored image does not match its declared size, format or checksum.")
	case errors.Is(err, errFinalizeStorage):
		writeError(w, r, 503, "storage_unavailable", "The image could not be finalized. Retry completion.")
	case err != nil:
		s.writeStoreError(w, r, err)
	}
	if err != nil {
		return
	}
	// Keep the upload available for retry until readiness commits. A live grant
	// cannot touch the canonical key; cleanup removes any recreated upload later.
	_ = s.attachmentStorage.Delete(r.Context(), uploadKey(id))
	writeJSON(w, 200, map[string]any{"attachment": a})
}
func (s *Server) attachmentReadGrant(w http.ResponseWriter, r *http.Request) {
	store, ok := s.attachmentStore(w, r)
	if !ok {
		return
	}
	org, id := chi.URLParam(r, "orgId"), chi.URLParam(r, "attachmentId")
	if requireUUID(org, "orgId") != nil || requireUUID(id, "attachmentId") != nil {
		writeError(w, r, 400, "invalid_request", "Invalid attachment ID.")
		return
	}
	a, err := store.GetAttachment(r.Context(), principalFrom(r), org, id)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if a.Status != "ready" {
		writeError(w, r, 409, "upload_incomplete", "The image is not ready.")
		return
	}
	grant, err := s.attachmentStorage.Read(r.Context(), canonicalKey(id))
	if err != nil {
		writeError(w, r, 503, "storage_unavailable", "The read grant could not be created.")
		return
	}
	writeJSON(w, 200, grant)
}
func (s *Server) workerAttachmentRead(w http.ResponseWriter, r *http.Request) {
	store, ok := s.attachmentStore(w, r)
	if !ok {
		return
	}
	claims := workerFrom(r)
	manifest, err := store.WorkerAttachments(r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch)
	if err != nil {
		s.writeWorkerStoreError(w, r, err)
		return
	}
	id := chi.URLParam(r, "attachmentId")
	for _, a := range manifest {
		if a.ID == id {
			grant, err := s.attachmentStorage.Read(r.Context(), canonicalKey(id))
			if err != nil {
				writeError(w, r, 503, "storage_unavailable", "The read grant could not be created.")
				return
			}
			writeJSON(w, 200, grant)
			return
		}
	}
	writeError(w, r, 404, "not_found", "The attachment does not belong to this session.")
}
func (s *Server) materializeAttachments(w http.ResponseWriter, r *http.Request) {
	store, ok := s.attachmentStore(w, r)
	if !ok {
		return
	}
	org, session, ok := workspaceRoute(w, r)
	if !ok {
		return
	}
	var input struct {
		AttachmentIDs []string `json:"attachmentIds"`
	}
	if decodeJSON(w, r, &input) != nil || len(input.AttachmentIDs) == 0 {
		writeError(w, r, 422, "validation_error", "Select at least one image.")
		return
	}
	metadata, err := store.LeaseAttachments(r.Context(), principalFrom(r), org, session, input.AttachmentIDs)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	payload, _ := json.Marshal(map[string]any{"attachments": metadata})
	result, ok := s.runWorkspaceRequest(w, r, org, session, "attachments.materialize", payload)
	if !ok {
		return
	}
	var response struct {
		Paths    []string `json:"paths"`
		WorkerID string   `json:"workerId"`
		Epoch    int64    `json:"epoch"`
	}
	if json.Unmarshal(result, &response) != nil || len(response.Paths) != len(metadata) {
		writeError(w, r, 502, "invalid_worker_response", "The worker did not acknowledge every image.")
		return
	}
	for i, path := range response.Paths {
		if path != worker.AttachmentPath(metadata[i]) {
			writeError(w, r, 502, "invalid_worker_response", "The worker returned an invalid image path.")
			return
		}
	}
	if err := store.RetainAttachments(r.Context(), principalFrom(r), org, session, input.AttachmentIDs, response.WorkerID, response.Epoch); err != nil {
		if errors.Is(err, postgres.ErrStaleWorker) {
			writeError(w, r, 409, "stale_worker", "The worker was replaced. Retry attachment preparation.")
		} else {
			s.writeStoreError(w, r, err)
		}
		return
	}
	writeJSON(w, 200, response)
}
