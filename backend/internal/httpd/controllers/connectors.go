package controllers

import (
	"cmp"
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
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
	Test(ctx context.Context, ref string) (connectors.Connector, error)
	StartSignIn(ctx context.Context, ref string) (string, error)
	CompleteSignIn(ctx context.Context, state, code, iss, refusal string) (connectors.Connector, error)
	SignedInBy(ctx context.Context, state string) (connectors.Connector, bool)
	FinishFromAddress(ctx context.Context, ref, address string) (connectors.Connector, error)
	SignOut(ctx context.Context, ref string) (connectors.Connector, error)
	CallbackURL() string
	SessionConnectors(ctx context.Context, sessionID string) ([]connectors.SessionConnector, error)
	ImportCandidates(ctx context.Context) ([]connectors.ImportCandidate, error)
	Import(ctx context.Context, ids []string, values map[string]map[string]string) ([]connectors.Connector, error)
	ServeProxy(w http.ResponseWriter, r *http.Request, sessionID, key string)
	ServeLaunch(w http.ResponseWriter, r *http.Request, sessionID, key string)
	Propose(ctx context.Context, in connectors.ProposalInput) (connectors.Proposal, error)
	GetProposal(ctx context.Context, id string) (connectors.Proposal, error)
	SessionProposals(ctx context.Context, sessionID string) []connectors.Proposal
	ApproveProposal(ctx context.Context, id string, projectID *string, values map[string]string) (connectors.Proposal, connectors.Connector, error)
	DeclineProposal(ctx context.Context, id string) (connectors.Proposal, error)
}

// ConnectorsController owns the connector routes.
type ConnectorsController struct {
	Svc ConnectorService
}

// Register mounts the connector routes.
func (c *ConnectorsController) Register(r chi.Router) {
	r.Get("/connectors", c.list)
	r.Post("/connectors", c.create)
	r.Get("/connectors/import", c.importCandidates)
	r.Post("/connectors/import", c.importChosen)
	r.Get("/connectors/{connectorId}", c.get)
	r.Patch("/connectors/{connectorId}", c.update)
	r.Delete("/connectors/{connectorId}", c.delete)
	r.Post("/connectors/{connectorId}/test", c.test)
	r.Post("/connectors/{connectorId}/connect", c.connect)
	r.Post("/connectors/{connectorId}/connect/finish", c.finishConnect)
	r.Post("/connectors/{connectorId}/disconnect", c.disconnect)
	r.Get("/sessions/{sessionId}/connectors", c.forSession)
	r.Post("/connectors/proposals", c.propose)
	r.Get("/connectors/proposals/{proposalId}", c.proposal)
	r.Post("/connectors/proposals/{proposalId}/approve", c.approveProposal)
	r.Post("/connectors/proposals/{proposalId}/decline", c.declineProposal)
	r.Get("/sessions/{sessionId}/connectors/proposals", c.sessionProposals)
}

// RegisterRoot mounts the two routes outside /api/v1: the page a sign-in server sends the browser back to, and the address agents call.
func (c *ConnectorsController) RegisterRoot(root chi.Router) {
	root.Get(connectorCallbackPath, c.callback)
	root.HandleFunc("/mcp/s/{sessionId}/connectors/{key}", func(w http.ResponseWriter, r *http.Request) {
		if c.Svc == nil {
			http.NotFound(w, r)
			return
		}
		c.Svc.ServeProxy(w, r, chi.URLParam(r, "sessionId"), chi.URLParam(r, "key"))
	})
	root.Get("/mcp/s/{sessionId}/connectors/{key}/launch", func(w http.ResponseWriter, r *http.Request) {
		if c.Svc == nil {
			http.NotFound(w, r)
			return
		}
		c.Svc.ServeLaunch(w, r, chi.URLParam(r, "sessionId"), chi.URLParam(r, "key"))
	})
}

func (c *ConnectorsController) forSession(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/connectors") {
		return
	}
	used, err := c.Svc.SessionConnectors(r.Context(), chi.URLParam(r, "sessionId"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]SessionConnectorResponse, 0, len(used))
	for _, one := range used {
		out = append(out, SessionConnectorResponse{
			Key: one.Key, Name: one.Name, Transport: string(one.Transport), Status: string(one.Status), Enabled: one.Enabled,
			Calls: one.Calls, LastTool: one.LastTool,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, ListSessionConnectorsResponse{Connectors: out})
}

func (c *ConnectorsController) propose(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors/proposals") {
		return
	}
	var req ConnectorProposalRequest
	if !decodeConnectorBody(w, r, &req) {
		return
	}
	created, err := c.Svc.Propose(r.Context(), connectors.ProposalInput{
		SessionID: req.SessionID, Key: req.Key, Name: req.Name, Transport: connectors.Transport(req.Transport), URL: req.URL,
		Command: req.Command, Args: req.Args, EnvKeys: req.EnvKeys, HeaderKeys: req.HeaderKeys, Auth: connectors.Auth(req.Auth), Reason: req.Reason,
	})
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusCreated, ConnectorProposalEnvelope{Proposal: proposalResponse(&created)})
}

