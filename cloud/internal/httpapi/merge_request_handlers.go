package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// mergeRequestStore is the store surface for guests' merge requests.
type mergeRequestStore interface {
	RequestPullRequestMerge(ctx context.Context, principal domain.Principal, orgID, sessionID string, number int, prURL string) (domain.MergeRequest, error)
	ListProjectMergeRequests(ctx context.Context, principal domain.Principal, orgID, projectID string) ([]domain.MergeRequest, error)
	PendingMergeRequestForDecision(ctx context.Context, principal domain.Principal, orgID, requestID string) (domain.MergeRequest, error)
	DecideMergeRequest(ctx context.Context, principal domain.Principal, orgID, requestID, status string) (domain.MergeRequest, error)
}

type mergeRequestResponse struct {
	ID          string                    `json:"id"`
	ProjectID   string                    `json:"projectId"`
	SessionID   string                    `json:"sessionId"`
	SessionName string                    `json:"sessionName"`
	PullRequest mergeRequestPRResponse    `json:"pullRequest"`
	RequestedBy sessionRequesterResponse  `json:"requestedBy"`
	Status      string                    `json:"status"`
	DecidedBy   *sessionRequesterResponse `json:"decidedBy,omitempty"`
	DecidedAt   *time.Time                `json:"decidedAt,omitempty"`
	CreatedAt   time.Time                 `json:"createdAt"`
}

type mergeRequestPRResponse struct {
	ID         string `json:"id"`
	Number     int    `json:"number"`
	URL        string `json:"url"`
	Title      string `json:"title"`
	Repository string `json:"repository"`
}

func toMergeRequestResponse(request domain.MergeRequest) mergeRequestResponse {
	response := mergeRequestResponse{
		ID: request.ID, ProjectID: request.ProjectID, SessionID: request.SessionID, SessionName: request.SessionName,
		PullRequest: mergeRequestPRResponse{
			ID: request.PullRequestID, Number: request.PRNumber, URL: request.PRURL, Title: request.PRTitle, Repository: request.Repository,
		},
		RequestedBy: sessionRequesterResponse{UserID: request.RequestedByUserID, Name: request.RequestedByName},
		Status:      request.Status,
		DecidedAt:   request.DecidedAt,
		CreatedAt:   request.CreatedAt,
	}
	if request.DecidedByUserID != "" {
		response.DecidedBy = &sessionRequesterResponse{UserID: request.DecidedByUserID, Name: request.DecidedByName}
	}
	return response
}

func (s *Server) mergeRequests(w http.ResponseWriter, r *http.Request) (mergeRequestStore, bool) {
	store, ok := s.store.(mergeRequestStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Merge requests are not available.")
	}
	return store, ok
}

// requestPullRequestMerge asks the owner of a shared project to merge one of
// a session's pull requests, for someone it was shared with who may not
// merge themselves.
func (s *Server) requestPullRequestMerge(w http.ResponseWriter, r *http.Request) {
	orgID, sessionID := chi.URLParam(r, "orgId"), chi.URLParam(r, "sessionId")
	number, err := strconv.Atoi(chi.URLParam(r, "number"))
	if err != nil || requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil || number <= 0 {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "A valid organization, session and PR number are required.")
		return
	}
	var input struct {
		PRURL string `json:"prUrl"`
	}
	if err := decodeJSON(w, r, &input); err != nil || strings.TrimSpace(input.PRURL) == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "A PR URL is required.")
		return
	}
	store, ok := s.mergeRequests(w, r)
	if !ok {
		return
	}
	request, err := store.RequestPullRequestMerge(r.Context(), principalFrom(r), orgID, sessionID, number, input.PRURL)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mergeRequest": toMergeRequestResponse(request)})
}

// listProjectMergeRequests lists a project's merge requests for its owner,
// or a guest's own.
func (s *Server) listProjectMergeRequests(w http.ResponseWriter, r *http.Request) {
	orgID, projectID := chi.URLParam(r, "orgId"), chi.URLParam(r, "projectId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(projectID, "projectId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and projectId must be UUIDs.")
		return
	}
	store, ok := s.mergeRequests(w, r)
	if !ok {
		return
	}
	requests, err := store.ListProjectMergeRequests(r.Context(), principalFrom(r), orgID, projectID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	response := make([]mergeRequestResponse, 0, len(requests))
	for _, request := range requests {
		response = append(response, toMergeRequestResponse(request))
	}
	writeJSON(w, http.StatusOK, map[string]any{"mergeRequests": response})
}

// approveMergeRequest merges the requested pull request as the approving
// owner, through the same checks as their own merge, and records it.
func (s *Server) approveMergeRequest(w http.ResponseWriter, r *http.Request) {
	orgID, requestID := chi.URLParam(r, "orgId"), chi.URLParam(r, "requestId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(requestID, "requestId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and requestId must be UUIDs.")
		return
	}
	var input struct {
		ExpectedHeadSHA string `json:"expectedHeadSha"`
	}
	if err := decodeJSON(w, r, &input); err != nil || strings.TrimSpace(input.ExpectedHeadSHA) == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The expected head SHA is required.")
		return
	}
	store, ok := s.mergeRequests(w, r)
	if !ok {
		return
	}
	principal := principalFrom(r)
	pending, err := store.PendingMergeRequestForDecision(r.Context(), principal, orgID, requestID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !s.mergePullRequestAs(w, r, principal, orgID, pending.SessionID, pending.PRNumber, mergePullRequestRequest{
		PRURL: pending.PRURL, ExpectedHeadSHA: input.ExpectedHeadSHA,
	}) {
		return
	}
	decided, err := store.DecideMergeRequest(r.Context(), principal, orgID, requestID, "merged")
	if err != nil {
		// GitHub accepted the merge; the request stays pending until the owner
		// looks again, and the pull request shows merged from its webhook.
		s.logger.Error("record approved merge request", "error", err, "merge_request_id", requestID)
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"mergeRequest": toMergeRequestResponse(decided)})
}

// rejectMergeRequest declines a guest's merge request.
func (s *Server) rejectMergeRequest(w http.ResponseWriter, r *http.Request) {
	orgID, requestID := chi.URLParam(r, "orgId"), chi.URLParam(r, "requestId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(requestID, "requestId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and requestId must be UUIDs.")
		return
	}
	store, ok := s.mergeRequests(w, r)
	if !ok {
		return
	}
	principal := principalFrom(r)
	if _, err := store.PendingMergeRequestForDecision(r.Context(), principal, orgID, requestID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	decided, err := store.DecideMergeRequest(r.Context(), principal, orgID, requestID, "rejected")
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"mergeRequest": toMergeRequestResponse(decided)})
}
