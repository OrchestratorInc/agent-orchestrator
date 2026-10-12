package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/logctx"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// recordSessionCreateRequested records an accepted session creation. Repository
// names and prompts are never recorded; the project is referenced by id.
func (s *Server) recordSessionCreateRequested(
	r *http.Request,
	orgID, sessionID, provider, connectionID, projectID, kind, harness string,
	projectSnapshot bool,
) {
	logctx.Annotate(r.Context(), logctx.Fields{Provider: provider, ConnectionID: connectionID})
	attrs := map[string]any{
		"project_id":       projectID,
		"session_kind":     kind,
		"harness":          harness,
		"project_snapshot": projectSnapshot,
	}
	if connectionID != "" {
		attrs["connection_id"] = connectionID
	}
	s.recordActivity(r, domain.ActivityEvent{
		OrgID: orgID, SessionID: sessionID, Kind: domain.ActivitySessionCreateRequested,
		Provider: provider, Attrs: attrs,
	})
}

// recordPlanRefusal records a request the plan refused (no plan, a full slot,
// a usage limit) on the organization's trail. It is called for every store
// error and ignores the ones that are not plan refusals.
func (s *Server) recordPlanRefusal(r *http.Request, err error) {
	if s.activity == nil || err == nil {
		return
	}
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		return
	}
	route := ""
	if routeContext := chi.RouteContext(r.Context()); routeContext != nil {
		route = routeContext.RoutePattern()
	}
	var limitErr *postgres.PlanLimitError
	switch {
	case errors.Is(err, postgres.ErrPlanRequired):
		s.recordActivity(r, domain.ActivityEvent{
			OrgID: orgID, SessionID: chi.URLParam(r, "sessionId"), Kind: domain.ActivityPlanRefused,
			Attrs: map[string]any{"code": "PLAN_REQUIRED", "route": route},
		})
	case errors.As(err, &limitErr):
		attrs := map[string]any{
			"code":  limitErr.Code,
			"plan":  limitErr.Plan,
			"limit": limitErr.Limit,
			"used":  limitErr.Used,
			"route": route,
		}
		if limitErr.ResetsAt != nil {
			attrs["resets_at"] = *limitErr.ResetsAt
		}
		s.recordActivity(r, domain.ActivityEvent{
			OrgID: orgID, SessionID: chi.URLParam(r, "sessionId"), Kind: domain.ActivityLimitHit,
			Attrs: attrs,
		})
	}
}

// coderValidation is the outcome of one BYO Coder connection save, which is
// also its connection test (URL shape, token, reachability via /users/me).
type coderValidation struct {
	result        string
	reason        string
	err           error
	baseURL       string
	connectionID  string
	ownerResolved bool
}

func (s *Server) recordCoderValidation(r *http.Request, orgID string, validation *coderValidation) {
	if s.activity == nil || validation == nil {
		return
	}
	attrs := map[string]any{
		"result":         validation.result,
		"owner_resolved": validation.ownerResolved,
	}
	if validation.reason != "" {
		attrs["reason"] = validation.reason
	}
	if validation.err != nil {
		attrs["error"] = activity.Excerpt(validation.err.Error(), 512)
	}
	if parsed, err := url.Parse(strings.TrimSpace(validation.baseURL)); err == nil && parsed.Host != "" {
		// Host only: a pasted URL may carry credentials or a path.
		attrs["coder_host"] = parsed.Host
	}
	if validation.connectionID != "" {
		attrs["connection_id"] = validation.connectionID
		logctx.Annotate(r.Context(), logctx.Fields{ConnectionID: validation.connectionID})
	}
	s.recordActivity(r, domain.ActivityEvent{
		OrgID: orgID, Kind: domain.ActivityCoderConnectionValidated, Provider: "coder", Attrs: attrs,
	})
}

// recordGitGrantFailure records a worker that could not get repository
// credentials, the usual reason a checkout or credential helper fails.
func (s *Server) recordGitGrantFailure(r *http.Request, claims worker.Claims, purpose, code string, err error) {
	if s.activity == nil {
		return
	}
	attrs := map[string]any{"purpose": purpose, "code": code, "worker_id": claims.WorkerID}
	if err != nil {
		attrs["error"] = activity.Excerpt(err.Error(), 512)
	}
	s.activity.Record(r.Context(), domain.ActivityEvent{
		OrgID: claims.OrgID, SessionID: claims.SessionID, Kind: domain.ActivityGitGrantFailed, Attrs: attrs,
	})
}

// recordCheckoutCompleted records a finished Stripe Checkout. The webhook
// carries the organization as the checkout's client reference.
func (s *Server) recordCheckoutCompleted(ctx context.Context, orgID string) {
	if s.activity == nil || requireUUID(orgID, "orgId") != nil {
		return
	}
	s.activity.Record(ctx, domain.ActivityEvent{OrgID: orgID, Kind: domain.ActivityCheckoutCompleted})
}

// recordPlanChanged records the subscription state a webhook applied.
func (s *Server) recordPlanChanged(ctx context.Context, orgID, status string, limits *domain.PlanLimits) {
	if s.activity == nil {
		return
	}
	attrs := map[string]any{"subscription_status": status}
	if limits != nil {
		attrs["plan"] = limits.Plan
	}
	s.activity.Record(ctx, domain.ActivityEvent{OrgID: orgID, Kind: domain.ActivityPlanChanged, Attrs: attrs})
}
