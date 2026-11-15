package controllers_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/presence"
)

type fakeRoster struct {
	devices []mobilebridge.PushDevice
	deleted []string
}

func (f *fakeRoster) List() []mobilebridge.PushDevice { return f.devices }

func (f *fakeRoster) Delete(installID string) error {
	f.deleted = append(f.deleted, installID)
	return nil
}

type liveSet interface {
	Live() map[string]bool
	LastSeen(installID string) (time.Time, bool)
}

type fakeLive map[string]bool

func (f fakeLive) Live() map[string]bool { return f }

func (f fakeLive) LastSeen(string) (time.Time, bool) { return time.Time{}, false }

func newRosterServer(t *testing.T, roster *fakeRoster, live liveSet) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log,
		nil, httpd.APIDeps{DeviceRoster: roster, DeviceLive: live}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestListDevicesMergesLiveFlag(t *testing.T) {
	now := time.Now().UTC()
	roster := &fakeRoster{devices: []mobilebridge.PushDevice{
		{InstallID: "i1", Token: "ExponentPushToken[a]", DeviceName: "iPhone", CreatedAt: now, LastSeenAt: now},
		{InstallID: "i2", Token: "ExponentPushToken[b]", DeviceName: "M31s", CreatedAt: now, LastSeenAt: now},
	}}
	srv := newRosterServer(t, roster, fakeLive{"i1": true})

	res, err := http.Get(srv.URL + "/api/v1/mobile/devices")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	var env struct {
		Devices []struct {
			InstallID string `json:"installId"`
			Live      bool   `json:"live"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Devices) != 2 {
		t.Fatalf("devices = %d, want 2", len(env.Devices))
	}
	live := map[string]bool{}
	for _, d := range env.Devices {
		live[d.InstallID] = d.Live
	}
	if !live["i1"] {
		t.Fatal("i1 should be live")
	}
	if live["i2"] {
		t.Fatal("i2 should be offline")
	}
}

func TestListDevicesFallsBackToPresenceWhenDeviceLiveOmitted(t *testing.T) {
	now := time.Now().UTC()
	roster := &fakeRoster{devices: []mobilebridge.PushDevice{
		{InstallID: "i1", Token: "ExponentPushToken[a]", DeviceName: "iPhone", CreatedAt: now, LastSeenAt: now},
	}}
	tracker := presence.NewTracker()
	tracker.Touch("i1")

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log,
		nil, httpd.APIDeps{DeviceRoster: roster, Presence: tracker}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	res, err := http.Get(srv.URL + "/api/v1/mobile/devices")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	var env struct {
		Devices []struct {
			InstallID string `json:"installId"`
			Live      bool   `json:"live"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(env.Devices) != 1 || !env.Devices[0].Live {
		t.Fatalf("devices = %+v, want i1 live via the Presence fallback", env.Devices)
	}
}

func TestListDevicesReportsNotificationsEnabled(t *testing.T) {
	now := time.Now().UTC()
	roster := &fakeRoster{devices: []mobilebridge.PushDevice{
		{InstallID: "i1", Token: "ExponentPushToken[a]", DeviceName: "iPhone", CreatedAt: now, LastSeenAt: now},
		{InstallID: "i2", Token: "", DeviceName: "Pixel", CreatedAt: now, LastSeenAt: now},
	}}
	srv := newRosterServer(t, roster, fakeLive{"i1": true, "i2": true})

	res, err := http.Get(srv.URL + "/api/v1/mobile/devices")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}

	var env struct {
		Devices []struct {
			InstallID            string `json:"installId"`
			NotificationsEnabled bool   `json:"notificationsEnabled"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byID := map[string]bool{}
	for _, d := range env.Devices {
		byID[d.InstallID] = d.NotificationsEnabled
	}
	if !byID["i1"] {
		t.Fatal("i1 has a token, want notificationsEnabled true")
	}
	if byID["i2"] {
		t.Fatal("i2 has no token, want notificationsEnabled false")
	}
}

func TestMobileDeviceRoutesUnavailableWithoutRegistry(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log,
		nil, httpd.APIDeps{DeviceRoster: nil, DeviceLive: fakeLive{}}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)

	cases := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{"list", http.MethodGet, "/api/v1/mobile/devices", ""},
		{"remove", http.MethodDelete, "/api/v1/mobile/devices/i1", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}
			req, err := http.NewRequest(tc.method, srv.URL+tc.path, body)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("%s: %v", tc.method, err)
			}
			defer res.Body.Close()
			if res.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, want 503", res.StatusCode)
			}
			var apiErr struct {
				Code string `json:"code"`
			}
			if err := json.NewDecoder(res.Body).Decode(&apiErr); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if apiErr.Code != "DEVICE_REGISTRY_UNAVAILABLE" {
				t.Fatalf("code = %q, want DEVICE_REGISTRY_UNAVAILABLE", apiErr.Code)
			}
		})
	}
}

func TestDeleteDevice(t *testing.T) {
	roster := &fakeRoster{}
	srv := newRosterServer(t, roster, fakeLive{})

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/v1/mobile/devices/i1", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 200/204", res.StatusCode)
	}
	if len(roster.deleted) != 1 || roster.deleted[0] != "i1" {
		t.Fatalf("deleted = %+v, want [i1]", roster.deleted)
	}
}

func TestMuteRouteIsGone(t *testing.T) {
	srv := newRosterServer(t, &fakeRoster{}, fakeLive{})
	req, _ := http.NewRequest(http.MethodPatch, srv.URL+"/api/v1/mobile/devices/i1", strings.NewReader(`{"muted":true}`))
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", res.StatusCode)
	}
}

func TestRosterRoutesBlockedOnLANListener(t *testing.T) {
	for _, path := range []string{"/api/v1/mobile/devices", "/api/v1/mobile/devices/i1"} {
		if !httpd.IsLANControlBlockedPathForTest(path) {
			t.Fatalf("%s must be blocked on the LAN listener — a phone must not read or change the roster", path)
		}
	}
}

func TestRosterMountedRoutesAreLANBlocked(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpd.NewRouterWithControl(config.Config{}, log, nil,
		httpd.APIDeps{DeviceRoster: &fakeRoster{}, DeviceLive: fakeLive{}}, httpd.ControlDeps{})

	checked := 0
	err := chi.Walk(router, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if !strings.HasPrefix(route, "/api/v1/mobile/devices") {
			return nil
		}
		checked++
		concrete := strings.ReplaceAll(route, "{installId}", "i1")
		if !httpd.IsLANControlBlockedPathForTest(concrete) {
			t.Errorf("%s %s is mounted but not blocked on the LAN listener", method, concrete)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk router: %v", err)
	}
	if checked == 0 {
		t.Fatal("no /api/v1/mobile/devices routes were mounted — router wiring changed?")
	}
}

type fakeLiveWithTimes struct {
	live  map[string]bool
	times map[string]time.Time
}

func (f fakeLiveWithTimes) Live() map[string]bool { return f.live }
func (f fakeLiveWithTimes) LastSeen(id string) (time.Time, bool) {
	at, ok := f.times[id]
	return at, ok
}

func TestListPrefersPresenceLastSeenOverStoredValue(t *testing.T) {
	registered := time.Now().UTC().Add(-3 * time.Hour)
	polledJustNow := time.Now().UTC().Add(-5 * time.Second)
	roster := &fakeRoster{devices: []mobilebridge.PushDevice{
		{InstallID: "i1", Token: "ExponentPushToken[a]", DeviceName: "iPhone", CreatedAt: registered, LastSeenAt: registered},
		{InstallID: "i2", Token: "ExponentPushToken[b]", DeviceName: "M31s", CreatedAt: registered, LastSeenAt: registered},
	}}
	presence := fakeLiveWithTimes{
		live:  map[string]bool{},
		times: map[string]time.Time{"i1": polledJustNow},
	}
	srv := newRosterServer(t, roster, presence)

	res, err := http.Get(srv.URL + "/api/v1/mobile/devices")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer res.Body.Close()
	var env struct {
		Devices []struct {
			InstallID  string    `json:"installId"`
			LastSeenAt time.Time `json:"lastSeenAt"`
		} `json:"devices"`
	}
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}

	byID := map[string]time.Time{}
	for _, d := range env.Devices {
		byID[d.InstallID] = d.LastSeenAt
	}
	if !byID["i1"].Equal(polledJustNow) {
		t.Fatalf("i1 lastSeenAt = %v, want the presence timestamp %v", byID["i1"], polledJustNow)
	}
	if !byID["i2"].Equal(registered) {
		t.Fatalf("i2 lastSeenAt = %v, want the stored value %v as fallback", byID["i2"], registered)
	}
}
