//go:build !windows

package agentlaunch

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestDevLauncherBuildPaths(t *testing.T) {
	env := map[string]string{"HOME": "/home/me", "XDG_CACHE_HOME": "/cache"}
	got := DevLauncherBuildPaths(func(key string) string { return env[key] })
	want := []string{"/cache/aoagents/agent-orchestrator/bin/ao", "/home/me/.cache/aoagents/agent-orchestrator/bin/ao"}
	if !slices.Equal(got, want) {
		t.Fatalf("build paths = %q, want %q", got, want)
	}
	env = map[string]string{"HOME": "relative", "XDG_CACHE_HOME": "also-relative"}
	if got := DevLauncherBuildPaths(func(key string) string { return env[key] }); len(got) != 0 {
		t.Fatalf("build paths from relative dirs = %q, want none", got)
	}
}

// devLauncherFixture lays out what scripts/daemon-build.sh leaves behind: a
// build under the cache directory and, in each of dirs, a link named ao to it.
func devLauncherFixture(t *testing.T, root string, dirs ...string) (build string, buildPaths []string) {
	t.Helper()
	build = filepath.Join(root, "cache", "aoagents", "agent-orchestrator", "bin", "ao")
	writeExecutableFixture(t, build)
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(build, filepath.Join(dir, "ao")); err != nil {
			t.Fatal(err)
		}
	}
	return build, []string{build}
}

func writeExecutableFixture(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
}

func TestIsDevLauncherNeedsTheExactBuildLink(t *testing.T) {
	root := t.TempDir()
	build, buildPaths := devLauncherFixture(t, root, filepath.Join(root, "local-bin"))
	foreign := filepath.Join(root, "foreign", "ao")
	writeExecutableFixture(t, foreign)
	link := func(name, target string) string {
		path := filepath.Join(root, name)
		if err := os.Symlink(target, path); err != nil {
			t.Fatal(err)
		}
		return path
	}
	// A cache cleaned up since the build still leaves AO's own link behind.
	dangling := filepath.Join(root, "gone", "aoagents", "agent-orchestrator", "bin", "ao")
	paths := append(slices.Clone(buildPaths), dangling)
	for _, tc := range []struct {
		name string
		path string
		want bool
	}{
		{"link to the build", filepath.Join(root, "local-bin", "ao"), true},
		{"dangling link to the build", link("dangling", dangling), true},
		{"link to another ao", link("foreign-link", foreign), false},
		// Only scripts/daemon-build.sh's own absolute link counts.
		{"relative link", link("relative", filepath.Join("cache", "aoagents", "agent-orchestrator", "bin", "ao")), false},
		{"copied launcher", foreign, false},
		{"the build itself", build, false},
		{"missing", filepath.Join(root, "missing"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsDevLauncher(tc.path, paths); got != tc.want {
				t.Fatalf("IsDevLauncher(%s) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
