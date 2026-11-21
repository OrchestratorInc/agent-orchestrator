package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

func TestBuildServiceSpec(t *testing.T) {
	env := map[string]string{"PATH": "/usr/bin", "HOME": "/h", "SECRET": "x", "AO_TELEMETRY_REMOTE": "off"}
	spec, err := buildServiceSpec("darwin", "/h", "/opt/ao", env, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Manager != "launchd" || spec.Name != "dev.agent-orchestrator.serve" || spec.File != filepath.Join("/h", "Library", "LaunchAgents", "dev.agent-orchestrator.serve.plist") {
		t.Fatalf("darwin spec = %#v", spec)
	}
	if strings.Join(spec.Args, " ") != "/opt/ao serve --foreground --port 4000" {
		t.Fatalf("args = %v", spec.Args)
	}
	if spec.Env["PATH"] != "/usr/bin" || spec.Env["AO_TELEMETRY_REMOTE"] != "off" {
		t.Fatalf("service env lost PATH or the analytics choice: %v", spec.Env)
	}
	if _, ok := spec.Env["SECRET"]; ok {
		t.Fatalf("service env carried an unrelated variable: %v", spec.Env)
	}

	spec, err = buildServiceSpec("linux", "/h", "/opt/ao", map[string]string{"AO_DATA_DIR": "/sandbox/data"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if spec.Manager != "systemd" || !strings.HasPrefix(spec.Name, "agent-orchestrator-serve-") || filepath.Dir(spec.File) != filepath.Join("/h", ".config", "systemd", "user") {
		t.Fatalf("linux spec for a custom data dir = %#v", spec)
	}

	xdg, _ := filepath.Abs("/xdg")
	spec, err = buildServiceSpec("linux", "/h", "/opt/ao", map[string]string{"XDG_CONFIG_HOME": xdg}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if spec.File != filepath.Join(xdg, "systemd", "user", "agent-orchestrator-serve.service") {
		t.Fatalf("XDG_CONFIG_HOME ignored: %s", spec.File)
	}

	if _, err := buildServiceSpec("windows", "/h", "/opt/ao", nil, 0); !errors.Is(err, errServiceUnsupported) {
		t.Fatalf("windows err = %v, want errServiceUnsupported", err)
	}
}

func TestRenderServiceFilesEscapeValues(t *testing.T) {
	spec := serviceSpec{Name: "dev.agent-orchestrator.serve", Args: []string{"/Apps/A & B/ao", "serve", "--foreground"}, Env: map[string]string{"PATH": `/a:/b"c%d$e`}}
	plist := renderLaunchdPlist(spec)
	for _, want := range []string{serviceMarker, "<string>/Apps/A &amp; B/ao</string>", "<key>SuccessfulExit</key>\n\t\t<false/>", "<key>RunAtLoad</key>\n\t<true/>", "<key>AbandonProcessGroup</key>\n\t<true/>"} {
		if !strings.Contains(plist, want) {
			t.Fatalf("plist missing %q:\n%s", want, plist)
		}
	}
	unit := renderSystemdUnit(spec)
	for _, want := range []string{"# " + serviceMarker, `ExecStart="/Apps/A & B/ao" "serve" "--foreground"`, `Environment="PATH=/a:/b\"c%%d$e"`, "Restart=on-failure", "KillMode=process", "WantedBy=default.target"} {
		if !strings.Contains(unit, want) {
			t.Fatalf("unit missing %q:\n%s", want, unit)
		}
	}
}

type serviceCall struct {
	name string
	args []string
}

func serviceTestDeps(calls *[]serviceCall, fail map[string]bool) Deps {
	now := time.Unix(1000, 0)
	return Deps{
		Executable:   func() (string, error) { return "/opt/ao/bin/ao", nil },
		ProcessAlive: func(int) bool { return false },
		Now:          func() time.Time { return now },
		Sleep:        func(d time.Duration) { now = now.Add(d) },
		CommandOutput: func(_ context.Context, name string, args ...string) ([]byte, error) {
			*calls = append(*calls, serviceCall{name, args})
			if fail[name+" "+args[0]] || (len(args) > 1 && fail[name+" "+args[0]+" "+args[1]]) {
				return []byte("refused"), errors.New("exit status 1")
			}
			return nil, nil
		},
	}
}

func TestServiceLaunchdInstallStatusUninstall(t *testing.T) {
	setConfigEnv(t)
	setServiceHome(t)
	old := serviceGOOS
	serviceGOOS = "darwin"
	t.Cleanup(func() { serviceGOOS = old })

	var calls []serviceCall
	deps := serviceTestDeps(&calls, nil)
	out, _, err := executeCLI(t, deps, "service", "install", "--no-wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st serviceStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if !st.Installed || !st.Loaded || st.Manager != "launchd" || !strings.HasPrefix(st.Name, "dev.agent-orchestrator.serve-") {
		t.Fatalf("install status = %#v", st)
	}
	data, err := os.ReadFile(st.File)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "<string>/opt/ao/bin/ao</string>") {
		t.Fatalf("plist does not run this ao:\n%s", data)
	}
	last := calls[len(calls)-1]
	if last.name != "launchctl" || last.args[0] != "bootstrap" || last.args[2] != st.File {
		t.Fatalf("last call = %#v, want launchctl bootstrap of the plist", last)
	}

	out, _, err = executeCLI(t, deps, "service", "status")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "installed: true") || !strings.Contains(out, "loaded: true") || !strings.Contains(out, "daemon: stopped") {
		t.Fatalf("status output:\n%s", out)
	}

	calls = nil
	if _, _, err := executeCLI(t, deps, "service", "uninstall"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.File); !os.IsNotExist(err) {
		t.Fatalf("uninstall left %s (err %v)", st.File, err)
	}
	if len(calls) < 2 || calls[1].args[0] != "bootout" {
		t.Fatalf("uninstall calls = %#v, want a launchctl bootout", calls)
	}
	// A second uninstall is a no-op.
	if _, _, err := executeCLI(t, deps, "service", "uninstall"); err != nil {
		t.Fatal(err)
	}
}

func TestServiceSystemdInstallAndRollback(t *testing.T) {
	setConfigEnv(t)
	setServiceHome(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	old := serviceGOOS
	serviceGOOS = "linux"
	t.Cleanup(func() { serviceGOOS = old })

	var calls []serviceCall
	out, _, err := executeCLI(t, serviceTestDeps(&calls, nil), "service", "install", "--no-wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st serviceStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range calls {
		if c.args[1] == "show" {
			continue // servicePID lookups
		}
		got = append(got, c.name+" "+strings.Join(c.args, " "))
	}
	want := []string{"systemctl --user daemon-reload", "systemctl --user enable " + st.Name + ".service", "systemctl --user restart " + st.Name + ".service"}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("systemctl calls:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if _, _, err := executeCLI(t, serviceTestDeps(&calls, nil), "service", "uninstall"); err != nil {
		t.Fatal(err)
	}

	// No user bus: nothing is left behind.
	calls = nil
	_, _, err = executeCLI(t, serviceTestDeps(&calls, map[string]bool{"systemctl --user daemon-reload": true}), "service", "install", "--no-wait")
	if err == nil || !strings.Contains(err.Error(), "systemd user session") {
		t.Fatalf("err = %v, want a systemd user session hint", err)
	}
	if _, err := os.Stat(st.File); !os.IsNotExist(err) {
		t.Fatalf("failed install left %s", st.File)
	}
}

func TestServiceLeavesForeignFileAlone(t *testing.T) {
	setConfigEnv(t)
	setServiceHome(t)
	old := serviceGOOS
	serviceGOOS = "darwin"
	t.Cleanup(func() { serviceGOOS = old })

	var calls []serviceCall
	deps := serviceTestDeps(&calls, nil)
	spec, err := (&commandContext{deps: deps.withDefaults()}).currentServiceSpec(0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(spec.File), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spec.File, []byte("<plist>mine</plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, sub := range []string{"install", "uninstall"} {
		if _, _, err := executeCLI(t, deps, "service", sub, "--json"); err == nil || !strings.Contains(err.Error(), "not written by `ao service install`") {
			t.Fatalf("%s err = %v, want refusal", sub, err)
		}
	}
	if data, _ := os.ReadFile(spec.File); string(data) != "<plist>mine</plist>" {
		t.Fatalf("foreign file was changed: %s", data)
	}
	if len(calls) != 0 {
		t.Fatalf("service manager was called for a foreign file: %#v", calls)
	}
}

func TestServiceUnsupportedPlatform(t *testing.T) {
	setConfigEnv(t)
	old := serviceGOOS
	serviceGOOS = "windows"
	t.Cleanup(func() { serviceGOOS = old })
	for _, sub := range []string{"install", "status", "uninstall"} {
		_, _, err := executeCLI(t, Deps{}, "service", sub)
		if !errors.Is(err, errServiceUnsupported) || ExitCode(err) != 1 {
			t.Fatalf("%s: err = %v (exit %d), want errServiceUnsupported exit 1", sub, err, ExitCode(err))
		}
	}
}

// setServiceHome points os.UserHomeDir at a temp dir on every OS.
func setServiceHome(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
}

func TestServiceInstallRejectsBadTimeout(t *testing.T) {
	setConfigEnv(t)
	setServiceHome(t)
	old := serviceGOOS
	serviceGOOS = "darwin"
	t.Cleanup(func() { serviceGOOS = old })
	var calls []serviceCall
	_, _, err := executeCLI(t, serviceTestDeps(&calls, nil), "service", "install", "--timeout", "0s")
	if ExitCode(err) != 2 {
		t.Fatalf("exit = %d (err %v), want 2", ExitCode(err), err)
	}
	if len(calls) != 0 {
		t.Fatalf("service manager called for a bad flag: %#v", calls)
	}
	spec, err := (&commandContext{deps: serviceTestDeps(&calls, nil).withDefaults()}).currentServiceSpec(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spec.File); !os.IsNotExist(err) {
		t.Fatalf("bad flag wrote %s", spec.File)
	}
}

func TestServiceSystemdRollbackDisablesFreshUnit(t *testing.T) {
	setConfigEnv(t)
	setServiceHome(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	old := serviceGOOS
	serviceGOOS = "linux"
	t.Cleanup(func() { serviceGOOS = old })
	var calls []serviceCall
	_, _, err := executeCLI(t, serviceTestDeps(&calls, map[string]bool{"systemctl --user restart": true}), "service", "install", "--no-wait")
	if err == nil {
		t.Fatal("install succeeded although restart failed")
	}
	last := calls[len(calls)-1]
	if last.args[1] != "disable" {
		t.Fatalf("last call = %#v, want the fresh unit disabled on rollback", last)
	}
}

func TestServiceSystemdUninstallKeepsUnitWhenStopFails(t *testing.T) {
	setConfigEnv(t)
	setServiceHome(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	old := serviceGOOS
	serviceGOOS = "linux"
	t.Cleanup(func() { serviceGOOS = old })
	var calls []serviceCall
	out, _, err := executeCLI(t, serviceTestDeps(&calls, nil), "service", "install", "--no-wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st serviceStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	_, _, err = executeCLI(t, serviceTestDeps(&calls, map[string]bool{"systemctl --user disable": true}), "service", "uninstall")
	if err == nil || !strings.Contains(err.Error(), "stop the service") {
		t.Fatalf("err = %v, want a stop failure", err)
	}
	if _, err := os.Stat(st.File); err != nil {
		t.Fatalf("unit removed although the service could not be stopped: %v", err)
	}
}

func TestServiceSystemdRollbackKeepsUnitWhenDisableFails(t *testing.T) {
	setConfigEnv(t)
	setServiceHome(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	old := serviceGOOS
	serviceGOOS = "linux"
	t.Cleanup(func() { serviceGOOS = old })
	var calls []serviceCall
	deps := serviceTestDeps(&calls, map[string]bool{"systemctl --user restart": true, "systemctl --user disable": true})
	_, _, err := executeCLI(t, deps, "service", "install", "--no-wait")
	if err == nil || !strings.Contains(err.Error(), "kept for `ao service uninstall`") {
		t.Fatalf("err = %v, want the kept-unit rollback error", err)
	}
	spec, err := (&commandContext{deps: deps.withDefaults()}).currentServiceSpec(0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(spec.File); err != nil {
		t.Fatalf("unit removed although its enablement could not be undone: %v", err)
	}
}

// launchctl bootout returns before the job is gone, and a bootstrap of the
// same label meanwhile fails; a reinstall must wait it out.
func TestServiceLaunchdReinstallWaitsForBootout(t *testing.T) {
	setConfigEnv(t)
	setServiceHome(t)
	old := serviceGOOS
	serviceGOOS = "darwin"
	t.Cleanup(func() { serviceGOOS = old })

	loaded, gonePrints, bootstrapFails := false, 0, 0
	var log []string
	now := time.Unix(1000, 0)
	deps := Deps{
		Executable:   func() (string, error) { return "/opt/ao/bin/ao", nil },
		ProcessAlive: func(int) bool { return false },
		Now:          func() time.Time { return now },
		Sleep:        func(d time.Duration) { now = now.Add(d) },
		CommandOutput: func(_ context.Context, name string, args ...string) ([]byte, error) {
			log = append(log, args[0])
			switch args[0] {
			case "print":
				if loaded {
					if gonePrints > 0 {
						gonePrints--
						if gonePrints == 0 {
							loaded = false
						}
					}
					return nil, nil
				}
				return []byte("Could not find service"), errors.New("exit status 113")
			case "bootout":
				gonePrints = 3 // still listed for a few polls
			case "bootstrap":
				if loaded || bootstrapFails > 0 {
					if bootstrapFails > 0 {
						bootstrapFails--
					}
					return []byte("Bootstrap failed: 5: Input/output error"), errors.New("exit status 5")
				}
				loaded = true
			}
			return nil, nil
		},
	}
	if _, _, err := executeCLI(t, deps, "service", "install", "--no-wait"); err != nil {
		t.Fatal(err)
	}
	log = nil
	bootstrapFails = 1 // and the first bootstrap after teardown is refused once
	if _, _, err := executeCLI(t, deps, "service", "install", "--no-wait"); err != nil {
		t.Fatalf("reinstall failed: %v (calls %v)", err, log)
	}
	if !loaded {
		t.Fatalf("service not loaded after reinstall (calls %v)", log)
	}
}

func TestServiceInstallReportsExistingDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	setServiceHome(t)
	old := serviceGOOS
	serviceGOOS = "darwin"
	t.Cleanup(func() { serviceGOOS = old })
	srv := fakeServeDaemon(t, 4242)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	// The service's serve (pid 777) has attached and watches daemon 4242.
	if err := os.WriteFile(serveAttachMarker(cfg.runFile, 777), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var calls []serviceCall
	deps := serviceTestDeps(&calls, nil)
	deps.ProcessAlive = func(pid int) bool { return pid == 4242 }
	loaded := false
	deps.CommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch args[0] {
		case "print":
			if !loaded {
				return nil, errors.New("exit status 113")
			}
			return []byte("\tstate = running\n\tpid = 777\n"), nil
		case "bootstrap":
			loaded = true
		}
		return nil, nil
	}
	out, _, err := executeCLI(t, deps, "service", "install")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "a daemon was already running") {
		t.Fatalf("install did not say it watches the existing daemon:\n%s", out)
	}
}

// A reinstall replaces the service's own daemon; that is not an existing one.
func TestServiceReinstallDoesNotReportItsOwnDaemon(t *testing.T) {
	cfg := setConfigEnv(t)
	setServiceHome(t)
	old := serviceGOOS
	serviceGOOS = "darwin"
	t.Cleanup(func() { serviceGOOS = old })
	srv := fakeServeDaemon(t, 4242)
	if err := runfile.Write(cfg.runFile, runfile.Info{PID: 4242, Port: serverPort(t, srv.URL), StartedAt: time.Unix(100, 0).UTC()}); err != nil {
		t.Fatal(err)
	}
	var calls []serviceCall
	deps := serviceTestDeps(&calls, nil)
	deps.ProcessAlive = func(pid int) bool { return pid == 4242 }
	deps.CommandOutput = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if args[0] == "print" {
			return []byte("\tpid = 4242\n"), nil // the old job runs daemon 4242 in process
		}
		return nil, nil
	}
	out, _, err := executeCLI(t, deps, "service", "install", "--no-wait", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var st serviceStatus
	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatal(err)
	}
	if st.ExistingDaemon {
		t.Fatal("reinstall reported the service's own daemon as an existing one")
	}
}

func TestServicePIDParsesManagers(t *testing.T) {
	out := map[string]string{"launchctl": "gui/501/x = {\n\tstate = running\n\tpid = 4321\n}", "systemctl": "987\n"}
	c := &commandContext{deps: Deps{CommandOutput: func(_ context.Context, name string, _ ...string) ([]byte, error) {
		return []byte(out[name]), nil
	}}.withDefaults()}
	if got := c.servicePID(context.Background(), serviceSpec{Manager: serviceManagerLaunchd, Name: "x"}); got != 4321 {
		t.Fatalf("launchd pid = %d", got)
	}
	if got := c.servicePID(context.Background(), serviceSpec{Manager: serviceManagerSystemd, Name: "x"}); got != 987 {
		t.Fatalf("systemd pid = %d", got)
	}
}
