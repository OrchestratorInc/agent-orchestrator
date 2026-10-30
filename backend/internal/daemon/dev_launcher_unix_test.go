//go:build !windows

package daemon

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
)

func TestDurableDesktopExecutable(t *testing.T) {
	const macApp = "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao"
	const linuxPkg = "/usr/lib/agent-orchestrator/resources/daemon/ao"
	for _, tc := range []struct {
		name string
		goos string
		exe  string
		env  map[string]string
		want bool
	}{
		{"installed macOS app", "darwin", macApp, nil, true},
		{"macOS app in ~/Applications", "darwin", "/Users/me/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao", map[string]string{"HOME": "/Users/me"}, true},
		{"deb or rpm install", "linux", linuxPkg, nil, true},
		{"package under /opt", "linux", "/opt/Agent Orchestrator/resources/daemon/ao", nil, true},
		// Paths that do not survive a restart must never become link targets.
		{"macOS app run from its disk image", "darwin", "/Volumes/Agent Orchestrator/Agent Orchestrator.app/Contents/Resources/daemon/ao", nil, false},
		{"macOS App Translocation", "darwin", "/private/var/folders/x/T/AppTranslocation/ABC/d/Agent Orchestrator.app/Contents/Resources/daemon/ao", nil, false},
		{"AppImage reported by the app", "linux", "/tmp/.mount_AgentO1/resources/daemon/ao", map[string]string{"AO_APPIMAGE": "/home/me/AO.AppImage"}, false},
		{"AppImage reported by its runtime", "linux", linuxPkg, map[string]string{"APPIMAGE": "/home/me/AO.AppImage"}, false},
		{"AppImage mount without the env", "linux", "/tmp/.mount_AgentO1/resources/daemon/ao", nil, false},
		// A build packaged from a checkout is replaced or deleted by the next one.
		{"macOS app packaged from a checkout", "darwin", "/Users/me/agent-orchestrator/frontend/out/Agent Orchestrator-darwin-arm64/Agent Orchestrator.app/Contents/Resources/daemon/ao", map[string]string{"HOME": "/Users/me"}, false},
		{"Linux zip unpacked in home", "linux", "/home/me/Downloads/Agent Orchestrator-linux-x64/resources/daemon/ao", map[string]string{"HOME": "/home/me"}, false},
		// Only the desktop's own layout counts as packaged.
		{"dev daemon from npm run dev", "darwin", "/Users/me/agent-orchestrator/frontend/daemon/ao", nil, false},
		{"go run or ao start", "linux", "/home/me/go/bin/ao", nil, false},
		{"resources layout outside an app bundle", "darwin", "/Applications/resources/daemon/ao", nil, false},
		{"binary not named ao", "darwin", "/Applications/Agent Orchestrator.app/Contents/Resources/daemon/ao-dev", nil, false},
		{"relative path", "linux", "resources/daemon/ao", nil, false},
		{"windows", "windows", `C:\Program Files\Agent Orchestrator\resources\daemon\ao.exe`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := durableDesktopExecutable(tc.goos, func() (string, error) { return tc.exe, nil }, func(key string) string { return tc.env[key] })
			if ok != tc.want || (ok && got != tc.exe) {
				t.Fatalf("durableDesktopExecutable(%s) = %q, %v; want %v", tc.exe, got, ok, tc.want)
			}
		})
	}
	if _, ok := durableDesktopExecutable("darwin", func() (string, error) { return "", errors.New("no exe") }, os.Getenv); ok {
		t.Fatal("unresolvable executable qualified")
	}
}

// devLauncherHome lays out a packaged app and the launcher scripts/daemon-build.sh
// left in ~/.local/bin, and returns the environment a desktop daemon would see.
func devLauncherHome(t *testing.T) (app, link string, env map[string]string) {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	app = filepath.Join(home, "Applications", "Agent Orchestrator.app", "Contents", "Resources", "daemon", "ao")
	build := filepath.Join(home, ".cache", "aoagents", "agent-orchestrator", "bin", "ao")
	link = filepath.Join(home, ".local", "bin", "ao")
	for path, label := range map[string]string{app: "app", build: "stale"} {
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+label+"\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(build, link); err != nil {
		t.Fatal(err)
	}
	// The daemon's PATH, from the app's login-shell probe, omits ~/.local/bin
	// here; the repair must still find the launcher.
	return app, link, map[string]string{"HOME": home, "PATH": "/usr/bin:/bin", envLoginShellEnv: "1"}
}

