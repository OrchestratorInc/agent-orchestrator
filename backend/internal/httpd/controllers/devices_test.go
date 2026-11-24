package controllers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/devices"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type hubOnlyProvider struct{ hub string }

func (p hubOnlyProvider) Name() string { return "local" }
func (p hubOnlyProvider) Platforms(context.Context) []ports.DevicePlatformAvailability {
	return []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformIOS, Available: true}}
}
func (p hubOnlyProvider) EnsureReady(context.Context) error { return nil }
func (p hubOnlyProvider) List(context.Context) ([]ports.DeviceInfo, error) {
	return []ports.DeviceInfo{{ID: "UDID-1", Name: "iPhone", Platform: ports.DevicePlatformIOS, Booted: true}}, nil
}
func (p hubOnlyProvider) Boot(_ context.Context, d ports.DeviceInfo) (ports.DeviceInfo, error) {
	return d, nil
}
func (p hubOnlyProvider) Shutdown(context.Context, ports.DeviceInfo) error { return nil }
func (p hubOnlyProvider) Install(context.Context, ports.DeviceInfo, string) error {
	return nil
}
func (p hubOnlyProvider) Launch(context.Context, ports.DeviceInfo, string) error { return nil }
func (p hubOnlyProvider) Screenshot(context.Context, ports.DeviceInfo) ([]byte, error) {
	return []byte("\x89PNG"), nil
}
func (p hubOnlyProvider) Hub(context.Context) (ports.DeviceHubEndpoint, error) {
	return ports.DeviceHubEndpoint{Origin: p.hub, Headers: map[string]string{"Authorization": "Bearer hub-secret"}}, nil
}
func (p hubOnlyProvider) AgentControl(d ports.DeviceInfo, session string) (ports.DeviceAgentControl, error) {
	return ports.DeviceAgentControl{Command: "/ao/agent-device", Args: []string{"--udid", d.ID, "--session", "ao-" + session}}, nil
}
func (p hubOnlyProvider) ReleaseControl(context.Context, ports.DeviceInfo, string) error {
	return nil
}
func (p hubOnlyProvider) Close() error { return nil }

type fixedCapabilities struct{}

func (fixedCapabilities) Valid(_ domain.SessionID, token, _ string) bool { return token == "good" }

