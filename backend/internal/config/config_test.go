package config

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	// Clear every recognised var so we observe pure defaults regardless of the
	// surrounding environment.
	for _, k := range []string{"AO_PORT", "AO_REQUEST_TIMEOUT", "AO_SHUTDOWN_TIMEOUT", "AO_RUN_FILE", "AO_DATA_DIR", "AO_AGENT", "AO_ALLOWED_ORIGINS", "AO_TELEMETRY_EVENTS", "AO_TELEMETRY_METRICS", "AO_TELEMETRY_REMOTE", "AO_TELEMETRY_POSTHOG_KEY", "AO_TELEMETRY_POSTHOG_HOST", "AO_TELEMETRY_DISABLED_EVENTS", "AO_TELEMETRY_APP_VERSION", "AO_HARNESS_UPDATE_CHECKS", "AO_HARNESS_UPDATE_INTERVAL"} {
		t.Setenv(k, "")
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Host != LoopbackHost {
		t.Errorf("Host = %q, want %q", cfg.Host, LoopbackHost)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("Port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.RequestTimeout != DefaultRequestTimeout {
		t.Errorf("RequestTimeout = %s, want %s", cfg.RequestTimeout, DefaultRequestTimeout)
	}
	if cfg.ShutdownTimeout != DefaultShutdownTimeout {
		t.Errorf("ShutdownTimeout = %s, want %s", cfg.ShutdownTimeout, DefaultShutdownTimeout)
	}
	if cfg.RunFilePath == "" {
		t.Error("RunFilePath is empty, want a resolved default path")
	}
	homeDir, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("UserHomeDir: %v", err)
	}
	wantRunFilePath := filepath.Join(homeDir, ".ao", "running.json")
	if cfg.RunFilePath != wantRunFilePath {
		t.Errorf("RunFilePath = %q, want %q", cfg.RunFilePath, wantRunFilePath)
	}
	if cfg.DataDir == "" {
		t.Error("DataDir is empty, want a resolved default path")
	}
	wantDataDir := filepath.Join(homeDir, ".ao", "data")
	if cfg.DataDir != wantDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, wantDataDir)
	}
	if wantStateDir := filepath.Join(homeDir, ".ao"); cfg.StateDir != wantStateDir {
		t.Errorf("StateDir = %q, want %q", cfg.StateDir, wantStateDir)
	}
	if cfg.Telemetry.Remote != TelemetryRemoteOff || cfg.Telemetry.PostHogHost != DefaultTelemetryPostHogHost {
		t.Fatalf("Telemetry defaults = %+v", cfg.Telemetry)
	}
	if cfg.Telemetry.EventsExplicit {
		t.Fatal("Telemetry.EventsExplicit = true for an absent setting")
	}
}

