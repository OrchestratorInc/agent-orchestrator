package httpapi

import (
	"context"
	"net/http"

	"github.com/aoagents/agent-orchestrator/backend/pkg/contract"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// workerReviewTargetStore resolves a worker-requested review without a user
// principal. Same narrow-interface pattern as the orchestration stores.
type workerReviewTargetStore interface {
	WorkerReviewTarget(context.Context, string, string) (string, []string, []domain.PullRequest, error)
}

// workerTriggerReviews starts AO reviews of the calling session's open pull
// requests, as the Reviews panel does, so `ao review trigger` in a sandbox
// opens the same reviewer terminal the desktop shows.
func (s *Server) workerTriggerReviews(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	claims := workerFrom(r)
	if !worker.HasScope(claims, "worker:git") {
		writeError(w, r, http.StatusForbidden, "SCOPE_REQUIRED", "The worker:git scope is required.")
		return
	}
	targets, ok := s.store.(workerReviewTargetStore)
	if s.reviewService == nil || !ok {
		writeError(w, r, http.StatusServiceUnavailable, "SCM_BROKER_UNAVAILABLE", "Starting a review is not available.")
		return
	}
	reviewerHarness, available, prs, err := targets.WorkerReviewTarget(r.Context(), claims.OrgID, claims.SessionID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !containsString(available, reviewerHarness) {
		writeError(w, r, http.StatusUnprocessableEntity, "REVIEWER_HARNESS_UNAVAILABLE", "The selected reviewer harness is not connected for this session.")
		return
	}
	response := worker.TriggerReviewResponse{Reviews: []worker.TriggeredReview{}}
	startedRunIDs := make([]string, 0, len(prs))
	for _, pr := range prs {
		if pr.Draft || pr.State != contract.PRStateOpen || pr.HeadSHA == "" {
			continue
		}
		run, created, err := s.reviewService.TriggerReview(r.Context(), claims.OrgID, claims.SessionID, reviewerHarness, pr)
		if err != nil {
			s.logger.Error("trigger worker review", "error", err, "request_id", requestID(r), "pull_request_id", pr.ID)
			if len(startedRunIDs) > 0 {
				if _, rollbackErr := s.reviewService.CancelReviewRuns(r.Context(), claims.OrgID, claims.SessionID, startedRunIDs); rollbackErr != nil {
					s.logger.Error("rollback worker review batch", "error", rollbackErr, "request_id", requestID(r))
				}
			}
			writeError(w, r, http.StatusBadGateway, "REVIEW_FAILED", "The review could not be started.")
			return
		}
		if created {
			startedRunIDs = append(startedRunIDs, run.ID)
		}
		response.Reviews = append(response.Reviews, worker.TriggeredReview{Number: pr.Number, URL: pr.URL, Started: created})
	}
	status := http.StatusOK
	if len(startedRunIDs) > 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, response)
}
