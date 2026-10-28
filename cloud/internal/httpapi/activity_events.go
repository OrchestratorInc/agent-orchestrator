package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/logctx"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
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
	ready := map[string]any{
		"phase":            "create",
		"startup_attempts": readiness.StartupAttempts,
		"session_kind":     readiness.SessionKind,
		"epoch":            epoch,
	}
	// A resume is timed from its own start, never from the session's creation
	// (days for a session woken from an idle pause); without one it has no ttr.
	if readiness.PreviousWorkerSeenAt != nil || readiness.WorkerConnectedBefore {
		ready["phase"] = "resume"
		if readiness.StartupStartedAt != nil {
			ready["ttr_ms"] = time.Since(*readiness.StartupStartedAt).Milliseconds()
		}
	} else {
		ready["ttr_ms"] = time.Since(readiness.SessionCreatedAt).Milliseconds()
	}
	if readiness.ConnectionID != "" {
		ready["connection_id"] = readiness.ConnectionID
	}
	s.activity.Record(r.Context(), domain.ActivityEvent{
		OrgID: orgID, SessionID: sessionID, Kind: domain.ActivitySandboxReady,
		Provider: readiness.Provider, Attrs: ready,
	})
}

// recordWorkspaceReady records a worker's startup timings. They are the
// worker's own report, so only well-formed values are kept.
func (s *Server) recordWorkspaceReady(r *http.Request, claims worker.Claims, startup worker.WorkspaceStartupTimings) {
	if s.activity == nil {
		return
	}
	attrs := map[string]any{"epoch": claims.Epoch, "first_start": startup.FirstStart, "self_updated": startup.SelfUpdated}
	for name, value := range map[string]int64{
		"connected_ms": startup.ConnectedMS, "checkout_ms": startup.CheckoutMS, "setup_ms": startup.SetupMS,
	} {
		if value >= 0 && value <= (24*time.Hour).Milliseconds() {
			attrs[name] = value
		}
	}
	switch startup.Checkout {
	case worker.CheckoutFromSnapshot, worker.CheckoutCloned, worker.CheckoutExisting, worker.CheckoutNone:
		attrs["checkout"] = startup.Checkout
	}
	s.activity.Record(r.Context(), domain.ActivityEvent{
		OrgID: claims.OrgID, SessionID: claims.SessionID, Kind: domain.ActivityWorkspaceReady, Attrs: attrs,
	})
}
