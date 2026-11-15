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

const (
	appStateFileName       = "app-state.json"
	updateSettingsFileName = "update-settings.json"
)

const installContextTTL = 5 * time.Minute

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

type staticEnvironment struct {
	os          string
	osVersion   string
	platform    string
	arch        string
	isCI        bool
	isContainer bool
}

func (e staticEnvironment) stamp(props map[string]any) {
	if e.os != "" {
		props["$os"] = e.os
	}
	if e.osVersion != "" {
		props["$os_version"] = e.osVersion
	}
	props["platform"] = e.platform
	props["arch"] = e.arch
	props["is_ci"] = e.isCI
	props["is_container"] = e.isContainer
}

func detectStaticEnvironment() staticEnvironment {
	isCI, isContainer := detectCI(os.Getenv, fileExists)
	return staticEnvironment{
		os:          posthogOSName(runtime.GOOS),
		osVersion:   safeOSVersion(osVersion()),
		platform:    nodePlatform(runtime.GOOS),
		arch:        nodeArch(runtime.GOARCH),
		isCI:        isCI,
		isContainer: isContainer,
	}
}

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

func nodePlatform(goos string) string {
	if goos == "windows" {
		return "win32"
	}
	return goos
}

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

var osVersionPattern = regexp.MustCompile(`^\d[0-9A-Za-z.+_-]{0,63}$`)

func safeOSVersion(v string) string {
	v = strings.TrimSpace(v)
	if osVersionPattern.MatchString(v) {
		return v
	}
	return ""
}

var ciEnvVars = []string{
	"GITHUB_ACTIONS", "GITLAB_CI", "BUILDKITE", "CIRCLECI", "TRAVIS", "JENKINS_URL",
	"HUDSON_URL", "TEAMCITY_VERSION", "TF_BUILD", "BITBUCKET_BUILD_NUMBER",
	"CODEBUILD_BUILD_ID", "DRONE", "APPVEYOR", "SEMAPHORE", "BITRISE_IO", "CIRRUS_CI",
	"BUILDKITE_AGENT_ID", "WOODPECKER", "HARNESS_BUILD_ID",
}

func detectCI(getenv func(string) string, exists func(string) bool) (bool, bool) {
	isContainer := getenv("KUBERNETES_SERVICE_HOST") != "" || getenv("container") != "" ||
		exists("/.dockerenv") || exists("/run/.containerenv")
	if v := strings.ToLower(strings.TrimSpace(getenv("CI"))); v != "" && v != "false" && v != "0" {
		return true, isContainer
	}
	for _, name := range ciEnvVars {
		if strings.TrimSpace(getenv(name)) != "" {
			return true, isContainer
		}
	}
	return false, isContainer
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

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

func installDay(raw string) string {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return t.UTC().Format(time.DateOnly)
}

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
