package telemetrymeta

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func TestErrorKindAndCode(t *testing.T) {
	kind, code := ErrorKindAndCode(apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"))
	if kind != "not_found" || code != "SESSION_NOT_FOUND" {
		t.Fatalf("typed error = (%q, %q), want (not_found, SESSION_NOT_FOUND)", kind, code)
	}

	kind, code = ErrorKindAndCode(errors.New("boom"))
	if kind != "internal" || code != "" {
		t.Fatalf("raw error = (%q, %q), want (internal, empty)", kind, code)
	}
}

func TestSafeErrorAndPanicKinds(t *testing.T) {
	for _, kind := range []apierr.Kind{
		apierr.KindInternal, apierr.KindInvalid, apierr.KindNotFound,
		apierr.KindConflict, apierr.KindForbidden, apierr.KindTooManyRequests,
		apierr.KindNotImplemented, apierr.KindUnavailable,
	} {
		if !IsErrorKind(ErrorKind(kind)) {
			t.Fatalf("produced error kind %v was rejected", kind)
		}
	}
	for _, value := range []any{errors.New("private panic"), "private panic", 7} {
		if !IsPanicKind(PanicKind(value)) {
			t.Fatalf("produced panic kind %T was rejected", value)
		}
	}
	if IsErrorKind("private_project") || IsPanicKind("private_session") {
		t.Fatal("dynamic error or panic kind was accepted")
	}
}

func TestRoutePatternPrefersChiPattern(t *testing.T) {
	var got string
	r := chi.NewRouter()
	r.Get("/api/v1/projects/{projectID}/sessions/{sessionID}", func(w http.ResponseWriter, req *http.Request) {
		got = RoutePattern(req).String()
		w.WriteHeader(http.StatusNoContent)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/mer/sessions/sess-1", nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got != "/api/v1/projects/{projectID}/sessions/{sessionID}" {
		t.Fatalf("route pattern = %q, want chi route pattern", got)
	}
}

func TestRoutePatternNeverFallsBackToRequestPath(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects/PrivateProjectOrSession_Z9", nil)
	if got := RoutePattern(req).String(); got != "unmatched" {
		t.Fatalf("unmatched route = %q, want fixed fallback", got)
	}
}

func TestFingerprintStableForSameInputs(t *testing.T) {
	first := Fingerprint("httpd", "http_request", "GET", "/api/v1/projects/{projectID}", "5xx", "internal")
	second := Fingerprint("httpd", "http_request", "GET", "/api/v1/projects/{projectID}", "5xx", "internal")
	other := Fingerprint("httpd", "http_request", "POST", "/api/v1/projects/{projectID}", "5xx", "internal")

	if first == "" || len(first) != 16 {
		t.Fatalf("fingerprint = %q, want 16-char digest", first)
	}
	if first != second {
		t.Fatalf("fingerprints differ for same inputs: %q vs %q", first, second)
	}
	if first == other {
		t.Fatalf("fingerprints should differ for different inputs: %q vs %q", first, other)
	}
}