func (c *ConnectorsController) proposal(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodGet, "/api/v1/connectors/proposals/{proposalId}") {
		return
	}
	found, err := c.Svc.GetProposal(r.Context(), chi.URLParam(r, "proposalId"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConnectorProposalEnvelope{Proposal: proposalResponse(&found)})
}

func (c *ConnectorsController) sessionProposals(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodGet, "/api/v1/sessions/{sessionId}/connectors/proposals") {
		return
	}
	pending := c.Svc.SessionProposals(r.Context(), chi.URLParam(r, "sessionId"))
	out := make([]ConnectorProposalResponse, 0, len(pending))
	for i := range pending {
		out = append(out, proposalResponse(&pending[i]))
	}
	envelope.WriteJSON(w, http.StatusOK, ListConnectorProposalsResponse{Proposals: out})
}

func (c *ConnectorsController) approveProposal(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors/proposals/{proposalId}/approve") {
		return
	}
	var req ApproveConnectorProposalRequest
	if !decodeConnectorBody(w, r, &req) {
		return
	}
	id := chi.URLParam(r, "proposalId")
	pending, err := c.Svc.GetProposal(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if rejectLANCommandConnector(w, r, string(pending.Transport)) {
		return
	}
	approved, created, err := c.Svc.ApproveProposal(r.Context(), id, req.ProjectID, req.Values)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ApproveConnectorProposalResponse{Proposal: proposalResponse(&approved), Connector: connectorResponse(&created)})
}

func (c *ConnectorsController) declineProposal(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors/proposals/{proposalId}/decline") {
		return
	}
	declined, err := c.Svc.DeclineProposal(r.Context(), chi.URLParam(r, "proposalId"))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConnectorProposalEnvelope{Proposal: proposalResponse(&declined)})
}

func proposalResponse(p *connectors.Proposal) ConnectorProposalResponse {
	out := ConnectorProposalResponse{
		ID: p.ID, SessionID: p.SessionID, Key: p.Key, Name: p.Name, Transport: string(p.Transport), URL: p.URL,
		Command: p.Command, Args: nonNil(p.Args), EnvKeys: nonNil(p.EnvKeys), HeaderKeys: nonNil(p.HeaderKeys), Auth: string(p.Auth),
		Reason: p.Reason, State: string(p.State), ConnectorKey: p.ConnectorKey, CreatedAt: p.CreatedAt,
	}
	if p.ProjectID != "" {
		out.ProjectID = &p.ProjectID
	}
	return out
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

func (c *ConnectorsController) importCandidates(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodGet, "/api/v1/connectors/import") {
		return
	}
	found, err := c.Svc.ImportCandidates(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]ConnectorImportCandidateResponse, 0, len(found))
	for i := range found {
		one := &found[i]
		out = append(out, ConnectorImportCandidateResponse{
			ID: one.ID, Agent: one.Agent, Source: one.Source, Key: one.Key, Transport: string(one.Transport),
			URL: one.URL, Command: one.Command, Args: one.Args, EnvKeys: one.EnvKeys, HeaderKeys: one.HeaderKeys,
			NeedsValue: one.NeedsValue, ProjectID: one.ProjectID, Exists: one.Exists,
		})
	}
	envelope.WriteJSON(w, http.StatusOK, ListConnectorImportCandidatesResponse{Candidates: out})
}

func (c *ConnectorsController) importChosen(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors/import") {
		return
	}
	var req ImportConnectorsRequest
	if !decodeConnectorBody(w, r, &req) {
		return
	}
	found, err := c.Svc.ImportCandidates(r.Context())
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	var transports []string
	for i := range found {
		if slices.Contains(req.IDs, found[i].ID) {
			transports = append(transports, string(found[i].Transport))
		}
	}
	if rejectLANCommandConnector(w, r, transports...) {
		return
	}
	added, err := c.Svc.Import(r.Context(), req.IDs, req.Values)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	out := make([]ConnectorResponse, 0, len(added))
	for i := range added {
		out = append(out, connectorResponse(&added[i]))
	}
	envelope.WriteJSON(w, http.StatusCreated, ListConnectorsResponse{Connectors: out})
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
	current, getErr := c.Svc.Get(r.Context(), ref)
	if getErr != nil {
		envelope.WriteError(w, r, getErr)
		return
	}
	if req.Enabled != nil && reflect.DeepEqual(req, ConnectorRequest{Enabled: req.Enabled}) {
		if *req.Enabled && rejectLANCommandConnector(w, r, string(current.Transport)) {
			return
		}
		updated, err = c.Svc.SetEnabled(r.Context(), ref, *req.Enabled)
	} else {
		if rejectLANCommandConnector(w, r, req.Transport, string(current.Transport)) {
			return
		}
		in := connectorInput(&req)
		if in.Transport == "" {
			in.Transport, in.URL, in.Auth = current.Transport, cmp.Or(in.URL, current.URL), cmp.Or(in.Auth, current.Auth)
			in.Command, in.PresetID = cmp.Or(in.Command, current.Command), cmp.Or(in.PresetID, current.PresetID)
			if in.Auth == connectors.AuthOAuth {
				in.OAuthClientID = cmp.Or(in.OAuthClientID, current.OwnClientID)
			}
			if in.Args == nil {
				in.Args = current.Args
			}
		}
		if req.ProjectID == nil {
			in.ProjectID = current.ProjectID
		}
		if strings.TrimSpace(req.Name) == "" {
			in.Name = current.Name
		}
		updated, err = c.Svc.Update(r.Context(), ref, in)
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

func (c *ConnectorsController) test(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors/{connectorId}/test") {
		return
	}
	current, err := c.Svc.Get(r.Context(), connectorRef(r))
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if rejectLANCommandConnector(w, r, string(current.Transport)) {
		return
	}
	tested, err := c.Svc.Test(r.Context(), connectorRef(r))
	c.writeOne(w, r, &tested, err)
}

