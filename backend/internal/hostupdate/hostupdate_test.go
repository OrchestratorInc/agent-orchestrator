package hostupdate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestInstallRoot(t *testing.T) {
	cases := map[string]string{
		"/home/u/.ao/host/releases/20261115010203-42/resources/daemon/ao": "/home/u/.ao/host",
		"/opt/ao/releases/r1/resources/daemon/ao":                         "/opt/ao",
		"/tmp/.mount_ao/resources/daemon/ao":                              "",
		"/home/u/.ao/host/current/resources/daemon/ao-dev":                "",
	}
	for exe, want := range cases {
		if got := installRoot(exe); got != want {
			t.Errorf("installRoot(%q) = %q, want %q", exe, got, want)
		}
	}
}

type fakeSystemd struct {
	state string
	log   string
	calls [][]string
}

func (f *fakeSystemd) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	switch name {
	case "systemctl":
		if args[1] == "show" && args[2] == "--property=InvocationID" {
			return []byte("4b1d\n"), nil
		}
		if args[1] == "show" {
			return []byte(f.state + "\n"), nil
		}
		return nil, nil
	case "journalctl":
		if args[1] != "_SYSTEMD_INVOCATION_ID=4b1d" {
			return []byte("Updated Orchestrator host to 0.14.0\n"), nil
		}
		return []byte(f.log), nil
	}
	return nil, nil
}

func updater(f *fakeSystemd, version string, env map[string]string) *Updater {
	return &Updater{version: version, kind: kindSystemd, root: "/home/u/.ao/host", run: f.run, getenv: func(name string) string { return env[name] }}
}

func TestStatus(t *testing.T) {
	ctx := context.Background()
	if got := (&Updater{version: "0.15.0"}).Status(ctx); got.Supported || got.State != StateIdle || got.Version != "0.15.0" {
		t.Fatalf("unsupported status = %+v", got)
	}
	for state, want := range map[string]State{"active": StateUpdating, "activating": StateUpdating, "inactive": StateIdle, "": StateIdle, "failed": StateFailed} {
		f := &fakeSystemd{state: state, log: "Downloading Orchestrator release: v0.15.1\nRelease SHA-256 mismatch.\n"}
		got := updater(f, "0.15.0", nil).Status(ctx)
		if !got.Supported || got.Method != MethodService || got.State != want {
			t.Errorf("state %q: got %+v, want %s", state, got, want)
		}
		if want == StateFailed && got.Error != "Release SHA-256 mismatch." {
			t.Errorf("failed error = %q", got.Error)
		}
	}
}

func TestStart(t *testing.T) {
	ctx := context.Background()
	if err := (&Updater{}).Start(ctx); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported start = %v", err)
	}
	if err := updater(&fakeSystemd{state: "active"}, "0.15.0", nil).Start(ctx); !errors.Is(err, ErrInProgress) {
		t.Fatalf("running start = %v", err)
	}

	f := &fakeSystemd{state: "failed"}
	env := map[string]string{"HOME": "/home/u", "PATH": "/usr/bin", "AO_PORT": "3011"}
	if err := updater(f, "0.15.1-nightly.202611140625", env).Start(ctx); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(f.calls, func(call []string) bool {
		return strings.Join(call, " ") == "systemctl --user reset-failed "+updateUnit
	}) {
		t.Errorf("failed unit was not reset: %v", f.calls)
	}
	run := f.calls[len(f.calls)-1]
	if run[0] != "systemd-run" {
		t.Fatalf("last call = %v", run)
	}
	for _, want := range []string{"--unit=" + updateUnit, "--property=RuntimeMaxSec=30min", "--setenv=PATH=/usr/bin", "--setenv=AO_PORT=3011", installerURL, "--upgrade", "--nightly"} {
		if !slices.Contains(run, want) {
			t.Errorf("systemd-run args missing %q: %v", want, run)
		}
	}
	if slices.ContainsFunc(run, func(arg string) bool { return strings.HasPrefix(arg, "--setenv=AO_HOST_INSTALL_DIR=") }) {
		t.Errorf("default install root passed explicitly: %v", run)
	}

	stable := &fakeSystemd{}
	if err := (&Updater{version: "0.15.0", kind: kindSystemd, root: "/opt/ao", run: stable.run, getenv: func(name string) string { return env[name] }}).Start(ctx); err != nil {
		t.Fatal(err)
	}
	run = stable.calls[len(stable.calls)-1]
	if slices.Contains(run, "--nightly") || !slices.Contains(run, "--setenv=AO_HOST_INSTALL_DIR=/opt/ao") {
		t.Errorf("stable custom-root args = %v", run)
	}
}

func TestDefaultRootThroughSymlinkedHome(t *testing.T) {
	realHome := t.TempDir()
	if err := os.MkdirAll(filepath.Join(realHome, ".ao", "host"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "home")
	if err := os.Symlink(realHome, link); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(filepath.Join(realHome, ".ao", "host"))
	if err != nil {
		t.Fatal(err)
	}
	u := &Updater{root: root, getenv: func(name string) string { return map[string]string{"HOME": link}[name] }}
	if slices.ContainsFunc(u.startArgs(), func(arg string) bool { return strings.HasPrefix(arg, "--setenv=AO_HOST_INSTALL_DIR=") }) {
		t.Errorf("symlinked default root passed explicitly: %v", u.startArgs())
	}
}

func TestRunCommandKeepsStderr(t *testing.T) {
	_, err := runCommand(context.Background(), "sh", "-c", "echo 'Failed to connect to bus' >&2; exit 1")
	if err == nil || !strings.Contains(err.Error(), "Failed to connect to bus") {
		t.Fatalf("err = %v", err)
	}
}
