package hostupdate

import (
	"context"
	"encoding/xml"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type fakeLaunchctl struct {
	print string
	calls [][]string
}

func (f *fakeLaunchctl) run(_ context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if args[0] == "print" && f.print == "" {
		return nil, errors.New("Could not find service")
	}
	return []byte(f.print), nil
}

func launchdUpdater(t *testing.T, f *fakeLaunchctl, version string) *Updater {
	t.Helper()
	root := t.TempDir()
	env := map[string]string{"HOME": "/Users/u", "PATH": "/usr/bin:/bin", "AO_PORT": "3927"}
	return &Updater{version: version, kind: kindLaunchd, root: root, run: f.run, getenv: func(name string) string { return env[name] }}
}

func TestLaunchdStatus(t *testing.T) {
	ctx := context.Background()
	job := func(lines ...string) string {
		return "gui/501/" + updateLabel + " = {\n\tactive count = 0\n" + strings.Join(lines, "\n") + "\n\tendpoints = {\n\t\tstate = active\n\t}\n}\n"
	}
	cases := []struct {
		print string
		want  State
	}{
		{"", StateIdle},
		{job("\tstate = running", "\tlast exit code = (never exited)"), StateUpdating},
		{job("\tstate = not running", "\tlast exit code = 0"), StateIdle},
		{job("\tstate = not running", "\tlast exit code = 1"), StateFailed},
		{job("\tstate = not running", "\tlast terminating signal = Terminated: 15"), StateFailed},
	}
	for _, c := range cases {
		u := launchdUpdater(t, &fakeLaunchctl{print: c.print}, "0.15.0")
		if err := os.MkdirAll(filepath.Dir(u.logPath()), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(u.logPath(), []byte("Downloading Orchestrator release: v0.15.1\nRelease SHA-256 mismatch.\n\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		got := u.Status(ctx)
		if !got.Supported || got.Method != MethodService || got.State != c.want {
			t.Errorf("%q: got %+v, want %s", c.print, got, c.want)
		}
		if c.want == StateFailed && got.Error != "Release SHA-256 mismatch." {
			t.Errorf("failed error = %q", got.Error)
		}
	}
}

func TestLaunchdStart(t *testing.T) {
	ctx := context.Background()
	if err := launchdUpdater(t, &fakeLaunchctl{print: "\tstate = running\n"}, "0.15.0").Start(ctx); !errors.Is(err, ErrInProgress) {
		t.Fatalf("running start = %v", err)
	}

	f := &fakeLaunchctl{}
	u := launchdUpdater(t, f, "0.15.1-nightly.202611140625")
	if err := os.MkdirAll(filepath.Dir(u.logPath()), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(u.logPath(), []byte("old failure\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := u.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(u.logPath()); len(data) != 0 {
		t.Errorf("log not cleared: %q", data)
	}
	plist := filepath.Join(u.root, "update.plist")
	domain := launchdDomain()
	last := f.calls[len(f.calls)-2:]
	if strings.Join(last[0], " ") != "launchctl bootout "+domain+"/"+updateLabel || strings.Join(last[1], " ") != "launchctl bootstrap "+domain+" "+plist {
		t.Fatalf("launchctl calls = %v", f.calls)
	}

	data, err := os.ReadFile(plist)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Dict struct {
			Keys  []string `xml:"key"`
			Array struct {
				Strings []string `xml:"string"`
			} `xml:"array"`
			Env struct {
				Keys   []string `xml:"key"`
				Values []string `xml:"string"`
			} `xml:"dict"`
		} `xml:"dict"`
	}
	if !strings.Contains(string(data), "&amp;\nset -euo pipefail\n") {
		t.Errorf("script newlines are not literal; launchd keeps only the first line of an escaped one:\n%s", data)
	}
	if err := xml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	wantArgs := []string{"/bin/bash", "-c", launchdRun, "ao-host-update", installerURL, "--upgrade", "--nightly"}
	if !slices.Equal(doc.Dict.Array.Strings, wantArgs) {
		t.Errorf("ProgramArguments = %q", doc.Dict.Array.Strings)
	}
	if slices.Contains(doc.Dict.Keys, "KeepAlive") || !slices.Contains(doc.Dict.Keys, "RunAtLoad") {
		t.Errorf("plist keys = %v", doc.Dict.Keys)
	}
	env := map[string]string{}
	for i, key := range doc.Dict.Env.Keys {
		env[key] = doc.Dict.Env.Values[i]
	}
	if env["HOME"] != "/Users/u" || env["PATH"] != "/usr/bin:/bin" || env["AO_PORT"] != "3927" || env["AO_HOST_INSTALL_DIR"] != u.root {
		t.Errorf("EnvironmentVariables = %v", env)
	}
	if plutil, err := exec.LookPath("plutil"); err == nil {
		if out, err := exec.Command(plutil, "-lint", plist).CombinedOutput(); err != nil {
			t.Errorf("plutil -lint: %v %s", err, out)
		}
	}
}
