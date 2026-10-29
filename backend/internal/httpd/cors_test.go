package httpd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

// TestCORS exercises the allowlist boundary on a real router: trusted origins
// get per-origin CORS headers (REST reads and preflights), everything else —
// including the opaque "null" origin and no-Origin CLI traffic — gets none.
func TestCORS(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"app://renderer"}}
	router := newTestRouter(cfg, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	tests := []struct {
		name       string
		method     string
		headers    map[string]string
		wantStatus int
		wantACAO   string
	}{
		{
			name:       "allowed origin gets ACAO",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "app://renderer"},
			wantStatus: http.StatusOK,
			wantACAO:   "app://renderer",
		},
		{
			// A local web server is not an authenticated AO renderer.
			name:       "loopback origin rejected without an allowlist entry",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://localhost:5181"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			name:       "loopback IP origin rejected",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://127.0.0.1:8080"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			name:       "isolated localhost preview origin rejected",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://ao-preview.mfxs2mi.localhost:5181"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			// localhost in the host position of a non-loopback origin must not
			// fool the predicate.
			name:       "lookalike origin rejected",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://localhost.evil.example"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			// Rejected outright, not just denied CORS headers: a missing ACAO
			// hides the response but a "simple" cross-origin POST would still
			// execute the handler on this no-auth daemon.
			name:       "unknown origin is rejected before handlers",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://evil.example"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			name:       "localhost suffix lookalike rejected",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "http://ao-preview.localhost.evil.example"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			name:       "null origin is rejected",
			method:     http.MethodGet,
			headers:    map[string]string{"Origin": "null"},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
		{
			name:       "no origin passes through untouched",
			method:     http.MethodGet,
			headers:    nil,
			wantStatus: http.StatusOK,
			wantACAO:   "",
		},
		{
			name:   "preflight from allowed origin",
			method: http.MethodOptions,
			headers: map[string]string{
				"Origin":                         "app://renderer",
				"Access-Control-Request-Method":  "POST",
				"Access-Control-Request-Headers": "content-type",
			},
			wantStatus: http.StatusNoContent,
			wantACAO:   "app://renderer",
		},
		{
			name:   "preflight from unknown origin is rejected",
			method: http.MethodOptions,
			headers: map[string]string{
				"Origin":                        "http://evil.example",
				"Access-Control-Request-Method": "POST",
			},
			wantStatus: http.StatusForbidden,
			wantACAO:   "",
		},
	}

	client := &http.Client{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, srv.URL+"/healthz", nil)
			if err != nil {
				t.Fatalf("NewRequest: %v", err)
			}
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("%s /healthz: %v", tt.method, err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if got := resp.Header.Get("Access-Control-Allow-Origin"); got != tt.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantACAO)
			}
			if tt.headers["Origin"] != "" && resp.Header.Get("Vary") == "" {
				t.Error("Vary header missing for request with Origin")
			}
		})
	}
}

func TestCodexAccountOriginBoundaryBlocksPreviewAndAllowsRenderers(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"app://renderer", "http://localhost:5173"}}
	router := newTestRouter(cfg, discardLogger(), nil)

	tests := []struct {
		name       string
		method     string
		path       string
		origin     string
		wantStatus int
		wantACAO   string
	}{
		{
			name:       "preview cannot delete an account",
			method:     http.MethodDelete,
			path:       "/api/v1/agents/codex/accounts/account-1",
			origin:     "http://ao-preview.hostile.localhost:5181",
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "packaged renderer can read accounts",
			method:     http.MethodGet,
			path:       "/api/v1/agents/codex/accounts",
			origin:     "app://renderer",
			wantStatus: http.StatusNotImplemented,
			wantACAO:   "app://renderer",
		},
		{
			name:       "configured development renderer can mutate accounts",
			method:     http.MethodDelete,
			path:       "/api/v1/agents/codex/accounts/account-1",
			origin:     "http://localhost:5173",
			wantStatus: http.StatusNotImplemented,
			wantACAO:   "http://localhost:5173",
		},
		{
			name:       "native caller without origin can mutate accounts",
			method:     http.MethodDelete,
			path:       "/api/v1/agents/codex/accounts/account-1",
			wantStatus: http.StatusNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			body := response.Body.String()
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, body)
			}
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != tt.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantACAO)
			}
			if tt.wantStatus == http.StatusForbidden && !strings.Contains(body, `"code":"ORIGIN_FORBIDDEN"`) {
				t.Errorf("forbidden response = %s, want ORIGIN_FORBIDDEN", body)
			}
		})
	}
}

