package telemetry

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestDetectCI(t *testing.T) {
	none := func(string) bool { return false }
	cases := []struct {
		name   string
		env    map[string]string
		exists func(string) bool
		want   bool
		signal string
	}{
		{name: "plain", env: nil, exists: none},
		{name: "CI=true", env: map[string]string{"CI": "true"}, exists: none, want: true, signal: "ci_env"},
		{name: "CI=1", env: map[string]string{"CI": "1"}, exists: none, want: true, signal: "ci_env"},
		{name: "CI=false", env: map[string]string{"CI": "false"}, exists: none},
		{name: "github actions", env: map[string]string{"GITHUB_ACTIONS": "true"}, exists: none, want: true, signal: "ci_env"},
		{name: "jenkins", env: map[string]string{"JENKINS_URL": "http://ci"}, exists: none, want: true, signal: "ci_env"},
		{name: "kubernetes", env: map[string]string{"KUBERNETES_SERVICE_HOST": "10.0.0.1"}, exists: none, want: true, signal: "container"},
		{name: "podman", env: map[string]string{"container": "podman"}, exists: none, want: true, signal: "container"},
		{name: "docker", exists: func(p string) bool { return p == "/.dockerenv" }, want: true, signal: "container"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, signal := detectCI(func(k string) string { return tc.env[k] }, tc.exists)
			if got != tc.want || signal != tc.signal {
				t.Fatalf("detectCI = %t, %q; want %t, %q", got, signal, tc.want, tc.signal)
			}
		})
	}
}

func TestPlatformNamesMatchTheRenderer(t *testing.T) {
	for goos, want := range map[string][2]string{
		"darwin":  {"Mac OS X", "darwin"},
		"windows": {"Windows", "win32"},
		"linux":   {"Linux", "linux"},
	} {
		if got := [2]string{posthogOSName(goos), nodePlatform(goos)}; got != want {
			t.Errorf("%s = %v, want %v", goos, got, want)
		}
	}
	for goarch, want := range map[string]string{"amd64": "x64", "arm64": "arm64", "386": "ia32"} {
		if got := nodeArch(goarch); got != want {
			t.Errorf("nodeArch(%s) = %q, want %q", goarch, got, want)
		}
	}
}

