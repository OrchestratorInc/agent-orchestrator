package daemon

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

func TestPublishRemoteHostAddressUsesScopedDigestWithoutRedirects(t *testing.T) {
	hash := strings.Repeat("a", 64)
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/cloud/v1/remote-hosts/h_test/address" || r.Header.Get("Authorization") != "Bearer "+hash {
			t.Errorf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("X-AO-Host-Signature"), ".") {
			t.Errorf("unsigned publish: %q", r.Header.Get("X-AO-Host-Signature"))
		}
		var input struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.URL != "https://new.example.com:443" {
			t.Errorf("unexpected address: %+v (%v)", input, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := publishRemoteHostAddress(context.Background(), srv.Client(), srv.URL, "h_test", "https://new.example.com:443", hash, filepath.Join(t.TempDir(), "mobile", "config.json")); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("address was not published")
	}
}

type fakeQuickTunnel struct{}

func (fakeQuickTunnel) Start(int) {}
func (fakeQuickTunnel) Stop()     {}
func (fakeQuickTunnel) Endpoint() *mobilebridge.TunnelEndpoint {
	return &mobilebridge.TunnelEndpoint{Ready: true, Hostname: "quick.trycloudflare.com"}
}
func (fakeQuickTunnel) Status() mobilebridge.TunnelStatus { return mobilebridge.TunnelStatus{} }

// AO Cloud answers one 401 for a link no account lists and for a listed one
// whose signature it does not accept, so status reports a refused link, not a
// missing listing. A link that then reaches AO Cloud nudges the publisher
// through remote access, so the refusal clears without waiting out its
// backoff, even at the same address and token (#7976).
func TestAddressPublisherReportsARefusedLinkUntilALinkNudgesIt(t *testing.T) {
	path := mobilebridge.Path(t.TempDir())
	if err := mobilebridge.Save(path, mobilebridge.State{Enabled: true, Password: "secret12", AccountTokenHash: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	listed := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if !listed {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":"unauthorized"}`))
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	none := func() []string { return nil }
	bridge := &controllers.BridgeService{
		LAN: &fakeLAN{started: true, port: 3011}, ConfigPath: path, HostID: "h_test",
		Tunnel: fakeQuickTunnel{}, PickLANHosts: none, PickTailscaleHosts: none,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go runRemoteHostAddressPublisher(ctx, bridge, srv.URL, slog.New(slog.NewTextHandler(io.Discard, nil)))

	waitFor := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for bridge.Status().AccountLinkRefused != want {
			if time.Now().After(deadline) {
				t.Fatalf("accountLinkRefused never became %v", want)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitFor(true)
	mu.Lock()
	listed = true
	mu.Unlock()
	if _, err := bridge.StartRemoteAccess(); err != nil {
		t.Fatal(err)
	}
	waitFor(false)
}
