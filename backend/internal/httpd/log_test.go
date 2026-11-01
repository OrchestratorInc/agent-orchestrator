package httpd

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/telemetrymeta"
)

// TestRequestLoggerRecords5xxCause: the wire envelope collapses unrecognized
// service errors into "Internal server error", so the access log line is the
// only place the cause can survive. A 500 must carry it; a typed 4xx (whose
// envelope already explains itself) must not.
func TestRequestLoggerRecords5xxCause(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantInLog string
		absent    bool
	}{
		{name: "raw error on 500 is logged", err: errors.New("gitworktree: worktree remove exploded"), wantInLog: "gitworktree: worktree remove exploded"},
		{name: "typed 404 carries no error attr", err: apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"), wantInLog: "error=", absent: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			log := slog.New(slog.NewTextHandler(&buf, nil))
			sink := &captureSink{}
			handler := requestLogger(log, sink)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				envelope.WriteError(w, r, tc.err)
			}))

			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/x/kill", nil))

			got := buf.String()
			if tc.absent {
				if strings.Contains(got, tc.wantInLog) {
					t.Fatalf("log line unexpectedly contains %q:\n%s", tc.wantInLog, got)
				}
				if len(sink.events) != 0 {
					t.Fatalf("5xx telemetry events = %d, want 0 for typed 4xx", len(sink.events))
				}
				return
			}
			if !strings.Contains(got, tc.wantInLog) {
				t.Fatalf("log line missing %q:\n%s", tc.wantInLog, got)
			}
			if len(sink.events) != 1 || sink.events[0].Name != "ao.http.5xx" {
				t.Fatalf("telemetry events = %#v, want one ao.http.5xx event", sink.events)
			}
			payload := sink.events[0].Payload
			if got := payload["component"]; got != "httpd" {
				t.Fatalf("payload.component = %#v, want httpd", got)
			}
			if got := payload["operation"]; got != "http_request" {
				t.Fatalf("payload.operation = %#v, want http_request", got)
			}
			if got := payload["method"]; got != http.MethodPost {
				t.Fatalf("payload.method = %#v, want POST", got)
			}
			if got := payload["path"]; got != "unmatched" {
				t.Fatalf("payload.path = %#v, want fixed unmatched fallback", got)
			}
			if got := payload["status"]; got != http.StatusInternalServerError {
				t.Fatalf("payload.status = %#v, want 500", got)
			}
			if got := payload["status_family"]; got != "5xx" {
				t.Fatalf("payload.status_family = %#v, want 5xx", got)
			}
			if got := payload["error_kind"]; got != "internal" {
				t.Fatalf("payload.error_kind = %#v, want internal", got)
			}
			if got := payload["fingerprint"]; got == "" {
				t.Fatalf("payload.fingerprint = %#v, want non-empty", got)
			}
			if got, want := payload["fingerprint"], telemetrymeta.Fingerprint("httpd", "http_request", http.MethodPost, "unmatched", "500", "internal", ""); got != want {
				t.Fatalf("PostHog fingerprint = %v, want %s", got, want)
			}
		})
	}
}

func TestRequestLoggerSeparatesStatusWithoutCapturedError(t *testing.T) {
	var fingerprints []string
	handler := requestLoggerWithCapture(slog.Default(), nil, func(_ context.Context, _ error, _ map[string]string, fingerprint string, _ telemetrymeta.SafeRoute, _ telemetrymeta.FailureSite) {
		fingerprints = append(fingerprints, fingerprint)
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("gateway") == "1" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		w.WriteHeader(http.StatusGatewayTimeout)
	}))
	for _, path := range []string{"/failure?gateway=1", "/failure", "/failure?gateway=1"} {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if len(fingerprints) != 3 || fingerprints[0] == fingerprints[1] || fingerprints[0] != fingerprints[2] {
		t.Fatalf("status fingerprints = %v", fingerprints)
	}
}

func TestRequestLoggerGroupsByRouteAndCodeWithoutProjectName(t *testing.T) {
	var fingerprints []string
	router := chi.NewRouter()
	router.Use(requestLoggerWithCapture(slog.Default(), nil, func(_ context.Context, _ error, _ map[string]string, fingerprint string, route telemetrymeta.SafeRoute, _ telemetrymeta.FailureSite) {
		if route.String() != "/api/v1/projects/{projectID}/workspace/git" {
			t.Fatalf("unsafe route = %q", route.String())
		}
		fingerprints = append(fingerprints, fingerprint)
	}))
	router.Get("/api/v1/projects/{projectID}/workspace/git", func(w http.ResponseWriter, r *http.Request) {
		code := "WORKSPACE_GIT_READ_FAILED"
		if r.URL.Query().Get("other") == "1" {
			code = "PROJECT_LOAD_FAILED"
		}
		envelope.WriteError(w, r, apierr.Internal(code, "Unable to read workspace Git state"))
	})
	for _, path := range []string{
		"/api/v1/projects/ai-sports-coach-113/workspace/git",
		"/api/v1/projects/private-repo/workspace/git",
		"/api/v1/projects/private-repo/workspace/git?other=1",
	} {
		router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, path, nil))
	}
	if len(fingerprints) != 3 || fingerprints[0] != fingerprints[1] || fingerprints[0] == fingerprints[2] {
		t.Fatalf("route and code fingerprints = %v", fingerprints)
	}
}

