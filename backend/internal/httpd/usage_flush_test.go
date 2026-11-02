package httpd

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

func TestUsageFlushIsLoopbackControlOnly(t *testing.T) {
	called := 0
	router := NewRouterWithControl(config.Config{}, discardLogger(), nil,
		APIDeps{UsageFlush: func(context.Context) { called++ }}, ControlDeps{})
	for _, tc := range []struct {
		name   string
		host   string
		origin string
		want   int
	}{
		{"desktop main", "127.0.0.1:3000", "", http.StatusNoContent},
		{"browser origin", "127.0.0.1:3000", "https://example.com", http.StatusForbidden},
		{"remote host", "example.com", "", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "http://"+tc.host+"/internal/telemetry/usage-flush", nil)
			if tc.origin != "" {
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d", w.Code, tc.want)
			}
			if tc.want == http.StatusForbidden {
				var body map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil ||
					(body["status"] != "forbidden" && body["error"] != "forbidden") {
					t.Fatalf("forbidden envelope = %q, err=%v", w.Body.String(), err)
				}
			}
		})
	}
	if called != 1 {
		t.Fatalf("flush calls = %d, want 1", called)
	}
	lan := lanControlBlock(router)
	w := httptest.NewRecorder()
	lan.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "http://127.0.0.1:3000/internal/telemetry/usage-flush", nil))
	if w.Code != http.StatusNotFound || called != 1 {
		t.Fatalf("LAN status=%d calls=%d", w.Code, called)
	}
}
