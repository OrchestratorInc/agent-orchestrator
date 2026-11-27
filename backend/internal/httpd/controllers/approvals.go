package controllers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/approval"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/caller"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/httpd/envelope"
)

// ApprovalService is the approval gate as the app sees it: pending requests,
// the user's decisions, policy, and the audit log.
type ApprovalService interface {
	Pending() []approval.Pending
	Subscribe() (<-chan approval.Event, func())
	Decide(id string, decision approval.Decision) error
	Audit(limit int) ([]approval.AuditEntry, error)
	SessionMode(sessionID string) approval.Mode
	SetSessionMode(sessionID string, mode approval.Mode) error
	DefaultMode() approval.Mode
	SetDefaultMode(mode approval.Mode) error
	SiteRules() []approval.SiteRule
	RemoveSiteRule(site, category string) error
}

// ApprovalsController serves the approval prompt, policy, and audit log.
type ApprovalsController struct {
	Svc ApprovalService
}

const maxAuditLimit = 500

// Register adds the REST routes.
func (c *ApprovalsController) Register(r chi.Router) {
	r.Get("/approvals", c.list)
	r.Post("/approvals/{id}/decision", c.decide)
	r.Get("/approvals/audit", c.audit)
	r.Get("/approvals/policy", c.policy)
	r.Put("/approvals/policy", c.setPolicy)
	r.Delete("/approvals/sites", c.removeSite)
}

// RegisterStream adds the long-lived event stream outside the REST timeout.
func (c *ApprovalsController) RegisterStream(r chi.Router) {
	r.Get("/approvals/stream", c.stream)
}

// fromAgent reports a request sent by an agent's own CLI. Approving or
// loosening the gate is the user's call, so those requests are refused. Agents
// share the user's machine, so this guards against an agent (or a page that
// prompt-injects it) approving itself through the normal client; it is not a
// boundary against arbitrary local processes.
func fromAgent(r *http.Request) bool {
	return caller.From(r.Context()) != "" || r.Header.Get(caller.Header) != "" || r.Header.Get(browserCapabilityHeader) != ""
}

func writeFromAgent(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "APPROVAL_REQUIRES_USER",
		"Only the user can answer approval prompts or change approval settings, from the Orchestrator app", nil)
}

func (c *ApprovalsController) list(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/approvals")
		return
	}
	// Agents never see request ids: with one, a header-less call could
	// answer the agent's own request.
	if fromAgent(r) {
		writeFromAgent(w, r)
		return
	}
	pending := c.Svc.Pending()
	out := ApprovalListResponse{Pending: make([]ApprovalPendingResponse, 0, len(pending))}
	for _, p := range pending {
		out.Pending = append(out.Pending, approvalPendingResponse(p))
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *ApprovalsController) decide(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/approvals/{id}/decision")
		return
	}
	if fromAgent(r) {
		writeFromAgent(w, r)
		return
	}
	var in ApprovalDecisionRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	id := chi.URLParam(r, "id")
	err := c.Svc.Decide(id, approval.Decision(in.Decision))
	switch {
	case errors.Is(err, approval.ErrInvalidDecision):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_DECISION", err.Error(), nil)
		return
	case errors.Is(err, approval.ErrSiteRuleNotAllowed):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "APPROVAL_SITE_RULE_NOT_ALLOWED", "This kind of action can only be allowed once or denied", nil)
		return
	case errors.Is(err, approval.ErrNotPending):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "APPROVAL_NOT_PENDING", "This request already ended: it was answered, timed out, or the agent stopped waiting", nil)
		return
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "APPROVAL_DECISION_FAILED", "Could not save the decision", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ApprovalDecisionResponse{ID: id, Decision: in.Decision})
}

