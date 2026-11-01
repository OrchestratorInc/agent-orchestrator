package telemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Segmentation context stamped on every daemon/CLI event: which OS and CPU the
// install runs on, how it was installed, which update channel the user chose,
// and whether it is a CI or container run. Without it, ~60% of product events
// (everything the daemon exports) cannot be split by OS or acquisition source,
// and a one-shot CI fleet is indistinguishable from real installs.

// Files the desktop app writes beside running.json (dirname of AO_RUN_FILE).
// The daemon only reads them; frontend/src/main/app-state.ts and
// frontend/src/main/update-settings.ts own the formats.
const (
	appStateFileName       = "app-state.json"
	updateSettingsFileName = "update-settings.json"
)

// installContextTTL bounds how stale the file-backed values can be. A user can
// switch update channel without restarting the daemon, but re-reading two small
// files on every event would be waste.
const installContextTTL = 5 * time.Minute

// InstallSources is the closed vocabulary for install_source. The desktop app
// records it once in app-state.json (frontend/src/shared/install-environment.ts keeps
// the same list): npm-bootstrap is `ao start` fetching the app, the installer
// formats are detected from the running bundle, dev is an unpackaged build,
// and unknown means the app has not recorded one (a CLI-only install has no
// app-state.json at all).
var InstallSources = map[string]struct{}{
	"npm-bootstrap": {},
	"dmg":           {},
	"brew-cask":     {},
	"exe":           {},
	"appimage":      {},
	"deb":           {},
	"rpm":           {},
	"dev":           {},
	"unknown":       {},
}

// NormalizeInstallSource maps a recorded value onto InstallSources. The value
// arrives from a command-line flag, so anything off-list becomes "other" rather
// than free text reaching PostHog.
func NormalizeInstallSource(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "unknown"
	}
	if _, ok := InstallSources[v]; ok {
		return v
	}
	return "other"
}

// staticEnvironment is computed once per process: none of it changes while
// the daemon runs.
type staticEnvironment struct {
	os        string
	osVersion string
	platform  string
	arch      string
	isCI      bool
	ciSignal  string
}

func (e staticEnvironment) stamp(props map[string]any) {
	// $os/$os_version use posthog-js's names and values, so renderer and daemon
	// events share one OS breakdown; platform and arch use Node's values, which
	// the renderer sends too.
	if e.os != "" {
		props["$os"] = e.os
	}
	if e.osVersion != "" {
		props["$os_version"] = e.osVersion
	}
	props["platform"] = e.platform
	props["arch"] = e.arch
	props["is_ci"] = e.isCI
	if e.ciSignal != "" {
		props["ci_signal"] = e.ciSignal
	}
}

func detectStaticEnvironment() staticEnvironment {
	isCI, signal := detectCI(os.Getenv, fileExists)
	return staticEnvironment{
		os:        posthogOSName(runtime.GOOS),
		osVersion: safeOSVersion(osVersion()),
		platform:  nodePlatform(runtime.GOOS),
		arch:      nodeArch(runtime.GOARCH),
		isCI:      isCI,
		ciSignal:  signal,
	}
}

// posthogOSName matches the $os values posthog-js derives from the renderer's
// user agent.
func posthogOSName(goos string) string {
	switch goos {
	case "darwin":
		return "Mac OS X"
	case "windows":
		return "Windows"
	case "linux":
		return "Linux"
	default:
		return ""
	}
}

// nodePlatform matches process.platform, which the renderer sends as platform.
func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

// nodeArch matches process.arch, which the renderer sends as arch.
func nodeArch(goarch string) string {
	switch goarch {
	case "amd64":
		return "x64"
	case "386":
		return "ia32"
	default:
		return goarch
	}
}

// osVersionPattern admits only a version-shaped token (13.6.1, 10.0.22631,
// 6.8.0-45-generic), never anything that could carry a hostname or path.
var osVersionPattern = regexp.MustCompile(`^\d[0-9A-Za-z.+_-]{0,63}$`)

