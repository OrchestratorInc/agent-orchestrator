// Package hostupdate updates a self-hosted (systemd) Orchestrator host in place.
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
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	updateUnit   = "ao-self-hosted-update.service"
	serviceUnit  = "ao-self-hosted.service"
	installerRun = `set -euo pipefail
installer="$(mktemp)"
trap 'rm -f -- "$installer"' EXIT
curl -fsSL --retry 3 --connect-timeout 20 --max-time 120 "$1" -o "$installer"
shift
bash "$installer" "$@"`
)

var installerURL = "https://raw.githubusercontent.com/OrchestratorInc/agent-orchestrator/main/scripts/setup-self-hosted.sh"

// Start refuses with these.
var (
	ErrUnsupported = errors.New("this host was not installed as a self-hosted service; update Orchestrator on that computer")
	ErrInProgress  = errors.New("an update is already running on this host")
)

// State is the last update's progress as systemd reports it.
type State string

// States of the last update.
const (
	StateIdle     State = "idle"
	StateUpdating State = "updating"
	StateFailed   State = "failed"
)

// Status is this host's version and its last update.
type Status struct {
	Version   string
	Supported bool
	State     State
	Error     string
}

type runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Updater reads and starts this host's update.
type Updater struct {
	mu      sync.Mutex
	version string
	root    string
	run     runner
	getenv  func(string) string
}

// New returns an updater that supports only a systemd host installed by setup-self-hosted.sh.
func New(version string) *Updater {
	u := &Updater{version: version, run: runCommand, getenv: os.Getenv}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
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
	if root == "" || !serviceInstalled() {
		return u
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
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

func serviceInstalled() bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	_, err = os.Stat(filepath.Join(home, ".config", "systemd", "user", serviceUnit))
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

// Status reads the update unit's state from systemd.
func (u *Updater) Status(ctx context.Context) Status {
	status := Status{Version: u.version, Supported: u.root != "", State: StateIdle}
	if !status.Supported {
		return status
	}
	switch u.activeState(ctx) {
	case "active", "activating", "reloading", "deactivating":
		status.State = StateUpdating
	case "failed":
		status.State = StateFailed
		status.Error = u.lastLine(ctx)
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

// Start runs the installer in a transient unit and returns once it is running.
func (u *Updater) Start(ctx context.Context) error {
	if u.root == "" {
		return ErrUnsupported
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.Status(ctx).State == StateUpdating {
		return ErrInProgress
	}
	_, _ = u.run(ctx, "systemctl", "--user", "reset-failed", updateUnit)
	_, err := u.run(ctx, "systemd-run", u.startArgs()...)
	return err
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

func (u *Updater) startArgs() []string {
	args := []string{"--user", "--unit=" + updateUnit, "--description=Orchestrator self-hosted update", "--property=RuntimeMaxSec=30min"}
	for _, name := range []string{"PATH", "AO_DATA_DIR", "AO_RUN_FILE", "AO_PORT"} {
		if value := u.getenv(name); value != "" {
			args = append(args, "--setenv="+name+"="+value)
		}
	}
	if !u.defaultRoot() {
		args = append(args, "--setenv=AO_HOST_INSTALL_DIR="+u.root)
	}
	args = append(args, "/bin/bash", "-c", installerRun, "ao-host-update", installerURL, "--upgrade")
	if strings.Contains(u.version, "-nightly.") {
		args = append(args, "--nightly")
	}
	return args
}
