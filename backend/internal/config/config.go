package config

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const EnvComputerUse = "AO_COMPUTER_USE"

const (
	LoopbackHost                 = "127.0.0.1"
	DefaultPort                  = 3001
	DefaultRequestTimeout        = 60 * time.Second
	DefaultShutdownTimeout       = 10 * time.Second
	DefaultHarnessUpdateInterval = 6 * time.Hour
	DefaultVersionRefreshIdle    = 10 * time.Minute
	MinHarnessUpdateInterval     = time.Hour
	DefaultAgent                 = "claude-code"
	DefaultTelemetryPostHogHost  = "https://us.i.posthog.com"

	defaultCloudControlPlaneURL = "https://api.aoagents.dev"
)

type TelemetryRemote string

const (
	TelemetryRemoteOff     TelemetryRemote = "off"
	TelemetryRemotePostHog TelemetryRemote = "posthog"
)

type TelemetryConfig struct {
	Events         bool
	EventsExplicit bool
	Metrics        bool
	Remote         TelemetryRemote
	PostHogKey     string
	PostHogHost    string
	DisabledEvents []string
	AppVersion     string
	SentryDSN      string
	LocalRetention time.Duration
}

type GitLabConfig struct {
	AllowedHosts []string
	HostTokens   map[string]string
}

var DefaultAllowedOrigins = []string{
	"app://renderer",
}

type Config struct {
	Host                     string
	Port                     int
	RequestTimeout           time.Duration
	ShutdownTimeout          time.Duration
	InstallerShutdownTimeout time.Duration
	RunFilePath              string
	DataDir                  string
	StateDir                 string
	Agent                    string
	AppRunID                 string
	AllowedOrigins           []string
	Telemetry                TelemetryConfig
	HarnessUpdateChecks      bool
	HarnessUpdateInterval    time.Duration
	UpdateWhileRunning       bool
	VersionRefresh           bool
	VersionRefreshIdle       time.Duration
	TUIVersionRefresh        bool
	UpdateWhenIdle           bool
	ManagedHarnessInstalls   bool
	StartupWorkingDirectory  string
	GitLab                   GitLabConfig
	TrackerIntake            bool
	Client                   string
	CloudOffering            bool
	LocalOffering            bool
	CloudControlPlaneURL     string
	AgentBrowserURL          string
	AgentBrowserToken        string
}

func (c Config) Addr() string {
	return net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
}

