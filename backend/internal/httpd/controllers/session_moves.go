package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

// SessionMover moves sessions between this machine and AO Cloud.
type SessionMover interface {
	PreviewMove(ctx context.Context, id domain.SessionID) (sessionsvc.MovePreview, error)
	ExportMove(ctx context.Context, id domain.SessionID, in sessionsvc.MoveExportInput) (sessionsvc.MoveExport, error)
	MoveTranscript(ctx context.Context, id domain.SessionID) (sessionsvc.MoveTranscript, error)
	ImportMove(ctx context.Context, in sessionsvc.MoveImportInput) (sessionsvc.MoveImportOutcome, error)
	CompleteMove(ctx context.Context, id domain.SessionID, in sessionsvc.MoveCompleteInput) (domain.Session, error)
}

// SessionMovesController serves the routes the app calls to move a session:
// export and complete for a move to AO Cloud, import for a move from it.
type SessionMovesController struct {
	Svc SessionMover
}

// Register mounts the session move routes.
func (c *SessionMovesController) Register(r chi.Router) {
	r.Get("/sessions/{sessionId}/move/preview", c.preview)
	r.Post("/sessions/{sessionId}/move/export", c.export)
	r.Get("/sessions/{sessionId}/move/transcript", c.transcript)
	r.Post("/sessions/move/import", c.importMove)
	r.Post("/sessions/{sessionId}/move/complete", c.complete)
}

func (c *SessionMovesController) preview(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/move/preview")
		return
	}
	out, err := c.Svc.PreviewMove(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	changes := make([]MoveChange, 0, len(out.Changes))
	for _, ch := range out.Changes {
		changes = append(changes, MoveChange{Path: ch.Path, Status: moveChangeStatus(ch.Status)})
	}
	envelope.WriteJSON(w, http.StatusOK, MovePreviewResponse{
		Branch: out.Branch, Changes: changes,
		Transcript: MoveTranscriptInfo{Bytes: out.Transcript.Bytes, Unavailable: out.Transcript.Unavailable},
	})
}

func (c *SessionMovesController) transcript(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/sessions/{sessionId}/move/transcript")
		return
	}
	out, err := c.Svc.MoveTranscript(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MoveTranscriptResponse{AgentSessionID: out.AgentSessionID, TranscriptGzip: out.Gzip})
}

// moveChangeStatus names git's diff status letter.
func moveChangeStatus(status byte) string {
	switch status {
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	default:
		return "modified"
	}
}

func (c *SessionMovesController) export(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/move/export")
		return
	}
	var in MoveExportRequest
	if err := decodeJSON(r, &in); err != nil {
		writeInvalidJSON(w, r)
		return
	}
	out, err := c.Svc.ExportMove(r.Context(), sessionID(r), sessionsvc.MoveExportInput{
		MoveID: in.MoveID, SourceLabel: in.SourceLabel,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MoveExportResponse{
		Manifest: out.Manifest, HandoffNote: out.HandoffNote, Changes: out.Changes, Submodules: out.Submodules,
	})
}

func (c *SessionMovesController) importMove(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/move/import")
		return
	}
	var in MoveImportRequest
	if err := decodeJSON(r, &in); err != nil {
		writeInvalidJSON(w, r)
		return
	}
	input := sessionsvc.MoveImportInput{
		ProjectID: domain.ProjectID(in.ProjectID), MoveID: in.MoveID, Manifest: in.Manifest, HandoffNote: in.HandoffNote,
	}
	if t := in.Transcript; t != nil {
		input.Transcript = &sessionsvc.MoveImportTranscript{AgentSessionID: t.AgentSessionID, ProjectDir: t.ProjectDir, Gzip: t.TranscriptGzip}
	}
	out, err := c.Svc.ImportMove(r.Context(), input)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MoveImportResponse{Session: sessionView(r, out.Session), Warnings: out.Warnings})
}

func (c *SessionMovesController) complete(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/move/complete")
		return
	}
	var in MoveCompleteRequest
	if err := decodeJSON(r, &in); err != nil {
		writeInvalidJSON(w, r)
		return
	}
	sess, err := c.Svc.CompleteMove(r.Context(), sessionID(r), sessionsvc.MoveCompleteInput{Destination: domain.SessionMove{
		MoveID: in.MoveID, OrgID: in.OrgID, SessionID: in.SessionID,
	}})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MoveCompleteResponse{Session: sessionView(r, sess)})
}

func writeInvalidJSON(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
}
