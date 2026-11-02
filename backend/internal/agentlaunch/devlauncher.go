package agentlaunch

import (
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
// installs: a symlink whose own target is exactly one of buildPaths. A copied
// launcher, a package manager's shim, or any regular file never matches.
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