// A session's device streams through the daemon: the hub's token is added on
// the way, AO's credentials are not passed on, only the leased device's
// stream routes are open, and the input socket works.
func TestDevicesAPI_LeaseAndStreamProxy(t *testing.T) {
	var mu sync.Mutex
	var seen []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI()+" auth="+r.Header.Get("Authorization")+" cookie="+r.Header.Get("Cookie"))
		mu.Unlock()
		if strings.HasSuffix(r.URL.Path, "/ws") {
			conn, err := websocket.Accept(w, r, nil)
			if err != nil {
				t.Errorf("accept: %v", err)
				return
			}
			defer func() { _ = conn.CloseNow() }()
			kind, msg, err := conn.Read(r.Context())
			if err == nil {
				err = conn.Write(r.Context(), kind, msg)
			}
			if err != nil {
				t.Errorf("echo: %v", err)
			}
			return
		}
		_, _ = io.WriteString(w, "frame")
	}))
	t.Cleanup(hub.Close)

	svc := devices.New(devices.Config{DataDir: t.TempDir(), Provider: hubOnlyProvider{hub: hub.URL}})
	svc.SetAvailable(true)
	if _, err := svc.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Sessions: newFakeSessionService(), Devices: svc, SessionCapabilities: fixedCapabilities{}}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	do := func(method, path, capability, body string) (*http.Response, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Cookie", "ao_conn=host-password")
		if capability != "" {
			req.Header.Set("X-AO-Browser-Capability", capability)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(res.Body)
		return res, raw
	}

	if res, _ := do(http.MethodPost, "/api/v1/sessions/ao-1/device/open", "stolen", ""); res.StatusCode != http.StatusForbidden {
		t.Fatalf("open with another session's capability = %d, want 403", res.StatusCode)
	}
	if res, _ := do(http.MethodGet, "/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/UDID-1/stream.mjpeg", "", ""); res.StatusCode != http.StatusNotFound {
		t.Fatalf("stream before a lease = %d, want 404", res.StatusCode)
	}
	res, raw := do(http.MethodPost, "/api/v1/sessions/ao-1/device/open", "good", `{"platform":"ios"}`)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("open = %d %s", res.StatusCode, raw)
	}
	var opened controllers.SessionDeviceResponse
	if err := json.Unmarshal(raw, &opened); err != nil || opened.Lease == nil {
		t.Fatalf("open body = %s, %v", raw, err)
	}
	if opened.Lease.Device.ID != "UDID-1" || opened.Lease.Reason == "" {
		t.Fatalf("lease = %+v", opened.Lease)
	}

	if res, body := do(http.MethodGet, "/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/UDID-1/stream.mjpeg", "", ""); res.StatusCode != http.StatusOK || string(body) != "frame" {
		t.Fatalf("stream = %d %q, want the hub's frame", res.StatusCode, body)
	}
	mu.Lock()
	first := seen[0]
	mu.Unlock()
	if first != "/vendor/serve-sim/helper/UDID-1/stream.mjpeg auth=Bearer hub-secret cookie=" {
		t.Fatalf("hub saw %q, want the stream path with the hub token and no AO cookie", first)
	}
	// The phone takes a simulator as H.264 from the same helper.
	if res, body := do(http.MethodGet, "/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/UDID-1/stream.avcc", "", ""); res.StatusCode != http.StatusOK || string(body) != "frame" {
		t.Fatalf("avcc stream = %d %q, want the hub's bytes", res.StatusCode, body)
	}
	for _, tail := range []string{
		"vendor/serve-sim/helper/OTHER/stream.mjpeg",
		"vendor/serve-sim/helper/OTHER/stream.avcc",
		"vendor/serve-sim/helper/ws?device=OTHER",
		"api/devices",
		"vendor/serve-sim/exec",
		"",
	} {
		if res, _ := do(http.MethodGet, "/api/v1/sessions/ao-1/device/hub/"+tail, "", ""); res.StatusCode != http.StatusNotFound {
			t.Fatalf("%q status = %d, want 404 (not this device's stream)", tail, res.StatusCode)
		}
	}

	conn, _, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/ws?device=UDID-1", nil)
	if err != nil {
		t.Fatalf("input socket through the daemon: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	if err := conn.Write(context.Background(), websocket.MessageBinary, []byte("tap")); err != nil {
		t.Fatal(err)
	}
	if _, msg, err := conn.Read(context.Background()); err != nil || string(msg) != "tap" {
		t.Fatalf("echo = %q, %v; want tap", msg, err)
	}

	if res, _ := do(http.MethodPost, "/api/v1/sessions/ao-1/device/close", "good", ""); res.StatusCode != http.StatusOK {
		t.Fatalf("close = %d", res.StatusCode)
	}
	if _, body := do(http.MethodGet, "/api/v1/sessions/ao-1/device", "", ""); strings.TrimSpace(string(body)) != `{"lease":null}` {
		t.Fatalf("lease after close = %s, want null", body)
	}
}

