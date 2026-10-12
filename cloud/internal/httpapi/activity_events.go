package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/logctx"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// sandboxReadinessStore is the optional store capability behind the
// sandbox.ready time-to-ready measurement.
type sandboxReadinessStore interface {
	SandboxReadiness(ctx context.Context, orgID, sessionID string) (postgres.SandboxReadiness, error)
}

var errReadinessUnavailable = errors.New("sandbox readiness is not available from this store")

func (s *Server) sandboxReadiness(ctx context.Context, orgID, sessionID string) (postgres.SandboxReadiness, error) {
	if s.activity == nil {
		return postgres.SandboxReadiness{}, errReadinessUnavailable
	}
	store, ok := s.store.(sandboxReadinessStore)
	if !ok {
		return postgres.SandboxReadiness{}, errReadinessUnavailable
	}
	return store.SandboxReadiness(ctx, orgID, sessionID)
}

// recordWorkerReady records a worker registration: worker.connected always,
// and sandbox.ready with the time from the session's creation (first start)
// or from the resume request (a later start) to this moment, which is when
// the worker is up, authenticated and about to send its first heartbeat.
func (s *Server) recordWorkerReady(
	r *http.Request,
	orgID, sessionID, workerID, version string,
	epoch int64,
	readiness postgres.SandboxReadiness,
	readinessErr error,
) {
	if s.activity == nil {
		return
	}
	logctx.Annotate(r.Context(), logctx.Fields{
		Provider:     readiness.Provider,
		ConnectionID: readiness.ConnectionID,
	})
	connected := map[string]any{
		"worker_id":      workerID,
		"epoch":          epoch,
		"worker_version": version,
	}
	if readiness.ConnectionID != "" {
		connected["connection_id"] = readiness.ConnectionID
	}
	s.activity.Record(r.Context(), domain.ActivityEvent{
		OrgID: orgID, SessionID: sessionID, Kind: domain.ActivityWorkerConnected,
		Provider: readiness.Provider, Attrs: connected,
	})
	if readinessErr != nil {
		if !errors.Is(readinessErr, errReadinessUnavailable) {
			s.logger.WarnContext(r.Context(), "read sandbox readiness", "error", readinessErr)
		}
		return
	}
	now := time.Now()
	phase := "create"
	since := readiness.SessionCreatedAt
	if readiness.PreviousWorkerSeenAt != nil {
		phase = "resume"
		if readiness.StartupStartedAt != nil {
			since = *readiness.StartupStartedAt
		}
	}
	ready := map[string]any{
		"phase":            phase,
		"ttr_ms":           now.Sub(since).Milliseconds(),
		"startup_attempts": readiness.StartupAttempts,
		"session_kind":     readiness.SessionKind,
		"epoch":            epoch,
	}
	if readiness.ConnectionID != "" {
		ready["connection_id"] = readiness.ConnectionID
	}
	s.activity.Record(r.Context(), domain.ActivityEvent{
		OrgID: orgID, SessionID: sessionID, Kind: domain.ActivitySandboxReady,
		Provider: readiness.Provider, Attrs: ready,
	})
}
