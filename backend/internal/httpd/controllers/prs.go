package controllers

import (
	"encoding/base64"
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
	r.Get("/prs/{id}/attachment-access", c.attachmentAccess)
	r.Get("/prs/{id}/stack", c.stack)
	r.Post("/prs/{id}/stack", c.addToStack)
	r.Post("/prs/{id}/stack/merge", c.mergeStack)
	r.Post("/prs/{id}/details", c.loadDetails)
}

func writeStackResult(w http.ResponseWriter, res prsvc.StackResult) {
	out := GetPRStackResponse{}
	if res.Stack != nil {
		stack := PRStackResponse{Number: res.Stack.Number, URL: res.Stack.URL, BaseBranch: res.Stack.BaseBranch, Open: res.Stack.Open, Layers: []PRStackLayerResponse{}}
		for _, layer := range res.Stack.Layers {
			stack.Layers = append(stack.Layers, PRStackLayerResponse{
				Number:         layer.Number,
				Title:          layer.Title,
				URL:            layer.URL,
				State:          layer.State,
				HeadBranch:     layer.HeadBranch,
				HeadSHA:        layer.HeadSHA,
				CI:             layer.CI,
				ReviewDecision: layer.ReviewDecision,
				Mergeability:   layer.Mergeability,
				NoChecks:       layer.NoChecks,
				SessionID:      layer.SessionID,
			})
		}
		out.Stack = &stack
	}
	if res.Parent != nil {
		out.Parent = &PRStackParentResponse{Number: res.Parent.Number, URL: res.Parent.URL}
	}
	envelope.WriteJSON(w, http.StatusOK, out)
}

func (c *PRsController) stack(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/prs/{id}/stack")
		return
	}
	prID := chi.URLParam(r, "id")
	prURL := strings.TrimSpace(r.URL.Query().Get("prUrl"))
	if !prIDPattern.MatchString(prID) || prURL == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR number and URL are required", nil)
		return
	}
	res, err := c.Svc.Stack(r.Context(), prID, prURL)
	if err != nil {
		writePRError(w, r, err)
		return
	}
	writeStackResult(w, res)
}

func (c *PRsController) addToStack(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/stack")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}
	var in AddPRToStackRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PRURL) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR URL is required", nil)
		return
	}
	res, err := c.Svc.AddToStack(r.Context(), prID, in.PRURL)
	if err != nil {
		writePRError(w, r, err)
		return
	}
	writeStackResult(w, res)
}

