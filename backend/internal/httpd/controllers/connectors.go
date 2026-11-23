package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/connectors"
)

const connectorBodyLimit = 256 << 10

// ConnectorService is the controller-facing connector contract.
type ConnectorService interface {
	List(ctx context.Context) ([]connectors.Connector, error)
	Get(ctx context.Context, ref string) (connectors.Connector, error)
	Create(ctx context.Context, in connectors.Input) (connectors.Connector, error)
	Update(ctx context.Context, ref string, in connectors.Input) (connectors.Connector, error)
	SetEnabled(ctx context.Context, ref string, enabled bool) (connectors.Connector, error)
	Delete(ctx context.Context, ref string) error
}

// ConnectorsController owns the connector routes.
type ConnectorsController struct {
	Svc ConnectorService
}

// Register mounts the connector routes.
func (c *ConnectorsController) Register(r chi.Router) {
	r.Get("/connectors", c.list)
	r.Post("/connectors", c.create)
	r.Get("/connectors/{connectorId}", c.get)
	r.Patch("/connectors/{connectorId}", c.update)
	r.Delete("/connectors/{connectorId}", c.delete)
}

func (c *ConnectorsController) ready(w http.ResponseWriter, r *http.Request, method, path string) bool {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, method, path)
		return false
	}
	return true
}

func (c *ConnectorsController) list(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodGet, "/api/v1/connectors") {
		return
	}
	all, err := c.Svc.List(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]ConnectorResponse, 0, len(all))
	for i := range all {
		out = append(out, connectorResponse(&all[i]))
	}
	envelope.WriteJSON(w, http.StatusOK, ListConnectorsResponse{Connectors: out})
}

func (c *ConnectorsController) get(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodGet, "/api/v1/connectors/{connectorId}") {
		return
	}
	found, err := c.Svc.Get(r.Context(), connectorRef(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConnectorEnvelope{Connector: connectorResponse(&found)})
}

func (c *ConnectorsController) create(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors") {
		return
	}
	var req ConnectorRequest
	if !decodeConnectorBody(w, r, &req) || rejectLANCommandConnector(w, r, req.Transport) {
		return
	}
	created, err := c.Svc.Create(r.Context(), connectorInput(&req))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ConnectorEnvelope{Connector: connectorResponse(&created)})
}

func (c *ConnectorsController) update(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPatch, "/api/v1/connectors/{connectorId}") {
		return
	}
	var req ConnectorRequest
	if !decodeConnectorBody(w, r, &req) {
		return
	}
	ref := connectorRef(r)
	var updated connectors.Connector
	var err error
	if req.Transport == "" && req.Enabled != nil {
		updated, err = c.Svc.SetEnabled(r.Context(), ref, *req.Enabled)
	} else {
		current, getErr := c.Svc.Get(r.Context(), ref)
		if getErr != nil {
			envelope.WriteError(w, r, getErr)
			return
		}
		if rejectLANCommandConnector(w, r, req.Transport, string(current.Transport)) {
			return
		}
		updated, err = c.Svc.Update(r.Context(), ref, connectorInput(&req))
	}
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConnectorEnvelope{Connector: connectorResponse(&updated)})
}

func (c *ConnectorsController) delete(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodDelete, "/api/v1/connectors/{connectorId}") {
		return
	}
	if err := c.Svc.Delete(r.Context(), connectorRef(r)); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func connectorRef(r *http.Request) string {
	ref, err := url.PathUnescape(chi.URLParam(r, "connectorId"))
	if err != nil {
		return chi.URLParam(r, "connectorId")
	}
	return ref
}

func decodeConnectorBody(w http.ResponseWriter, r *http.Request, into any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, connectorBodyLimit)
	err := decodeJSONStrict(r, into)
	if err == nil {
		return true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "bad_request", "CONNECTOR_BODY_TOO_LARGE", "The connector request body is too large", nil)
	} else {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
	}
	return false
}

func rejectLANCommandConnector(w http.ResponseWriter, r *http.Request, transports ...string) bool {
	if !requestscope.IsLAN(r.Context()) {
		return false
	}
	for _, transport := range transports {
		if transport == string(connectors.TransportStdio) {
			envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "CONNECTOR_COMMAND_LOOPBACK_REQUIRED", "A connector that runs a command must be set up on the machine that runs it", nil)
			return true
		}
	}
	return false
}

func connectorInput(req *ConnectorRequest) connectors.Input {
	in := connectors.Input{
		Key: req.Key, Name: req.Name, PresetID: req.PresetID,
		Transport: connectors.Transport(req.Transport), URL: req.URL, Command: req.Command, Args: req.Args,
		Env: req.Env, Headers: req.Headers, Auth: connectors.Auth(req.Auth), Enabled: req.Enabled,
	}
	if req.ProjectID != nil {
		in.ProjectID = *req.ProjectID
	}
	return in
}

func connectorResponse(c *connectors.Connector) ConnectorResponse {
	out := ConnectorResponse{
		ID: c.ID, Key: c.Key, Name: c.Name, PresetID: c.PresetID, Transport: string(c.Transport),
		URL: withoutQuery(c.URL), Command: c.Command, Args: nonNil(c.Args), EnvKeys: nonNil(c.EnvKeys), HeaderKeys: nonNil(c.HeaderKeys),
		Auth: string(c.Auth), Enabled: c.Enabled, Status: string(c.Status), Tools: nonNil(c.Tools),
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if c.ProjectID != "" {
		out.ProjectID = &c.ProjectID
	}
	if c.StatusStep != "" || c.StatusError != "" {
		out.StatusDetail = &ConnectorStatusDetail{Step: c.StatusStep, Error: c.StatusError}
	}
	if !c.CheckedAt.IsZero() {
		out.CheckedAt = &c.CheckedAt
	}
	return out
}

func withoutQuery(raw string) string {
	target, err := url.Parse(raw)
	if err != nil || raw == "" {
		return raw
	}
	target.RawQuery = ""
	return target.String()
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
