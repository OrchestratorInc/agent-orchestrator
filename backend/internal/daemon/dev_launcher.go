package daemon

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

// envLoginShellEnv is set to 1 by the desktop when the daemon env includes the
// user's login-shell exports. Without it the daemon cannot see an
// AO_KEEP_DEV_LAUNCHER=1 from a shell profile, so it leaves launchers alone.
const envLoginShellEnv = "AO_LOGIN_SHELL_ENV"

// repointDevLaunchers points the ao launcher scripts/daemon-build.sh leaves on
// PATH at this packaged daemon. Login shells apply the user's startup files
// after AO's session PATH pin: Codex runs every agent command through one, and
// every terminal is one. A ~/.zprofile that prepends ~/.local/bin then hands
// agents and users that launcher's old source build (#3562). Only an installed
// desktop daemon acts, and only on a symlink whose target is exactly that
// build, or a link it made earlier to an install that no longer exists; any
// other ao is left for `ao doctor` to report.
func repointDevLaunchers(log *slog.Logger, goos string, executable func() (string, error), getenv func(string) string) {
	exe, ok := durableDesktopExecutable(goos, executable, getenv)
	if !ok {
		return
	}
	if getenv(envLoginShellEnv) != "1" {
		log.Info("left the scripts/daemon-build.sh ao launcher alone: the login-shell environment did not reach the daemon, so " +
			agentlaunch.EnvKeepDevLauncher + " cannot be checked")
		return
	}
	if keep, _ := strconv.ParseBool(getenv(agentlaunch.EnvKeepDevLauncher)); keep {
		return
	}
	home := getenv("HOME")
	dirs := filepath.SplitList(getenv("PATH"))
	// scripts/daemon-build.sh installs into one of these when ao is not already
	// on PATH, and a daemon started without the login-shell PATH may omit them.
	if filepath.IsAbs(home) {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, "go", "bin"))
	}
	for _, gopath := range filepath.SplitList(getenv("GOPATH")) {
		dirs = append(dirs, filepath.Join(gopath, "bin"))
	}
	dirs = append(dirs, "/usr/local/bin", "/opt/homebrew/bin")
	buildPaths := agentlaunch.DevLauncherBuildPaths(getenv)
	owned := func(target string) bool {
		if slices.Contains(buildPaths, target) {
			return true
		}
		// A link this repair made for an install since moved or removed.
		if target == exe || !agentlaunch.IsDesktopDaemonPath(goos, target, home) {
			return false
		}
		_, err := os.Lstat(target)
		return errors.Is(err, fs.ErrNotExist)
	}
	repointed, err := agentlaunch.RepointDevLaunchers(exe, dirs, owned)
	for _, link := range repointed {
		log.Info("repointed the scripts/daemon-build.sh ao launcher at the desktop app's ao",
			"link", link, "target", exe, "opt_out", agentlaunch.EnvKeepDevLauncher+"=1")
	}
	if err != nil {
		log.Warn("repoint scripts/daemon-build.sh ao launcher", "err", err)
	}
}

// durableDesktopExecutable returns this daemon's path when it is an installed
// desktop app's bundled binary (see agentlaunch.IsDesktopDaemonPath). A dev,
// test or headless daemon never qualifies, and neither does an AppImage, which
// is remounted under /tmp on every launch.
func durableDesktopExecutable(goos string, executable func() (string, error), getenv func(string) string) (string, bool) {
	exe, err := executable()
	if err != nil || !agentlaunch.IsDesktopDaemonPath(goos, exe, getenv("HOME")) {
		return "", false
	}
	if goos == "linux" && (getenv("AO_APPIMAGE") != "" || getenv("APPIMAGE") != "") {
		return "", false
	}
	return filepath.Clean(exe), true
}
