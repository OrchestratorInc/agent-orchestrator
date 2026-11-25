//go:build !windows

package local

import (
	"context"
	"encoding/json"
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
	"sync/atomic"
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
		// npm ci, run in the install dir: install the one dependency package.json names.
		script += `prefix="$PWD"; [ -f "$prefix/package-lock.json" ] || { echo 'no lockfile' >&2; exit 1; }
pkg=$(sed -n 's/.*"dependencies": *{ *"\([^"]*\)".*/\1/p;/"dependencies"/{n;s/^ *"\([^"]*\)".*/\1/p;}' "$prefix/package.json" | head -1)
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
	if !strings.HasPrefix(string(raw), "ci ") || !strings.Contains(string(raw), "--ignore-scripts") {
		t.Fatalf("npm args %q: want a locked install without scripts", raw)
	}
	manifest, _ := os.ReadFile(filepath.Join(agentDeviceTool.dir(root), "package.json"))
	if !strings.Contains(string(manifest), `"agent-device": "`+agentDeviceTool.version+`"`) {
		t.Fatalf("package.json does not pin the version:\n%s", manifest)
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
	got, _, _ := p.unlistedSimulators(context.Background(), []ports.DeviceInfo{{ID: "SEEN"}})
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
	p.run = func(context.Context, string, ...string) ([]byte, error) { return []byte(`{"devices":{}}`), nil }
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
	got, _ := p.unlistedEmulators(context.Background(), []ports.DeviceInfo{{ID: "seen", Name: "seen"}})
	if len(got) != 2 || got[0].ID != "fresh" || got[0].Version != "API 35" || got[0].Booted ||
		got[1].ID != "emulator-5590" || got[1].Name != "up" || !got[1].Booted {
		t.Fatalf("got %+v, want the unlisted AVDs, the running one by serial", got)
	}
	// The hub already lists the running one by serial: not offered twice.
	got, _ = p.unlistedEmulators(context.Background(), []ports.DeviceInfo{{ID: "seen", Name: "seen"}, {ID: "emulator-5590", Name: "up"}})
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
	var mu sync.Mutex
	var seen []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.RequestURI()+" auth="+r.Header.Get("Authorization")+" "+strings.TrimSpace(string(body)))
		mu.Unlock()
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
	mu.Lock()
	got := append([]string(nil), seen...)
	mu.Unlock()
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

// A hub that takes the connection but never answers must not hold the
// readiness wait (and the provider's lock) past its deadline.
func TestReadyProbeGivesUpOnAHubThatNeverAnswers(t *testing.T) {
	release := make(chan struct{})
	hub := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-release }))
	t.Cleanup(func() { close(release); hub.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if ready(ctx, hub.URL+"/readyz") {
		t.Fatal("a hub that never answered counted as ready")
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("probe waited %v, want it to stop at its 200ms deadline", waited)
	}
}

// The hub's address is the one it prints for the port it bound itself, read
// across writes however they split.
func TestPortWatcherReadsTheHubsOwnAddress(t *testing.T) {
	w := newPortWatcher()
	for _, chunk := range []string{"Expo Device Hub ready\n\n  Local:   http://local", "host:51234/?token=abc\n", "  Local:   http://localhost:9\n"} {
		_, _ = w.Write([]byte(chunk))
	}
	select {
	case port := <-w.port:
		if port != 51234 {
			t.Fatalf("port = %d, want 51234", port)
		}
	default:
		t.Fatal("no port read from the hub's output")
	}
	select {
	case port := <-w.port:
		t.Fatalf("a second port %d was passed on", port)
	default:
	}
}

// A simulator this boot powered on is shut down again when its stream does not
// start (nobody would own it); one that was already running is left alone.
func TestBootRollsBackASimulatorWhoseStreamDidNotStart(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		if r.URL.Path == "/vendor/serve-sim/grid/api/start" {
			_, _ = io.WriteString(w, `{"ok":false,"error":"capture failed"}`)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}
	p.run = func(context.Context, string, ...string) ([]byte, error) { return []byte(`{"devices":{}}`), nil }
	shutdowns := func(booted bool) int {
		mu.Lock()
		paths = nil
		mu.Unlock()
		got, err := p.Boot(context.Background(), ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS, Booted: booted})
		if err == nil {
			t.Fatal("a failed stream start was reported as a boot")
		}
		if !booted && got.Booted {
			t.Fatal("the rolled-back simulator still reads as booted")
		}
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, path := range paths {
			if path == "/api/devices/shutdown" {
				n++
			}
		}
		return n
	}
	if n := shutdowns(false); n != 1 {
		t.Fatalf("simulator this boot started: %d shutdowns, want 1", n)
	}
	if n := shutdowns(true); n != 0 {
		t.Fatalf("simulator already running: %d shutdowns, want none", n)
	}
}

// A write that ends inside the port number waits for the rest of it.
func TestPortWatcherWaitsForTheWholePort(t *testing.T) {
	w := newPortWatcher()
	_, _ = w.Write([]byte("  Local:   http://localhost:5"))
	select {
	case port := <-w.port:
		t.Fatalf("published port %d before the number ended", port)
	default:
	}
	_, _ = w.Write([]byte("1234\n"))
	select {
	case port := <-w.port:
		if port != 51234 {
			t.Fatalf("port = %d, want 51234", port)
		}
	default:
		t.Fatal("no port once the number ended")
	}
}

// A hub exit while a restart is running is not dropped: it is queued.
func TestHubExitDuringARestartIsQueued(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	p.reviving = true
	p.reviveHub() // the exit callback, while a restart runs
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.reviveAgain {
		t.Fatal("an exit seen during a restart was dropped")
	}
}

// A lease's command stops working when the lease ends: run again, it would
// open a driver session on a device another session may hold.
func TestRevokeControlTurnsTheLeaseCommandOff(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	if err := p.RevokeControl("aa-47"); err != nil {
		t.Fatalf("revoking a session that never had a command = %v", err)
	}
	ctl, err := p.AgentControl(ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS}, "aa-47")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RevokeControl("aa-47"); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(ctl.Command, "snapshot", "-i").CombinedOutput() //nolint:gosec // test runs the wrapper it just wrote
	if err == nil || !strings.Contains(string(out), "no device lease") || strings.Contains(string(out), "UDID-1") {
		t.Fatalf("revoked command ran: err=%v out=%q", err, out)
	}
}

// Shutting down must not wait for an install or hub start in flight, and a
// start that arrives after shutdown must not run a hub nobody stops.
func TestCloseDoesNotWaitForASlowStart(t *testing.T) {
	dir := t.TempDir()
	slowNPM := filepath.Join(dir, "npm")
	if err := os.WriteFile(slowNPM, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	p := New(Config{DataDir: t.TempDir()})
	p.lookPath = func(name string) (string, error) {
		if name == "npm" {
			return slowNPM, nil
		}
		return "/usr/bin/true", nil
	}
	started := make(chan error, 1)
	go func() { started <- p.EnsureReady(context.Background()) }()
	time.Sleep(200 * time.Millisecond) // inside the install
	closed := time.Now()
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if waited := time.Since(closed); waited > 2*time.Second {
		t.Fatalf("Close waited %v for the install", waited)
	}
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("a start cut short by Close reported success")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the install kept running after Close")
	}
	if err := p.EnsureReady(context.Background()); !errors.Is(err, errProviderClosed) {
		t.Fatalf("EnsureReady after Close = %v, want errProviderClosed", err)
	}
}

// The hub reports errors as {id, message, error} objects, and a success can
// carry some too (a boot whose camera feed failed): ok decides, and a failure
// says the hub's own reason.
func TestHubResultsDecodeTheHubsErrorObjects(t *testing.T) {
	var ok hubActionResult
	if err := json.Unmarshal([]byte(`{"ok":true,"id":"emulator-5554","errors":[{"id":"camera","message":"Camera feed seed failed","error":"Error: ENOENT"}]}`), &ok); err != nil {
		t.Fatalf("a success with errors did not decode: %v", err)
	}
	if err := ok.err("boot"); err != nil {
		t.Fatalf("a success with errors failed: %v", err)
	}
	var failed hubActionResult
	if err := json.Unmarshal([]byte(`{"ok":false,"error":"Failed to boot","errors":[{"id":"x","message":"Failed to allocate an emulator port for AO_QA","error":"Error: spawn avdmanager ENOENT\n    at ..."}]}`), &failed); err != nil {
		t.Fatal(err)
	}
	if err := failed.err("boot"); err == nil || !strings.Contains(err.Error(), "Failed to allocate an emulator port for AO_QA") {
		t.Fatalf("err = %v, want the hub's reason", err)
	}
	var legacy hubActionResult
	if err := json.Unmarshal([]byte(`{"ok":false,"errors":["plain reason"]}`), &legacy); err != nil || !strings.Contains(legacy.err("boot").Error(), "plain reason") {
		t.Fatalf("plain string errors: %v / %v", err, legacy.err("boot"))
	}
}

// A simulator the list still shows as off but that is running now (booted by
// another open, or from Xcode) is not reset: the reset would shut it down.
func TestBootDoesNotResetASimulatorThatIsAlreadyRunning(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}
	p.run = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-5":[{"udid":"UDID-1","state":"Booted"}]}}`), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := p.Boot(ctx, ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, path := range paths {
		if path == "/vendor/serve-sim/grid/api/shutdown" {
			t.Fatalf("a running simulator was reset: %v", paths)
		}
	}
}