// TestRepointDevLaunchersFixesLoginShells is the #3562 reproduction end to end:
// ~/.profile prepends ~/.local/bin, where scripts/daemon-build.sh left a link to
// an old build. After the packaged daemon starts, a login shell runs the app's
// own ao.
func TestRepointDevLaunchersFixesLoginShells(t *testing.T) {
	app, link, env := devLauncherHome(t)
	profile := "export PATH='" + filepath.Dir(link) + "':\"$PATH\"\n"
	if err := os.WriteFile(filepath.Join(env["HOME"], ".profile"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	loginShellAO := func() string {
		t.Helper()
		cmd := exec.Command("/bin/sh", "-l", "-c", "ao")
		cmd.Env = []string{"HOME=" + env["HOME"], "PATH=" + filepath.Dir(app) + ":/usr/bin:/bin"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("login shell: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if got := loginShellAO(); got != "stale" {
		t.Fatalf("before the repair a login shell ran %q, want the stale build", got)
	}

	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	repointDevLaunchers(log, "darwin", func() (string, error) { return app, nil }, func(key string) string { return env[key] })

	if got := loginShellAO(); got != "app" {
		t.Fatalf("after the repair a login shell ran %q, want the app's ao", got)
	}
	if target, err := os.Readlink(link); err != nil || target != app {
		t.Fatalf("%s -> %q (%v), want %q", link, target, err, app)
	}
	if !strings.Contains(logs.String(), "repointed") || !strings.Contains(logs.String(), agentlaunch.EnvKeepDevLauncher+"=1") {
		t.Fatalf("log = %q, want the repoint and its opt-out", logs.String())
	}
}

func TestRepointDevLaunchersLeavesLinkAlone(t *testing.T) {
	for _, tc := range []struct {
		name string
		goos string
		exe  func(app string) string
		env  map[string]string
	}{
		{"developer opted out", "darwin", func(app string) string { return app }, map[string]string{agentlaunch.EnvKeepDevLauncher: "1"}},
		// A failed login-shell probe hides a profile's opt-out from the daemon.
		{"login-shell env missing", "darwin", func(app string) string { return app }, map[string]string{envLoginShellEnv: ""}},
		{"dev daemon", "darwin", func(app string) string { return filepath.Join(filepath.Dir(filepath.Dir(app)), "ao") }, nil},
		{"AppImage", "linux", func(app string) string { return app }, map[string]string{"AO_APPIMAGE": "/home/me/AO.AppImage"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, link, env := devLauncherHome(t)
			for key, value := range tc.env {
				env[key] = value
			}
			before, err := os.Readlink(link)
			if err != nil {
				t.Fatal(err)
			}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			repointDevLaunchers(log, tc.goos, func() (string, error) { return tc.exe(app), nil }, func(key string) string { return env[key] })
			if after, err := os.Readlink(link); err != nil || after != before {
				t.Fatalf("%s -> %q (%v), want it kept at %q", link, after, err, before)
			}
		})
	}
}

// TestRepointDevLaunchersFollowsAMovedInstall covers a link this repair made
// earlier: once its install is gone, the next installed daemon takes it over,
// while a link to another install that still exists is left alone.
func TestRepointDevLaunchersFollowsAMovedInstall(t *testing.T) {
	app, link, env := devLauncherHome(t)
	home := env["HOME"]
	moved := filepath.Join(home, "Applications", "Old", "Agent Orchestrator.app", "Contents", "Resources", "daemon", "ao")
	other := filepath.Join(home, "Applications", "AO Beta.app", "Contents", "Resources", "daemon", "ao")
	if err := os.MkdirAll(filepath.Dir(other), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, tc := range []struct {
		name, target, want string
	}{
		{"install moved away", moved, app},
		{"another install still present", other, other},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(tc.target, link); err != nil {
				t.Fatal(err)
			}
			repointDevLaunchers(log, "darwin", func() (string, error) { return app, nil }, func(key string) string { return env[key] })
			if got, err := os.Readlink(link); err != nil || got != tc.want {
				t.Fatalf("%s -> %q (%v), want %q", link, got, err, tc.want)
			}
		})
	}
}