func (c *ConnectorsController) disconnect(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors/{connectorId}/disconnect") {
		return
	}
	out, err := c.Svc.SignOut(r.Context(), connectorRef(r))
	c.writeOne(w, r, &out, err)
}

func (c *ConnectorsController) connect(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors/{connectorId}/connect") {
		return
	}
	ref := connectorRef(r)
	page, err := c.Svc.StartSignIn(r.Context(), ref)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	current, err := c.Svc.Get(r.Context(), ref)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConnectorSignInResponse{
		AuthorizationURL: page, CallbackURL: c.Svc.CallbackURL(), Connector: connectorResponse(&current),
	})
}

func (c *ConnectorsController) finishConnect(w http.ResponseWriter, r *http.Request) {
	if !c.ready(w, r, http.MethodPost, "/api/v1/connectors/{connectorId}/connect/finish") {
		return
	}
	var req FinishConnectorSignInRequest
	if !decodeConnectorBody(w, r, &req) {
		return
	}
	done, err := c.Svc.FinishFromAddress(r.Context(), connectorRef(r), req.CallbackURL)
	c.writeOne(w, r, &done, err)
}

func (c *ConnectorsController) writeOne(w http.ResponseWriter, r *http.Request, one *connectors.Connector, err error) {
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ConnectorEnvelope{Connector: connectorResponse(one)})
}

const connectorCallbackPath = "/oauth/mcp/callback"

var connectorCallbackPage = template.Must(template.New("callback").Parse(`<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Orchestrator</title>
<body style="font: 16px/1.5 -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif; max-width: 34rem; margin: 18vh auto; padding: 0 1.25rem; color: #1d1d1f">
<h1 style="font-size: 1.25rem">{{.Title}}</h1>
<p style="color: #55555a">{{.Detail}}</p>
</body></html>`))

func (c *ConnectorsController) callback(w http.ResponseWriter, r *http.Request) {
	page := struct{ Title, Detail string }{"The sign-in did not complete", "Open Orchestrator and try again."}
	status := http.StatusBadRequest
	if c.Svc != nil {
		query := r.URL.Query()
		done, err := c.Svc.CompleteSignIn(r.Context(), query.Get("state"), query.Get("code"), query.Get("iss"), query.Get("error"))
		if again, ok := c.Svc.SignedInBy(r.Context(), query.Get("state")); ok && err != nil && query.Get("error") == "" {
			done, err = again, nil
		}
		switch {
		case err == nil:
			status = http.StatusOK
			page.Title, page.Detail = "Signed in to "+done.Name, "You can close this tab and go back to Orchestrator."
		case connectorErrorCode(err) == "CONNECTOR_SIGNIN_NOT_FOUND":
			status = http.StatusNotFound
			page.Title = "This sign-in is not open on this computer"
			page.Detail = "If you started it for another machine, copy the address of this page and paste it in Orchestrator."
		default:
			page.Detail = err.Error()
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.WriteHeader(status)
	_ = connectorCallbackPage.Execute(w, page)
}

func connectorErrorCode(err error) string {
	var typed *apierr.Error
	if errors.As(err, &typed) {
		return typed.Code
	}
	return ""
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
		OAuthClientID: req.OAuthClientID, OAuthClientSecret: req.OAuthClientSecret,
	}
	if req.ProjectID != nil {
		in.ProjectID = *req.ProjectID
	}
	return in
}

func connectorResponse(c *connectors.Connector) ConnectorResponse {
	out := ConnectorResponse{
		ID: c.ID, Key: c.Key, Name: c.Name, PresetID: c.PresetID, Transport: string(c.Transport),
		URL: connectors.MaskedURL(c.URL), Command: c.Command, Args: nonNil(c.Args), EnvKeys: nonNil(c.EnvKeys), HeaderKeys: nonNil(c.HeaderKeys),
		Auth: string(c.Auth), OAuthClientID: c.OwnClientID, SignedIn: c.SignedIn,
		Enabled: c.Enabled, Status: string(c.Status), Tools: nonNil(c.Tools),
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

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
