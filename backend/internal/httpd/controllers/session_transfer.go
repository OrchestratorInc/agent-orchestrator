package controllers

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// sessionTransferService moves sessions between machines. It is optional so
// focused SessionService fakes need not implement it.
type sessionTransferService interface {
	PrepareTransfer(ctx context.Context, id domain.SessionID, transferID, sourceLabel string, dest domain.SessionTransferDestination, stopTurn bool) (domain.SessionTransfer, error)
	CancelTransfer(ctx context.Context, id domain.SessionID, transferID string) error
	TransferArchivePath(id domain.SessionID, transferID string) (string, error)
	StoreTransferArchive(transferID string, r io.Reader) error
	ReceiveTransfer(ctx context.Context, projectID domain.ProjectID, t domain.SessionTransfer) (domain.Session, bool, []string, error)
	CommitTransfer(ctx context.Context, id domain.SessionID, transferID string, dest domain.SessionTransferDestination) (domain.SessionMovedTo, error)
	SessionMovedTo(ctx context.Context, id domain.SessionID) (domain.SessionMovedTo, bool, error)
}

// TransferIDParam names a transfer in a route.
type TransferIDParam struct {
	TransferID string `path:"transferId" description:"Transfer identifier chosen by the client."`
}

// PrepareTransferRequest is the body of POST /sessions/{sessionId}/transfer/prepare.
type PrepareTransferRequest struct {
	TransferID  string                            `json:"transferId" minLength:"8" maxLength:"64"`
	SourceLabel string                            `json:"sourceLabel" maxLength:"200" description:"This machine's name as the user sees it, for the destination's note; empty uses its hostname."`
	Destination domain.SessionTransferDestination `json:"destination" description:"The machine the session is moving to; sessionId is not known yet."`
	StopTurn    bool                              `json:"stopTurn,omitempty" description:"End a turn in progress instead of refusing with TRANSFER_AGENT_BUSY."`
}

// CancelTransferRequest is the body of POST /sessions/{sessionId}/transfer/cancel.
type CancelTransferRequest struct {
	TransferID string `json:"transferId" minLength:"8" maxLength:"64"`
}

// CancelTransferResponse confirms a cancelled move.
type CancelTransferResponse struct {
	OK bool `json:"ok"`
}

// SessionTransferResponse returns a prepared transfer.
type SessionTransferResponse struct {
	Transfer domain.SessionTransfer `json:"transfer"`
}

// ReceiveTransferRequest is the body of POST /sessions/transfer/receive.
type ReceiveTransferRequest struct {
	ProjectID domain.ProjectID       `json:"projectId"`
	Transfer  domain.SessionTransfer `json:"transfer"`
}

// ReceiveTransferResponse returns the session continuing a transfer.
type ReceiveTransferResponse struct {
	Session  SessionView `json:"session"`
	Warnings []string    `json:"warnings"`
	Resumed  bool        `json:"resumed" description:"True when the agent resumed its moved conversation; false when it started from the handoff note."`
}

// CommitTransferRequest is the body of POST /sessions/{sessionId}/transfer/commit.
type CommitTransferRequest struct {
	TransferID  string                            `json:"transferId" minLength:"8" maxLength:"64"`
	Destination domain.SessionTransferDestination `json:"destination"`
}

// CommitTransferResponse reports where the moved session continues.
type CommitTransferResponse struct {
	MovedTo domain.SessionMovedTo `json:"movedTo"`
}

// SessionTransferStateResponse reports where a session moved, or is being moved.
type SessionTransferStateResponse struct {
	MovedTo *domain.SessionMovedTo `json:"movedTo,omitempty"`
	// Pending is a move that was prepared but not finished; its sessionId is empty.
	Pending *domain.SessionMovedTo `json:"pending,omitempty" description:"A move that was prepared but not finished. Resume is refused until it is finished or cancelled."`
}

func (c *SessionsController) transferService(w http.ResponseWriter, r *http.Request, method, path string) (sessionTransferService, bool) {
	svc, ok := c.Svc.(sessionTransferService)
	if c.Svc == nil || !ok {
		apispec.NotImplemented(w, r, method, path)
		return nil, false
	}
	return svc, true
}

func (c *SessionsController) prepareTransfer(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.transferService(w, r, "POST", "/api/v1/sessions/{sessionId}/transfer/prepare")
	if !ok {
		return
	}
	var in PrepareTransferRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	t, err := svc.PrepareTransfer(r.Context(), sessionID(r), strings.TrimSpace(in.TransferID), in.SourceLabel, in.Destination, in.StopTurn)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, SessionTransferResponse{Transfer: t})
}

func (c *SessionsController) transferArchive(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.transferService(w, r, "GET", "/api/v1/sessions/{sessionId}/transfer/{transferId}/archive")
	if !ok {
		return
	}
	path, err := svc.TransferArchivePath(sessionID(r), chi.URLParam(r, "transferId"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	http.ServeContent(w, r, "transfer.tar", info.ModTime(), f)
}

func (c *SessionsController) putTransferArchive(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.transferService(w, r, "PUT", "/api/v1/sessions/transfer/{transferId}/archive")
	if !ok {
		return
	}
	body := http.MaxBytesReader(w, r.Body, sessionmanager.MaxTransferArchiveBytes)
	if err := svc.StoreTransferArchive(chi.URLParam(r, "transferId"), body); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *SessionsController) receiveTransfer(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.transferService(w, r, "POST", "/api/v1/sessions/transfer/receive")
	if !ok {
		return
	}
	var in ReceiveTransferRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if in.ProjectID == "" || in.Transfer.TransferID == "" || in.Transfer.Branch == "" || in.Transfer.HeadSHA == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "TRANSFER_INCOMPLETE", "projectId, transfer.transferId, transfer.branch and transfer.headSha are required", nil)
		return
	}
	sess, resumed, warnings, err := svc.ReceiveTransfer(r.Context(), in.ProjectID, in.Transfer)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if warnings == nil {
		warnings = []string{}
	}
	envelope.WriteJSON(w, http.StatusOK, ReceiveTransferResponse{Session: sessionView(r, sess), Warnings: warnings, Resumed: resumed})
}

func (c *SessionsController) commitTransfer(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.transferService(w, r, "POST", "/api/v1/sessions/{sessionId}/transfer/commit")
	if !ok {
		return
	}
	var in CommitTransferRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	moved, err := svc.CommitTransfer(r.Context(), sessionID(r), strings.TrimSpace(in.TransferID), in.Destination)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, CommitTransferResponse{MovedTo: moved})
}

func (c *SessionsController) cancelTransfer(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.transferService(w, r, "POST", "/api/v1/sessions/{sessionId}/transfer/cancel")
	if !ok {
		return
	}
	var in CancelTransferRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := svc.CancelTransfer(r.Context(), sessionID(r), strings.TrimSpace(in.TransferID)); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, CancelTransferResponse{OK: true})
}

func (c *SessionsController) transferState(w http.ResponseWriter, r *http.Request) {
	svc, ok := c.transferService(w, r, "GET", "/api/v1/sessions/{sessionId}/transfer")
	if !ok {
		return
	}
	moved, found, err := svc.SessionMovedTo(r.Context(), sessionID(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	var resp SessionTransferStateResponse
	switch {
	case found && moved.Pending():
		resp.Pending = &moved
	case found:
		resp.MovedTo = &moved
	}
	envelope.WriteJSON(w, http.StatusOK, resp)
}
