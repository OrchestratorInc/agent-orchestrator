// Package hostupdate updates an Orchestrator host in place: a self-hosted
// service (systemd or launchd), or the desktop app that owns this daemon.
package hostupdate

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	updateUnit   = "ao-self-hosted-update.service"
	serviceUnit  = "ao-self-hosted.service"
	launchAgent  = "dev.aoagents.self-hosted"
	updateLabel  = "dev.aoagents.self-hosted-update"
	installerRun = `set -euo pipefail
installer="$(mktemp)"
trap 'rm -f -- "$installer"' EXIT
curl -fsSL --retry 3 --connect-timeout 20 --max-time 120 "$1" -o "$installer"
shift
bash "$installer" "$@"`
	launchdRun = `( sleep 1800; echo "The update took longer than 30 minutes." >&2; kill $$ ) &
` + installerRun
)

var installerURL = "https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/main/scripts/setup-self-hosted.sh"

// Start refuses with these.
var (
	ErrUnsupported = errors.New("this host was not installed as a self-hosted service; update Orchestrator on that computer")
	ErrInProgress  = errors.New("an update is already running on this host")
)

// State is the last update's progress.
type State string

// States of the last update.
const (
	StateIdle     State = "idle"
	StateUpdating State = "updating"
	StateFailed   State = "failed"
)

// Methods a host updates by.
const (
	MethodService = "service"
	MethodApp     = "app"
)

type kind string

const (
	kindSystemd kind = "systemd"
	kindLaunchd kind = "launchd"
	kindApp     kind = "app"
)

// Status is this host's version and its last update.
type Status struct {
	Version   string
	Supported bool
	Method    string
	State     State
	Error     string
}

type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Updater reads and starts this host's update.
type Updater struct {
	mu      sync.Mutex
	version string
	kind    kind
	root    string
	run     runner
	getenv  func(string) string

	app         AppControl
	resultPath  string
	restartWait time.Duration
	appRunning  bool
	appErr      string
	notApplied  string
}

// New picks how this host updates: through the desktop app that owns this
// daemon on macOS, or through the service setup-self-hosted.sh installed.
func New(version, dataDir string, app AppControl) *Updater {
	u := &Updater{version: version, run: runCommand, getenv: os.Getenv}
	if runtime.GOOS == "darwin" && os.Getenv("AO_OWNER") == "app" && app != nil {
		u.kind, u.app, u.resultPath, u.restartWait = kindApp, app, filepath.Join(dataDir, resultFile), 2*time.Minute
		u.loadAppResult()
		return u
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return u
	}
	root := installRoot(exe)
	home, err := os.UserHomeDir()
	if root == "" || err != nil {
		return u
	}
	switch {
	case runtime.GOOS == "linux" && runtime.GOARCH == "amd64" && exists(filepath.Join(home, ".config", "systemd", "user", serviceUnit)) && onPath("systemd-run"):
		u.kind = kindSystemd
	case runtime.GOOS == "darwin" && exists(filepath.Join(home, "Library", "LaunchAgents", launchAgent+".plist")) && onPath("launchctl"):
		u.kind = kindLaunchd
	default:
		return u
	}
	u.root = root
	return u
}

