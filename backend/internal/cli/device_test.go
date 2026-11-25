package cli

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type deviceCapture struct {
	method, path, body, capability string
}

func deviceServer(t *testing.T, status int, respBody string) (*httptest.Server, *deviceCapture) {
	t.Helper()
	capture := &deviceCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if !strings.HasPrefix(r.URL.Path, "/api/v1/") {
			w.WriteHeader(http.StatusNoContent) // CLI telemetry, not a device call
			return
		}
		capture.method, capture.path, capture.body = r.Method, r.URL.Path, string(raw)
		capture.capability = r.Header.Get(browserCapabilityHeader)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, respBody)
	}))
	t.Cleanup(srv.Close)
	return srv, capture
}

const leaseJSON = `{"lease":{"sessionId":"aa-47","provider":"local","reason":"Runs on this machine",
"device":{"id":"UDID-1","name":"iPhone 17 Pro","platform":"ios","version":"iOS 26.5","booted":true},
"agentDevice":{"command":"/ao/devices/leases/aa-47/agent-device","args":[]}}}`

func TestDeviceOpen_PrintsPinnedDriverInvocation(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	t.Setenv("AO_BROWSER_CAPABILITY", "cap-1")
	cfg := setConfigEnv(t)
	srv, capture := deviceServer(t, http.StatusOK, leaseJSON)
	writeRunFileFor(t, cfg, srv)

	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "open", "--platform", "ios")
	if err != nil {
		t.Fatalf("unexpected error: %v\nstderr=%s", err, errOut)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/aa-47/device/open" {
		t.Fatalf("request = %s %s", capture.method, capture.path)
	}
	if capture.body != `{"platform":"ios"}` || capture.capability != "cap-1" {
		t.Fatalf("body = %q capability = %q", capture.body, capture.capability)
	}
	for _, want := range []string{
		"Opened iPhone 17 Pro (iOS 26.5) via local: Runs on this machine.",
		"Device id: UDID-1 (aim builds at it: --device UDID-1 or -destination id=UDID-1).",
		"/ao/devices/leases/aa-47/agent-device snapshot -i\n",
		"already bound to this session's device",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDeviceOpen_Validation(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := deviceServer(t, http.StatusOK, leaseJSON)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "open", "--platform", "windows")
	var usage usageError
	if !errors.As(err, &usage) {
		t.Fatalf("err = %v, want a usage error", err)
	}
	if capture.path != "" {
		t.Fatalf("daemon contacted for an invalid platform: %s %s", capture.method, capture.path)
	}

	t.Setenv("AO_SESSION_ID", "")
	if _, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "open"); !errors.As(err, &usage) {
		t.Fatalf("err outside a session = %v, want a usage error", err)
	}
}

func TestDeviceInstall_SendsAbsolutePath(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := deviceServer(t, http.StatusOK, leaseJSON)
	writeRunFileFor(t, cfg, srv)
	t.Chdir(t.TempDir())

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "install", "build/App.app")
	if err != nil {
		t.Fatal(err)
	}
	// The CLI sends the platform's absolute path (C:\... on Windows).
	want, err := filepath.Abs(filepath.Join("build", "App.app"))
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Path string }
	if capture.path != "/api/v1/sessions/aa-47/device/install" || json.Unmarshal([]byte(capture.body), &body) != nil || body.Path != want || !filepath.IsAbs(body.Path) {
		t.Fatalf("request = %s %s, want path %s", capture.path, capture.body, want)
	}
	if !strings.Contains(out, "on iPhone 17 Pro") {
		t.Fatalf("output = %q", out)
	}
}

func TestDeviceOpen_SurfacesDaemonError(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, _ := deviceServer(t, http.StatusConflict, `{"error":{"code":"DEVICES_DISABLED","message":"Device support is off."}}`)
	writeRunFileFor(t, cfg, srv)

	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "open")
	var apiErr apiResponseError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusConflict {
		t.Fatalf("err = %v, want the daemon's 409", err)
	}
}

func TestDeviceStatus_ListsPlatformsAndLeases(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, capture := deviceServer(t, http.StatusOK, `{"available":true,"enabled":true,"state":"ready",
"platforms":[{"platform":"ios","available":true},{"platform":"android","available":false,"reason":"Install the Android SDK."}],
"devices":[{"id":"UDID-1","name":"iPhone 17 Pro","platform":"ios","version":"iOS 26.5","booted":true}],
"leases":[{"sessionId":"aa-47","provider":"local","reason":"r","device":{"id":"UDID-1","name":"iPhone 17 Pro","platform":"ios","booted":true},"agentDevice":{"command":"","args":[]}}]}`)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "status")
	if err != nil {
		t.Fatal(err)
	}
	if capture.path != "/api/v1/devices" {
		t.Fatalf("path = %s", capture.path)
	}
	for _, want := range []string{"Device support: ready", "android  unavailable: Install the Android SDK.", "[booted, leased to aa-47]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
}

func TestDeviceClose_SaysWhenThereIsNothingToRelease(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := deviceServer(t, http.StatusOK, `{"lease":null}`)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "close")
	if err != nil {
		t.Fatal(err)
	}
	if capture.method != http.MethodGet || capture.path != "/api/v1/sessions/aa-47/device" {
		t.Fatalf("request = %s %s, want only the lease read", capture.method, capture.path)
	}
	if strings.TrimSpace(out) != "This session has no device." {
		t.Fatalf("output = %q", out)
	}
}

func TestDeviceClose_ReleasesAHeldDevice(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, capture := deviceServer(t, http.StatusOK, leaseJSON)
	writeRunFileFor(t, cfg, srv)

	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "close", "--shutdown")
	if err != nil {
		t.Fatal(err)
	}
	if capture.method != http.MethodPost || capture.path != "/api/v1/sessions/aa-47/device/close" || capture.body != `{"shutdown":true}` {
		t.Fatalf("request = %s %s %s", capture.method, capture.path, capture.body)
	}
	if !strings.Contains(out, "Released") {
		t.Fatalf("output = %q", out)
	}
}

// Devices that could not be listed are an error, not an empty machine.
func TestDeviceStatus_FailsWhenTheDevicesCannotBeListed(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "aa-47")
	cfg := setConfigEnv(t)
	srv, _ := deviceServer(t, http.StatusOK, `{"available":true,"enabled":true,"state":"ready","error":"list devices: Failed to run avdmanager","platforms":[],"devices":[],"leases":[]}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "device", "status")
	if err == nil {
		t.Fatalf("status exited 0 although listing failed:\n%s", out)
	}
	if !strings.Contains(out, "Failed to run avdmanager") {
		t.Fatalf("output does not say why:\n%s", out)
	}
}

func TestDeviceStatusSaysWhenDeveloperModeIsOff(t *testing.T) {
	for _, tc := range []struct {
		status deviceStatusDTO
		want   string
	}{
		{deviceStatusDTO{Available: false, Enabled: true, State: "ready"}, "Developer mode"},
		{deviceStatusDTO{Available: true, Enabled: false, State: "off"}, "ao device setup"},
		{deviceStatusDTO{Available: true, Enabled: true, State: "ready"}, "Device support: ready"},
	} {
		var out strings.Builder
		if err := writeDeviceStatus(&out, tc.status, false); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("status %+v printed %q, want it to mention %q", tc.status, out.String(), tc.want)
		}
	}
}
