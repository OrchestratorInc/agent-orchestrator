package httpapi

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// chatStartFallbackStore moves a never-started Chat session to the TUI
// (postgres.Store). Optional so test stores need not implement it; without it
// a session keeps the interface it was created with.
type chatStartFallbackStore interface {
	FallBackChatStart(ctx context.Context, orgID, sessionID, code, detail string) (bool, error)
	WorkerFallBackChatStart(ctx context.Context, orgID, sessionID, workerID string, epoch int64, code, detail string) (bool, error)
}

// chatStartFallbackDetails are the notices a session moved off Chat at its
// start shows, by the error code its interface notice carries. They are
// authored here, not by the worker, so every worker's notice reads the same.
var chatStartFallbackDetails = map[string]string{
	worker.ChatStartUnsupported: "Started in the terminal: the AO worker in this workspace is an older " +
		"version that can't set up Chat for Claude. You can switch it to Chat.",
	worker.ChatStartAdapterUnavailable: "Started in the terminal: Chat for Claude needs the Claude ACP " +
		"adapter, which this workspace doesn't have yet.",
}

// startLaunchInTUIIfChatUnsupported keeps a bootstrapping worker that does
// not report worker.CapabilityChatStart from starting a Claude session created
// in Chat. Such a worker starts in the launch interface, but it neither
// installs the Claude ACP adapter nor falls back to the terminal when the
// adapter is missing, so every Chat turn could fail. The session moves to the
// TUI first (FallBackChatStart), and launch says so. Other harnesses start in
// Chat on those workers. It runs only at bootstrap: a live worker reconnecting
// (to refresh its scripts after a Setup retry) keeps the interface it started
// in, so moving the session then would settle a first turn it never runs.
func (s *Server) startLaunchInTUIIfChatUnsupported(r *http.Request, orgID string, launch *domain.WorkerLaunch, startsInChat bool) error {
	if launch.Interface != domain.SessionInterfaceChat || startsInChat || launch.Harness != "claude-code" {
		return nil
	}
	store, ok := s.store.(chatStartFallbackStore)
	if !ok {
		return nil
	}
	moved, err := store.FallBackChatStart(r.Context(), orgID, launch.SessionID,
		worker.ChatStartUnsupported, chatStartFallbackDetails[worker.ChatStartUnsupported])
	if err != nil {
		return err
	}
	if moved {
		launch.Interface = domain.SessionInterfaceTUI
		s.logger.Info("session moved to the terminal: its worker cannot set up Claude Chat",
			"org_id", orgID, "session_id", launch.SessionID, "request_id", requestID(r))
	}
	return nil
}

// workerChatStartFallback is a worker reporting it cannot start the session's
// Chat controller (worker.ChatStartFallbackRequest). A session created in Chat
// that never started moves to the TUI; the response says whether it did, and
// the worker starts the TUI only then.
func (s *Server) workerChatStartFallback(w http.ResponseWriter, r *http.Request) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	var input worker.ChatStartFallbackRequest
	if err := decodeJSONTolerant(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	detail, known := chatStartFallbackDetails[input.Code]
	if !known {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "Unknown Chat start fallback code.")
		return
	}
	store, ok := s.store.(chatStartFallbackStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Chat start fallback is unavailable.")
		return
	}
	moved, err := store.WorkerFallBackChatStart(r.Context(), claims.OrgID, claims.SessionID,
		claims.WorkerID, claims.Epoch, input.Code, detail)
	if err != nil {
		s.writeWorkerStoreError(w, r, err)
		return
	}
	if moved {
		s.logger.Info("session moved to the terminal: its worker cannot start Chat",
			"org_id", claims.OrgID, "session_id", claims.SessionID, "code", input.Code, "request_id", requestID(r))
	}
	writeJSON(w, http.StatusOK, worker.ChatStartFallbackResponse{MovedToTUI: moved})
}
