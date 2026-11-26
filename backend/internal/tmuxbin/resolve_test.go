package tmuxbin

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func TestResolveWithPrefersConfiguredTmux(t *testing.T) {
	configured := filepath.Join(string(filepath.Separator), "opt", "ao", "tmux")
	var lookups []string
	got, err := ResolveWith(configured, func() (string, error) {
		t.Fatal("executable lookup must not run when tmux is configured")
		return "", nil
	}, func(name string) (string, error) {
		lookups = append(lookups, name)
		return name, nil
	})
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if got.Path != configured || got.Source != SourceConfigured {
		t.Fatalf("resolution = %+v, want configured %q", got, configured)
	}
	if !reflect.DeepEqual(lookups, []string{configured}) {
		t.Fatalf("lookups = %v, want configured path only", lookups)
	}
}

func TestResolveWithDoesNotFallbackFromMissingConfiguredTmux(t *testing.T) {
	configured := filepath.Join(string(filepath.Separator), "missing", "bundled", "tmux")
	var lookups []string
	_, err := resolveWith(configured, func() (string, error) { return "", nil }, func(name string) (string, error) {
		lookups = append(lookups, name)
		if name == "tmux" || name == "/opt/homebrew/bin/tmux" {
			return name, nil
		}
		return "", errors.New("not found")
	}, installPathsFor("darwin"))
	if err == nil {
		t.Fatal("ResolveWith error = nil, want missing configured tmux error")
	}
	if !reflect.DeepEqual(lookups, []string{configured}) {
		t.Fatalf("lookups = %v, want no system fallback", lookups)
	}
}

func TestResolveWithFindsPackagedTmuxOnMacOSAndLinuxLayouts(t *testing.T) {
	for _, resourcesName := range []string{"Resources", "resources"} {
		t.Run(resourcesName, func(t *testing.T) {
			self := filepath.Join(t.TempDir(), resourcesName, "daemon", "ao")
			bundled := filepath.Join(filepath.Dir(filepath.Dir(self)), "tmux", "bin", "tmux")
			got, err := ResolveWith("", func() (string, error) { return self, nil }, func(name string) (string, error) {
				if name == bundled {
					return bundled, nil
				}
				return "", errors.New("not found")
			})
			if err != nil {
				t.Fatalf("ResolveWith: %v", err)
			}
			if got.Path != bundled || got.Source != SourceBundled {
				t.Fatalf("resolution = %+v, want bundled %q", got, bundled)
			}
		})
	}
}

func TestResolveWithFollowsExecutableSymlinkIntoBundle(t *testing.T) {
	daemonDir := filepath.Join(t.TempDir(), "AO.app", "Contents", "Resources", "daemon")
	if err := os.MkdirAll(daemonDir, 0o755); err != nil {
		t.Fatal(err)
	}
	realAO := filepath.Join(daemonDir, "ao")
	if err := os.WriteFile(realAO, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	shim := filepath.Join(t.TempDir(), "ao")
	if err := os.Symlink(realAO, shim); err != nil {
		t.Fatal(err)
	}
	canonicalAO, err := filepath.EvalSymlinks(realAO)
	if err != nil {
		t.Fatal(err)
	}
	bundled := filepath.Join(filepath.Dir(filepath.Dir(canonicalAO)), "tmux", "bin", "tmux")
	got, err := ResolveWith("", func() (string, error) { return shim, nil }, func(name string) (string, error) {
		if name == bundled {
			return bundled, nil
		}
		return "", errors.New("not found")
	})
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if got.Path != bundled || got.Source != SourceBundled {
		t.Fatalf("resolution = %+v, want bundled %q", got, bundled)
	}
}

func TestResolveWithFallsBackToSystemTmux(t *testing.T) {
	got, err := ResolveWith("", func() (string, error) { return "/usr/local/bin/ao", nil }, func(name string) (string, error) {
		if name == "tmux" {
			return "/usr/local/bin/tmux", nil
		}
		return "", errors.New("not found")
	})
	if err != nil {
		t.Fatalf("ResolveWith: %v", err)
	}
	if got.Path != "/usr/local/bin/tmux" || got.Source != SourceSystem {
		t.Fatalf("resolution = %+v, want system tmux", got)
	}
}

func TestResolveWithReturnsErrorWhenTmuxIsUnavailable(t *testing.T) {
	_, err := ResolveWith("", func() (string, error) { return "/usr/local/bin/ao", nil }, func(string) (string, error) {
		return "", errors.New("not found")
	})
	if err == nil {
		t.Fatal("ResolveWith error = nil, want missing tmux error")
	}
}

// A daemon started by launchd, cron, or a minimal-env shell inherits a PATH
// without the package manager's bin directory, so PATH finds no tmux.
func TestResolveWithFindsMacOSPackageManagerTmuxOutsidePATH(t *testing.T) {
	for _, installed := range []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/opt/local/bin/tmux"} {
		t.Run(installed, func(t *testing.T) {
			got, err := resolveWith("", func() (string, error) { return "/usr/local/bin/ao", nil }, func(name string) (string, error) {
				if name == installed {
					return installed, nil
				}
				return "", errors.New("not found")
			}, installPathsFor("darwin"))
			if err != nil {
				t.Fatalf("resolveWith: %v", err)
			}
			if got.Path != installed || got.Source != SourceSystem {
				t.Fatalf("resolution = %+v, want system %q", got, installed)
			}
		})
	}
}