// A hub that dies during startup is reported, not restarted forever.
func TestHubThatDiesDuringStartupIsNotRevived(t *testing.T) {
	dir := t.TempDir()
	node := filepath.Join(dir, "node")
	if err := os.WriteFile(node, []byte("#!/bin/sh\necho 'boom' >&2\nexit 1\n"), 0o755); err != nil { //nolint:gosec // test fixture must be executable
		t.Fatal(err)
	}
	p := New(Config{DataDir: t.TempDir()})
	if _, err := p.startHub(context.Background(), node, "hub.mjs"); err == nil {
		t.Fatal("a hub that exited at once started")
	}
	time.Sleep(200 * time.Millisecond)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reviving {
		t.Fatal("a hub that never became ready is being restarted")
	}
	raw, _ := os.ReadFile(filepath.Join(p.stateRoot(), "hub.log"))
	if !strings.Contains(string(raw), "boom") {
		t.Fatalf("hub.log lost the failed start's output: %q", raw)
	}
}

// Each pinned tool installs from a lockfile made for exactly its version: a
// version bump without a fresh lockfile fails here, not on a user's machine.
func TestToolLocksPinTheToolVersions(t *testing.T) {
	for _, tool := range []tool{hubTool, agentDeviceTool} {
		raw, err := toolLocks.ReadFile("locks/" + tool.pkg + ".package-lock.json")
		if err != nil {
			t.Fatalf("%s has no lockfile: %v", tool.pkg, err)
		}
		var lock struct {
			Name     string `json:"name"`
			Packages map[string]struct {
				Version      string            `json:"version"`
				Dependencies map[string]string `json:"dependencies"`
			} `json:"packages"`
		}
		if err := json.Unmarshal(raw, &lock); err != nil {
			t.Fatal(err)
		}
		if lock.Name != toolManifestName || lock.Packages[""].Dependencies[tool.pkg] != tool.version || lock.Packages["node_modules/"+tool.pkg].Version != tool.version {
			t.Fatalf("%s lockfile does not pin %s (regenerate it with npm install --package-lock-only --save-exact)", tool.pkg, tool.version)
		}
	}
}

