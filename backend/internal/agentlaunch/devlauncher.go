package agentlaunch

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// EnvKeepDevLauncher lets a developer who runs scripts/daemon-build.sh beside
// the desktop app keep that build as the ao on PATH.
const EnvKeepDevLauncher = "AO_KEEP_DEV_LAUNCHER"

// devLauncherBuild is where scripts/daemon-build.sh builds ao for a source
// checkout, relative to the user's cache directory.
var devLauncherBuild = filepath.Join("aoagents", "agent-orchestrator", "bin", aoBinaryName)

// DevLauncherBuildPaths returns where scripts/daemon-build.sh builds ao:
// ${XDG_CACHE_HOME:-$HOME/.cache}/aoagents/agent-orchestrator/bin/ao. Both
// spellings are returned because the build may have run under a different
// XDG_CACHE_HOME than the caller sees.
func DevLauncherBuildPaths(getenv func(string) string) []string {
	var paths []string
	if cache := getenv("XDG_CACHE_HOME"); filepath.IsAbs(cache) {
		paths = append(paths, filepath.Join(cache, devLauncherBuild))
	}
	if home := getenv("HOME"); filepath.IsAbs(home) {
		paths = append(paths, filepath.Join(home, ".cache", devLauncherBuild))
	}
	return paths
}

// IsDesktopDaemonPath reports whether exe is the daemon an installed desktop
// app bundles: <name>.app/Contents/Resources/daemon/ao under /Applications or
// ~/Applications on macOS, and <install>/resources/daemon/ao under /usr or
// /opt on Linux, where the deb and rpm packages put it. An app packaged from a
// checkout, run from a disk image or App Translocation, or unpacked from a zip
// elsewhere is not an install and never qualifies.
func IsDesktopDaemonPath(goos, exe, home string) bool {
	if !filepath.IsAbs(exe) {
		return false
	}
	exe = filepath.Clean(exe)
	daemonDir := filepath.Dir(exe)
	resources := filepath.Dir(daemonDir)
	if filepath.Base(exe) != aoBinaryName || filepath.Base(daemonDir) != "daemon" {
		return false
	}
	var roots []string
	switch goos {
	case "darwin":
		contents := filepath.Dir(resources)
		if filepath.Base(resources) != "Resources" || filepath.Base(contents) != "Contents" ||
			filepath.Ext(filepath.Dir(contents)) != ".app" {
			return false
		}
		roots = []string{"/Applications"}
		if filepath.IsAbs(home) {
			roots = append(roots, filepath.Join(filepath.Clean(home), "Applications"))
		}
	case "linux":
		if filepath.Base(resources) != "resources" {
			return false
		}
		roots = []string{"/usr", "/opt"}
	default:
		return false
	}
	for _, root := range roots {
		if strings.HasPrefix(exe, root+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// launcherTarget returns the absolute target of the symlink at path.
func launcherTarget(path string) (string, bool) {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return "", false
	}
	target, err := os.Readlink(path)
	if err != nil || !filepath.IsAbs(target) {
		return "", false
	}
	return filepath.Clean(target), true
}

// IsDevLauncher reports whether path is the launcher scripts/daemon-build.sh
// installs: a symlink whose own target is exactly one of buildPaths. Only that
// link is provably AO's. A copied launcher, a package manager's shim, or any
// regular file never matches.
func IsDevLauncher(path string, buildPaths []string) bool {
	target, ok := launcherTarget(path)
	return ok && slices.Contains(buildPaths, target)
}

// RepointDevLaunchers points every symlink named ao in dirs whose absolute
// target owned accepts at exe, so a login shell whose startup files put one of
// those directories first runs exe instead of an old source build. Each link
// is swapped atomically by a rename, and every other ao is left untouched. It
// returns the links it repointed and every failure.
func RepointDevLaunchers(exe string, dirs []string, owned func(target string) bool) ([]string, error) {
	var repointed []string
	var errs []error
	seen := map[string]bool{}
	for _, dir := range dirs {
		if !filepath.IsAbs(dir) {
			continue
		}
		link := filepath.Join(filepath.Clean(dir), aoBinaryName)
		if seen[link] {
			continue
		}
		seen[link] = true
		if target, ok := launcherTarget(link); !ok || !owned(target) {
			continue
		}
		if err := swapSymlink(link, exe); err != nil {
			errs = append(errs, fmt.Errorf("repoint %s: %w", link, err))
			continue
		}
		repointed = append(repointed, link)
	}
	return repointed, errors.Join(errs...)
}

// swapSymlink replaces link with a symlink to target.
func swapSymlink(link, target string) error {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	temp := filepath.Join(filepath.Dir(link), ".ao-launcher-"+hex.EncodeToString(suffix))
	if err := os.Symlink(target, temp); err != nil {
		return err
	}
	if err := os.Rename(temp, link); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return nil
}
