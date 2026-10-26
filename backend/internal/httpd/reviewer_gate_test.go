package httpd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// TestReviewerGate pins the daemon side of #6842 on the real router: a request
// marked as coming from a reviewer pane may read and submit its review, and
// every other mutation is refused before it reaches a handler.
func TestReviewerGate(t *testing.T) {
	router := newTestRouter(config.Config{}, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	tests := []struct {
		name     string
		method   string
		path     string
		reviewer bool
		upgrade  bool
		blocked  bool
	}{
		{name: "reviewer spawn", method: http.MethodPost, path: "/api/v1/sessions", reviewer: true, blocked: true},
		{name: "reviewer send", method: http.MethodPost, path: "/api/v1/sessions/w-1/send", reviewer: true, blocked: true},
		{name: "reviewer kill", method: http.MethodDelete, path: "/api/v1/sessions/w-1", reviewer: true, blocked: true},
		{name: "reviewer patch", method: http.MethodPatch, path: "/api/v1/sessions/w-1", reviewer: true, blocked: true},
		{name: "reviewer review trigger", method: http.MethodPost, path: "/api/v1/sessions/w-1/reviews", reviewer: true, blocked: true},
		{name: "reviewer shutdown", method: http.MethodPost, path: "/shutdown", reviewer: true, blocked: true},
		{name: "reviewer terminal stream", method: http.MethodGet, path: "/mux", reviewer: true, upgrade: true, blocked: true},
		{name: "reviewer submit lookalike", method: http.MethodPost, path: "/api/v1/sessions/w-1/reviews/submit/extra", reviewer: true, blocked: true},
		{name: "reviewer submit", method: http.MethodPost, path: "/api/v1/sessions/w-1/reviews/submit", reviewer: true},
		{name: "reviewer activity", method: http.MethodPost, path: "/api/v1/reviews/rv-1/activity", reviewer: true},
		{name: "reviewer telemetry", method: http.MethodPost, path: "/internal/telemetry/cli-invoked", reviewer: true},
		{name: "reviewer read", method: http.MethodGet, path: "/api/v1/sessions", reviewer: true},
		{name: "reviewer health", method: http.MethodGet, path: "/healthz", reviewer: true},
		{name: "user spawn unaffected", method: http.MethodPost, path: "/api/v1/sessions"},
		{name: "user shutdown unaffected", method: http.MethodPost, path: "/shutdown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, srv.URL+tt.path, strings.NewReader("{}"))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			if tt.reviewer {
				req.Header.Set(ReviewSessionHeader, "rv-1")
			}
			if tt.upgrade {
				req.Header.Set("Connection", "Upgrade")
				req.Header.Set("Upgrade", "websocket")
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = resp.Body.Close() }()
			var body struct {
				Code string `json:"code"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&body)
			gated := resp.StatusCode == http.StatusForbidden && body.Code == "REVIEWER_ACTION_FORBIDDEN"
			if gated != tt.blocked {
				t.Fatalf("%s %s: status %d code %q, blocked = %v, want %v", tt.method, tt.path, resp.StatusCode, body.Code, gated, tt.blocked)
			}
		})
	}
}