func safeOSVersion(v string) string {
	v = strings.TrimSpace(v)
	if osVersionPattern.MatchString(v) {
		return v
	}
	return ""
}

// ciEnvVars are set by common CI providers. CI itself is the de facto standard
// and is checked separately so CI=false opts back out.
var ciEnvVars = []string{
	"GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TRAVIS", "JENKINS_URL",
	"HUDSON_URL", "TEAMCITY_VERSION", "TF_BUILD", "BITBUCKET_BUILD_NUMBER",
	"CODEBUILD_BUILD_ID", "DRONE", "APPVEYOR", "SEMAPHORE", "BITRISE_IO", "CIRRUS_CI",
	"BUILDKITE_AGENT_ID", "WOODPECKER", "HARNESS_BUILD_ID",
}

// detectCI flags a CI runner or a container. ci_signal says which, so a
// dashboard can drop CI runs and decide separately about containers (a dev
// container can be a real person). The frontend mirror is
// frontend/src/shared/install-environment.ts.
func detectCI(getenv func(string) string, exists func(string) bool) (bool, string) {
	if v := strings.ToLower(strings.TrimSpace(getenv("CI"))); v != "" && v != "false" && v != "0" {
		return true, "ci_env"
	}
	for _, name := range ciEnvVars {
		if strings.TrimSpace(getenv(name)) != "" {
			return true, "ci_env"
		}
	}
	if getenv("KUBERNETES_SERVICE_HOST") != "" || getenv("container") != "" ||
		exists("/.dockerenv") || exists("/run/.containerenv") {
		return true, "container"
	}
	return false, ""
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// installContext reads the desktop app's markers under the state dir (~/.ao).
type installContext struct {
	stateDir string
	now      func() time.Time

	mu             sync.Mutex
	loadedAt       time.Time
	installSource  string
	installedAt    string
	releaseChannel string
}

func newInstallContext(stateDir string, now func() time.Time) *installContext {
	if now == nil {
		now = time.Now
	}
	return &installContext{stateDir: stateDir, now: now}
}

func (c *installContext) stamp(props map[string]any) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if c.loadedAt.IsZero() || c.now().Sub(c.loadedAt) >= installContextTTL {
		c.load()
		c.loadedAt = c.now()
	}
	source, installedAt, channel := c.installSource, c.installedAt, c.releaseChannel
	c.mu.Unlock()
	props["install_source"] = source
	if installedAt != "" {
		props["installed_at"] = installedAt
	}
	props["release_channel"] = channel
}

func (c *installContext) load() {
	c.installSource, c.installedAt, c.releaseChannel = "unknown", "", "unknown"
	if c.stateDir == "" {
		return
	}
	var state struct {
		InstalledAt   string `json:"installedAt"`
		InstallSource string `json:"installSource"`
	}
	if readJSON(filepath.Join(c.stateDir, appStateFileName), &state) {
		c.installSource = NormalizeInstallSource(state.InstallSource)
		c.installedAt = installDay(state.InstalledAt)
	}
	var settings struct {
		Channel string          `json:"channel"`
		Feature json.RawMessage `json:"feature"`
	}
	if readJSON(filepath.Join(c.stateDir, updateSettingsFileName), &settings) {
		c.releaseChannel = releaseChannelFrom(settings.Channel, settings.Feature)
	}
}

// installDay coarsens the recorded install time to its UTC day: enough to
// cohort by install date, no finer than install_age_days already is.
func installDay(raw string) string {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.DateOnly)
}

// releaseChannelFrom mirrors the renderer's releaseChannelFrom: what the user
// opted into, as opposed to version_channel (what is running).
func releaseChannelFrom(channel string, feature json.RawMessage) string {
	if f := strings.TrimSpace(string(feature)); f != "" && f != "null" {
		return "feature"
	}
	switch channel {
	case "nightly":
		return "nightly"
	case "latest":
		return "stable"
	default:
		return "unknown"
	}
}

func readJSON(path string, out any) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return json.Unmarshal(b, out) == nil
}
