package envelope

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/ownership"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

type errCapture struct{ captured CapturedError }

type CapturedError struct {
	Err            error
	Code           string
	ReportingOwner ownership.Owner
	Fields         map[string]any
	FailureSite    telemetrymeta.FailureSite
}

type errCaptureKey struct{}

var safeTelemetryFields = map[string]struct{}{
	"browser_command":    {},
	"runtime_link_state": {},
}

func WithErrorCapture(r *http.Request) (*http.Request, func() CapturedError) {
	capture := &errCapture{}
	req := r.WithContext(context.WithValue(r.Context(), errCaptureKey{}, capture))
	return req, func() CapturedError { return capture.captured }
}

func captureError(r *http.Request, err error) {
	if c, ok := r.Context().Value(errCaptureKey{}).(*errCapture); ok {
		c.captured.Err = err
		c.captured.FailureSite = telemetrymeta.CurrentFailureSite()
		c.captured.ReportingOwner = ownership.OwnerOf(err)
	}
}

func CaptureError(r *http.Request, err error) {
	captureError(r, err)
}

func SetTelemetryField(r *http.Request, key string, value any) {
	c, ok := r.Context().Value(errCaptureKey{}).(*errCapture)
	if !ok || c == nil {
		return
	}
	if _, allowed := safeTelemetryFields[key]; !allowed {
		return
	}
	if c.captured.Fields == nil {
		c.captured.Fields = make(map[string]any)
	}
	c.captured.Fields[key] = value
}

type APIError struct {
	Error          string          `json:"error"`
	Code           string          `json:"code"`
	Message        string          `json:"message"`
	RequestID      string          `json:"requestId,omitempty"`
	Details        map[string]any  `json:"details,omitempty"`
	ReportingOwner ownership.Owner `json:"reporting_owner,omitempty" enum:"http"`
}

func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func WriteAPIError(w http.ResponseWriter, r *http.Request, status int, kind, code, message string, details map[string]any) {
	if status >= http.StatusInternalServerError {
		if c, ok := r.Context().Value(errCaptureKey{}).(*errCapture); ok {
			c.captured.Code = code
			if c.captured.FailureSite.String() == "" {
				c.captured.FailureSite = telemetrymeta.CurrentFailureSite()
			}
		}
	}
	writeAPIError(w, r, status, kind, code, message, details, "")
}

func writeAPIError(w http.ResponseWriter, r *http.Request, status int, kind, code, message string, details map[string]any, reportingOwner ownership.Owner) {
	WriteJSON(w, status, newAPIError(r, kind, code, message, details, reportingOwner))
}

func newAPIError(r *http.Request, kind, code, message string, details map[string]any, reportingOwner ownership.Owner) APIError {
	return APIError{
		Error:          kind,
		Code:           code,
		Message:        message,
		RequestID:      middleware.GetReqID(r.Context()),
		Details:        details,
		ReportingOwner: reportingOwner,
	}
}

func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	status, body := errorEnvelope(r, err)
	WriteJSON(w, status, body)
}

func StreamError(r *http.Request, err error) APIError {
	_, body := errorEnvelope(r, err)
	return body
}

func errorEnvelope(r *http.Request, err error) (int, APIError) {
	captureError(r, err)
	reportingOwner := ownership.OwnerOf(err)
	body := func(status int, kind, code, message string, details map[string]any) (int, APIError) {
		return status, newAPIError(r, kind, code, message, details, reportingOwner)
	}
	var e *apierr.Error
	if errors.As(err, &e) {
		status, kind := httpStatus(e.Kind)
		return body(status, kind, e.Code, e.Message, e.Details)
	}
	if isTransient(err) {
		return body(http.StatusServiceUnavailable, "unavailable", "SERVICE_UNAVAILABLE",
			"The service is momentarily unavailable, please retry.", nil)
	}
	if errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		return body(http.StatusNotFound, "not_found", "PROJECT_FOLDER_MISSING",
			"Project repository is missing from disk", nil)
	}
	return body(http.StatusInternalServerError, "internal", "INTERNAL_ERROR", "Internal server error", nil)
}

func WriteErrorWithCode(w http.ResponseWriter, r *http.Request, err error, code, message string) {
	var typed *apierr.Error
	if errors.As(err, &typed) || isTransient(err) || errors.Is(err, ports.ErrWorkspaceRepoUnavailable) {
		WriteError(w, r, err)
		return
	}
	WriteError(w, r, apierr.InternalCause(code, message, err))
}

const (
	sqliteBusy   = 5
	sqliteLocked = 6
)

func isTransient(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var coder interface{ Code() int }
	if errors.As(err, &coder) {
		c := coder.Code()
		return c == sqliteBusy || c == sqliteLocked || c&0xFF == sqliteBusy || c&0xFF == sqliteLocked
	}
	return false
}

func httpStatus(k apierr.Kind) (int, string) {
	switch k {
	case apierr.KindInvalid:
		return http.StatusBadRequest, "bad_request"
	case apierr.KindNotFound:
		return http.StatusNotFound, "not_found"
	case apierr.KindConflict:
		return http.StatusConflict, "conflict"
	case apierr.KindForbidden:
		return http.StatusForbidden, "forbidden"
	case apierr.KindTooManyRequests:
		return http.StatusTooManyRequests, "too_many_requests"
	case apierr.KindNotImplemented:
		return http.StatusNotImplemented, "not_implemented"
	case apierr.KindUnavailable:
		return http.StatusServiceUnavailable, "unavailable"
	case apierr.KindInternal:
		return http.StatusInternalServerError, "internal"
	default:
		return http.StatusInternalServerError, "internal"
	}
}