// The hub puts what failed in error (inspect output) and often ends message
// at a colon: both are shown, the detail by its first line.
func TestHubErrorsShowTheFirstLineOfTheDetail(t *testing.T) {
	spawn := "Error: spawn /sdk/cmdline-tools/latest/bin/avdmanager ENOENT\n    at ChildProcess._handle.onexit\n  errno: -2\n}"
	got := hubErrors{{Message: "Failed to allocate an emulator port for QA: [android-utils] Failed to run `avdmanager list avd`:", Error: spawn}}.String()
	if want := "Failed to allocate an emulator port for QA: [android-utils] Failed to run `avdmanager list avd`: Error: spawn /sdk/cmdline-tools/latest/bin/avdmanager ENOENT"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := (hubErrors{{Message: "Camera feed seed failed", Error: "Error: ENOENT"}}).String(); got != "Camera feed seed failed: Error: ENOENT" {
		t.Fatalf("got %q", got)
	}
	if got := (hubErrors{{Error: spawn}}).String(); got != "Error: spawn /sdk/cmdline-tools/latest/bin/avdmanager ENOENT" {
		t.Fatalf("detail only: got %q, want its first line", got)
	}
	if got := (hubErrors{{Message: "plain reason"}}).String(); got != "plain reason" {
		t.Fatalf("got %q", got)
	}
}

