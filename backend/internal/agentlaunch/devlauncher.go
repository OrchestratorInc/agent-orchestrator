package agentlaunch

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
)

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

// IsDevLauncher reports whether path is the launcher scripts/daemon-build.sh
// installs: a symlink whose own target is exactly one of buildPaths. Only that
// link is provably AO's. A copied launcher, a package manager's shim, or any
// regular file never matches.
func IsDevLauncher(path string, buildPaths []string) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		return false
	}
	target, err := os.Readlink(path)
	if err != nil || !filepath.IsAbs(target) {
		return false
	}
	return slices.Contains(buildPaths, filepath.Clean(target))
}

// RepointDevLaunchers points every dev launcher named ao in dirs at exe, so a
// login shell whose startup files put one of those directories first runs exe
// instead of an old source build. Each link is swapped atomically by a rename,
// and anything that is not a dev launcher is left untouched. It returns the
// links it repointed and every failure.
func RepointDevLaunchers(exe string, dirs, buildPaths []string) ([]string, error) {
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
		if !IsDevLauncher(link, buildPaths) {
			continue
		}
		swapped, err := swapSymlink(link, exe, buildPaths)
		if err != nil {
			errs = append(errs, fmt.Errorf("repoint %s: %w", link, err))
			continue
		}
		if swapped {
			repointed = append(repointed, link)
		}
	}
	return repointed, errors.Join(errs...)
}

// swapSymlink replaces link with a symlink to target. It reports false, and
// keeps link, when link stopped being a dev launcher after the scan.
func swapSymlink(link, target string, buildPaths []string) (bool, error) {
	suffix := make([]byte, 8)
	if _, err := rand.Read(suffix); err != nil {
		return false, err
	}
	temp := filepath.Join(filepath.Dir(link), ".ao-launcher-"+hex.EncodeToString(suffix))
	if err := os.Symlink(target, temp); err != nil {
		return false, err
	}
	if !IsDevLauncher(link, buildPaths) {
		_ = os.Remove(temp)
		return false, nil
	}
	if err := os.Rename(temp, link); err != nil {
		_ = os.Remove(temp)
		return false, err
	}
	return true, nil
}
