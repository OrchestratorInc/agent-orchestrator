//go:build !windows

package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func writeLoginShellFixture(t *testing.T, path string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path
}

// linkDevLauncher reproduces the launcher scripts/daemon-build.sh leaves on
// PATH: a link named ao into its build cache.
func linkDevLauncher(t *testing.T, root string) string {
	t.Helper()
	build := writeLoginShellFixture(t, filepath.Join(root, "cache", "aoagents", "agent-orchestrator", "bin", "ao"))
	link := filepath.Join(root, "local-bin", "ao")
	if err := os.MkdirAll(filepath.Dir(link), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(build, link); err != nil {
		t.Fatal(err)
	}
	return link
}

// TestDoctorLoginShellReportsWhichAOItRuns covers the ao-login-shell check:
// a login shell starts with the app's ao first on PATH, as every session
// does, and doctor reports whether startup files left it there (#3562).
func TestDoctorLoginShellReportsWhichAOItRuns(t *testing.T) {
	root := t.TempDir()
	app := writeLoginShellFixture(t, filepath.Join(root, "app", "ao"))
	daemon := writeLoginShellFixture(t, filepath.Join(root, "daemon", "ao"))
	other := writeLoginShellFixture(t, filepath.Join(root, "other", "ao"))
	devLink := linkDevLauncher(t, root)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/usr/bin:/bin")

	cases := []struct {
		name      string
		daemonExe string
		// answer is what the shell writes; nil means it never got to answer.
		answer    *string
		err       error
		wantLevel doctorLevel
		wantIn    []string
		wantNotIn string
	}{
		{"app binary still wins", "", ptr(app + "\n"), nil, doctorPass, []string{"still runs this binary (" + app + ")"}, ""},
		{"running daemon is the authority", daemon, ptr(daemon + "\n"), nil, doctorPass, []string{"the running daemon's binary (" + daemon + ")"}, ""},
		{"startup file prepends another ao", "", ptr(other + "\n"), nil, doctorWarn, []string{"runs " + other + ", not this binary " + app, "Codex"}, "daemon-build.sh"},
		{"AO dev launcher is named with its fix", "", ptr(devLink + "\n"), nil, doctorWarn, []string{"runs " + devLink, "scripts/daemon-build.sh", "rm '" + devLink + "'"}, ""},
		// command -v exits 1 when nothing resolves.
		{"startup file replaces PATH", "", ptr(""), errors.New("exit status 1"), doctorWarn, []string{"finds no ao even with " + filepath.Dir(app) + " first"}, ""},
		{"alias shadows ao", "", ptr("alias ao=/opt/old/ao\n"), nil, doctorWarn, []string{`"alias ao=/opt/old/ao"`, "alias or function"}, ""},
		{"shell fails before answering", "", nil, errors.New("exit status 1"), doctorWarn, []string{"could not run a zsh login shell", "exit status 1"}, ""},
		{"startup file exits cleanly first", "", nil, nil, doctorWarn, []string{"ended the shell before it checked"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotName string
			var gotArgs []string
			deps := Deps{
				Executable: func() (string, error) { return app, nil },
				CommandOutput: func(ctx context.Context, name string, args ...string) ([]byte, error) {
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > loginShellProbeTimeout {
						t.Fatalf("probe deadline = %v (set %v), want within %s", deadline, ok, loginShellProbeTimeout)
					}
					gotName, gotArgs = name, args
					if tc.answer != nil {
						if err := os.WriteFile(args[len(args)-1], []byte(*tc.answer), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					return nil, tc.err
				},
				ProcessAlive: func(int) bool { return false },
			}
			c := &commandContext{deps: deps.withDefaults()}
			check, ok := c.checkAOLoginShell(context.Background(), tc.daemonExe)
			if !ok {
				t.Fatal("check skipped, want a result")
			}
			if check.Level != tc.wantLevel || check.Name != "ao-login-shell" || check.Section != doctorSectionTools {
				t.Fatalf("check = %+v, want %s ao-login-shell in %s", check, tc.wantLevel, doctorSectionTools)
			}
			for _, want := range tc.wantIn {
				if !strings.Contains(check.Message, want) {
					t.Fatalf("message %q missing %q", check.Message, want)
				}
			}
			if tc.wantNotIn != "" && strings.Contains(check.Message, tc.wantNotIn) {
				t.Fatalf("message %q should not mention %q", check.Message, tc.wantNotIn)
			}
			pinned := filepath.Dir(app)
			if tc.daemonExe != "" {
				pinned = filepath.Dir(tc.daemonExe)
			}
			wantArgs := []string{"-c", `exec "$@" </dev/null >/dev/null 2>&1`, "sh",
				"/usr/bin/env", "PATH=" + pinned + ":/usr/bin:/bin", "/bin/zsh", "-l", "-c", `command -v ao >"$1"`, "zsh"}
			if gotName != "/bin/sh" || len(gotArgs) != len(wantArgs)+1 || !slices.Equal(gotArgs[:len(wantArgs)], wantArgs) {
				t.Fatalf("probe = %s %q, want /bin/sh %q <answer file>", gotName, gotArgs, wantArgs)
			}
			if _, err := os.Stat(gotArgs[len(gotArgs)-1]); !os.IsNotExist(err) {
				t.Fatalf("answer file left behind (stat err %v)", err)
			}
		})
	}
}

func ptr(s string) *string { return &s }

func TestDoctorLoginShellSkipsWhatItCannotProbe(t *testing.T) {
	dir := t.TempDir()
	app := writeLoginShellFixture(t, filepath.Join(dir, "ao"))
	dev := writeLoginShellFixture(t, filepath.Join(dir, "ao-dev"))
	for _, tc := range []struct{ name, shell, exe string }{
		{"shell without POSIX command -v", "/opt/homebrew/bin/fish", app},
		{"SHELL unset", "", app},
		{"relative SHELL", "zsh", app},
		// Sessions only pin a binary named ao, so there is nothing to compare.
		{"binary not named ao", "/bin/zsh", dev},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHELL", tc.shell)
			deps := Deps{
				Executable: func() (string, error) { return tc.exe, nil },
				CommandOutput: func(_ context.Context, name string, args ...string) ([]byte, error) {
					t.Fatalf("probed %s %q, want no probe", name, args)
					return nil, nil
				},
				ProcessAlive: func(int) bool { return false },
			}
			c := &commandContext{deps: deps.withDefaults()}
			if check, ok := c.checkAOLoginShell(context.Background(), ""); ok {
				t.Fatalf("check = %+v, want skipped", check)
			}
		})
	}
}

// TestDoctorLoginShellCatchesStartupFilePrepend runs a real login shell with
// an isolated HOME, the reproduction from #3562: the app's ao starts first on
// PATH, then ~/.profile prepends the directory holding AO's old dev launcher.
func TestDoctorLoginShellCatchesStartupFilePrepend(t *testing.T) {
	root := t.TempDir()
	app := writeLoginShellFixture(t, filepath.Join(root, "app", "ao"))
	devLink := linkDevLauncher(t, root)
	home := filepath.Join(root, "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	t.Setenv("PATH", "/usr/bin:/bin")
	c := &commandContext{deps: Deps{
		Executable:   func() (string, error) { return app, nil },
		ProcessAlive: func(int) bool { return false },
	}.withDefaults()}

	check, ok := c.checkAOLoginShell(context.Background(), "")
	if !ok {
		t.Fatal("check skipped, want a result")
	}
	if check.Level != doctorPass {
		// macOS path_helper moves /etc/paths ahead of the pin; a machine with
		// an ao there already shadows the app, which this test cannot undo.
		if strings.Contains(check.Message, root) || !strings.Contains(check.Message, " runs /") {
			t.Fatalf("clean login shell check = %+v, want PASS", check)
		}
		t.Skipf("system login PATH already holds an ao: %s", check.Message)
	}

	// The background sleep keeps the shell's stdout open after it exits; doctor
	// must still answer as soon as the shell itself is done.
	profile := "export PATH='" + filepath.Dir(devLink) + "':\"$PATH\"\nsleep 5 &\n"
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	check, ok = c.checkAOLoginShell(context.Background(), "")
	if elapsed := time.Since(start); elapsed >= loginShellProbeTimeout {
		t.Fatalf("probe took %s: a startup file's background process held doctor open", elapsed)
	}
	if !ok || check.Level != doctorWarn || !strings.Contains(check.Message, "runs "+devLink+", not this binary "+app) ||
		!strings.Contains(check.Message, "scripts/daemon-build.sh") {
		t.Fatalf("check after ~/.profile prepends the dev launcher = %+v (ok %v), want WARN naming %s", check, ok, devLink)
	}
}
