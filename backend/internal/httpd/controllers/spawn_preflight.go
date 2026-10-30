package controllers

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/spawnpreflight"
)

// SpawnPreflighter is the controller-facing contract for spawn pre-flight.
type SpawnPreflighter interface {
	Check(ctx context.Context, req spawnpreflight.Request) (spawnpreflight.Report, error)
}

// SpawnPreflightController owns POST /api/v1/sessions/preflight.
type SpawnPreflightController struct {
	Svc SpawnPreflighter
}

// Register mounts the pre-flight route.
func (c *SpawnPreflightController) Register(r chi.Router) {
	r.Post("/sessions/preflight", c.check)
}

func (c *SpawnPreflightController) check(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, http.MethodPost, "/api/v1/sessions/preflight")
		return
	}
	var in SpawnPreflightRequest
	if err := decodeJSONStrict(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	report, err := c.Svc.Check(r.Context(), in)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, report)
}
