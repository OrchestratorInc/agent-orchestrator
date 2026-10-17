package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

type workerTaskNameStore interface {
	NameWorkerTask(context.Context, string, string, string, int64, domain.TaskNameRequest) (domain.TaskNameResult, error)
}

func (s *Server) workerTaskName(w http.ResponseWriter, r *http.Request) {
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:connect") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:connect scope is required.")
		return
	}
	var input *domain.TaskNameRequest
	if err := decodeJSONLimit(w, r, &input, maxWorkerControlBody); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if input == nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Task naming requires a JSON object.")
		return
	}
	if len(input.Title) > 400 || len(input.Slug) > 100 || len(input.Branch) > 1024 || len(input.Occupied) > 100 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Task naming metadata exceeds its limit.")
		return
	}
	store, ok := s.store.(workerTaskNameStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Task naming is unavailable.")
		return
	}
	result, err := store.NameWorkerTask(r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch, *input)
	if err != nil {
		s.writeWorkerStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Workers predating task naming can retain the original checkout only while
// naming is pending. Once a name is reserved, an upgrade is required to recover
// its filesystem journal and use the committed checkout safely.
func (s *Server) workerNamingCompatibility(w http.ResponseWriter, r *http.Request, launch *domain.WorkerLaunch, claims worker.Claims, supported bool) bool {
	if supported || launch.TaskNamingState == "" || launch.TaskNamingState == domain.TaskNamingDone && launch.TaskNameSlug == "" {
		return true
	}
	if launch.TaskNamingState != domain.TaskNamingPending {
		writeError(w, r, http.StatusConflict, "WORKER_UPGRADE_REQUIRED", "Upgrade the worker to restore this session's named workspace.")
		return false
	}
	store, ok := s.store.(workerTaskNameStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Task naming compatibility is unavailable.")
		return false
	}
	result, err := store.NameWorkerTask(r.Context(), claims.OrgID, claims.SessionID, claims.WorkerID, claims.Epoch, domain.TaskNameRequest{Skip: true})
	if err != nil {
		if errors.Is(err, postgres.ErrConflict) {
			writeError(w, r, http.StatusConflict, "WORKER_UPGRADE_REQUIRED", "Upgrade the worker to restore this session's named workspace.")
			return false
		}
		s.writeWorkerStoreError(w, r, err)
		return false
	}
	// Naming may have advanced after the launch spec was read.
	if result.State != domain.TaskNamingDone || result.Slug != "" {
		writeError(w, r, http.StatusConflict, "WORKER_UPGRADE_REQUIRED", "Upgrade the worker to restore this session's named workspace.")
		return false
	}
	launch.TaskNamingState, launch.TaskNameSlug, launch.Branch = result.State, result.Slug, result.Branch
	return true
}
