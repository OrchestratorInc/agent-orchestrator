package daemon

import (
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

// envKeepDevLauncher lets a developer who runs scripts/daemon-build.sh beside
// the desktop app keep that build as the ao on PATH.
const envKeepDevLauncher = "AO_KEEP_DEV_LAUNCHER"

// repointDevLaunchers points the ao launcher scripts/daemon-build.sh leaves on
// PATH at this packaged daemon. Login shells apply the user's startup files
// after AO's session PATH pin: Codex runs every agent command through one, and
// every terminal is one. A ~/.zprofile that prepends ~/.local/bin then hands
// agents and users that launcher's old source build (#3562). Only the packaged
// desktop daemon at a durable path acts, and only on a symlink whose target is
// exactly that build; any other ao is left for `ao doctor` to report.
func repointDevLaunchers(log *slog.Logger, goos string, executable func() (string, error), getenv func(string) string) {
	exe, ok := durableDesktopExecutable(goos, executable, getenv)
	if !ok {
		return
	}
	if keep, _ := strconv.ParseBool(getenv(envKeepDevLauncher)); keep {
		return
	}
	dirs := filepath.SplitList(getenv("PATH"))
	// scripts/daemon-build.sh installs into one of these when ao is not already
	// on PATH, and a daemon started without the login-shell PATH may omit them.
	if home := getenv("HOME"); filepath.IsAbs(home) {
		dirs = append(dirs, filepath.Join(home, ".local", "bin"), filepath.Join(home, "go", "bin"))
	}
	for _, gopath := range filepath.SplitList(getenv("GOPATH")) {
		dirs = append(dirs, filepath.Join(gopath, "bin"))
	}
	dirs = append(dirs, "/usr/local/bin", "/opt/homebrew/bin")
	repointed, err := agentlaunch.RepointDevLaunchers(exe, dirs, agentlaunch.DevLauncherBuildPaths(getenv))
	for _, link := range repointed {
		log.Info("repointed the scripts/daemon-build.sh ao launcher at the desktop app's ao",
			"link", link, "target", exe, "opt_out", envKeepDevLauncher+"=1")
	}
	if err != nil {
		log.Warn("repoint scripts/daemon-build.sh ao launcher", "err", err)
	}
}

// durableDesktopExecutable returns this daemon's path when it is the packaged
// desktop app's bundled binary at a path that survives restarts:
// <resources>/daemon/ao inside an installed app. A dev, test or headless
// daemon never qualifies, and neither does an AppImage (remounted under /tmp
// on every launch) or a macOS app run from a disk image or App Translocation.
func durableDesktopExecutable(goos string, executable func() (string, error), getenv func(string) string) (string, bool) {
	if goos != "darwin" && goos != "linux" {
		return "", false
	}
	exe, err := executable()
	if err != nil || !filepath.IsAbs(exe) {
		return "", false
	}
	exe = filepath.Clean(exe)
	daemonDir := filepath.Dir(exe)
	if filepath.Base(exe) != "ao" || filepath.Base(daemonDir) != "daemon" ||
		!strings.EqualFold(filepath.Base(filepath.Dir(daemonDir)), "resources") {
		return "", false
	}
	switch goos {
	case "darwin":
		if !strings.Contains(exe, ".app/Contents/Resources/daemon/") ||
			strings.Contains(exe, "/AppTranslocation/") || strings.HasPrefix(exe, "/Volumes/") {
			return "", false
		}
	case "linux":
		if getenv("AO_APPIMAGE") != "" || getenv("APPIMAGE") != "" || strings.HasPrefix(exe, "/tmp/.mount_") {
			return "", false
		}
	}
	return exe, true
}