func TestLoadAbsolutizesRelativeOverrides(t *testing.T) {
	// A relative override must be resolved to absolute at Load time. The daemon
	// chdir's into its data dir at startup, so a relative path left as-is would
	// be re-resolved against the new cwd and double-nest state.
	t.Setenv("AO_RUN_FILE", "rel-running.json")
	t.Setenv("AO_DATA_DIR", "rel-data")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !filepath.IsAbs(cfg.RunFilePath) {
		t.Errorf("RunFilePath = %q, want absolute", cfg.RunFilePath)
	}
	if !filepath.IsAbs(cfg.DataDir) {
		t.Errorf("DataDir = %q, want absolute", cfg.DataDir)
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "rel-data"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
	if cfg.StateDir != cfg.DataDir {
		t.Errorf("StateDir = %q, want explicit DataDir %q", cfg.StateDir, cfg.DataDir)
	}
	if want := filepath.Join(cwd, "rel-running.json"); cfg.RunFilePath != want {
		t.Errorf("RunFilePath = %q, want %q", cfg.RunFilePath, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	overrideDir := t.TempDir()
	runFilePath := filepath.Join(overrideDir, "ao-test-running.json")
	dataDir := filepath.Join(overrideDir, "ao-test-data")

	t.Setenv("AO_PORT", "4002")
	t.Setenv("AO_REQUEST_TIMEOUT", "5s")
	t.Setenv("AO_SHUTDOWN_TIMEOUT", "3s")
	t.Setenv("AO_RUN_FILE", runFilePath)
	t.Setenv("AO_DATA_DIR", dataDir)
	t.Setenv("AO_TELEMETRY_EVENTS", "on")
	t.Setenv("AO_TELEMETRY_METRICS", "off")
	t.Setenv("AO_TELEMETRY_REMOTE", "posthog")
	t.Setenv("AO_TELEMETRY_POSTHOG_KEY", "phc_test")
	t.Setenv("AO_TELEMETRY_POSTHOG_HOST", "https://eu.i.posthog.com")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Addr() != "127.0.0.1:4002" {
		t.Errorf("Addr() = %q, want 127.0.0.1:4002", cfg.Addr())
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Errorf("RequestTimeout = %s, want 5s", cfg.RequestTimeout)
	}
	if cfg.ShutdownTimeout != 3*time.Second {
		t.Errorf("ShutdownTimeout = %s, want 3s", cfg.ShutdownTimeout)
	}
	if cfg.RunFilePath != runFilePath {
		t.Errorf("RunFilePath = %q, want %q", cfg.RunFilePath, runFilePath)
	}
	if cfg.DataDir != dataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, dataDir)
	}
	if cfg.StateDir != dataDir {
		t.Errorf("StateDir = %q, want explicit DataDir %q", cfg.StateDir, dataDir)
	}
	if !cfg.Telemetry.Events || cfg.Telemetry.Metrics {
		t.Fatalf("Telemetry toggles = %+v", cfg.Telemetry)
	}
	if !cfg.Telemetry.EventsExplicit {
		t.Fatal("Telemetry.EventsExplicit = false for AO_TELEMETRY_EVENTS=on")
	}
	if cfg.Telemetry.Remote != TelemetryRemotePostHog || cfg.Telemetry.PostHogKey != "phc_test" || cfg.Telemetry.PostHogHost != "https://eu.i.posthog.com" {
		t.Fatalf("Telemetry remote = %+v", cfg.Telemetry)
	}
}

func TestLoadInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"non-numeric port", map[string]string{"AO_PORT": "abc"}},
		{"port out of range", map[string]string{"AO_PORT": "70000"}},
		{"bad request timeout", map[string]string{"AO_REQUEST_TIMEOUT": "soon"}},
		{"bad shutdown timeout", map[string]string{"AO_SHUTDOWN_TIMEOUT": "later"}},
		{"zero request timeout", map[string]string{"AO_REQUEST_TIMEOUT": "0s"}},
		{"negative request timeout", map[string]string{"AO_REQUEST_TIMEOUT": "-1s"}},
		{"zero shutdown timeout", map[string]string{"AO_SHUTDOWN_TIMEOUT": "0s"}},
		{"negative shutdown timeout", map[string]string{"AO_SHUTDOWN_TIMEOUT": "-5s"}},
		{"null origin", map[string]string{"AO_ALLOWED_ORIGINS": "app://renderer,null"}},
		{"wildcard origin", map[string]string{"AO_ALLOWED_ORIGINS": "*"}},
		{"bad telemetry events", map[string]string{"AO_TELEMETRY_EVENTS": "maybe"}},
		{"bad telemetry metrics", map[string]string{"AO_TELEMETRY_METRICS": "maybe"}},
		{"bad telemetry remote", map[string]string{"AO_TELEMETRY_REMOTE": "otlp"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load() = nil error, want error")
			}
		})
	}
}

func TestLoadAllowedOrigins(t *testing.T) {
	t.Run("default includes the packaged renderer origin", func(t *testing.T) {
		t.Setenv("AO_ALLOWED_ORIGINS", "")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		found := false
		for _, origin := range cfg.AllowedOrigins {
			if origin == "app://renderer" {
				found = true
			}
		}
		if !found {
			t.Errorf("AllowedOrigins = %v, want app://renderer included", cfg.AllowedOrigins)
		}
	})

	t.Run("override replaces defaults and trims entries", func(t *testing.T) {
		t.Setenv("AO_ALLOWED_ORIGINS", " app://renderer , http://localhost:9999 ,")
		cfg, err := Load()
		if err != nil {
			t.Fatalf("Load: %v", err)
		}
		want := []string{"app://renderer", "http://localhost:9999"}
		if len(cfg.AllowedOrigins) != len(want) {
			t.Fatalf("AllowedOrigins = %v, want %v", cfg.AllowedOrigins, want)
		}
		for i, origin := range want {
			if cfg.AllowedOrigins[i] != origin {
				t.Errorf("AllowedOrigins[%d] = %q, want %q", i, cfg.AllowedOrigins[i], origin)
			}
		}
	})
}

