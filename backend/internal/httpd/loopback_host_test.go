package httpd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
)

func TestLoopbackRequestHost(t *testing.T) {
	for _, tc := range []struct {
		host string
		want bool
	}{
		{"", true},
		{"127.0.0.1", true},
		{"127.0.0.1:3001", true},
		{"127.8.9.10:3001", true},
		{"[::1]:3001", true},
		{"::1", true},
		{"localhost", true},
		{"LOCALHOST:3001", true},
		{"localhost.:3001", true},
		{"events-1.localhost:3001", true},
		{"ao-1.preview.localhost:3001", true},
		{"attacker.example:3001", false},
		{"attacker.example", false},
		{"localhost.attacker.example:3001", false},
		{"attackerlocalhost:3001", false},
		{"192.168.1.20:3001", false},
		{"0.0.0.0:3001", false},
		{"[::ffff:192.168.1.20]:3001", false},
	} {
		if got := loopbackRequestHost(tc.host); got != tc.want {
			t.Errorf("loopbackRequestHost(%q) = %v, want %v", tc.host, got, tc.want)
		}
	}
}

// A DNS-rebound page reaches the loopback listener with its own domain in Host
// and, for a same-origin GET, no Origin header. The CORS boundary lets origin-less
// preview-file reads through, so the Host guard must stop them first.
func TestLoopbackHostGuardRejectsRebindingRequest(t *testing.T) {
	reached := false
	h := loopbackHostGuard(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "http://attacker.example:3001/api/v1/sessions/ao-1/preview/files/.env", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || reached {
		t.Fatalf("rebinding request: status %d, reached handler %v; want 403 and not reached", rec.Code, reached)
	}

	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:3001/healthz", nil)
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !reached {
		t.Fatalf("loopback request: status %d, reached handler %v; want 200 and reached", rec.Code, reached)
	}
}

// The loopback listener applies the guard; the handler shared with the LAN
// listener does not, because LAN and tunnel requests carry other hosts.
func TestServerGuardsLoopbackHostOnly(t *testing.T) {
	cfg := config.Config{
		Host:            "127.0.0.1",
		Port:            0,
		ShutdownTimeout: 5 * time.Second,
		RunFilePath:     filepath.Join(t.TempDir(), "running.json"),
	}
	srv, err := NewWithDeps(cfg, discardLogger(), nil, APIDeps{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		<-runErr
	})
	base := "http://" + srv.Addr().String()
	waitForHealth(t, base)

	req, err := http.NewRequest(http.MethodGet, base+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "attacker.example"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET with foreign Host: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("loopback listener with foreign Host: status %d, want 403", resp.StatusCode)
	}

	lanReq := httptest.NewRequest(http.MethodGet, "http://192.168.1.20:3001/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, lanReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("shared LAN handler with LAN Host: status %d, want 200", rec.Code)
	}
}
