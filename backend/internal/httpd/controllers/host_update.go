package controllers

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/hostupdate"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// HostUpdater is the hostupdate.Updater surface the controller uses.
type HostUpdater interface {
	Status(context.Context) hostupdate.Status
	Start(ctx context.Context, version string) error
	Check(context.Context) (string, error)
}

// HostUpdateController reports this host's version and updates a self-hosted host.
type HostUpdateController struct {
	Updater HostUpdater
}

// Register mounts the update routes.
func (c *HostUpdateController) Register(r chi.Router) {
	r.Get("/remote-host/update", c.status)
	r.Post("/remote-host/update", c.start)
	r.Post("/remote-host/update/check", c.check)
}

func (c *HostUpdateController) status(w http.ResponseWriter, r *http.Request) {
	if c.Updater == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/remote-host/update")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, hostUpdateResponse(c.Updater.Status(r.Context())))
}

func (c *HostUpdateController) start(w http.ResponseWriter, r *http.Request) {
	if c.Updater == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/remote-host/update")
		return
	}
	var in RemoteHostUpdateRequest
	if err := decodeJSONStrict(r, &in); err != nil && !errors.Is(err, io.EOF) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	switch err := c.Updater.Start(r.Context(), in.Version); {
	case errors.Is(err, hostupdate.ErrUnsupported):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "HOST_UPDATE_UNSUPPORTED", err.Error(), nil)
	case errors.Is(err, hostupdate.ErrInProgress):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "HOST_UPDATE_IN_PROGRESS", err.Error(), nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "HOST_UPDATE_FAILED", "could not start the update: "+err.Error(), nil)
	default:
		envelope.WriteJSON(w, http.StatusAccepted, hostUpdateResponse(c.Updater.Status(r.Context())))
	}
}

func (c *HostUpdateController) check(w http.ResponseWriter, r *http.Request) {
	if c.Updater == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/remote-host/update/check")
		return
	}
	switch version, err := c.Updater.Check(r.Context()); {
	case errors.Is(err, hostupdate.ErrNoCheck):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "HOST_UPDATE_NO_CHECK", err.Error(), nil)
	case errors.Is(err, hostupdate.ErrInProgress):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "HOST_UPDATE_IN_PROGRESS", err.Error(), nil)
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "HOST_UPDATE_CHECK_FAILED", err.Error(), nil)
	default:
		envelope.WriteJSON(w, http.StatusOK, RemoteHostUpdateCheckResponse{Version: version})
	}
}

func hostUpdateResponse(status hostupdate.Status) RemoteHostUpdateResponse {
	return RemoteHostUpdateResponse{Version: status.Version, Supported: status.Supported, Method: status.Method, State: string(status.State), Error: status.Error}
}
