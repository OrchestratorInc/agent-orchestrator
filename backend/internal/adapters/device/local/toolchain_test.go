//go:build !windows

package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeNPM writes a script that mimics `npm install --prefix <dir> <pkg>@<v>`
// by creating the package's entry file, or fails when told to.
func fakeNPM(t *testing.T, fail bool) (path, calls string) {
	t.Helper()
	dir := t.TempDir()
	calls = filepath.Join(dir, "calls")
	script := "#!/bin/sh\necho \"$@\" >> '" + calls + "'\n"
	if fail {
		script += "echo 'npm ERR! network' >&2\nexit 1\n"
	} else {
		script += `prefix="$3"; spec="$7"; pkg="${spec%@*}"
mkdir -p "$prefix/node_modules/$pkg/bin" "$prefix/node_modules/$pkg/dist/server"
touch "$prefix/node_modules/$pkg/bin/agent-device.mjs" "$prefix/node_modules/$pkg/dist/server/cli.mjs"
`
	}
	path = filepath.Join(dir, "npm")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	return path, calls
}

func TestEnsureToolInstallsOnceAndPublishesAtomically(t *testing.T) {
	root := t.TempDir()
	npm, calls := fakeNPM(t, false)
	for range 2 {
		if err := ensureTool(context.Background(), root, npm, agentDeviceTool); err != nil {
			t.Fatal(err)
		}
	}
	if !agentDeviceTool.installed(root) {
		t.Fatal("tool not reported installed")
	}
	raw, _ := os.ReadFile(calls)
	if n := strings.Count(string(raw), "\n"); n != 1 {
		t.Fatalf("npm ran %d times, want once", n)
	}
	if !strings.Contains(string(raw), "agent-device@"+agentDeviceTool.version) {
		t.Fatalf("npm args %q do not pin the version", raw)
	}
	leftovers, _ := filepath.Glob(filepath.Join(root, agentDeviceTool.pkg, ".install-*"))
	if len(leftovers) != 0 {
		t.Fatalf("staging dirs left behind: %v", leftovers)
	}
}

func TestEnsureToolFailureLeavesNothingInstalled(t *testing.T) {
	root := t.TempDir()
	npm, _ := fakeNPM(t, true)
	err := ensureTool(context.Background(), root, npm, hubTool)
	if err == nil || !strings.Contains(err.Error(), "npm ERR! network") {
		t.Fatalf("err = %v, want the npm failure output", err)
	}
	if hubTool.installed(root) {
		t.Fatal("failed install reported as installed")
	}
	if _, statErr := os.Stat(hubTool.dir(root)); !os.IsNotExist(statErr) {
		t.Fatalf("failed install left %s", hubTool.dir(root))
	}
}

func TestAgentDeviceShimPinsStateAndEntry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bin", "agent-device")
	if err := writeAgentDeviceShim(path, "/usr/bin/node", "/tools/agent-device.mjs", "/ao/devices/agent-device"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"AGENT_DEVICE_STATE_DIR='/ao/devices/agent-device'", "exec '/usr/bin/node' '/tools/agent-device.mjs' \"$@\""} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("shim missing %q:\n%s", want, raw)
		}
	}
	if err := writeAgentDeviceShim(path, "/bin/no'de", "/x", "/y"); err == nil {
		t.Fatal("quoted path accepted")
	}
}

func TestCloseStopsTheAgentDeviceDaemon(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	var calls [][]string
	p.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls = append(calls, append([]string{name}, args...))
		return nil, nil
	}
	if err := p.Close(); err != nil || len(calls) != 0 {
		t.Fatalf("Close before setup ran %v, err %v; want nothing", calls, err)
	}
	if err := writeAgentDeviceShim(p.ShimPath(), "/usr/bin/node", "/tools/agent-device.mjs", "/ao/state"); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 1 || strings.Join(calls[0][1:], " ") != "daemon stop" || calls[0][0] != p.ShimPath() {
		t.Fatalf("Close ran %v, want the shim's daemon stop", calls)
	}
}

