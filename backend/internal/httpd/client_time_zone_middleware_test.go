package httpd

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/clienttz"
)

func TestClientTimeZoneMiddlewareRecordsHeader(t *testing.T) {
	dir := t.TempDir()
	h := clientTimeZoneMiddleware(clienttz.NewRecorder(dir))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", nil)
	req.Header.Set(clienttz.Header, "Pacific/Chatham")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := clienttz.Read(dir); got != "Pacific/Chatham" {
		t.Fatalf("recorded zone = %q, want Pacific/Chatham", got)
	}

	// Requests without the header leave the recorded zone alone.
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/sessions", nil))
	if got := clienttz.Read(dir); got != "Pacific/Chatham" {
		t.Fatalf("recorded zone = %q after headerless request", got)
	}
}

func TestClientTimeZoneMiddlewareNilRecorder(t *testing.T) {
	h := clientTimeZoneMiddleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status = %d", rec.Code)
	}
}