// TestComputerUseOriginBoundaryBlocksPreviewAndAllowsRenderers keeps workspace
// previews, which corsMiddleware admits as loopback content, from driving the
// desktop through Cua Driver or turning on agent computer use.
func TestComputerUseOriginBoundaryBlocksPreviewAndAllowsRenderers(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"app://renderer", "http://localhost:5173"}}
	router := newTestRouter(cfg, discardLogger(), nil)
	const preview = "http://ao-preview.hostile.localhost:17018"

	tests := []struct {
		name       string
		method     string
		path       string
		origin     string
		preflight  bool
		wantStatus int
		wantACAO   string
	}{
		{
			name:       "preview cannot call a tool",
			method:     http.MethodPost,
			path:       "/api/v1/computer-use/call",
			origin:     preview,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "preview preflight for a tool call is refused",
			method:     http.MethodOptions,
			path:       "/api/v1/computer-use/call",
			origin:     preview,
			preflight:  true,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "preview cannot read status",
			method:     http.MethodGet,
			path:       "/api/v1/computer-use/status",
			origin:     preview,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "preview cannot turn on agent computer use",
			method:     http.MethodPatch,
			path:       "/api/v1/settings/computer-use",
			origin:     preview,
			wantStatus: http.StatusForbidden,
		},
		{
			name:       "packaged renderer can call a tool",
			method:     http.MethodPost,
			path:       "/api/v1/computer-use/call",
			origin:     "app://renderer",
			wantStatus: http.StatusNotImplemented,
			wantACAO:   "app://renderer",
		},
		{
			name:       "packaged renderer preflight is answered",
			method:     http.MethodOptions,
			path:       "/api/v1/computer-use/call",
			origin:     "app://renderer",
			preflight:  true,
			wantStatus: http.StatusNoContent,
			wantACAO:   "app://renderer",
		},
		{
			name:       "configured development renderer can read status",
			method:     http.MethodGet,
			path:       "/api/v1/computer-use/status",
			origin:     "http://localhost:5173",
			wantStatus: http.StatusNotImplemented,
			wantACAO:   "http://localhost:5173",
		},
		{
			name:       "native caller without origin can call a tool",
			method:     http.MethodPost,
			path:       "/api/v1/computer-use/call",
			wantStatus: http.StatusNotImplemented,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.path, strings.NewReader(`{"tool":"list_apps"}`))
			req.Header.Set("Content-Type", "application/json")
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.preflight {
				req.Header.Set("Access-Control-Request-Method", http.MethodPost)
				req.Header.Set("Access-Control-Request-Headers", "content-type")
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, req)
			body := response.Body.String()
			if response.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", response.Code, tt.wantStatus, body)
			}
			if got := response.Header().Get("Access-Control-Allow-Origin"); got != tt.wantACAO {
				t.Errorf("Access-Control-Allow-Origin = %q, want %q", got, tt.wantACAO)
			}
			if tt.wantStatus == http.StatusForbidden && !strings.Contains(body, `"code":"ORIGIN_FORBIDDEN"`) {
				t.Errorf("forbidden response = %s, want ORIGIN_FORBIDDEN", body)
			}
		})
	}
}

// TestCORSPreflightHeaders pins the preflight grant shape: methods, echoed
// request headers, max-age, and the private-network opt-in.
func TestCORSPreflightHeaders(t *testing.T) {
	cfg := config.Config{AllowedOrigins: []string{"app://renderer"}}
	router := newTestRouter(cfg, discardLogger(), nil)
	srv := httptest.NewServer(router)
	defer srv.Close()

	req, err := http.NewRequest(http.MethodOptions, srv.URL+"/api/v1/sessions", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Origin", "app://renderer")
	req.Header.Set("Access-Control-Request-Method", "POST")
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	req.Header.Set("Access-Control-Request-Private-Network", "true")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatalf("OPTIONS: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	for header, want := range map[string]string{
		"Access-Control-Allow-Origin":          "app://renderer",
		"Access-Control-Allow-Methods":         "GET, POST, PATCH, PUT, DELETE, OPTIONS",
		"Access-Control-Allow-Headers":         "content-type",
		"Access-Control-Max-Age":               "600",
		"Access-Control-Allow-Private-Network": "true",
	} {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
}

func TestRenderDocumentNavigationBoundary(t *testing.T) {
	for _, tc := range []struct {
		method, path, origin string
		want                 int
	}{
		{"GET", "/api/v1/sessions/s1/renders/r1", "", 204},
		{"HEAD", "/api/v1/sessions/s1/renders/r1?source=1", "", 204},
		{"POST", "/api/v1/sessions/s1/renders/r1", "", 403},
		{"GET", "/api/v1/sessions/s1/renders/check", "", 403},
		{"GET", "/api/v1/sessions/s1/renders/r1/artifact", "", 403},
		{"GET", "/api/v1/sessions/s1/workspace/file/blob", "", 403},
		{"GET", "/api/v1/sessions/s1/renders/r1", "null", 403},
		{"GET", "/api/v1/sessions/s1/renders/r1", "http://evil.localhost", 403},
	} {
		for _, site := range []string{"cross-site", "none"} {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			r.Header.Set("Sec-Fetch-Site", site)
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			corsMiddleware([]string{"app://renderer"})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })).ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Errorf("%s %s origin=%q site=%s: got %d want %d", tc.method, tc.path, tc.origin, site, w.Code, tc.want)
			}
		}
	}
}