func TestDevicesAPI_OpenNeedsOptIn(t *testing.T) {
	svc := devices.New(devices.Config{DataDir: t.TempDir(), Provider: hubOnlyProvider{hub: "http://127.0.0.1:1"}})
	svc.SetAvailable(true)
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Sessions: newFakeSessionService(), Devices: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	res, err := http.Post(srv.URL+"/api/v1/sessions/ao-1/device/open", "application/json", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusConflict {
		t.Fatalf("open before opt-in = %d, want 409", res.StatusCode)
	}
	status, err := http.Get(srv.URL + "/api/v1/devices")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = status.Body.Close() }()
	var body controllers.DeviceStatusResponse
	if err := json.NewDecoder(status.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Enabled || body.State != "off" || len(body.Platforms) != 1 {
		t.Fatalf("status = %+v, want off with platform availability", body)
	}
}

// A browser <img> needs the last frame sent again once the stream goes quiet; a
// native viewer paints every part it gets and asks for none (repeat=0). The
// parameter is for the daemon and never reaches the hub.
func TestDevicesAPI_StreamRepeatsOnlyForBrowsers(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		queries = append(queries, r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		body := "AAAA"
		_, _ = io.WriteString(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body+"\r\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(hub.Close)
	svc := devices.New(devices.Config{DataDir: t.TempDir(), Provider: hubOnlyProvider{hub: hub.URL}})
	svc.SetAvailable(true)
	if _, err := svc.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Sessions: newFakeSessionService(), Devices: svc, SessionCapabilities: fixedCapabilities{}}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	open, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/ao-1/device/open", strings.NewReader(`{"platform":"ios"}`))
	open.Header.Set("Cookie", "ao_conn=host-password")
	open.Header.Set("X-AO-Browser-Capability", "good")
	if res, err := http.DefaultClient.Do(open); err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("open: %v %v", res, err)
	}

	countParts := func(query string) int {
		ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
		defer cancel()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/UDID-1/stream.mjpeg"+query, nil)
		req.Header.Set("Cookie", "ao_conn=host-password")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(res.Body) // until the deadline
		return strings.Count(string(raw), "--frame")
	}
	if got := countParts(""); got < 2 {
		t.Fatalf("a browser got %d part(s), want the frame repeated once the stream was quiet", got)
	}
	if got := countParts("?repeat=0"); got != 1 {
		t.Fatalf("a native viewer got %d parts, want exactly the hub's one", got)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, q := range queries {
		if strings.Contains(q, "repeat") {
			t.Fatalf("the hub saw query %q, want repeat kept to the daemon", q)
		}
	}
}

// A phone's input socket with pace=1 reaches the hub through the daemon's
// own connection: the hub token is added, pace is not passed on, each touch
// loses its stamp, and a burst of moves goes on at the spacing the phone made
// them (an iPhone's Wi-Fi pause hands them over together).
func TestDevicesAPI_PacedInputReplaysTouchSpacing(t *testing.T) {
	type got struct {
		at   time.Time
		data string
	}
	var mu sync.Mutex
	var uri, auth string
	var msgs []got
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		uri, auth = r.URL.RequestURI(), r.Header.Get("Authorization")
		mu.Unlock()
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer func() { _ = conn.CloseNow() }()
		for {
			_, data, err := conn.Read(r.Context())
			if err != nil {
				return
			}
			mu.Lock()
			msgs = append(msgs, got{time.Now(), string(data)})
			mu.Unlock()
			if strings.Contains(string(data), "home") {
				_ = conn.Write(r.Context(), websocket.MessageText, []byte("pressed"))
			}
		}
	}))
	t.Cleanup(hub.Close)

	svc := devices.New(devices.Config{DataDir: t.TempDir(), Provider: hubOnlyProvider{hub: hub.URL}})
	svc.SetAvailable(true)
	if _, err := svc.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Sessions: newFakeSessionService(), Devices: svc, SessionCapabilities: fixedCapabilities{}}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/v1/sessions/ao-1/device/open", strings.NewReader(`{"platform":"ios"}`))
	req.Header.Set("X-AO-Browser-Capability", "good")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("open: %v %v", res, err)
	}
	_ = res.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+"/api/v1/sessions/ao-1/device/hub/vendor/serve-sim/helper/ws?device=UDID-1&pace=1", nil)
	if err != nil {
		t.Fatalf("paced input socket: %v", err)
	}
	defer func() { _ = conn.CloseNow() }()
	touch := func(phase string, ms int) []byte {
		return append([]byte{0x03}, fmt.Sprintf(`{"type":%q,"x":0.5,"y":0.5,"t":%d}`, phase, ms)...)
	}
	frames := [][]byte{touch("begin", 1000)}
	for i := 1; i <= 5; i++ {
		frames = append(frames, touch("move", 1000+20*i))
	}
	frames = append(frames, touch("end", 1120), append([]byte{0x04}, `{"button":"home"}`...))
	for _, frame := range frames { // all at once, as after a Wi-Fi pause
		if err := conn.Write(ctx, websocket.MessageBinary, frame); err != nil {
			t.Fatal(err)
		}
	}
	if _, reply, err := conn.Read(ctx); err != nil || string(reply) != "pressed" {
		t.Fatalf("hub reply = %q, %v", reply, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if uri != "/vendor/serve-sim/helper/ws?device=UDID-1" || auth != "Bearer hub-secret" {
		t.Fatalf("hub saw %q auth %q, want no pace and the hub token", uri, auth)
	}
	if len(msgs) != len(frames) {
		t.Fatalf("hub got %d messages, want %d", len(msgs), len(frames))
	}
	for i, m := range msgs {
		if strings.Contains(m.data, `"t"`) {
			t.Fatalf("message %d kept its stamp: %q", i, m.data)
		}
	}
	if !strings.Contains(msgs[0].data, "begin") || !strings.Contains(msgs[6].data, "end") || !strings.Contains(msgs[7].data, "home") {
		t.Fatalf("order changed: %+v", msgs)
	}
	for i := 1; i <= 6; i++ {
		if gap := msgs[i].at.Sub(msgs[i-1].at); gap < 15*time.Millisecond {
			t.Fatalf("message %d came %v after the last, want the phone's ~20 ms", i, gap)
		}
	}
}

// Devices start unavailable on every daemon boot. The desktop app's Developer
// mode turns them on and off through PUT /devices/availability; while off, the
// status says so and device work answers DEVICES_UNAVAILABLE.
func TestDeviceAvailabilityFollowsDeveloperMode(t *testing.T) {
	svc := devices.New(devices.Config{DataDir: t.TempDir(), Provider: hubOnlyProvider{hub: "http://127.0.0.1:1"}})
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Sessions: newFakeSessionService(), Devices: svc, SessionCapabilities: fixedCapabilities{}}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	do := func(method, path, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Cookie", "ao_conn=host-password")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		raw, _ := io.ReadAll(res.Body)
		return res.StatusCode, raw
	}
	available := func() bool {
		t.Helper()
		code, raw := do(http.MethodGet, "/api/v1/devices", "")
		var st controllers.DeviceStatusResponse
		if code != http.StatusOK || json.Unmarshal(raw, &st) != nil {
			t.Fatalf("status = %d %s", code, raw)
		}
		return st.Available
	}

	if available() {
		t.Fatal("devices available before the desktop turned them on")
	}
	if code, raw := do(http.MethodPost, "/api/v1/devices/setup", ""); code != http.StatusConflict || !strings.Contains(string(raw), "DEVICES_UNAVAILABLE") {
		t.Fatalf("setup while unavailable = %d %s, want 409 DEVICES_UNAVAILABLE", code, raw)
	}
	if code, raw := do(http.MethodPut, "/api/v1/devices/availability", `{}`); code != http.StatusBadRequest {
		t.Fatalf("availability without enabled = %d %s, want 400", code, raw)
	}
	if code, raw := do(http.MethodPut, "/api/v1/devices/availability", `{"enabled":true}`); code != http.StatusOK {
		t.Fatalf("turn on = %d %s", code, raw)
	}
	if !available() {
		t.Fatal("devices not available after turning on")
	}
	if code, raw := do(http.MethodPost, "/api/v1/devices/setup", ""); code != http.StatusAccepted {
		t.Fatalf("setup = %d %s", code, raw)
	}
	if code, raw := do(http.MethodPut, "/api/v1/devices/availability", `{"enabled":false}`); code != http.StatusOK {
		t.Fatalf("turn off = %d %s", code, raw)
	}
	if code, raw := do(http.MethodPost, "/api/v1/sessions/ao-1/device/open", `{"platform":"ios"}`); code != http.StatusConflict || !strings.Contains(string(raw), "DEVICES_UNAVAILABLE") {
		t.Fatalf("open while unavailable = %d %s, want 409 DEVICES_UNAVAILABLE", code, raw)
	}
	if code, raw := do(http.MethodPost, "/api/v1/sessions/ao-1/device/close", `{}`); code >= 300 {
		t.Fatalf("close while unavailable = %d %s, want success", code, raw)
	}
}