// The kill switch and the supervisor-supplied version are user-visible
// boundaries: the daemon reads them from the environment the desktop app hands
// it, so a parsing regression here silently disables the switch.
func TestLoadTelemetryDisabledEventsAndAppVersion(t *testing.T) {
	t.Setenv("AO_TELEMETRY_DISABLED_EVENTS", " ao.v2.app.active , ao.renderer.* ,, ")
	t.Setenv("AO_TELEMETRY_APP_VERSION", "  0.11.2  ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := []string{"ao.v2.app.active", "ao.renderer.*"}
	if len(cfg.Telemetry.DisabledEvents) != len(want) {
		t.Fatalf("DisabledEvents = %#v, want %#v", cfg.Telemetry.DisabledEvents, want)
	}
	for i, name := range want {
		if cfg.Telemetry.DisabledEvents[i] != name {
			t.Fatalf("DisabledEvents[%d] = %q, want %q", i, cfg.Telemetry.DisabledEvents[i], name)
		}
	}
	if cfg.Telemetry.AppVersion != "0.11.2" {
		t.Fatalf("AppVersion = %q, want trimmed 0.11.2", cfg.Telemetry.AppVersion)
	}
}

// An unparseable or blank list must never stop the daemon booting: the switch
// has to be usable in a hurry, so a bad entry is inert rather than fatal.
func TestLoadTelemetryDisabledEventsBlankIsInert(t *testing.T) {
	t.Setenv("AO_TELEMETRY_DISABLED_EVENTS", " , , ")
	t.Setenv("AO_TELEMETRY_APP_VERSION", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Telemetry.DisabledEvents) != 0 {
		t.Fatalf("DisabledEvents = %#v, want empty", cfg.Telemetry.DisabledEvents)
	}
	if cfg.Telemetry.AppVersion != "" {
		t.Fatalf("AppVersion = %q, want empty", cfg.Telemetry.AppVersion)
	}
}

func TestLoadAppVersionOnlyFromEnvironment(t *testing.T) {
	t.Setenv("AO_TELEMETRY_APP_VERSION", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Telemetry.AppVersion != "" {
		t.Fatalf("AppVersion = %q, want empty without supervisor value", cfg.Telemetry.AppVersion)
	}
	t.Setenv("AO_TELEMETRY_APP_VERSION", "0.11.2")
	if cfg, _ = Load(); cfg.Telemetry.AppVersion != "0.11.2" {
		t.Fatalf("AppVersion = %q, want supervisor value", cfg.Telemetry.AppVersion)
	}
}

func TestLoadOfferingDefaults(t *testing.T) {
	// Clear the offering vars so we observe pure defaults: no client identity,
	// cloud off, local on.
	for _, k := range []string{"AO_CLIENT", "AO_CLOUD_OFFERING", "AO_LOCAL_OFFERING", "AO_CLOUD_CONTROL_PLANE_URL"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Client != "" {
		t.Errorf("Client = %q, want empty", cfg.Client)
	}
	if cfg.CloudOffering {
		t.Error("CloudOffering = true, want false by default")
	}
	if !cfg.LocalOffering {
		t.Error("LocalOffering = false, want true by default")
	}
	if cfg.CloudControlPlaneURL != "https://api.aoagents.dev" {
		t.Errorf("CloudControlPlaneURL = %q, want the baked default", cfg.CloudControlPlaneURL)
	}
}

func TestLoadOfferingOverrides(t *testing.T) {
	t.Setenv("AO_CLIENT", "  eleven_x  ")
	t.Setenv("AO_CLOUD_OFFERING", "true")
	t.Setenv("AO_LOCAL_OFFERING", "false")
	t.Setenv("AO_CLOUD_CONTROL_PLANE_URL", " https://cp.example.com ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Client != "eleven_x" {
		t.Errorf("Client = %q, want trimmed eleven_x", cfg.Client)
	}
	if !cfg.CloudOffering {
		t.Error("CloudOffering = false, want true")
	}
	if cfg.LocalOffering {
		t.Error("LocalOffering = true, want explicit false to stick")
	}
	if cfg.CloudControlPlaneURL != "https://cp.example.com" {
		t.Errorf("CloudControlPlaneURL = %q, want trimmed https://cp.example.com", cfg.CloudControlPlaneURL)
	}
}

// The offering toggles must accept the numeric spellings the supervisor may
// hand down: 1 switches cloud on, 0 is the only way local goes off.
func TestLoadOfferingNumericToggles(t *testing.T) {
	t.Setenv("AO_CLOUD_OFFERING", "1")
	t.Setenv("AO_LOCAL_OFFERING", "0")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !cfg.CloudOffering {
		t.Error("CloudOffering = false, want true for AO_CLOUD_OFFERING=1")
	}
	if cfg.LocalOffering {
		t.Error("LocalOffering = true, want false for AO_LOCAL_OFFERING=0")
	}
}

func TestLoadOfferingInvalid(t *testing.T) {
	tests := []struct {
		name string
		env  map[string]string
	}{
		{"bad cloud offering toggle", map[string]string{"AO_CLOUD_OFFERING": "maybe"}},
		{"bad local offering toggle", map[string]string{"AO_LOCAL_OFFERING": "maybe"}},
		{"control plane without scheme", map[string]string{"AO_CLOUD_CONTROL_PLANE_URL": "cp.example.com"}},
		{"control plane non-http scheme", map[string]string{"AO_CLOUD_CONTROL_PLANE_URL": "ftp://cp.example.com"}},
		{"control plane without host", map[string]string{"AO_CLOUD_CONTROL_PLANE_URL": "https://"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			if _, err := Load(); err == nil {
				t.Fatal("Load() = nil error, want error")
			}
		})
	}
}

func TestLoadGitLabDefaults(t *testing.T) {
	// Clear the GitLab config vars so we observe pure defaults.
	for _, k := range []string{"AO_GITLAB_ALLOWED_HOSTS", "AO_GITLAB_HOST_TOKENS"} {
		t.Setenv(k, "")
	}
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.GitLab.AllowedHosts != nil {
		t.Errorf("GitLab.AllowedHosts = %v, want nil", cfg.GitLab.AllowedHosts)
	}
	if cfg.GitLab.HostTokens != nil {
		t.Errorf("GitLab.HostTokens = %v, want nil", cfg.GitLab.HostTokens)
	}
}

func TestLoadGitLabAllowedHosts(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want []string
	}{
		{"single host", "gitlab.mycompany.com", []string{"gitlab.mycompany.com"}},
		{"comma-separated", "gitlab.mycompany.com,gitlab.internal.corp", []string{"gitlab.mycompany.com", "gitlab.internal.corp"}},
		{"trimmed whitespace", " gitlab.mycompany.com , gitlab.internal.corp ", []string{"gitlab.mycompany.com", "gitlab.internal.corp"}},
		{"empty entries skipped", "gitlab.mycompany.com,,gitlab.internal.corp,", []string{"gitlab.mycompany.com", "gitlab.internal.corp"}},
		{"with port", "gitlab.internal:8443", []string{"gitlab.internal:8443"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_GITLAB_ALLOWED_HOSTS", tc.env)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.GitLab.AllowedHosts) != len(tc.want) {
				t.Fatalf("AllowedHosts = %v, want %v", cfg.GitLab.AllowedHosts, tc.want)
			}
			for i, h := range tc.want {
				if cfg.GitLab.AllowedHosts[i] != h {
					t.Errorf("AllowedHosts[%d] = %q, want %q", i, cfg.GitLab.AllowedHosts[i], h)
				}
			}
		})
	}
}

func TestLoadGitLabHostTokens(t *testing.T) {
	tests := []struct {
		name string
		env  string
		want map[string]string
	}{
		{
			name: "single host=token",
			env:  "gitlab.mycompany.com=token-1",
			want: map[string]string{"gitlab.mycompany.com": "token-1"},
		},
		{
			name: "multiple host=token pairs",
			env:  "gitlab.mycompany.com=token-1,gitlab.internal.corp=token-2",
			want: map[string]string{
				"gitlab.mycompany.com": "token-1",
				"gitlab.internal.corp": "token-2",
			},
		},
		{
			name: "trimmed whitespace around entries",
			env:  " gitlab.mycompany.com = token-1 , gitlab.internal.corp = token-2 ",
			want: map[string]string{
				"gitlab.mycompany.com": "token-1",
				"gitlab.internal.corp": "token-2",
			},
		},
		{
			name: "host with port",
			env:  "gitlab.internal:8443=token-port",
			want: map[string]string{"gitlab.internal:8443": "token-port"},
		},
		{
			name: "empty entries skipped",
			env:  "gitlab.mycompany.com=token-1,,",
			want: map[string]string{"gitlab.mycompany.com": "token-1"},
		},
		{
			name: "entry without equals skipped",
			env:  "gitlab.mycompany.com=token-1,not-a-pair",
			want: map[string]string{"gitlab.mycompany.com": "token-1"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_GITLAB_HOST_TOKENS", tc.env)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if len(cfg.GitLab.HostTokens) != len(tc.want) {
				t.Fatalf("HostTokens = %v, want %v", cfg.GitLab.HostTokens, tc.want)
			}
			for k, v := range tc.want {
				got, ok := cfg.GitLab.HostTokens[k]
				if !ok {
					t.Errorf("HostTokens missing key %q", k)
					continue
				}
				if got != v {
					t.Errorf("HostTokens[%q] = %q, want %q", k, got, v)
				}
			}
		})
	}
}

