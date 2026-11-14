package agentlaunch

import (
	"os"
	"path/filepath"
	"slices"
)

var devLauncherBuild = filepath.Join("aoagents", "agent-orchestrator", "bin", aoBinaryName)

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
