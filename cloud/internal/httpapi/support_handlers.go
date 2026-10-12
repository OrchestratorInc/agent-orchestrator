package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// supportLookupStore is the operator read behind the support endpoint. It is
// an optional capability of the store so test fakes need not implement it.
type supportLookupStore interface {
	SupportLookup(context.Context, postgres.SupportQuery) (postgres.SupportReport, error)
}

// supportLookup answers "what happened to this customer": user accounts by
// email, their organizations, each organization's recent sessions (with
// startup errors) and the activity trail. It is operator-only, gated by the
// AO_CLOUD_SUPPORT_TOKEN bearer, and read-only.
//
//	GET /api/cloud/v1/control/support/lookup?email=...|orgId=...|sessionId=...
//	    [&sessions=20][&activity=100]
func (s *Server) supportLookup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !constantTimeBearer(r.Header.Get("Authorization"), s.supportToken) {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Operator authentication is required.")
		return
	}
	store, ok := s.store.(supportLookupStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Support lookup is not available.")
		return
	}
	query := r.URL.Query()
	request := postgres.SupportQuery{
		Email:         strings.TrimSpace(query.Get("email")),
		OrgID:         strings.TrimSpace(query.Get("orgId")),
		SessionID:     strings.TrimSpace(query.Get("sessionId")),
		SessionLimit:  boundedQueryInt(query.Get("sessions"), 20, 200),
		ActivityLimit: boundedQueryInt(query.Get("activity"), 100, 1000),
	}
	report, err := store.SupportLookup(r.Context(), request)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	// Who looked up what is itself worth keeping: the lookup key is logged as
	// a kind (never the email) alongside the request id.
	lookup := "email"
	switch {
	case request.Email != "":
	case request.OrgID != "":
		lookup = "org"
	case request.SessionID != "":
		lookup = "session"
	}
	s.logger.InfoContext(r.Context(), "operator support lookup", "lookup", lookup,
		"users", len(report.Users), "orgs", len(report.Orgs))
	writeJSON(w, http.StatusOK, report)
}

func boundedQueryInt(raw string, fallback, maximum int) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value <= 0 {
		return fallback
	}
	return min(value, maximum)
}

// recordActivity adds an event to the organization's support trail without
// blocking or failing the request. The authenticated user is filled in when
// the event does not name one.
func (s *Server) recordActivity(r *http.Request, event domain.ActivityEvent) {
	if s.activity == nil {
		return
	}
	if event.UserID == "" {
		event.UserID = principalFrom(r).UserID
	}
	s.activity.Record(r.Context(), event)
}