func installRoot(exe string) string {
	daemonDir := filepath.Dir(exe)
	resources := filepath.Dir(daemonDir)
	release := filepath.Dir(resources)
	releases := filepath.Dir(release)
	if filepath.Base(exe) != "ao" || filepath.Base(daemonDir) != "daemon" || filepath.Base(resources) != "resources" || filepath.Base(releases) != "releases" {
		return ""
	}
	return filepath.Dir(releases)
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func onPath(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	out, err := process.CommandContext(ctx, name, args...).Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && len(bytes.TrimSpace(exitErr.Stderr)) > 0 {
		return out, fmt.Errorf("%w: %s", err, bytes.TrimSpace(exitErr.Stderr))
	}
	return out, err
}

// Status reads the last update's state.
func (u *Updater) Status(ctx context.Context) Status {
	status := Status{Version: u.version, Supported: u.kind != "", State: StateIdle}
	switch u.kind {
	case kindSystemd:
		status.Method = MethodService
		switch u.activeState(ctx) {
		case "active", "activating", "reloading", "deactivating":
			status.State = StateUpdating
		case "failed":
			status.State = StateFailed
			status.Error = u.lastLine(ctx)
		}
	case kindLaunchd:
		status.Method = MethodService
		status.State = u.launchdState(ctx)
		if status.State == StateFailed {
			status.Error = lastLogLine(u.logPath())
		}
	case kindApp:
		status.Method = MethodApp
		u.mu.Lock()
		switch {
		case u.appRunning:
			status.State = StateUpdating
		case u.appErr != "":
			status.State, status.Error = StateFailed, u.appErr
		}
		u.mu.Unlock()
	}
	return status
}

func (u *Updater) activeState(ctx context.Context) string {
	out, err := u.run(ctx, "systemctl", "--user", "show", "--property=ActiveState", "--value", updateUnit)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (u *Updater) lastLine(ctx context.Context) string {
	invocation, err := u.run(ctx, "systemctl", "--user", "show", "--property=InvocationID", "--value", updateUnit)
	if err != nil || strings.TrimSpace(string(invocation)) == "" {
		return ""
	}
	out, err := u.run(ctx, "journalctl", "--user", "_SYSTEMD_INVOCATION_ID="+strings.TrimSpace(string(invocation)), "--lines=20", "--output=cat", "--no-pager")
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func launchdDomain() string { return "gui/" + strconv.Itoa(os.Getuid()) }

func (u *Updater) launchdState(ctx context.Context) State {
	out, err := u.run(ctx, "launchctl", "print", launchdDomain()+"/"+updateLabel)
	if err != nil {
		return StateIdle
	}
	state := StateIdle
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimPrefix(line, "\t"), " = ")
		if !ok || strings.HasPrefix(key, "\t") {
			continue
		}
		switch {
		case key == "state" && value == "running":
			return StateUpdating
		case key == "last exit code" && value != "0" && value != "(never exited)", key == "last terminating signal":
			state = StateFailed
		}
	}
	return state
}

func (u *Updater) logPath() string { return filepath.Join(u.root, "logs", "update.log") }

func lastLogLine(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// Start begins the update and returns once it is running.
func (u *Updater) Start(ctx context.Context) error {
	switch u.kind {
	case "":
		return ErrUnsupported
	case kindApp:
		return u.startApp()
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Status(ctx).State == StateUpdating {
		return ErrInProgress
	}
	if u.kind == kindLaunchd {
		return u.startLaunchd(ctx)
	}
	_, _ = u.run(ctx, "systemctl", "--user", "reset-failed", updateUnit)
	_, err := u.run(ctx, "systemd-run", u.startArgs()...)
	return err
}

func (u *Updater) startLaunchd(ctx context.Context) error {
	if err := os.MkdirAll(filepath.Dir(u.logPath()), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(u.logPath(), nil, 0o600); err != nil {
		return err
	}
	plist := filepath.Join(u.root, "update.plist")
	if err := os.WriteFile(plist, u.launchdPlist(), 0o600); err != nil {
		return err
	}
	_, _ = u.run(ctx, "launchctl", "bootout", launchdDomain()+"/"+updateLabel)
	_, err := u.run(ctx, "launchctl", "bootstrap", launchdDomain(), plist)
	return err
}

func (u *Updater) launchdPlist() []byte {
	var b bytes.Buffer
	text := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + updateLabel + `</string>
<key>ProgramArguments</key><array>`)
	for _, arg := range append([]string{"/bin/bash", "-c", launchdRun}, u.installerArgs()...) {
		b.WriteString("<string>" + text(arg) + "</string>")
	}
	b.WriteString("</array>\n<key>EnvironmentVariables</key><dict>")
	for _, pair := range u.installerEnv() {
		name, value, _ := strings.Cut(pair, "=")
		b.WriteString("<key>" + text(name) + "</key><string>" + text(value) + "</string>")
	}
	b.WriteString("</dict>\n<key>RunAtLoad</key><true/>\n")
	b.WriteString("<key>StandardOutPath</key><string>" + text(u.logPath()) + "</string>\n")
	b.WriteString("<key>StandardErrorPath</key><string>" + text(u.logPath()) + "</string>\n</dict></plist>\n")
	return b.Bytes()
}

func (u *Updater) defaultRoot() bool {
	home := u.getenv("HOME")
	if home == "" {
		return false
	}
	want := filepath.Join(home, ".ao", "host")
	if resolved, err := filepath.EvalSymlinks(want); err == nil {
		want = resolved
	}
	return want == u.root
}

func (u *Updater) installerEnv() []string {
	var env []string
	for _, name := range []string{"PATH", "HOME", "AO_DATA_DIR", "AO_RUN_FILE", "AO_PORT"} {
		if value := u.getenv(name); value != "" {
			env = append(env, name+"="+value)
		}
	}
	if !u.defaultRoot() {
		env = append(env, "AO_HOST_INSTALL_DIR="+u.root)
	}
	return env
}

func (u *Updater) installerArgs() []string {
	args := []string{"ao-host-update", installerURL, "--upgrade"}
	if strings.Contains(u.version, "-nightly.") {
		args = append(args, "--nightly")
	}
	return args
}

func (u *Updater) startArgs() []string {
	args := []string{"--user", "--unit=" + updateUnit, "--description=Orchestrator self-hosted update", "--property=RuntimeMaxSec=30min"}
	for _, pair := range u.installerEnv() {
		args = append(args, "--setenv="+pair)
	}
	return slices.Concat(args, []string{"/bin/bash", "-c", installerRun}, u.installerArgs())
}