// The hub runs avdmanager before every Android boot; an SDK without the
// command-line tools must not offer Android, or every open fails.
func TestAndroidNeedsTheCommandLineTools(t *testing.T) {
	sdk := t.TempDir()
	for _, rel := range []string{"platform-tools/adb", "emulator/emulator"} {
		path := filepath.Join(sdk, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("ANDROID_HOME", sdk)
	p := New(Config{DataDir: t.TempDir()})
	p.run = func(context.Context, string, ...string) ([]byte, error) { return []byte("/usr/bin/simctl\n"), nil }
	android := func() ports.DevicePlatformAvailability {
		for _, platform := range p.Platforms(context.Background()) {
			if platform.Platform == ports.DevicePlatformAndroid {
				return platform
			}
		}
		t.Fatal("no android entry")
		return ports.DevicePlatformAvailability{}
	}
	if got := android(); got.Available || !strings.Contains(got.Reason, "Command-line Tools") {
		t.Fatalf("without avdmanager: %+v, want unavailable with what to install", got)
	}
	tool := avdmanagerPath(sdk)
	if err := os.MkdirAll(filepath.Dir(tool), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := android(); !got.Available {
		t.Fatalf("with avdmanager: %+v, want available", got)
	}
}

// A list where one platform failed still succeeds with the rest, but asking
// for that platform alone fails: its devices may only be missing.
func TestListPlatformFailsForAPlatformThatDidNotList(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"simulators":[],"emulators":[{"id":"emulator-5554","name":"Pixel","platform":"android","booted":true}],`+
			`"errors":[{"id":"x","message":"Failed to list iOS simulators:","error":"Error: xcrun simctl list exited 1"}]}`)
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}
	// simctl and the SDK (whichever this machine has) list nothing more.
	p.run = func(_ context.Context, _ string, args ...string) ([]byte, error) {
		switch {
		case len(args) > 0 && args[0] == "simctl":
			return []byte(`{"devices":{}}`), nil
		case len(args) > 0 && args[0] == "-list-avds":
			return nil, nil
		case len(args) > 0 && args[0] == "devices":
			return []byte("List of devices attached\n"), nil
		}
		return nil, errors.New("unexpected")
	}
	ctx := context.Background()
	if all, err := p.List(ctx); err != nil || len(all) != 1 {
		t.Fatalf("List = %+v, %v; want the emulator", all, err)
	}
	if android, err := p.ListPlatform(ctx, ports.DevicePlatformAndroid); err != nil || len(android) != 1 {
		t.Fatalf("ListPlatform(android) = %+v, %v; want the emulator", android, err)
	}
	if _, err := p.ListPlatform(ctx, ports.DevicePlatformIOS); err == nil || !strings.Contains(err.Error(), "simctl") {
		t.Fatalf("ListPlatform(ios) err = %v, want the listing failure", err)
	}
}

// replacementsSeen counts the hub replacements reported on replaced within a
// short wait: NoteBooted starts them on a goroutine.
func replacementsSeen(replaced <-chan struct{}) int {
	n := 0
	deadline := time.After(150 * time.Millisecond)
	for {
		select {
		case <-replaced:
			n++
		case <-deadline:
			return n
		}
	}
}

func fakeReplacements(p *Provider) <-chan struct{} {
	replaced := make(chan struct{}, 8)
	p.replaceHub = func() {
		p.mu.Lock()
		p.replacingHub = false
		p.mu.Unlock()
		replaced <- struct{}{}
	}
	return replaced
}

// A simulator booted outside AO (an agent's simctl boot, Xcode) keeps the
// hub's stream helper from its last boot, whose touch input goes nowhere: the
// hub is replaced once a leased simulator turns up on a boot AO did not start.
// A first sighting, the same boot again, a device that is off and an emulator
// are left alone.
func TestNoteBootedReplacesTheHubForABootOutsideAO(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{done: make(chan struct{})}
	replaced := fakeReplacements(p)
	sim := func(boot string, booted bool) ports.DeviceInfo {
		return ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS, Booted: booted, BootID: boot}
	}
	p.NoteBooted(sim("boot-1", true))
	p.NoteBooted(sim("boot-1", true))
	p.NoteBooted(sim("boot-2", false))
	p.NoteBooted(ports.DeviceInfo{ID: "emulator-5554", Platform: ports.DevicePlatformAndroid, Booted: true, BootID: "x"})
	p.NoteBooted(ports.DeviceInfo{ID: "emulator-5554", Platform: ports.DevicePlatformAndroid, Booted: true, BootID: "y"})
	if n := replacementsSeen(replaced); n != 0 {
		t.Fatalf("replaced %d times before any outside boot", n)
	}
	p.NoteBooted(sim("boot-2", true))
	p.NoteBooted(sim("boot-2", true))
	if n := replacementsSeen(replaced); n != 1 {
		t.Fatalf("replaced %d times for one outside boot, want 1", n)
	}
}

// A boot Boot is running is expected and does not replace the hub, however
// often it is seen; once Boot has ended, the next unexpected boot does.
func TestNoteBootedAcceptsItsOwnBoot(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{done: make(chan struct{})}
	replaced := fakeReplacements(p)
	sim := func(boot string) ports.DeviceInfo {
		return ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS, Booted: true, BootID: boot}
	}
	p.NoteBooted(sim("boot-1"))
	p.expectBoot("UDID-1")
	p.NoteBooted(sim("boot-2"))
	p.NoteBooted(sim("boot-2"))
	if n := replacementsSeen(replaced); n != 0 {
		t.Fatalf("AO's own boot replaced the hub %d times", n)
	}
	p.mu.Lock()
	delete(p.ownBoots, "UDID-1") // Boot ended
	p.mu.Unlock()
	p.NoteBooted(sim("boot-3"))
	if n := replacementsSeen(replaced); n != 1 {
		t.Fatalf("the next outside boot replaced the hub %d times, want 1", n)
	}
}

// simctlBoots answers `simctl list devices` with UDID-1 on the given boot.
func simctlBoots(boot *string, mu *sync.Mutex) func(context.Context, string, ...string) ([]byte, error) {
	return func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name != "xcrun" {
			return nil, errors.New("not here")
		}
		mu.Lock()
		defer mu.Unlock()
		return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-5":[{"udid":"UDID-1","name":"Lab","state":"Shutdown","lastUsedAt":"` + *boot + `"}]}}`), nil
	}
}