// The command an agent gets is bound to its session's device: run with only a
// subcommand, it reaches the shim with the lease's flags after it.
func TestAgentControlBindsTheLeaseWithoutFlags(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	logPath := filepath.Join(t.TempDir(), "args")
	shim := filepath.Join(t.TempDir(), "shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+logPath+"'\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.ShimPath()), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.Symlink(shim, p.ShimPath()); err != nil {
		t.Fatal(err)
	}
	ctl, err := p.AgentControl(ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS}, "aa-47")
	if err != nil {
		t.Fatal(err)
	}
	if len(ctl.Args) != 0 || !strings.Contains(ctl.Command, filepath.Join("leases", "aa-47")) {
		t.Fatalf("control = %+v, want a flagless per-lease command", ctl)
	}
	if err := exec.Command(ctl.Command, "snapshot", "-i").Run(); err != nil { //nolint:gosec // test runs the wrapper it just wrote
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	want := "snapshot\n-i\n--platform\nios\n--udid\nUDID-1\n--session\nao-aa-47\n"
	if string(raw) != want {
		t.Fatalf("shim got %q, want %q", raw, want)
	}
	// help takes no device flags: agent-device refuses them there, and the
	// device guide sends agents to `<agent-device> help workflow`.
	if err := exec.Command(ctl.Command, "help", "workflow").Run(); err != nil { //nolint:gosec // test runs the wrapper it just wrote
		t.Fatal(err)
	}
	if raw, _ := os.ReadFile(logPath); string(raw) != "help\nworkflow\n" {
		t.Fatalf("help got %q, want it unbound", raw)
	}
	android, err := p.AgentControl(ports.DeviceInfo{ID: "emulator-5554", Platform: ports.DevicePlatformAndroid}, "aa-48")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(android.Command)
	if !strings.Contains(string(body), "'--serial' 'emulator-5554'") {
		t.Fatalf("android wrapper = %s", body)
	}
	if _, err := p.AgentControl(ports.DeviceInfo{ID: "x'; rm -rf /", Platform: ports.DevicePlatformIOS}, "s"); err == nil {
		t.Fatal("unsafe device id accepted")
	}
}

func TestLaunchAndroidNamesTheLauncherActivity(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	var calls []string
	p.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, strings.Join(args, " "))
		if strings.Contains(strings.Join(args, " "), "resolve-activity") {
			return []byte("priority=0 preferredOrder=0 match=0x108000\naoagents.dev.devices/aoagents.dev.MainActivity\n"), nil
		}
		return []byte("Starting: Intent { cmp=aoagents.dev.devices/aoagents.dev.MainActivity }\n"), nil
	}
	dev := ports.DeviceInfo{ID: "emulator-5554", Name: "emu", Platform: ports.DevicePlatformAndroid}
	if err := p.Launch(context.Background(), dev, "aoagents.dev.devices"); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.HasSuffix(calls[1], "am start -n aoagents.dev.devices/aoagents.dev.MainActivity") {
		t.Fatalf("calls = %v, want a resolve then an explicit am start", calls)
	}
	// Not installed: say so instead of reporting success.
	p.run = func(context.Context, string, ...string) ([]byte, error) { return []byte("No activity found\n"), nil }
	if err := p.Launch(context.Background(), dev, "dev.missing.app"); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v, want a not-installed error", err)
	}
	if err := p.Launch(context.Background(), dev, "x; reboot"); err == nil {
		t.Fatal("unsafe package name accepted")
	}
}

func TestIOSRuntimeVersion(t *testing.T) {
	if v, ok := iosRuntimeVersion("com.apple.CoreSimulator.SimRuntime.iOS-26-5"); !ok || v != "iOS 26.5" {
		t.Fatalf("got %q, %v", v, ok)
	}
	if _, ok := iosRuntimeVersion("com.apple.CoreSimulator.SimRuntime.tvOS-26-0"); ok {
		t.Fatal("tvOS accepted")
	}
}

