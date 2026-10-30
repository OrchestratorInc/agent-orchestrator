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
				if ww.Status() >= http.StatusInternalServerError {
					route := telemetrymeta.RoutePattern(r)
					path := route.String()
					capErr := captured.Err
					var errorKind, errorCode string
					if capErr != nil {
						errorKind, errorCode = telemetrymeta.ErrorKindAndCode(capErr)
					}
					// Codex account management is absent for this process. The
					// renderer holds one quiet stream instead of treating that
					// expected condition as a fault. Other 5xx still go to
					// PostHog and, when ShouldCaptureStatus allows, to Sentry once.
					if errorCode == "CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE" {
						return
					}
					// Group the same error code at a registered route together even
					// when several controller functions can produce it. The caller
					// frame remains available separately as the Sentry culprit.
					groupCode := errorCode
					if groupCode == "" {
						groupCode = errorKind
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
					// 503 (transient contention) is excluded by ShouldCaptureStatus.
					captureSentry := sentryobs.ShouldCaptureStatus(ww.Status())
					if errorCode == "LINK_PREVIEW_FETCH_FAILED" && ww.Status() == http.StatusBadGateway {
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
							"category":   "http_5xx",
							"error_kind": errorKind,
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