// An agent opens a device with nobody watching, then it is rebooted outside
// AO: the first look at it afterwards must still catch the reboot, because
// Boot recorded the boot it made.
func TestOutsideBootAfterAnUnwatchedAOBootIsCaught(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("simctl is macOS only")
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}
	replaced := fakeReplacements(p)
	var mu sync.Mutex
	boot := "boot-ao"
	p.run = simctlBoots(&boot, &mu)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := p.Boot(ctx, ports.DeviceInfo{ID: "UDID-1", Name: "Lab", Platform: ports.DevicePlatformIOS}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	recorded, expected := p.bootIDs["UDID-1"], p.ownBoots["UDID-1"]
	p.mu.Unlock()
	if recorded != "boot-ao" || expected {
		t.Fatalf("after Boot: recorded %q, still expected %v; want boot-ao and no mark", recorded, expected)
	}
	p.NoteBooted(ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS, Booted: true, BootID: "boot-outside"})
	if n := replacementsSeen(replaced); n != 1 {
		t.Fatalf("an unwatched outside boot replaced the hub %d times, want 1", n)
	}
}

// A hub's helpers belong to the boots its simulators were on when it started:
// a lease restored after a daemon restart, or taken on a simulator already
// running, is caught on its first look after an outside reboot.
func TestHubStartRecordsEverySimulatorsBoot(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("simctl is macOS only")
	}
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{done: make(chan struct{})}
	replaced := fakeReplacements(p)
	var mu sync.Mutex
	boot := "boot-1"
	p.run = simctlBoots(&boot, &mu)
	p.recordBoots(context.Background())
	p.NoteBooted(ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS, Booted: true, BootID: "boot-2"})
	if n := replacementsSeen(replaced); n != 1 {
		t.Fatalf("a reboot since the hub started replaced the hub %d times, want 1", n)
	}
}

