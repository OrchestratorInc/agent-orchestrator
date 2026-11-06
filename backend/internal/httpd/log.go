package httpd

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/observe/sentryobs"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// requestLogger emits one structured access-log line per request via the
// daemon's slog logger. Chi's built-in middleware.Logger writes to stdout
// using stdlib log; reusing the daemon's slog keeps every line on stderr in
// the same key=value shape as the rest of the daemon (one stream for the
// Electron supervisor to capture, one format to grep).
//
// Status, bytes, and duration come from a wrapped ResponseWriter so the log
// is accurate even when the handler returns without calling WriteHeader. The
// request id is read off the context populated by middleware.RequestID, so
// this middleware must be mounted after it.
//
// A 5xx line additionally carries the raw service error recorded by
// envelope.WriteError: the wire envelope hides internals ("Internal server
// error"), so without this the cause of a 500 was lost entirely.
func requestLogger(log *slog.Logger, sink ports.EventSink) func(http.Handler) http.Handler {
	return requestLoggerWithCapture(log, sink, sentryobs.CaptureError)
}

type captureHTTPErrorFunc func(context.Context, error, map[string]string, string, telemetrymeta.SafeRoute, telemetrymeta.FailureSite)

func requestLoggerWithCapture(log *slog.Logger, sink ports.EventSink, captureHTTPError captureHTTPErrorFunc) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			r, capturedErr := envelope.WithErrorCapture(r)
			start := time.Now()
			defer func() {
				attrs := []any{
					"id", middleware.GetReqID(r.Context()),
					"method", r.Method,
					"path", r.URL.Path,
					"status", ww.Status(),
					"bytes", ww.BytesWritten(),
					"duration", time.Since(start),
					"remote", r.RemoteAddr,
				}
				captured := capturedErr()
				if captured.Err != nil && ww.Status() >= http.StatusInternalServerError {
					attrs = append(attrs, "error", captured.Err)
				}
				log.Info("http request", attrs...)
				if status := ww.Status(); status >= http.StatusBadRequest && status < http.StatusInternalServerError {
					captureFaultCode(r, status, captured, captureHTTPError)
				}
				if ww.Status() >= http.StatusInternalServerError {
					route := telemetrymeta.RoutePattern(r)
					path := route.String()
					capErr := captured.Err
					var errorKind, errorCode string
					if capErr != nil {
						errorKind, errorCode = telemetrymeta.ErrorKindAndCode(capErr)
					}
					if errorCode == "" {
						errorCode = captured.Code
					}
					errorType := telemetrymeta.ErrorType(capErr)
					// Codex account management is absent for this process. The
					// renderer holds one quiet stream instead of treating that
					// expected condition as a fault. Other 5xx still go to
					// PostHog and, when ShouldCaptureStatus allows, to Sentry once.
					if errorCode == "CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE" {
						return
					}
					// Group the same code or bounded cause type at a registered
					// route together without using request or error text.
					groupCode := errorCode
					if groupCode == "" {
						if errorType != "unknown" {
							groupCode = errorType
						}
					}
					if groupCode == "" {
						groupCode = strconv.Itoa(ww.Status())
					}
					sentryFingerprint := telemetrymeta.Fingerprint("httpd", "http_request", r.Method, path, groupCode)
					posthogFingerprint := telemetrymeta.Fingerprint("httpd", "http_request", r.Method, path, strconv.Itoa(ww.Status()), errorKind, errorCode)
					if sink != nil {
						payload := map[string]any{
							"component":     "httpd",
							"operation":     "http_request",
							"method":        r.Method,
							"path":          path,
							"status":        ww.Status(),
							"status_family": telemetrymeta.StatusFamily(ww.Status()),
							"duration":      time.Since(start).Milliseconds(),
						}
						if capErr != nil {
							payload["error_kind"] = errorKind
							payload["error_type"] = errorType
							if errorCode != "" {
								payload["error_code"] = errorCode
							}
							payload["fingerprint"] = posthogFingerprint
						}
						for key, value := range captured.Fields {
							payload[key] = value
						}
						if r.URL.Path == "/api/v1/browser/commands" {
							runtimeLinkState, _ := captured.Fields["runtime_link_state"].(string)
							switch errorCode {
							case "BROWSER_RUNTIME_RECONNECTING":
								runtimeLinkState = "reconnecting"
							case "BROWSER_RUNTIME_UNAVAILABLE":
								runtimeLinkState = "disconnected"
							}
							if runtimeLinkState == "" {
								runtimeLinkState = "unknown"
							}
							payload["runtime_link_state"] = runtimeLinkState
						}
						sink.Emit(r.Context(), ports.TelemetryEvent{
							Name:       "ao.http.5xx",
							Source:     "http",
							OccurredAt: time.Now().UTC(),
							Level:      ports.TelemetryLevelError,
							RequestID:  middleware.GetReqID(r.Context()),
							Payload:    payload,
						})
					}
					// Sentry keeps safe diagnostic fields and stack frames, but
					// replaces error text before sending.
					// A plain 503 (transient contention) is excluded by
					// ShouldCaptureStatus. A 503 is still captured when its code is
					// a registered fault or its cause is a server-side deadline,
					// because those are faults and hangs, not contention.
					category := "http_5xx"
					captureSentry := sentryobs.ShouldCaptureStatus(ww.Status())
					if !captureSentry {
						switch {
						case sentryobs.IsFaultCode(errorCode):
							captureSentry, category = true, "http_fault"
						case sentryobs.IsServerTimeout(ww.Status(), capErr):
							captureSentry, category = true, "http_timeout"
						}
					}
					if capErr != nil && errorCode == "LINK_PREVIEW_FETCH_FAILED" && ww.Status() == http.StatusBadGateway {
						captureSentry = false
					}
					if captureSentry {
						err := capErr
						if err == nil {
							err = fmt.Errorf("HTTP %d %s %s", ww.Status(), r.Method, path)
						}
						captureHTTPError(r.Context(), err, map[string]string{
							"component":  "httpd",
							"operation":  "http_request",
							"method":     r.Method,
							"path":       path,
							"status":     strconv.Itoa(ww.Status()),
							"category":   category,
							"error_kind": errorKind,
							"error_type": errorType,
							"error_code": errorCode,
							"area":       sentryobs.Area(capErr, errorCode),
						}, sentryFingerprint, route, captured.FailureSite)
					}
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// captureFaultCode sends a registered daemon fault that answered with a 4xx to
// Sentry. Ordinary 4xx (bad input, not found, expected conflicts) stay out:
// only codes listed in sentryobs.IsFaultCode qualify. PostHog's ao.http.5xx is
// untouched; these requests already carry their own product events.
func captureFaultCode(r *http.Request, status int, captured envelope.CapturedError, captureHTTPError captureHTTPErrorFunc) {
	errorKind, errorCode := "", captured.Code
	if captured.Err != nil {
		var code string
		errorKind, code = telemetrymeta.ErrorKindAndCode(captured.Err)
		if code != "" {
			errorCode = code
		}
	}
	if !sentryobs.IsFaultCode(errorCode) {
		return
	}
	route := telemetrymeta.RoutePattern(r)
	path := route.String()
	err := captured.Err
	if err == nil {
		err = fmt.Errorf("HTTP %d %s %s", status, r.Method, path)
	}
	errorType := telemetrymeta.ErrorType(captured.Err)
	captureHTTPError(r.Context(), err, map[string]string{
		"component":  "httpd",
		"operation":  "http_request",
		"method":     r.Method,
		"path":       path,
		"status":     strconv.Itoa(status),
		"category":   "http_fault",
		"error_kind": errorKind,
		"error_type": errorType,
		"error_code": errorCode,
		"area":       sentryobs.Area(captured.Err, errorCode),
	}, telemetrymeta.Fingerprint("httpd", "http_request", r.Method, path, errorCode), route, captured.FailureSite)
}
