package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func pairingDaemon(t *testing.T, requests *[]string) {
	t.Helper()
	cfg := setConfigEnv(t)
	requireDeviceCredentials := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.HasPrefix(r.URL.Path, "/api/v1/") {
			*requests = append(*requests, strings.TrimSpace(r.Method+" "+r.URL.Path+" "+string(body)))
		}
		w.Header().Set("Content-Type", "application/json")
		status := func() {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"enabled": true, "hostId": "host-a", "password": "pairing-secret", "requireDeviceCredentials": requireDeviceCredentials,
				"endpoints": []map[string]any{{"kind": "tunnel", "host": "h-1.useao.dev", "port": 443, "secure": true}},
			})
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/mobile/status", "POST /api/v1/mobile/enable-lan-only", "POST /api/v1/mobile/regenerate":
			status()
		case "POST /api/v1/mobile/require-device-credentials":
			var req struct{ Enabled bool }
			_ = json.Unmarshal(body, &req)
			requireDeviceCredentials = req.Enabled
			status()
		case "POST /api/v1/mobile/pairing-code":
			_ = json.NewEncoder(w).Encode(map[string]any{"code": "Code1234", "expiresAt": time.Now().Add(10 * time.Minute)})
		case "GET /api/v1/mobile/devices":
			_ = json.NewEncoder(w).Encode(map[string]any{"devices": []map[string]any{
				{"id": "dev_1", "kind": "device", "deviceName": "Dhruv's iPhone", "live": true},
				{"id": "account", "kind": "account", "lastSeenAt": "2026-11-10T09:00:00Z"},
				{"id": "install-old", "kind": "password", "deviceName": "Old phone"},
			}})
		case "DELETE /api/v1/mobile/devices/dev_1", "DELETE /api/v1/mobile/devices/account", "DELETE /api/v1/mobile/devices/install-old":
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	writeRunFileFor(t, cfg, srv)
}

func runRemoteHost(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, append([]string{"remote-host"}, args...)...)
	return out, err
}

func TestRemoteHostEnableAndPairPrintAOneTimeCode(t *testing.T) {
	var requests []string
	pairingDaemon(t, &requests)
	for _, verb := range []string{"enable", "pair"} {
		out, err := runRemoteHost(t, verb)
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{"Address: https://h-1.useao.dev:443", "Pairing code: Code1234", "within 10 minutes"} {
			if !strings.Contains(out, want) {
				t.Fatalf("%s output %q missing %q", verb, out, want)
			}
		}
		if strings.Contains(out, "pairing-secret") {
			t.Fatalf("%s printed the pairing password: %q", verb, out)
		}
	}
}

func TestRemoteHostEnablePrintsThePasswordOnlyWithoutPairingCodes(t *testing.T) {
	for name, tc := range map[string]struct {
		status       int
		body         string
		wantPassword bool
	}{
		"no such route":   {http.StatusNotFound, `{"code":"NOT_FOUND"}`, true},
		"not implemented": {http.StatusNotImplemented, `{"code":"DEVICE_PAIRING"}`, true},
		"server error":    {http.StatusInternalServerError, `{"code":"DEVICE_PAIRING","message":"boom"}`, false},
		"empty code":      {http.StatusOK, `{"code":""}`, false},
	} {
		t.Run(name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/api/v1/mobile/pairing-code" {
					w.WriteHeader(tc.status)
					_, _ = io.WriteString(w, tc.body)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "hostId": "host-a", "password": "pairing-secret"})
			}))
			t.Cleanup(srv.Close)
			writeRunFileFor(t, cfg, srv)
			out, err := runRemoteHost(t, "enable")
			if (err == nil) != tc.wantPassword || strings.Contains(out, "pairing-secret") != tc.wantPassword {
				t.Fatalf("out=%q err=%v", out, err)
			}
		})
	}
}

func TestRemoteHostDevicesListsAndRevokes(t *testing.T) {
	var requests []string
	pairingDaemon(t, &requests)
	out, err := runRemoteHost(t, "devices")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"dev_1", "Dhruv's iPhone", "own token", "connected", "Account devices", "account token (shared)", "Old phone", "pairing password", "never"} {
		if !strings.Contains(out, want) {
			t.Fatalf("devices output %q missing %q", out, want)
		}
	}

	for id, want := range map[string]string{
		"dev_1":       "Revoked Dhruv's iPhone.",
		"account":     "Unlinked the account",
		"install-old": "still holds the pairing password",
	} {
		requests = nil
		out, err := runRemoteHost(t, "devices", "revoke", id)
		if err != nil || !strings.Contains(out, want) {
			t.Fatalf("revoke %s: %q %v, want %q", id, out, err, want)
		}
		if got := requests[len(requests)-1]; got != "DELETE /api/v1/mobile/devices/"+id {
			t.Fatalf("revoke %s sent %q", id, got)
		}
	}

	requests = nil
	if _, err := runRemoteHost(t, "devices", "revoke", "dev_missing"); err == nil || !strings.Contains(err.Error(), "no device with ID") {
		t.Fatalf("revoke of an unknown device: %v", err)
	}
	for _, request := range requests {
		if strings.HasPrefix(request, "DELETE") {
			t.Fatalf("an unknown device still sent %q", request)
		}
	}
	for _, args := range [][]string{{"devices", "revoke"}, {"devices", "revoke", "dev_1", "--all"}} {
		if _, err := runRemoteHost(t, args...); err == nil {
			t.Fatalf("%v succeeded", args)
		}
	}

	requests = nil
	out, err = runRemoteHost(t, "devices", "revoke", "--all")
	if err != nil || !strings.Contains(out, "Revoked every device") || strings.Contains(out, "pairing-secret") {
		t.Fatalf("revoke --all: %q %v", out, err)
	}
	if len(requests) != 1 || requests[0] != "POST /api/v1/mobile/regenerate" {
		t.Fatalf("revoke --all requests = %v", requests)
	}
}

func TestRemoteHostRequireDeviceCredentials(t *testing.T) {
	var requests []string
	pairingDaemon(t, &requests)
	out, err := runRemoteHost(t, "require-device-credentials", "on")
	if err != nil || !strings.Contains(out, "no longer accepted") {
		t.Fatalf("on: %q %v", out, err)
	}
	if got := requests[len(requests)-1]; got != `POST /api/v1/mobile/require-device-credentials {"enabled":true}` {
		t.Fatalf("on sent %q", got)
	}
	for _, args := range [][]string{{"status"}, {"status", "--show-password"}, {"enable"}} {
		out, err := runRemoteHost(t, args...)
		if err != nil || !strings.Contains(out, "The pairing password is off") || strings.Contains(out, "pairing-secret") {
			t.Fatalf("%v with the password off: %q %v", args, out, err)
		}
	}
	out, err = runRemoteHost(t, "require-device-credentials", "off")
	if err != nil || !strings.Contains(out, "accepted again") {
		t.Fatalf("off: %q %v", out, err)
	}
	if _, err := runRemoteHost(t, "require-device-credentials", "maybe"); err == nil {
		t.Fatal("an invalid value was accepted")
	}
}