func TestResolveWithPrefersPATHTmuxOverInstallPaths(t *testing.T) {
	const onPATH = "/Users/me/bin/tmux"
	var lookups []string
	got, err := resolveWith("", func() (string, error) { return "/usr/local/bin/ao", nil }, func(name string) (string, error) {
		lookups = append(lookups, name)
		if name == "tmux" {
			return onPATH, nil
		}
		return name, nil
	}, installPathsFor("darwin"))
	if err != nil {
		t.Fatalf("resolveWith: %v", err)
	}
	if got.Path != onPATH || got.Source != SourceSystem {
		t.Fatalf("resolution = %+v, want PATH tmux %q", got, onPATH)
	}
	if !reflect.DeepEqual(lookups, []string{"tmux"}) {
		t.Fatalf("lookups = %v, want PATH only", lookups)
	}
}

func TestResolveWithPrefersBundledTmuxOverInstallPaths(t *testing.T) {
	self := filepath.Join(t.TempDir(), "Resources", "daemon", "ao")
	bundled := filepath.Join(filepath.Dir(filepath.Dir(self)), "tmux", "bin", "tmux")
	got, err := resolveWith("", func() (string, error) { return self, nil }, func(name string) (string, error) {
		if name == "tmux" {
			return "", errors.New("not found")
		}
		return name, nil
	}, installPathsFor("darwin"))
	if err != nil {
		t.Fatalf("resolveWith: %v", err)
	}
	if got.Path != bundled || got.Source != SourceBundled {
		t.Fatalf("resolution = %+v, want bundled %q", got, bundled)
	}
}

func TestResolveWithProbesInstallPathsOnlyOnMacOS(t *testing.T) {
	for _, goos := range []string{"linux", "windows"} {
		if paths := installPathsFor(goos); len(paths) != 0 {
			t.Fatalf("installPathsFor(%q) = %v, want none", goos, paths)
		}
	}
	_, err := resolveWith("", func() (string, error) { return "/usr/local/bin/ao", nil }, func(name string) (string, error) {
		if name == "/opt/homebrew/bin/tmux" {
			return name, nil
		}
		return "", errors.New("not found")
	}, installPathsFor("linux"))
	if err == nil {
		t.Fatal("resolveWith error = nil, want missing tmux outside macOS")
	}
}

// The install-path probe goes through exec.LookPath in production, which must
// accept an executable absolute path and skip one that cannot be run.
func TestResolveWithInstallPathsUseRealLookPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits decide executability")
	}
	t.Setenv("PATH", t.TempDir())
	dir := t.TempDir()
	notExecutable := filepath.Join(dir, "plain", "tmux")
	installed := filepath.Join(dir, "installed", "tmux")
	for path, mode := range map[string]os.FileMode{notExecutable: 0o644, installed: 0o755} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), mode); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(dir, "missing", "tmux")

	got, err := resolveWith("", func() (string, error) { return "/usr/local/bin/ao", nil }, exec.LookPath, []string{missing, notExecutable, installed})
	if err != nil {
		t.Fatalf("resolveWith: %v", err)
	}
	if got.Path != installed || got.Source != SourceSystem {
		t.Fatalf("resolution = %+v, want executable install path %q", got, installed)
	}
}
