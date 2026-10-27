package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/worker"
)

// The share page is public, cached nowhere, and hands the share to the
// desktop deep link from the fragment without the server ever seeing it.
func TestAppShareLinkPageIsPublicAndForwardsTheFragment(t *testing.T) {
	server := New(Options{
		Store:            &artifactRouterStore{memoryArtifactRows: newMemoryArtifactRows()},
		LocalAuthEnabled: true,
		WorkerTokens:     worker.NewTokenManager([]byte(strings.Repeat("k", 32))),
		Logger:           slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	recorder := httptest.NewRecorder()
	server.Handler().ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/app/share", nil))

	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 without a bearer token", recorder.Code)
	}
	if got := recorder.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Fatalf("content-type = %q, want text/html", got)
	}
	for header, want := range map[string]string{"Cache-Control": "no-store", "Referrer-Policy": "no-referrer"} {
		if got := recorder.Header().Get(header); got != want {
			t.Fatalf("%s = %q, want %q", header, got, want)
		}
	}
	body := recorder.Body.String()
	for _, want := range []string{`window.location.hash`, `"ao-app://share/"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("page does not contain %s", want)
		}
	}
}