func (c *ApprovalsController) audit(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/approvals/audit")
		return
	}
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > maxAuditLimit {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_LIMIT", fmt.Sprintf("limit must be 1-%d", maxAuditLimit), nil)
			return
		}
		limit = n
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("sessionId"))
	read := limit
	if sessionID != "" {
		// Filter a wider window so a busy log still yields this session's rows.
		read = maxAuditLimit * 4
	}
	entries, err := c.Svc.Audit(read)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "APPROVAL_AUDIT_UNREADABLE", "Could not read the approval log", nil)
		return
	}
	out := ApprovalAuditResponse{Entries: make([]ApprovalAuditEntryResponse, 0, min(len(entries), limit))}
	for _, entry := range entries {
		if sessionID != "" && entry.SessionID != sessionID {
			continue
		}
		out.Entries = append(out.Entries, ApprovalAuditEntryResponse{
			ID: entry.ID, Time: entry.Time, Kind: entry.Kind, SessionID: entry.SessionID, Site: entry.Site, URL: entry.URL,
			Action: entry.Action, Category: entry.Category, Reason: entry.Reason, Decision: string(entry.Decision), DecidedAt: entry.DecidedAt,
		})
		if len(out.Entries) == limit {
			break
		}
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *ApprovalsController) policy(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/approvals/policy")
		return
	}
	envelope.WriteJSON(w, http.StatusOK, c.policyResponse(strings.TrimSpace(r.URL.Query().Get("sessionId"))))
}

func (c *ApprovalsController) setPolicy(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPut, "/api/v1/approvals/policy")
		return
	}
	if fromAgent(r) {
		writeFromAgent(w, r)
		return
	}
	var in ApprovalPolicyRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	in.SessionID = strings.TrimSpace(in.SessionID)
	set := func(mode approval.Mode) error { return c.Svc.SetSessionMode(in.SessionID, mode) }
	if in.SessionID == "" {
		set = c.Svc.SetDefaultMode
	}
	if err := set(approval.Mode(in.Mode)); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_MODE", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, c.policyResponse(in.SessionID))
}

func (c *ApprovalsController) removeSite(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodDelete, "/api/v1/approvals/sites")
		return
	}
	if fromAgent(r) {
		writeFromAgent(w, r)
		return
	}
	site := strings.TrimSpace(r.URL.Query().Get("site"))
	category := strings.TrimSpace(r.URL.Query().Get("category"))
	if site == "" || category == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "SITE_RULE_REQUIRED", "site and category are required", nil)
		return
	}
	if err := c.Svc.RemoveSiteRule(site, category); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "APPROVAL_POLICY_UNWRITABLE", "Could not save approval settings", nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, c.policyResponse(""))
}

func (c *ApprovalsController) policyResponse(sessionID string) ApprovalPolicyResponse {
	defaultMode := string(c.Svc.DefaultMode())
	out := ApprovalPolicyResponse{Mode: defaultMode, DefaultMode: defaultMode, Sites: []ApprovalSiteRuleResponse{}}
	if sessionID != "" {
		out.Mode = string(c.Svc.SessionMode(sessionID))
	}
	for _, rule := range c.Svc.SiteRules() {
		out.Sites = append(out.Sites, ApprovalSiteRuleResponse{Site: rule.Site, Category: rule.Category, CreatedAt: rule.CreatedAt})
	}
	return out
}

func (c *ApprovalsController) stream(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodGet, "/api/v1/approvals/stream")
		return
	}
	// Agents never see request ids: with one, a header-less call could
	// answer the agent's own request.
	if fromAgent(r) {
		writeFromAgent(w, r)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "SSE_UNSUPPORTED", "Streaming is not supported by this server", nil)
		return
	}
	events, unsubscribe := c.Svc.Subscribe()
	defer unsubscribe()
	h := w.Header()
	h.Set("Content-Type", "text/event-stream; charset=utf-8")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if _, err := fmt.Fprint(w, "event: ready\ndata: {}\n\n"); err != nil {
		return
	}
	flusher.Flush()
	heartbeat := time.NewTicker(10 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-heartbeat.C:
			if _, err := fmt.Fprint(w, "event: heartbeat\ndata: {}\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case <-r.Context().Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			name, payload := "approval_resolved", any(ApprovalDecisionResponse{ID: event.ID, Decision: string(event.Decision)})
			if event.Type == approval.EventRequested && event.Pending != nil {
				name, payload = "approval_requested", approvalPendingResponse(*event.Pending)
			}
			data, err := json.Marshal(payload)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func approvalPendingResponse(p approval.Pending) ApprovalPendingResponse {
	return ApprovalPendingResponse{
		ID: p.ID, Kind: p.Kind, SessionID: p.SessionID, Site: p.Site, URL: p.URL, PageTitle: p.PageTitle,
		Action: p.Action, Category: p.Category, Reason: p.Reason, Element: p.Element, Image: p.Image,
		CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt,
	}
}
