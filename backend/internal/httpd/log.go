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

func requestLogger(log *slog.Logger, sink ports.EventSink) func(http.Handler) http.Handler {
	return requestLoggerWithCapture(log, sink, sentryobs.CaptureErrorAsync)
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
					if errorCode == "CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE" {
						return
					}
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
					if errorCode == "CODEX_ACCOUNT_SETUP_INCOMPLETE" {
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