func TestLoadGitLabInvalidHostTokens(t *testing.T) {
	tests := []struct {
		name string
		env  string
	}{
		{"token contains equals", "gitlab.mycompany.com=token=with=equals"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("AO_GITLAB_HOST_TOKENS", tc.env)
			_, err := Load()
			if err == nil {
				t.Fatal("Load() = nil error, want error for malformed AO_GITLAB_HOST_TOKENS")
			}
		})
	}
}

func TestLoadTrackerIntakeDefaultsOff(t *testing.T) {
	// A blank-but-present value counts as unset. Load() feeds every ao
	// command, so treating it as malformed would fail `ao status`/`ao stop`
	// over an empty export rather than just leaving intake off.
	for _, raw := range []string{"", " ", "\t"} {
		t.Run(fmt.Sprintf("%q", raw), func(t *testing.T) {
			t.Setenv("AO_TRACKER_INTAKE", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.TrackerIntake {
				t.Error("TrackerIntake = true, want false by default")
			}
		})
	}
}

func TestLoadTrackerIntakeToggle(t *testing.T) {
	on := []string{"on", "true", "1", "yes"}
	off := []string{"off", "false", "0", "no"}
	for _, raw := range on {
		t.Run("on/"+raw, func(t *testing.T) {
			t.Setenv("AO_TRACKER_INTAKE", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if !cfg.TrackerIntake {
				t.Errorf("TrackerIntake = false for %q, want true", raw)
			}
		})
	}
	for _, raw := range off {
		t.Run("off/"+raw, func(t *testing.T) {
			t.Setenv("AO_TRACKER_INTAKE", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.TrackerIntake {
				t.Errorf("TrackerIntake = true for %q, want false", raw)
			}
		})
	}
}

func TestLoadTrackerIntakeRejectsGarbage(t *testing.T) {
	t.Setenv("AO_TRACKER_INTAKE", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("Load() = nil error, want error for malformed AO_TRACKER_INTAKE")
	}
}

// The daemon proxies desktop requests to the agent browser, so it may only
// ever point at this host.
func TestLoadAgentBrowserURL(t *testing.T) {
	for _, tc := range []struct {
		raw  string
		want string
	}{
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080"},
		{" http://localhost:8080/ ", "http://localhost:8080"},
		{"http://10.0.0.5:8080", ""},
		{"http://example.com:8080", ""},
		{"https://127.0.0.1:8080", ""},
		{"http://user:pw@127.0.0.1:8080", ""},
		{"http://127.0.0.1:8080/?token=x", ""},
	} {
		t.Setenv("AO_AGENT_BROWSER_URL", tc.raw)
		t.Setenv("AO_AGENT_BROWSER_TOKEN", "rb-secret")
		cfg, err := Load()
		if tc.want == "" {
			if err == nil {
				t.Errorf("%q: loaded, want an error", tc.raw)
			}
			continue
		}
		if err != nil || cfg.AgentBrowserURL != tc.want || cfg.AgentBrowserToken != "rb-secret" {
			t.Errorf("%q: got %q, %q, %v; want %q", tc.raw, cfg.AgentBrowserURL, cfg.AgentBrowserToken, err, tc.want)
		}
	}
}

// The agent browser refuses every request without its exact token: a padded
// one would never match, and a missing one must stop the daemon with a reason
// rather than leave the agent tab silently empty.
func TestLoadAgentBrowserToken(t *testing.T) {
	t.Setenv("AO_AGENT_BROWSER_URL", "http://127.0.0.1:8080")
	t.Setenv("AO_AGENT_BROWSER_TOKEN", " rb-secret\n")
	if cfg, err := Load(); err != nil || cfg.AgentBrowserToken != "rb-secret" {
		t.Errorf("padded token: got %q, %v; want rb-secret", cfg.AgentBrowserToken, err)
	}
	for _, token := range []string{"", " \t"} {
		t.Setenv("AO_AGENT_BROWSER_TOKEN", token)
		if _, err := Load(); err == nil || !strings.Contains(err.Error(), "AO_AGENT_BROWSER_TOKEN") {
			t.Errorf("token %q: got %v, want an error naming AO_AGENT_BROWSER_TOKEN", token, err)
		}
	}
}

func TestLoadHarnessUpdateChecksToggle(t *testing.T) {
	t.Setenv("AO_HARNESS_UPDATE_CHECKS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.HarnessUpdateChecks {
		t.Fatal("harness update checks are off by default")
	}
	t.Setenv("AO_HARNESS_UPDATE_CHECKS", "off")
	if cfg, err = Load(); err != nil || cfg.HarnessUpdateChecks {
		t.Fatalf("AO_HARNESS_UPDATE_CHECKS=off: checks=%t err=%v", cfg.HarnessUpdateChecks, err)
	}
	t.Setenv("AO_HARNESS_UPDATE_CHECKS", "sometimes")
	if _, err := Load(); err == nil {
		t.Fatal("invalid AO_HARNESS_UPDATE_CHECKS value was accepted")
	}
}

func TestLoadUpdateWhileRunningToggle(t *testing.T) {
	t.Setenv("AO_UPDATE_WHILE_RUNNING", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.UpdateWhileRunning {
		t.Fatal("update while running is off by default")
	}
	t.Setenv("AO_UPDATE_WHILE_RUNNING", "off")
	if cfg, err = Load(); err != nil || cfg.UpdateWhileRunning {
		t.Fatalf("AO_UPDATE_WHILE_RUNNING=off: enabled=%t err=%v", cfg.UpdateWhileRunning, err)
	}
	t.Setenv("AO_UPDATE_WHILE_RUNNING", "maybe")
	if _, err := Load(); err == nil {
		t.Fatal("invalid AO_UPDATE_WHILE_RUNNING value was accepted")
	}
}

func TestLoadVersionRefreshSettings(t *testing.T) {
	t.Setenv("AO_VERSION_REFRESH", "")
	t.Setenv("AO_VERSION_REFRESH_IDLE", "")
	cfg, err := Load()
	if err != nil || !cfg.VersionRefresh || cfg.VersionRefreshIdle != DefaultVersionRefreshIdle {
		t.Fatalf("defaults: refresh=%t idle=%s err=%v", cfg.VersionRefresh, cfg.VersionRefreshIdle, err)
	}
	t.Setenv("AO_VERSION_REFRESH", "off")
	t.Setenv("AO_VERSION_REFRESH_IDLE", "30m")
	if cfg, err = Load(); err != nil || cfg.VersionRefresh || cfg.VersionRefreshIdle != 30*time.Minute {
		t.Fatalf("overrides: refresh=%t idle=%s err=%v", cfg.VersionRefresh, cfg.VersionRefreshIdle, err)
	}
	t.Setenv("AO_VERSION_REFRESH_IDLE", "soon")
	if _, err := Load(); err == nil {
		t.Fatal("invalid AO_VERSION_REFRESH_IDLE was accepted")
	}
}

func TestLoadPhaseThreeToggles(t *testing.T) {
	t.Setenv("AO_TUI_VERSION_REFRESH", "")
	t.Setenv("AO_WINDOWS_UPDATE_WHEN_IDLE", "")
	cfg, err := Load()
	if err != nil || !cfg.TUIVersionRefresh || !cfg.WindowsUpdateWhenIdle {
		t.Fatalf("defaults: tui=%t windows=%t err=%v", cfg.TUIVersionRefresh, cfg.WindowsUpdateWhenIdle, err)
	}
	t.Setenv("AO_TUI_VERSION_REFRESH", "off")
	t.Setenv("AO_WINDOWS_UPDATE_WHEN_IDLE", "off")
	if cfg, err = Load(); err != nil || cfg.TUIVersionRefresh || cfg.WindowsUpdateWhenIdle {
		t.Fatalf("off: tui=%t windows=%t err=%v", cfg.TUIVersionRefresh, cfg.WindowsUpdateWhenIdle, err)
	}
}

func TestLoadManagedHarnessInstallsToggle(t *testing.T) {
	t.Setenv("AO_MANAGED_HARNESS_INSTALLS", "")
	cfg, err := Load()
	if err != nil || !cfg.ManagedHarnessInstalls {
		t.Fatalf("default managed installs = %t, %v", cfg.ManagedHarnessInstalls, err)
	}
	t.Setenv("AO_MANAGED_HARNESS_INSTALLS", "off")
	if cfg, err = Load(); err != nil || cfg.ManagedHarnessInstalls {
		t.Fatalf("off: managed installs = %t, %v", cfg.ManagedHarnessInstalls, err)
	}
}

func TestLoadHarnessUpdateInterval(t *testing.T) {
	t.Setenv("AO_HARNESS_UPDATE_INTERVAL", "")
	cfg, err := Load()
	if err != nil || cfg.HarnessUpdateInterval != DefaultHarnessUpdateInterval {
		t.Fatalf("default interval = %s, err=%v; want %s", cfg.HarnessUpdateInterval, err, DefaultHarnessUpdateInterval)
	}
	t.Setenv("AO_HARNESS_UPDATE_INTERVAL", "12h")
	if cfg, err = Load(); err != nil || cfg.HarnessUpdateInterval != 12*time.Hour {
		t.Fatalf("12h interval = %s, err=%v", cfg.HarnessUpdateInterval, err)
	}
	for _, raw := range []string{"30m", "0s", "-1h", "soon"} {
		t.Setenv("AO_HARNESS_UPDATE_INTERVAL", raw)
		if _, err := Load(); err == nil {
			t.Fatalf("AO_HARNESS_UPDATE_INTERVAL=%q was accepted", raw)
		}
	}
}

func TestScrubTelemetryEnvKeepsLoadedConfigAndClearsProcessEnv(t *testing.T) {
	t.Setenv("AO_TELEMETRY_EVENTS", "on")
	t.Setenv("AO_TELEMETRY_REMOTE", "posthog")
	t.Setenv("AO_TELEMETRY_POSTHOG_KEY", "phc_test")
	t.Setenv("AO_TELEMETRY_APP_VERSION", "0.13.7")
	t.Setenv("AO_SENTRY_DSN", "https://k@o.ingest.sentry.io/1")
	t.Setenv("AO_PORT", "3123")
	// Desktop-only, documented opt-out the daemon never reads: must survive.
	t.Setenv("AO_TELEMETRY_RENDERER", "off")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ScrubTelemetryEnv()

	if cfg.Telemetry.Remote != TelemetryRemotePostHog || cfg.Telemetry.AppVersion != "0.13.7" || cfg.Telemetry.PostHogKey != "phc_test" || cfg.Telemetry.SentryDSN == "" {
		t.Fatalf("loaded config lost telemetry settings: %+v", cfg.Telemetry)
	}
	for _, key := range telemetryEnvKeys {
		if _, present := os.LookupEnv(key); present {
			t.Fatalf("%s still set in process env after scrub", key)
		}
	}
	// A child started from os.Environ() after the scrub sees none of the
	// scrubbed keys, and still sees everything else.
	for _, entry := range os.Environ() {
		if isScrubbedTelemetryEntry(entry) {
			t.Fatalf("child environment would inherit %q", entry)
		}
	}
	if os.Getenv("AO_PORT") != "3123" || os.Getenv("AO_TELEMETRY_RENDERER") != "off" {
		t.Fatal("scrub must leave variables outside telemetryEnvKeys alone")
	}
	// The real check: a child process started the way the daemon starts every
	// child (exec with the inherited environment) must not see the settings.
	if runtime.GOOS == "windows" {
		return
	}
	out, err := exec.Command("env").Output()
	if err != nil {
		t.Fatalf("env: %v", err)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if isScrubbedTelemetryEntry(line) {
			t.Fatalf("child process inherited %q after scrub", line)
		}
	}
	if !strings.Contains(string(out), "AO_PORT=3123\n") || !strings.Contains(string(out), "AO_TELEMETRY_RENDERER=off\n") {
		t.Fatal("child process should still inherit variables outside telemetryEnvKeys")
	}
}

func isScrubbedTelemetryEntry(entry string) bool {
	key, _, _ := strings.Cut(entry, "=")
	for _, scrubbed := range telemetryEnvKeys {
		if key == scrubbed {
			return true
		}
	}
	return false
}
