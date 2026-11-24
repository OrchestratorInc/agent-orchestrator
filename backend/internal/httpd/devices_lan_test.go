package httpd

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/devices"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type lanDeviceProvider struct{ hub string }

func (p lanDeviceProvider) Name() string { return "local" }
func (p lanDeviceProvider) Platforms(context.Context) []ports.DevicePlatformAvailability {
	return []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformIOS, Available: true}}
}
func (p lanDeviceProvider) EnsureReady(context.Context) error { return nil }
func (p lanDeviceProvider) List(context.Context) ([]ports.DeviceInfo, error) {
	return []ports.DeviceInfo{{ID: "UDID-1", Name: "iPhone", Platform: ports.DevicePlatformIOS, Booted: true}}, nil
}
func (p lanDeviceProvider) Boot(_ context.Context, d ports.DeviceInfo) (ports.DeviceInfo, error) {
	return d, nil
}
func (p lanDeviceProvider) Shutdown(context.Context, ports.DeviceInfo) error        { return nil }
func (p lanDeviceProvider) Install(context.Context, ports.DeviceInfo, string) error { return nil }
func (p lanDeviceProvider) Launch(context.Context, ports.DeviceInfo, string) error  { return nil }
func (p lanDeviceProvider) Screenshot(context.Context, ports.DeviceInfo) ([]byte, error) {
	return []byte("\x89PNG"), nil
}
func (p lanDeviceProvider) Hub(context.Context) (ports.DeviceHubEndpoint, error) {
	return ports.DeviceHubEndpoint{Origin: p.hub, Headers: map[string]string{"Authorization": "Bearer hub-secret"}}, nil
}
func (p lanDeviceProvider) AgentControl(d ports.DeviceInfo, s string) (ports.DeviceAgentControl, error) {
	return ports.DeviceAgentControl{Command: "agent-device", Args: []string{"--session", "ao-" + s}}, nil
}
func (p lanDeviceProvider) ReleaseControl(context.Context, ports.DeviceInfo, string) error {
	return nil
}
func (p lanDeviceProvider) Close() error { return nil }

// A paired phone reaches the session's device the way the AO app does: over
// the password-protected LAN listener, with the bearer and React Native's
// pinned loopback Origin, both for screenshots and for the input socket. A
// phone without the password gets neither.
func TestDevicesReachablePairedPhoneOnly(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		kind, msg, err := conn.Read(r.Context())
		if err == nil {
			_ = conn.Write(r.Context(), kind, msg)
		}
	}))
	t.Cleanup(hub.Close)

	svc := devices.New(devices.Config{DataDir: t.TempDir(), Provider: lanDeviceProvider{hub: hub.URL}})
	svc.SetAvailable(true)
	if _, err := svc.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(context.Background(), "ao-1", devices.OpenRequest{}); err != nil {
		t.Fatal(err)
	}
	router := NewRouterWithControl(config.Config{AllowedOrigins: config.DefaultAllowedOrigins}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		APIDeps{Devices: svc}, ControlDeps{})
	state := &authState{}
	state.setHash(mobilebridge.HashPassword("pairing-secret"))
	lan := httptest.NewServer(lanControlBlock(authMiddleware(state, newLockout(time.Now), nil)(router)))
	t.Cleanup(lan.Close)

	get := func(auth string) int {
		req, _ := http.NewRequest(http.MethodGet, lan.URL+"/api/v1/sessions/ao-1/device/screenshot", nil)
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = res.Body.Close()
		return res.StatusCode
	}
	if code := get("pairing-secret"); code != http.StatusOK {
		t.Fatalf("paired screenshot = %d, want 200", code)
	}
	if code := get(""); code != http.StatusUnauthorized {
		t.Fatalf("unpaired screenshot = %d, want 401", code)
	}

	wsURL := "ws" + strings.TrimPrefix(lan.URL, "http") + "/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/ws?device=UDID-1"
	conn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer pairing-secret"}, "Origin": {"http://localhost"}},
	})
	if err != nil {
		t.Fatalf("paired input socket: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("tap")); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := conn.Read(context.Background()); err != nil || string(msg) != "tap" {
		t.Fatalf("echo = %q, %v; want tap", msg, err)
	}
	if _, res, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://localhost"}}}); err == nil || res == nil || res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unpaired input socket err = %v, want 401", err)
	}
}