func TestRequestLoggerIncludesBrowserFailureMetadata(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	sink := &captureSink{}
	handler := requestLogger(log, sink)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope.SetTelemetryField(r, "browser_command", "snapshot")
		envelope.WriteError(w, r, apierr.Unavailable("BROWSER_RUNTIME_RECONNECTING", "retry shortly"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/browser/commands", nil))

	if len(sink.events) != 1 {
		t.Fatalf("telemetry events = %#v, want one event", sink.events)
	}
	payload := sink.events[0].Payload
	if got := payload["browser_command"]; got != "snapshot" {
		t.Fatalf("payload.browser_command = %#v, want snapshot", got)
	}
	if got := payload["runtime_link_state"]; got != "reconnecting" {
		t.Fatalf("payload.runtime_link_state = %#v, want reconnecting", got)
	}
}

func TestRequestLoggerPreservesObservedDisconnectedStateOnTimeout(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	sink := &captureSink{}
	handler := requestLogger(log, sink)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		envelope.SetTelemetryField(r, "browser_command", "snapshot")
		envelope.SetTelemetryField(r, "runtime_link_state", "disconnected")
		envelope.WriteError(w, r, apierr.Unavailable("BROWSER_COMMAND_TIMEOUT", "timed out"))
	}))

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/browser/commands", nil))
	if len(sink.events) != 1 {
		t.Fatalf("telemetry events = %#v, want one event", sink.events)
	}
	if got := sink.events[0].Payload["runtime_link_state"]; got != "disconnected" {
		t.Fatalf("runtime_link_state = %#v, want disconnected", got)
	}
}

func TestRequestLoggerSkipsExpectedCodexUnavailableAndCapturesReal500Once(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	sink := &captureSink{}
	captures := 0
	handler := requestLoggerWithCapture(log, sink, func(context.Context, error, map[string]string, string, telemetrymeta.SafeRoute, telemetrymeta.FailureSite) {
		captures++
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/unavailable" {
			envelope.WriteError(w, r, apierr.Unavailable("CODEX_ACCOUNT_MANAGEMENT_UNAVAILABLE", "Codex account management is unavailable"))
			return
		}
		envelope.WriteError(w, r, errors.New("boom"))
	}))
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/unavailable", nil))
	if len(sink.events) != 0 || captures != 0 {
		t.Fatalf("unavailable events=%d captures=%d, want none", len(sink.events), captures)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/real", nil))
	if len(sink.events) != 1 || sink.events[0].Name != "ao.http.5xx" || captures != 1 {
		t.Fatalf("real failure events=%#v captures=%d, want one PostHog event and one Sentry capture", sink.events, captures)
	}
}

func TestRequestLoggerSkipsOnlyExpectedLinkPreviewBadGateway(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	captures := 0
	router := chi.NewRouter()
	router.Use(requestLoggerWithCapture(log, nil, func(_ context.Context, _ error, _ map[string]string, _ string, _ telemetrymeta.SafeRoute, site telemetrymeta.FailureSite) {
		captures++
		if !strings.Contains(site.String(), "TestRequestLoggerSkipsOnlyExpectedLinkPreviewBadGateway") {
			t.Fatalf("failure site = %q", site.String())
		}
	}))
	router.Get("/api/v1/link-preview", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("expected") == "1" {
			envelope.CaptureError(r, apierr.Internal("LINK_PREVIEW_FETCH_FAILED", "Link preview fetch failed"))
		}
		envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "LINK_PREVIEW_FETCH_FAILED", "upstream fetch failed", nil)
	})
	router.Get("/api/v1/other", func(w http.ResponseWriter, r *http.Request) {
		envelope.CaptureError(r, errors.New("other gateway failure"))
		envelope.WriteAPIError(w, r, http.StatusBadGateway, "bad_gateway", "OTHER_GATEWAY_FAILURE", "other gateway failed", nil)
	})
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/link-preview?expected=1&url=https://example.test", nil))
	if captures != 0 {
		t.Fatalf("expected link preview failure captured %d times", captures)
	}
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/link-preview?url=https://example.test", nil))
	if captures != 1 {
		t.Fatalf("unmarked link preview failure captured %d times, want one", captures)
	}
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/other", nil))
	if captures != 2 {
		t.Fatalf("unrelated failure captured %d times, want two", captures)
	}
}

type captureSink struct {
	events []ports.TelemetryEvent
}

func (s *captureSink) Emit(_ context.Context, ev ports.TelemetryEvent) {
	s.events = append(s.events, ev)
}

func (s *captureSink) Close(context.Context) error { return nil }