func TestUnlistedSimulatorsAddsWhatTheHubHides(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("simctl is macOS only")
	}
	p := New(Config{DataDir: t.TempDir()})
	p.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"devices":{
"com.apple.CoreSimulator.SimRuntime.iOS-26-5":[{"udid":"SEEN","name":"Seen","state":"Shutdown"},{"udid":"NEW","name":"Fresh iPhone","state":"Shutdown"},{"udid":"UP","name":"Running","state":"Booted"}],
"com.apple.CoreSimulator.SimRuntime.tvOS-26-0":[{"udid":"TV","name":"Apple TV","state":"Shutdown"}]}}`), nil
	}
	got := p.unlistedSimulators(context.Background(), []ports.DeviceInfo{{ID: "SEEN"}})
	if len(got) != 2 || got[0].Name != "Fresh iPhone" || got[0].Version != "iOS 26.5" || got[1].Name != "Running" || !got[1].Booted {
		t.Fatalf("got %+v, want the unlisted iOS simulators only", got)
	}
}

func TestBootLeavesARunningEmulatorAlone(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	// No hub is running: any call to it would fail the boot.
	got, err := p.Boot(context.Background(), ports.DeviceInfo{ID: "emulator-5590", Platform: ports.DevicePlatformAndroid, Booted: true})
	if err != nil || got.ID != "emulator-5590" || !got.Booted {
		t.Fatalf("Boot = %+v, %v; want the running emulator returned untouched", got, err)
	}
}

// A simulator coming back on gets a fresh stream helper: the hub's old one kept
// the previous boot's touch input, so every touch after a restart went nowhere.
// A running simulator keeps its helper (its viewers would drop).
func TestBootResetsAnOffSimulatorsStreamHelper(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.Method+" "+r.URL.Path)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}
	boot := func(booted bool) []string {
		mu.Lock()
		paths = nil
		mu.Unlock()
		// No live stream comes from this hub: cut the wait for one short.
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()
		if _, err := p.Boot(ctx, ports.DeviceInfo{ID: "UDID-1", Name: "Lab", Platform: ports.DevicePlatformIOS, Booted: booted}); err != nil {
			t.Fatal(err)
		}
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), paths...)
	}
	off := boot(false)
	if len(off) < 3 || off[0] != "POST /vendor/serve-sim/grid/api/shutdown" || off[1] != "POST /api/devices/boot" || off[2] != "POST /vendor/serve-sim/grid/api/start" {
		t.Fatalf("booting an off simulator: hub saw %q, want its helper reset, then boot and grid start", off)
	}
	for _, path := range boot(true) {
		if strings.Contains(path, "grid/api/shutdown") {
			t.Fatalf("a running simulator's helper was reset: %q", path)
		}
	}
}

func TestUnlistedEmulatorsAddsAVDsTheHubHides(t *testing.T) {
	sdk := t.TempDir()
	for _, tool := range []string{"emulator/emulator", "platform-tools/adb"} {
		path := filepath.Join(sdk, tool)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	avdHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(avdHome, "fresh.ini"), []byte("target=android-35\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANDROID_HOME", sdk)
	t.Setenv("ANDROID_AVD_HOME", avdHome)
	p := New(Config{DataDir: t.TempDir()})
	p.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		switch {
		case strings.HasSuffix(name, "emulator") && args[0] == "-list-avds":
			return []byte("seen\nfresh\nup\n"), nil
		case args[0] == "devices":
			return []byte("List of devices attached\nemulator-5590\tdevice\nABC123\tdevice\n"), nil
		case args[len(args)-1] == "name":
			return []byte("up\nOK\n"), nil
		}
		return nil, fmt.Errorf("unexpected %s %v", name, args)
	}
	got := p.unlistedEmulators(context.Background(), []ports.DeviceInfo{{ID: "seen", Name: "seen"}})
	if len(got) != 2 || got[0].ID != "fresh" || got[0].Version != "API 35" || got[0].Booted ||
		got[1].ID != "emulator-5590" || got[1].Name != "up" || !got[1].Booted {
		t.Fatalf("got %+v, want the unlisted AVDs, the running one by serial", got)
	}
	// The hub already lists the running one by serial: not offered twice.
	got = p.unlistedEmulators(context.Background(), []ports.DeviceInfo{{ID: "seen", Name: "seen"}, {ID: "emulator-5590", Name: "up"}})
	if len(got) != 1 || got[0].ID != "fresh" {
		t.Fatalf("got %+v, want only the stopped AVD", got)
	}
}

// iOS Recents goes through the agent driver, because the hub's app switcher
// presses a Home button a Face ID simulator does not have. Other buttons stay
// on the stream socket.
func TestPressButtonRunsIOSHomeAndAppSwitcherThroughTheDriver(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	logPath := filepath.Join(t.TempDir(), "args")
	shim := filepath.Join(t.TempDir(), "shim")
	if err := os.WriteFile(shim, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > '"+logPath+"'\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.ShimPath()), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.Symlink(shim, p.ShimPath()); err != nil {
		t.Fatal(err)
	}
	ios := ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS}
	if err := p.PressButton(context.Background(), ios, "aa-47", "recents"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	if want := "app-switcher\n--platform\nios\n--udid\nUDID-1\n--session\nao-aa-47\n"; string(raw) != want {
		t.Fatalf("driver got %q, want %q", raw, want)
	}
	if err := p.PressButton(context.Background(), ios, "aa-47", "home"); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(logPath)
	if want := "home\n--platform\nios\n--udid\nUDID-1\n--session\nao-aa-47\n"; string(raw) != want {
		t.Fatalf("driver got %q, want %q", raw, want)
	}
	for _, c := range []struct {
		device ports.DeviceInfo
		button string
	}{{ios, "back"}, {ports.DeviceInfo{ID: "emulator-5554", Platform: ports.DevicePlatformAndroid}, "recents"}} {
		if err := p.PressButton(context.Background(), c.device, "aa-47", c.button); !errors.Is(err, ports.ErrDeviceButtonUnsupported) {
			t.Fatalf("%s on %s = %v, want the socket's to carry it", c.button, c.device.Platform, err)
		}
	}
}

// Each stream gets the preset its helper understands: Android a size cap, bitrate
// and frame rate on the emulator helper, iOS a size cap, frame rate and JPEG
// quality on the simulator's.
func TestSetStreamQualityPatchesTheDevicesHelper(t *testing.T) {
	var got []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization")+" "+strings.TrimSpace(string(body)))
		_, _ = io.WriteString(w, "{}")
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}

	android := ports.DeviceInfo{ID: "emulator-5554", Platform: ports.DevicePlatformAndroid}
	ios := ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS}
	if err := p.SetStreamQuality(context.Background(), android, ports.DeviceQualityLow); err != nil {
		t.Fatal(err)
	}
	if err := p.SetStreamQuality(context.Background(), ios, ports.DeviceQualityHigh); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`PATCH /vendor/serve-emu/api/stream-settings?device=emulator-5554 auth=Bearer tok {"h264Bitrate":800000,"h264Fps":20,"maxDimension":720}`,
		`PATCH /vendor/serve-sim/helper/UDID-1/stream-settings auth=Bearer tok {"h264Bitrate":6000000,"h264Fps":60,"maxDimension":1600,"mjpegFps":60,"mjpegQuality":0.7}`,
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("hub saw %q, want %q", got, want)
	}
	if err := p.SetStreamQuality(context.Background(), android, "turbo"); err == nil {
		t.Fatal("an unknown quality was sent to the hub")
	}
}

// A person pressing a button before any agent has opened the driver's session
// still works: the provider attaches one (no app) and presses again.
func TestPressButtonOpensTheDriverSessionWhenNoneExists(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	dir := t.TempDir()
	marker, logPath := filepath.Join(dir, "session"), filepath.Join(dir, "calls")
	script := "#!/bin/sh\n" +
		"echo \"$@\" >> '" + logPath + "'\n" +
		"if [ \"$1\" = open ]; then touch '" + marker + "'; exit 0; fi\n" +
		"if [ ! -f '" + marker + "' ]; then echo 'Error (SESSION_NOT_FOUND): No active session.'; exit 1; fi\n"
	shim := filepath.Join(dir, "shim")
	if err := os.WriteFile(shim, []byte(script), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p.ShimPath()), 0o755); err != nil { //nolint:gosec // test fixture
		t.Fatal(err)
	}
	if err := os.Symlink(shim, p.ShimPath()); err != nil {
		t.Fatal(err)
	}
	ios := ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS}
	if err := p.PressButton(context.Background(), ios, "aa-47", "home"); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(logPath)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "home ") || !strings.HasPrefix(lines[1], "open ") || !strings.HasPrefix(lines[2], "home ") {
		t.Fatalf("driver calls = %q, want home, open, home", lines)
	}
}

// An iPad is told apart by its model, not its name ("AO lab iPhone" can be
// anything): the hub's frame for listed simulators, simctl's device type for
// the ones it hides.
func TestListMarksTablets(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"simulators":[
{"id":"PHONE","name":"Lab iPad?","platform":"ios","version":"iOS 26.5","deviceFrame":"ios:iphone-17-pro"},
{"id":"PAD","name":"Lab device","platform":"ios","version":"iOS 26.5","deviceFrame":"ios:ipad-pro-11"}],
"emulators":[{"id":"tab","name":"tab","platform":"android","version":"Android 15.0","deviceFrame":"android:pixel-tablet"}]}`)
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}
	p.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		if name == "xcrun" {
			return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-5":[
{"udid":"HIDDEN-PAD","name":"Spare","state":"Shutdown","deviceTypeIdentifier":"com.apple.CoreSimulator.SimDeviceType.iPad-Air-11-inch-M3"}]}}`), nil
		}
		return nil, errors.New("not here")
	}
	devices, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tablet := map[string]bool{}
	for _, d := range devices {
		tablet[d.ID] = d.Tablet
	}
	if tablet["PHONE"] || !tablet["PAD"] || !tablet["tab"] {
		t.Fatalf("tablets = %v, want PAD and tab only among the hub's", tablet)
	}
	if runtime.GOOS == "darwin" && !tablet["HIDDEN-PAD"] {
		t.Fatalf("simctl's iPad not marked: %v", tablet)
	}
}
