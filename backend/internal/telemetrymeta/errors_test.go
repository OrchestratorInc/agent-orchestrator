package telemetrymeta

import (
	"context"
	"errors"
	"fmt"
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

func TestRoutePatternPrefersChiPattern(t *testing.T) {
	var got string
	r := chi.NewRouter()
	r.Get("/api/v1/projects/{projectID}/sessions/{sessionID}", func(w http.ResponseWriter, req *http.Request) {
		got = RoutePattern(req)
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

type codeError int

func (e codeError) Error() string { return "sqlite" }
func (e codeError) Code() int     { return int(e) }

func TestTransientCause(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"busy", codeError(5), "sqlite_busy"},
		{"busy extended", fmt.Errorf("wrap: %w", codeError(261)), "sqlite_busy"},
		{"locked", codeError(6), "sqlite_locked"},
		{"deadline", fmt.Errorf("wrap: %w", context.DeadlineExceeded), "deadline"},
		{"constraint", codeError(19), ""},
		{"canceled", context.Canceled, ""},
		{"nil", nil, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TransientCause(tc.err); got != tc.want {
				t.Fatalf("TransientCause = %q, want %q", got, tc.want)
			}
		})
	}
}