// Boots seen while a replacement runs do not start another one.
func TestNoteBootedReplacesOneAtATime(t *testing.T) {
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{done: make(chan struct{})}
	release := make(chan struct{})
	var starts atomic.Int32
	p.replaceHub = func() {
		starts.Add(1)
		<-release
		p.mu.Lock()
		p.replacingHub = false
		p.mu.Unlock()
	}
	for _, boot := range []string{"boot-1", "boot-2", "boot-3", "boot-4"} {
		p.NoteBooted(ports.DeviceInfo{ID: "UDID-1", Platform: ports.DevicePlatformIOS, Booted: true, BootID: boot})
		time.Sleep(20 * time.Millisecond)
	}
	close(release)
	if n := starts.Load(); n != 1 {
		t.Fatalf("%d replacements started, want 1 while the first runs", n)
	}
}

// A Boot that fails leaves no expected boot behind: a later boot outside AO
// must not pass as this one.
func TestBootThatFailsExpectsNoBoot(t *testing.T) {
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/devices/boot" {
			http.Error(w, "no", http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}
	p.run = func(context.Context, string, ...string) ([]byte, error) { return []byte(`{"devices":{}}`), nil }
	if _, err := p.Boot(context.Background(), ports.DeviceInfo{ID: "UDID-1", Name: "Lab", Platform: ports.DevicePlatformIOS}); err == nil {
		t.Fatal("Boot succeeded against a failing hub")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ownBoots["UDID-1"] {
		t.Fatal("a failed Boot left its boot expected")
	}
}

// The listing carries each simulator's boot (simctl's lastUsedAt), for the
// hub's simulators and simctl's extras alike.
func TestListCarriesSimulatorBoots(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("simctl is macOS only")
	}
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"simulators":[{"id":"LISTED","name":"Lab","platform":"ios","version":"iOS 26.5","booted":true}],"emulators":[]}`)
	}))
	t.Cleanup(hub.Close)
	p := New(Config{DataDir: t.TempDir()})
	p.hub = &hubProcess{origin: hub.URL, token: "tok", done: make(chan struct{})}
	p.run = func(_ context.Context, name string, _ ...string) ([]byte, error) {
		if name == "xcrun" {
			return []byte(`{"devices":{"com.apple.CoreSimulator.SimRuntime.iOS-26-5":[
{"udid":"LISTED","name":"Lab","state":"Booted","lastUsedAt":"2026-11-25T01:00:00Z"},
{"udid":"EXTRA","name":"Spare","state":"Shutdown","lastUsedAt":"2026-11-20T09:00:00Z"}]}}`), nil
		}
		return nil, errors.New("not here")
	}
	devices, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	boots := map[string]string{}
	for _, d := range devices {
		boots[d.ID] = d.BootID
	}
	if boots["LISTED"] != "2026-11-25T01:00:00Z" || boots["EXTRA"] != "2026-11-20T09:00:00Z" {
		t.Fatalf("boots = %v, want simctl's lastUsedAt for both", boots)
	}
}