func TestSafeOSVersionRejectsNonVersionText(t *testing.T) {
	for in, want := range map[string]string{
		"15.1.2":           "15.1.2",
		"10.0.22631":       "10.0.22631",
		"6.8.0-45-generic": "6.8.0-45-generic",
		"":                 "",
		"my laptop":        "",
		"/home/me":         "",
	} {
		if got := safeOSVersion(in); got != want {
			t.Errorf("safeOSVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeInstallSource(t *testing.T) {
	for in, want := range map[string]string{
		"npm-bootstrap":     "npm-bootstrap",
		"unknown":           "unknown",
		"dmg":               "dmg",
		"brew-cask":         "brew-cask",
		"":                  "unknown",
		"me@example.com":    "other",
		"/Users/me/Downl":   "other",
		" npm-bootstrap \n": "npm-bootstrap",
	} {
		if got := NormalizeInstallSource(in); got != want {
			t.Errorf("NormalizeInstallSource(%q) = %q, want %q", in, got, want)
		}
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInstallContextReadsDesktopMarkersAndRefreshes(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	ctx := newInstallContext(dir, func() time.Time { return now })

	props := map[string]any{}
	ctx.stamp(props)
	if props["install_source"] != "unknown" || props["release_channel"] != "unknown" {
		t.Fatalf("no markers: %#v", props)
	}
	if _, ok := props["installed_at"]; ok {
		t.Fatalf("installed_at without a marker: %#v", props)
	}

	writeJSONFile(t, filepath.Join(dir, appStateFileName), map[string]any{
		"installSource": "npm-bootstrap", "installedAt": "2026-10-20T23:59:59.123Z",
		"acq_channel_self_reported": "x",
	})
	writeJSONFile(t, filepath.Join(dir, updateSettingsFileName), map[string]any{"channel": "nightly", "feature": nil})
	// Cached until the TTL passes.
	props = map[string]any{}
	ctx.stamp(props)
	if props["install_source"] != "unknown" {
		t.Fatalf("re-read before TTL: %#v", props)
	}
	now = now.Add(installContextTTL)
	props = map[string]any{}
	ctx.stamp(props)
	if props["install_source"] != "npm-bootstrap" || props["installed_at"] != "2026-10-20" || props["release_channel"] != "nightly" {
		t.Fatalf("after TTL: %#v", props)
	}
	if _, ok := props["acq_channel_self_reported"]; ok {
		t.Fatalf("unrelated app-state keys must not be exported: %#v", props)
	}
}

func TestReleaseChannelFromMatchesRenderer(t *testing.T) {
	cases := []struct {
		channel, feature, want string
	}{
		{"latest", "null", "stable"},
		{"nightly", "", "nightly"},
		{"latest", `{"pr":42}`, "feature"},
		{"", "", "unknown"},
	}
	for _, tc := range cases {
		if got := releaseChannelFrom(tc.channel, json.RawMessage(tc.feature)); got != tc.want {
			t.Errorf("releaseChannelFrom(%q, %q) = %q, want %q", tc.channel, tc.feature, got, tc.want)
		}
	}
}

func TestPostHogPropertiesCarrySegmentation(t *testing.T) {
	stateDir := t.TempDir()
	writeJSONFile(t, filepath.Join(stateDir, appStateFileName), map[string]any{"installSource": "dmg", "installedAt": "2026-10-01T10:00:00Z"})
	sink, err := NewPostHogSink(t.TempDir(), "phc_test", "https://us.i.posthog.com", "0.20.0", "", roundTripClient(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = sink.Close(context.Background()) }()
	sink.SetInstallStateDir(stateDir)

	props := sink.properties(ports.TelemetryEvent{Name: "ao.daemon.started", Source: "daemon"})
	if props["platform"] != nodePlatform(runtime.GOOS) || props["arch"] != nodeArch(runtime.GOARCH) {
		t.Fatalf("platform/arch = %#v/%#v", props["platform"], props["arch"])
	}
	if props["$os"] != posthogOSName(runtime.GOOS) {
		t.Fatalf("$os = %#v", props["$os"])
	}
	if _, ok := props["is_ci"].(bool); !ok {
		t.Fatalf("is_ci = %#v, want a bool", props["is_ci"])
	}
	if props["install_source"] != "dmg" || props["installed_at"] != "2026-10-01" || props["release_channel"] != "unknown" {
		t.Fatalf("install context = %#v", props)
	}
}

func TestPayloadCannotOverrideSegmentation(t *testing.T) {
	// Common properties are not in any per-event allowlist, so an event payload
	// cannot smuggle a different install_source or is_ci past the sanitizer.
	for name, allowed := range remotePayloadAllowlist {
		for _, key := range []string{"install_source", "installed_at", "is_ci", "ci_signal", "arch", "platform", "$os", "$os_version", "release_channel"} {
			if _, ok := allowed[key]; ok {
				t.Errorf("%s allowlists common property %s", name, key)
			}
		}
	}
}

func TestMilestonesClaimOnce(t *testing.T) {
	m := NewMilestones(t.TempDir())
	if first, known := m.Claim("session_spawned"); !first || !known {
		t.Fatalf("first claim = %t, %t", first, known)
	}
	if first, known := m.Claim("session_spawned"); first || !known {
		t.Fatalf("second claim = %t, %t", first, known)
	}
	if first, known := m.Claim("../escape"); first || known {
		t.Fatalf("invalid name claim = %t, %t", first, known)
	}
	var nilMilestones *Milestones
	if first, known := nilMilestones.Claim("x"); first || known {
		t.Fatalf("nil claim = %t, %t", first, known)
	}
	if NewMilestones("") != nil {
		t.Fatal("NewMilestones(\"\") should be nil")
	}
	unwritable := NewMilestones(filepath.Join(t.TempDir(), "missing"))
	if first, known := unwritable.Claim("x"); first || known {
		t.Fatalf("unwritable claim = %t, %t", first, known)
	}
}

func TestFirstOpenEventOncePerInstallIDOnAnyBuild(t *testing.T) {
	dir := t.TempDir()
	ident, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	// An old install: the install event was spent (or never fresh). first_open
	// must still fire once.
	if err := os.WriteFile(filepath.Join(dir, installedEventFile), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, OptOutFile), []byte("1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if marked, err := ident.MarkFirstOpenEvent(context.Background()); marked || err != nil {
		t.Fatalf("opted out: %t, %v", marked, err)
	}
	if err := os.Remove(filepath.Join(dir, OptOutFile)); err != nil {
		t.Fatal(err)
	}
	if marked, err := ident.MarkFirstOpenEvent(context.Background()); !marked || err != nil {
		t.Fatalf("first: %t, %v", marked, err)
	}
	restarted, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if marked, err := restarted.MarkFirstOpenEvent(context.Background()); marked || err != nil {
		t.Fatalf("after restart: %t, %v", marked, err)
	}
	// A replaced install ID is a new install and sends its own first_open.
	if err := os.WriteFile(filepath.Join(dir, installIDFile), []byte("ins_replaced\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replaced, err := NewIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	if marked, err := replaced.MarkFirstOpenEvent(context.Background()); !marked || err != nil {
		t.Fatalf("new install id: %t, %v", marked, err)
	}
}
