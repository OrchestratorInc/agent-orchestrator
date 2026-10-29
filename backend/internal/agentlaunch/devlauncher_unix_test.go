//go:build !windows

package agentlaunch

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestRepointDevLaunchersRepointsOnlyAOsOwnLinks(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "Agent Orchestrator.app", "Contents", "Resources", "daemon", "ao")
	writeExecutableFixture(t, app)
	localBin := filepath.Join(root, "local-bin")
	goBin := filepath.Join(root, "go-bin")
	_, buildPaths := devLauncherFixture(t, root, localBin, goBin)
	foreignDir := filepath.Join(root, "homebrew-bin")
	foreign := filepath.Join(root, "node_modules", "ao")
	writeExecutableFixture(t, foreign)
	if err := os.MkdirAll(foreignDir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(foreign, filepath.Join(foreignDir, "ao")); err != nil {
		t.Fatal(err)
	}
	copyDir := filepath.Join(root, "copy-bin")
	writeExecutableFixture(t, filepath.Join(copyDir, "ao"))

	dirs := []string{localBin, localBin + "/", "relative/bin", foreignDir, copyDir, filepath.Join(root, "no-such-dir"), goBin}
	repointed, err := RepointDevLaunchers(app, dirs, buildPaths)
	if err != nil {
		t.Fatalf("repoint: %v", err)
	}
	want := []string{filepath.Join(localBin, "ao"), filepath.Join(goBin, "ao")}
	if !slices.Equal(repointed, want) {
		t.Fatalf("repointed = %q, want %q", repointed, want)
	}
	for _, link := range want {
		if target, err := os.Readlink(link); err != nil || target != app {
			t.Fatalf("%s -> %q (%v), want %q", link, target, err, app)
		}
	}
	if target, err := os.Readlink(filepath.Join(foreignDir, "ao")); err != nil || target != foreign {
		t.Fatalf("foreign link -> %q (%v), want it untouched at %q", target, err, foreign)
	}
	if info, err := os.Lstat(filepath.Join(copyDir, "ao")); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("copied launcher changed: %v %v", info, err)
	}
	for _, dir := range []string{localBin, goBin} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".ao-launcher-") {
				t.Fatalf("temporary link %s left in %s", entry.Name(), dir)
			}
		}
	}

	// A second start finds nothing left to repoint.
	if again, err := RepointDevLaunchers(app, dirs, buildPaths); err != nil || len(again) != 0 {
		t.Fatalf("second repoint = %q, %v; want nothing", again, err)
	}
}

func TestRepointDevLaunchersReportsAnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := t.TempDir()
	app := filepath.Join(root, "app", "ao")
	writeExecutableFixture(t, app)
	locked := filepath.Join(root, "locked-bin")
	build, buildPaths := devLauncherFixture(t, root, locked)
	if err := os.Chmod(locked, 0o500); err != nil { //nolint:gosec // test makes the directory read-only on purpose
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o750) }) //nolint:gosec // restore so TempDir cleanup can remove it

	repointed, err := RepointDevLaunchers(app, []string{locked}, buildPaths)
	if err == nil || !strings.Contains(err.Error(), filepath.Join(locked, "ao")) || len(repointed) != 0 {
		t.Fatalf("repoint into read-only dir = %q, %v; want an error naming the link", repointed, err)
	}
	if target, err := os.Readlink(filepath.Join(locked, "ao")); err != nil || target != build {
		t.Fatalf("link -> %q (%v), want it kept at %q", target, err, build)
	}
}
