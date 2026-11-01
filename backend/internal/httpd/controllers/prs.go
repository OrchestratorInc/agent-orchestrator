package controllers

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	prsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/pr"
)

var prIDPattern = regexp.MustCompile(`^[1-9]\d*$`)

// PRsController owns the /prs action routes.
type PRsController struct {
	Svc prsvc.ActionManager
}

// Register mounts the PR action routes on the supplied router.
func (c *PRsController) Register(r chi.Router) {
	r.Post("/prs/{id}/merge", c.merge)
	r.Post("/prs/{id}/resolve-comments", c.resolveComments)
	r.Post("/prs/{id}/state", c.changeState)
	r.Post("/prs/{id}/comments", c.postComment)
}

func (c *PRsController) merge(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/merge")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}
	var in MergePRRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PRURL) == "" || strings.TrimSpace(in.ExpectedHeadSHA) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR URL and expected head SHA are required", nil)
		return
	}
	switch in.Method {
	case "", "squash", "merge", "rebase":
	default:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ACTION", "Merge method must be squash, merge, or rebase", nil)
		return
	}
	res, err := c.Svc.Merge(r.Context(), prsvc.MergeRequest{PRID: prID, PRURL: in.PRURL, ExpectedHeadSHA: in.ExpectedHeadSHA, Method: in.Method})
	if err != nil {
		writePRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MergePRResponse{
		OK:             true,
		PRNumber:       res.PRNumber,
		Method:         res.Method,
		State:          res.State,
		StateChangedAt: res.StateChangedAt,
	})
}

func (c *PRsController) resolveComments(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/resolve-comments")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}

	// Body is optional: omitting it resolves all unresolved threads.
	var in ResolveCommentsRequest
	if err := decodeJSON(r, &in); err != nil && !isEmptyBody(err) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}

	res, err := c.Svc.ResolveComments(r.Context(), prID, in.CommentIDs)
	if err != nil {
		writePRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ResolveCommentsResponse{OK: true, Resolved: res.Resolved})
}

func (c *PRsController) changeState(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/state")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}
	var in ChangePRStateRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PRURL) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR URL is required", nil)
		return
	}
	switch in.Action {
	case "close", "reopen", "ready", "draft":
	default:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ACTION", "Action must be close, reopen, ready, or draft", nil)
		return
	}
	res, err := c.Svc.ChangeState(r.Context(), prsvc.StateRequest{PRID: prID, PRURL: in.PRURL, Action: in.Action})
	if err != nil {
		writePRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChangePRStateResponse{
		OK:             true,
		PRNumber:       res.PRNumber,
		State:          res.State,
		StateChangedAt: res.StateChangedAt,
	})
}

func (c *PRsController) postComment(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/comments")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}
	var in PostPRCommentRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PRURL) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR URL is required", nil)
		return
	}
	if strings.TrimSpace(in.Body) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_COMMENT", "Comment body is required", nil)
		return
	}
	res, err := c.Svc.PostComment(r.Context(), prsvc.CommentRequest{PRID: prID, PRURL: in.PRURL, Body: in.Body, ThreadID: in.ThreadID})
	if err != nil {
		writePRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PostPRCommentResponse{OK: true, ID: res.ID, URL: res.URL, CreatedAt: res.CreatedAt})
}

// writePRError maps PR sentinel errors to their locked HTTP envelopes,
// falling back to 500 for unexpected failures.
func writePRError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, prsvc.ErrInvalidPR):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR", nil)
	case errors.Is(err, prsvc.ErrInvalidAction):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ACTION", "Invalid PR action", nil)
	case errors.Is(err, prsvc.ErrInvalidComment):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_COMMENT", "Invalid comment", nil)
	case errors.Is(err, prsvc.ErrUnsupported):
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "SCM_UNSUPPORTED", "The PR's SCM provider does not support this action", nil)
	case errors.Is(err, prsvc.ErrInvalidTransition):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "INVALID_STATE_TRANSITION", "PR state does not allow this action", nil)
	case errors.Is(err, prsvc.ErrPRRejected):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "PR_ACTION_REJECTED", "The SCM provider rejected the PR action", nil)
	case errors.Is(err, prsvc.ErrThreadNotFound):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "REVIEW_THREAD_NOT_FOUND", "Review thread is not part of this PR", nil)
	case errors.Is(err, prsvc.ErrPRNotFound):
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "PR_NOT_FOUND", "Unknown PR", nil)
	case errors.Is(err, prsvc.ErrPRNotMergeable):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_NOT_MERGEABLE", "PR is not mergeable", nil)
	case errors.Is(err, prsvc.ErrPRHeadChanged):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_HEAD_CHANGED", "PR head changed; refresh before merging", nil)
	case errors.Is(err, prsvc.ErrPRPreconditions):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "PR_PRECONDITIONS_UNMET", "PR merge preconditions are not met", nil)
	case errors.Is(err, prsvc.ErrNothingToResolve):
		envelope.WriteAPIError(w, r, http.StatusUnprocessableEntity, "unprocessable", "NOTHING_TO_RESOLVE", "No unresolved review threads to resolve", nil)
	default:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "PR_OPERATION_FAILED", "PR operation failed", nil)
	}
}

// isEmptyBody reports whether err signals an absent or empty request body.
// io.ErrUnexpectedEOF means a truncated/malformed body — bad request, not absent.
func isEmptyBody(err error) bool {
	return errors.Is(err, io.EOF)
}