func Load() (Config, error) {
	cfg := Config{
		Host:            LoopbackHost,
		Port:            DefaultPort,
		RequestTimeout:  DefaultRequestTimeout,
		ShutdownTimeout: DefaultShutdownTimeout,
		Agent:           DefaultAgent,
		AllowedOrigins:  DefaultAllowedOrigins,
		Telemetry: TelemetryConfig{
			Remote:      TelemetryRemoteOff,
			PostHogHost: DefaultTelemetryPostHogHost,
		},
		CloudOffering:          true,
		LocalOffering:          true,
		HarnessUpdateChecks:    true,
		HarnessUpdateInterval:  DefaultHarnessUpdateInterval,
		UpdateWhileRunning:     true,
		VersionRefresh:         true,
		VersionRefreshIdle:     DefaultVersionRefreshIdle,
		TUIVersionRefresh:      true,
		UpdateWhenIdle:         true,
		ManagedHarnessInstalls: true,
	}

	if raw := os.Getenv("AO_PORT"); raw != "" {
		port, err := strconv.Atoi(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AO_PORT %q: %w", raw, err)
		}
		if port < 1 || port > 65535 {
			return Config{}, fmt.Errorf("invalid AO_PORT %d: out of range 1-65535", port)
		}
		cfg.Port = port
	}

	if raw := os.Getenv("AO_REQUEST_TIMEOUT"); raw != "" {
		d, err := parsePositiveDuration("AO_REQUEST_TIMEOUT", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.RequestTimeout = d
	}

	if raw := os.Getenv("AO_SHUTDOWN_TIMEOUT"); raw != "" {
		d, err := parsePositiveDuration("AO_SHUTDOWN_TIMEOUT", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.ShutdownTimeout = d
	}

	if raw := os.Getenv("AO_INSTALLER_SHUTDOWN_TIMEOUT"); raw != "" {
		d, err := parsePositiveDuration("AO_INSTALLER_SHUTDOWN_TIMEOUT", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.InstallerShutdownTimeout = d
	}

	if raw := os.Getenv("AO_AGENT"); raw != "" {
		cfg.Agent = raw
	}

	if raw := os.Getenv("AO_APP_RUN_ID"); raw != "" {
		cfg.AppRunID = raw
	} else {
		cfg.AppRunID = newAppRunID()
	}

	if raw, ok := os.LookupEnv("AO_ALLOWED_ORIGINS"); ok && raw != "" {
		origins := make([]string, 0, 4)
		for _, origin := range strings.Split(raw, ",") {
			origin = strings.TrimSpace(origin)
			if origin == "" {
				continue
			}
			if origin == "null" || origin == "*" {
				return Config{}, fmt.Errorf("invalid AO_ALLOWED_ORIGINS entry %q: wildcard and null origins are not allowed", origin)
			}
			origins = append(origins, origin)
		}
		cfg.AllowedOrigins = origins
	}

	if raw, present := os.LookupEnv("AO_HARNESS_UPDATE_CHECKS"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("AO_HARNESS_UPDATE_CHECKS", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.HarnessUpdateChecks = v
	}
	if raw, present := os.LookupEnv("AO_UPDATE_WHILE_RUNNING"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("AO_UPDATE_WHILE_RUNNING", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.UpdateWhileRunning = v
	}
	if raw, present := os.LookupEnv("AO_VERSION_REFRESH"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("AO_VERSION_REFRESH", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.VersionRefresh = v
	}
	if raw, present := os.LookupEnv("AO_TUI_VERSION_REFRESH"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("AO_TUI_VERSION_REFRESH", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.TUIVersionRefresh = v
	}
	for _, name := range []string{"AO_WINDOWS_UPDATE_WHEN_IDLE", "AO_UPDATE_WHEN_IDLE"} {
		if raw, present := os.LookupEnv(name); present && strings.TrimSpace(raw) != "" {
			v, err := parseToggleEnv(name, raw)
			if err != nil {
				return Config{}, err
			}
			cfg.UpdateWhenIdle = v
		}
	}
	if raw, present := os.LookupEnv("AO_MANAGED_HARNESS_INSTALLS"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("AO_MANAGED_HARNESS_INSTALLS", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.ManagedHarnessInstalls = v
	}
	if raw, present := os.LookupEnv("AO_VERSION_REFRESH_IDLE"); present && strings.TrimSpace(raw) != "" {
		d, err := parsePositiveDuration("AO_VERSION_REFRESH_IDLE", strings.TrimSpace(raw))
		if err != nil {
			return Config{}, err
		}
		cfg.VersionRefreshIdle = d
	}
	if raw, present := os.LookupEnv("AO_HARNESS_UPDATE_INTERVAL"); present && strings.TrimSpace(raw) != "" {
		d, err := parsePositiveDuration("AO_HARNESS_UPDATE_INTERVAL", strings.TrimSpace(raw))
		if err != nil {
			return Config{}, err
		}
		if d < MinHarnessUpdateInterval {
			return Config{}, fmt.Errorf("invalid AO_HARNESS_UPDATE_INTERVAL %q: must be at least %s", raw, MinHarnessUpdateInterval)
		}
		cfg.HarnessUpdateInterval = d
	}
	if raw, present := os.LookupEnv("AO_TELEMETRY_EVENTS"); present && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("AO_TELEMETRY_EVENTS", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.Telemetry.Events = v
		cfg.Telemetry.EventsExplicit = true
	}
	if raw := os.Getenv("AO_TELEMETRY_METRICS"); raw != "" {
		v, err := parseToggleEnv("AO_TELEMETRY_METRICS", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.Telemetry.Metrics = v
	}
	if raw := os.Getenv("AO_TELEMETRY_REMOTE"); raw != "" {
		remote, err := parseTelemetryRemote(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid AO_TELEMETRY_REMOTE %q: %w", raw, err)
		}
		cfg.Telemetry.Remote = remote
	}
	if raw := os.Getenv("AO_TELEMETRY_POSTHOG_KEY"); raw != "" {
		cfg.Telemetry.PostHogKey = raw
	}
	if raw := os.Getenv("AO_TELEMETRY_POSTHOG_HOST"); raw != "" {
		cfg.Telemetry.PostHogHost = raw
	}
	if raw := os.Getenv("AO_TELEMETRY_DISABLED_EVENTS"); raw != "" {
		cfg.Telemetry.DisabledEvents = parseTelemetryDisabledEvents(raw)
	}
	if raw := os.Getenv("AO_TELEMETRY_APP_VERSION"); raw != "" {
		cfg.Telemetry.AppVersion = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("AO_SENTRY_DSN"); raw != "" {
		cfg.Telemetry.SentryDSN = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("AO_TELEMETRY_RETENTION"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			days, derr := strconv.Atoi(raw)
			if derr != nil || days < 1 || uint64(days) > uint64((1<<63-1)/(24*time.Hour)) {
				return Config{}, fmt.Errorf("invalid AO_TELEMETRY_RETENTION %q: must be a Go duration (e.g. 720h) or a number of days", raw)
			}
			d = time.Duration(days) * 24 * time.Hour
		}
		if d < 24*time.Hour {
			return Config{}, fmt.Errorf("invalid AO_TELEMETRY_RETENTION %q: minimum is 1 day (24h)", raw)
		}
		cfg.Telemetry.LocalRetention = d
	}

	if raw, ok := os.LookupEnv("AO_GITLAB_ALLOWED_HOSTS"); ok && raw != "" {
		hosts := make([]string, 0, 4)
		for _, h := range strings.Split(raw, ",") {
			h = strings.TrimSpace(h)
			if h == "" {
				continue
			}
			hosts = append(hosts, h)
		}
		cfg.GitLab.AllowedHosts = hosts
	}

	if raw, ok := os.LookupEnv("AO_GITLAB_HOST_TOKENS"); ok && raw != "" {
		tokens, err := parseHostTokenMap("AO_GITLAB_HOST_TOKENS", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.GitLab.HostTokens = tokens
	}

	if raw, ok := os.LookupEnv("AO_TRACKER_INTAKE"); ok && strings.TrimSpace(raw) != "" {
		v, err := parseToggleEnv("AO_TRACKER_INTAKE", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.TrackerIntake = v
	}

	if raw := os.Getenv("AO_CLIENT"); raw != "" {
		cfg.Client = strings.TrimSpace(raw)
	}
	if raw := os.Getenv("AO_CLOUD_OFFERING"); raw != "" {
		v, err := parseToggleEnv("AO_CLOUD_OFFERING", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.CloudOffering = v
	}
	if raw := os.Getenv("AO_LOCAL_OFFERING"); raw != "" {
		v, err := parseToggleEnv("AO_LOCAL_OFFERING", raw)
		if err != nil {
			return Config{}, err
		}
		cfg.LocalOffering = v
	}
	cfg.CloudControlPlaneURL = defaultCloudControlPlaneURL
	if raw := os.Getenv("AO_CLOUD_CONTROL_PLANE_URL"); strings.TrimSpace(raw) != "" {
		u, err := parseCloudControlPlaneURL(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.CloudControlPlaneURL = u
	}

	if raw := strings.TrimSpace(os.Getenv("AO_AGENT_BROWSER_URL")); raw != "" {
		u, err := parseAgentBrowserURL(raw)
		if err != nil {
			return Config{}, err
		}
		cfg.AgentBrowserURL = u
		cfg.AgentBrowserToken = strings.TrimSpace(os.Getenv("AO_AGENT_BROWSER_TOKEN"))
		if cfg.AgentBrowserToken == "" {
			return Config{}, fmt.Errorf("AO_AGENT_BROWSER_URL is set but AO_AGENT_BROWSER_TOKEN is empty: the agent browser refuses every request without its token")
		}
	}

	stateDir, dataDir, runFile, err := ResolvePaths()
	if err != nil {
		return Config{}, err
	}
	cfg.StateDir, cfg.DataDir, cfg.RunFilePath = stateDir, dataDir, runFile

	return cfg, nil
}

var telemetryEnvKeys = []string{
	"AO_TELEMETRY_EVENTS",
	"AO_TELEMETRY_METRICS",
	"AO_TELEMETRY_REMOTE",
	"AO_TELEMETRY_POSTHOG_KEY",
	"AO_TELEMETRY_POSTHOG_HOST",
	"AO_TELEMETRY_DISABLED_EVENTS",
	"AO_TELEMETRY_APP_VERSION",
	"AO_SENTRY_DSN",
}

func ScrubTelemetryEnv() {
	for _, key := range telemetryEnvKeys {
		_ = os.Unsetenv(key)
	}
}

func parseToggleEnv(name, raw string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "on", "true", "1", "yes":
		return true, nil
	case "off", "false", "0", "no":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be off|on", name)
	}
}

func parseTelemetryRemote(raw string) (TelemetryRemote, error) {
	switch TelemetryRemote(strings.ToLower(strings.TrimSpace(raw))) {
	case TelemetryRemoteOff:
		return TelemetryRemoteOff, nil
	case TelemetryRemotePostHog:
		return TelemetryRemotePostHog, nil
	default:
		return "", fmt.Errorf("must be off|posthog")
	}
}

func parseTelemetryDisabledEvents(raw string) []string {
	var names []string
	for _, part := range strings.Split(raw, ",") {
		if name := strings.TrimSpace(part); name != "" {
			names = append(names, name)
		}
	}
	return names
}

func parseHostTokenMap(name, raw string) (map[string]string, error) {
	tokens := make(map[string]string, 4)
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		eq := strings.IndexByte(entry, '=')
		if eq < 0 {
			continue
		}
		host := strings.TrimSpace(entry[:eq])
		token := strings.TrimSpace(entry[eq+1:])
		if host == "" {
			continue
		}
		if strings.ContainsRune(token, '=') {
			return nil, fmt.Errorf("invalid %s entry %q: token contains '='", name, entry)
		}
		tokens[host] = token
	}
	return tokens, nil
}

func parseCloudControlPlaneURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid AO_CLOUD_CONTROL_PLANE_URL %q: %w", raw, err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid AO_CLOUD_CONTROL_PLANE_URL %q: must be an http(s) URL", raw)
	}
	return trimmed, nil
}

func parseAgentBrowserURL(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid AO_AGENT_BROWSER_URL %q: %w", raw, err)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || ip != nil && ip.IsLoopback()
	if u.Scheme != "http" || u.User != nil || u.RawQuery != "" || !loopback {
		return "", fmt.Errorf("invalid AO_AGENT_BROWSER_URL %q: must be a loopback http URL without credentials or query", raw)
	}
	return strings.TrimRight(raw, "/"), nil
}

func parsePositiveDuration(name, raw string) (time.Duration, error) {
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", name, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s %q: must be > 0", name, raw)
	}
	return d, nil
}

func newAppRunID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "apprun-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "apprun-" + hex.EncodeToString(buf)
}

func resolveRunFilePath() (string, error) {
	if p, ok := os.LookupEnv("AO_RUN_FILE"); ok && p != "" {
		return absOverride("AO_RUN_FILE", p)
	}
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "running.json"), nil
}

func resolveDataDir() (string, error) {
	if p, ok := os.LookupEnv("AO_DATA_DIR"); ok && p != "" {
		return absOverride("AO_DATA_DIR", p)
	}
	stateDir, err := defaultStateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(stateDir, "data"), nil
}

func defaultStateDir() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve state dir: %w", err)
	}
	return filepath.Join(homeDir, ".ao"), nil
}

func absOverride(name, p string) (string, error) {
	abs, err := filepath.Abs(p)
	if err != nil {
		return "", fmt.Errorf("resolve %s %q: %w", name, p, err)
	}
	return abs, nil
}

func ResolvePaths() (stateDir, dataDir, runFile string, err error) {
	dataDir, err = resolveDataDir()
	if err != nil {
		return
	}
	runFile, err = resolveRunFilePath()
	if err != nil {
		return
	}
	stateDir = filepath.Dir(dataDir)
	if os.Getenv("AO_DATA_DIR") != "" {
		stateDir = dataDir
	}
	return
}