// loadDetails reads a tracked PR's details from its provider when none are
// stored, as for a PR that merged before the observer read them.
func (c *PRsController) loadDetails(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/details")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}
	var in LoadPRDetailsRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PRURL) == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR URL is required", nil)
		return
	}
	if err := c.Svc.LoadDetails(r.Context(), prID, in.PRURL); err != nil {
		writePRError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (c *PRsController) mergeStack(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "POST", "/api/v1/prs/{id}/stack/merge")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}
	var in MergePRStackRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if strings.TrimSpace(in.PRURL) == "" || len(in.ExpectedHeads) == 0 {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR URL and expected heads are required", nil)
		return
	}
	switch in.Method {
	case "", "squash", "merge", "rebase":
	default:
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ACTION", "Merge method must be squash, merge, or rebase", nil)
		return
	}
	heads := make([]prsvc.StackHead, 0, len(in.ExpectedHeads))
	for _, head := range in.ExpectedHeads {
		heads = append(heads, prsvc.StackHead{Number: head.Number, HeadSHA: head.HeadSHA})
	}
	res, err := c.Svc.MergeStack(r.Context(), prsvc.StackMergeRequest{PRID: prID, PRURL: in.PRURL, Method: in.Method, ExpectedHeads: heads, AllowNoChecks: in.AllowNoChecks})
	if err != nil {
		writePRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, MergePRStackResponse{
		OK:             true,
		PRNumber:       res.PRNumber,
		Numbers:        res.Numbers,
		Method:         res.Method,
		Status:         res.Status,
		StateChangedAt: res.StateChangedAt,
	})
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
	res, err := c.Svc.Merge(r.Context(), prsvc.MergeRequest{PRID: prID, PRURL: in.PRURL, ExpectedHeadSHA: in.ExpectedHeadSHA, Method: in.Method, AllowNoChecks: in.AllowNoChecks})
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
	// A comment can carry its files, so this route reads a body that large.
	r.Body = http.MaxBytesReader(w, r.Body, maxPRCommentBodyBytes)
	var in PostPRCommentRequest
	if err := decodeJSON(r, &in); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeAttachmentTooLarge(w, r)
			return
		}
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
	attachments := make([]prsvc.CommentAttachment, 0, len(in.Attachments))
	for _, file := range in.Attachments {
		if strings.TrimSpace(file.Name) == "" || file.Data == "" {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ATTACHMENT", "Attachment name and data are required", nil)
			return
		}
		if len(file.Data) > maxPRAttachmentDataChars {
			writeAttachmentTooLarge(w, r)
			return
		}
		data, err := base64.StdEncoding.DecodeString(file.Data)
		if err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ATTACHMENT", "Attachment data must be standard base64", nil)
			return
		}
		attachments = append(attachments, prsvc.CommentAttachment{Name: file.Name, ContentType: file.ContentType, Data: data})
	}
	res, err := c.Svc.PostComment(r.Context(), prsvc.CommentRequest{PRID: prID, PRURL: in.PRURL, Body: in.Body, ThreadID: in.ThreadID, Attachments: attachments})
	if err != nil {
		writePRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PostPRCommentResponse{OK: true, ID: res.ID, URL: res.URL, CreatedAt: res.CreatedAt})
}

// maxPRAttachmentDataChars is the base64 length of the largest allowed
// attachment, so an oversized file is refused before it is decoded.
var maxPRAttachmentDataChars = base64.StdEncoding.EncodedLen(prsvc.MaxAttachmentBytes)

// maxPRCommentBodyBytes bounds a comment's body: all of its files, base64,
// plus headroom for the text, URL, and JSON envelope.
var maxPRCommentBodyBytes = int64(base64.StdEncoding.EncodedLen(prsvc.MaxCommentAttachmentsBytes)) + (1 << 20)

func (c *PRsController) attachmentAccess(w http.ResponseWriter, r *http.Request) {
	if c.Svc == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/prs/{id}/attachment-access")
		return
	}
	prID := chi.URLParam(r, "id")
	if !prIDPattern.MatchString(prID) {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "Invalid PR number", nil)
		return
	}
	prURL := strings.TrimSpace(r.URL.Query().Get("prUrl"))
	if prURL == "" {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_PR", "PR URL is required", nil)
		return
	}
	res, err := c.Svc.AttachmentAccess(r.Context(), prID, prURL)
	if err != nil {
		writePRError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, PRAttachmentAccessResponse{Allowed: res.Allowed, Location: res.Location, Reason: res.Reason})
}

func writeAttachmentTooLarge(w http.ResponseWriter, r *http.Request) {
	envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "ATTACHMENT_TOO_LARGE", "An attachment exceeds 10 MiB, or a comment's attachments exceed 25 MiB together", nil)
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
	case errors.Is(err, prsvc.ErrInvalidAttachment):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_ATTACHMENT", "Invalid attachment", nil)
	case errors.Is(err, prsvc.ErrAttachmentTooLarge):
		writeAttachmentTooLarge(w, r)
	case errors.Is(err, prsvc.ErrAttachmentTypeNotAllowed):
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "ATTACHMENT_TYPE_NOT_ALLOWED", "Attachment type is not allowed", nil)
	case errors.Is(err, prsvc.ErrAttachmentsForbidden):
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "ATTACHMENTS_FORBIDDEN", "No write access to the pull request's repository, where its comments' files are stored", nil)
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
	case errors.Is(err, prsvc.ErrPRStacked):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "PR_STACKED", "This pull request has unmerged layers below it; merge the stack instead", nil)
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
