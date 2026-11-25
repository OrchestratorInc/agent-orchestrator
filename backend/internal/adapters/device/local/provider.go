// Package local is the device provider for simulators and emulators on this
// machine. It supervises expo-device-hub on a loopback port for streaming and
// device lifecycle, and exposes a pinned agent-device launcher for agents.
package local

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// androidPackagePattern is the shape of an Android application id.
var androidPackagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)

// safeLeaseToken limits what is written into a lease wrapper script.
var safeLeaseToken = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,128}$`)

const (
	hubReadyTimeout = 30 * time.Second
	// bootTimeout outlasts the hub's own 180 s wait for a cold boot, so a slow
	// boot ends with the hub's answer rather than our deadline mid-boot.
	bootTimeout    = 4 * time.Minute
	commandTimeout = 2 * time.Minute
)

// Config locates the provider's tools and state under the AO data dir.
type Config struct {
	DataDir string
	Log     *slog.Logger
}

// Provider is the local DeviceProvider.
type Provider struct {
	cfg  Config
	log  *slog.Logger
	http *http.Client

	lookPath func(string) (string, error)
	run      func(ctx context.Context, name string, args ...string) ([]byte, error)

	// startMu serialises the slow part of EnsureReady (installing the tools,
	// starting the hub) without holding mu, which Close and Hub need.
	startMu sync.Mutex
	// life ends when Close starts: an install or hub start in flight stops.
	life     context.Context
	stopLife context.CancelFunc

	mu  sync.Mutex
	hub *hubProcess
	// closed stops a hub that exits on its own from being restarted once the
	// daemon is shutting down; reviving guards against two restart loops.
	closed   bool
	reviving bool
	// reviveAgain records a hub exit seen while a restart was running: the
	// restart's own hub may have died before it finished.
	reviveAgain bool
	// tuned holds each device's last stream quality. A stream's settings live
	// in the hub, so a restarted hub would stream at full size (~90 Mbit/s for
	// a simulator) until something asked again; they are put back instead.
	tuned map[string]tunedStream
	// bootIDs holds the boot each leased simulator was on when last seen
	// running, and ownBoots the simulators this provider is booting, whose
	// next boot is expected (see NoteBooted). replacingHub guards against a
	// second replacement while one runs.
	bootIDs      map[string]string
	ownBoots     map[string]bool
	replacingHub bool
	// replaceHub stops the hub and starts a new one; a variable for tests.
	replaceHub func()
}

type tunedStream struct {
	device  ports.DeviceInfo
	quality ports.DeviceStreamQuality
}

type hubProcess struct {
	origin string
	token  string
	cancel context.CancelFunc
	done   chan struct{}
	// ready is set once the hub answered its readiness probe. Only a hub that
	// ran is restarted when it exits: one that dies during startup (a wrong
	// Node, a broken install) would otherwise be respawned forever.
	ready atomic.Bool
}

var _ ports.DeviceProvider = (*Provider)(nil)

// New returns a provider that starts nothing until EnsureReady.
func New(cfg Config) *Provider {
	log := cfg.Log
	if log == nil {
		log = slog.Default()
	}
	life, stopLife := context.WithCancel(context.Background())
	p := &Provider{
		cfg:      cfg,
		log:      log,
		life:     life,
		stopLife: stopLife,
		http:     &http.Client{Timeout: bootTimeout},
		lookPath: exec.LookPath,
		run: func(ctx context.Context, name string, args ...string) ([]byte, error) {
			return process.CommandContext(ctx, name, args...).CombinedOutput()
		},
	}
	p.replaceHub = p.restartHub
	return p
}

// Name is the provider id shown to users.
func (p *Provider) Name() string { return "local" }

func (p *Provider) toolsRoot() string { return filepath.Join(p.cfg.DataDir, "tools", "device") }

func (p *Provider) stateRoot() string { return filepath.Join(p.cfg.DataDir, "devices") }

// ShimPath is the agent-device launcher agents run.
func (p *Provider) ShimPath() string { return filepath.Join(p.toolsRoot(), "bin", "agent-device") }

// Platforms reports what this machine can run, with plain reasons.
func (p *Provider) Platforms(ctx context.Context) []ports.DevicePlatformAvailability {
	ios := ports.DevicePlatformAvailability{Platform: ports.DevicePlatformIOS}
	android := ports.DevicePlatformAvailability{Platform: ports.DevicePlatformAndroid}
	if runtime.GOOS == "windows" {
		ios.Reason = "iOS Simulators need macOS with Xcode."
		android.Reason = "Device support does not run on Windows yet."
		return []ports.DevicePlatformAvailability{ios, android}
	}
	if _, err := p.lookPath("node"); err != nil {
		reason := "Install Node.js 20 or newer; device support runs on Node."
		ios.Reason, android.Reason = reason, reason
		return []ports.DevicePlatformAvailability{ios, android}
	}
	switch {
	case runtime.GOOS != "darwin":
		ios.Reason = "iOS Simulators need macOS with Xcode."
	default:
		if out, err := p.run(ctx, "xcrun", "--find", "simctl"); err != nil || len(bytes.TrimSpace(out)) == 0 {
			ios.Reason = "Install Xcode and an iOS Simulator runtime (Xcode Settings → Components)."
		} else {
			ios.Available = true
		}
	}
	if sdk := androidSDK(); sdk == "" {
		android.Reason = "Install the Android SDK (platform-tools and emulator) or set ANDROID_HOME."
	} else if !executable(filepath.Join(sdk, "platform-tools", "adb")) || !executable(filepath.Join(sdk, "emulator", "emulator")) {
		android.Reason = "The Android SDK at " + sdk + " is missing platform-tools or the emulator."
	} else if !executable(avdmanagerPath(sdk)) {
		// The hub lists AVDs with avdmanager before every boot; Android
		// Studio's default SDK does not include it, and every boot would fail.
		android.Reason = "The Android SDK at " + sdk + " is missing the command-line tools (avdmanager). Install \"Android SDK Command-line Tools (latest)\" in Android Studio → SDK Manager → SDK Tools."
	} else {
		android.Available = true
	}
	return []ports.DevicePlatformAvailability{ios, android}
}

// avdmanagerPath is where the hub runs avdmanager from.
func avdmanagerPath(sdk string) string {
	return filepath.Join(sdk, "cmdline-tools", "latest", "bin", "avdmanager")
}

// errProviderClosed is EnsureReady's answer once the daemon is shutting down.
var errProviderClosed = errors.New("device support is shutting down")

// EnsureReady installs the pinned tools, writes the agent shim, and starts the
// hub. Concurrent callers share one start; a running hub returns at once.
func (p *Provider) EnsureReady(ctx context.Context) error {
	p.startMu.Lock()
	defer p.startMu.Unlock()
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return errProviderClosed
	}
	if p.hub != nil {
		select {
		case <-p.hub.done:
			p.hub = nil
		default:
			p.mu.Unlock()
			return nil
		}
	}
	p.mu.Unlock()
	// The work below can take minutes (an npm install) and must stop when the
	// daemon shuts down, not hold Close up until it finishes.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stop := context.AfterFunc(p.life, cancel)
	defer stop()
	nodePath, err := p.lookPath("node")
	if err != nil {
		return errors.New("node was not found on PATH; install Node.js 20 or newer")
	}
	npmPath, err := p.lookPath("npm")
	if err != nil {
		return errors.New("npm was not found on PATH; install Node.js 20 or newer")
	}
	root := p.toolsRoot()
	for _, t := range []tool{hubTool, agentDeviceTool} {
		if err := ensureTool(ctx, root, npmPath, t); err != nil {
			return err
		}
	}
	agentState := filepath.Join(p.stateRoot(), "agent-device")
	if err := os.MkdirAll(agentState, 0o750); err != nil {
		return fmt.Errorf("create agent-device state dir: %w", err)
	}
	if err := writeAgentDeviceShim(p.ShimPath(), nodePath, agentDeviceTool.entryPath(root), agentState); err != nil {
		return err
	}
	p.stopStaleHub(hubTool.entryPath(root))
	hub, err := p.startHub(ctx, nodePath, hubTool.entryPath(root))
	if err != nil {
		if p.life.Err() != nil {
			return errProviderClosed
		}
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		// Close ran while the hub started: nobody would ever stop it.
		hub.cancel()
		return errProviderClosed
	}
	p.hub = hub
	if p.tuned == nil {
		// A restarted daemon: put back what the last one had set.
		p.tuned = p.loadTuned()
	}
	if len(p.tuned) > 0 {
		restore := make([]tunedStream, 0, len(p.tuned))
		for _, t := range p.tuned {
			restore = append(restore, t)
		}
		// After EnsureReady releases p.mu: SetStreamQuality asks Hub for the endpoint.
		go p.retune(restore) //nolint:gosec // G118: outlives the request that started the hub
	}
	return nil
}

// retune puts back the stream settings a previous hub had.
func (p *Provider) retune(streams []tunedStream) {
	for _, t := range streams {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		if err := p.SetStreamQuality(ctx, t.device, t.quality); err != nil {
			p.log.Debug("restore device stream quality", "device", t.device.ID, "err", err)
		}
		cancel()
	}
}

// startHub starts the hub and waits for it to answer. ctx bounds only that
// wait (with hubReadyTimeout); the hub itself runs until Close.
func (p *Provider) startHub(ctx context.Context, nodePath, entry string) (*hubProcess, error) {
	token, err := randomToken()
	if err != nil {
		return nil, err
	}
	hubState := filepath.Join(p.stateRoot(), "hub")
	if err := os.MkdirAll(hubState, 0o750); err != nil {
		return nil, fmt.Errorf("create device hub state dir: %w", err)
	}
	tmp := filepath.Join(hubState, "tmp")
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return nil, fmt.Errorf("create device hub temp dir: %w", err)
	}
	// Appended, not truncated: a failed start's own output is what the error
	// sends the user to read, and a restart must not wipe it.
	logFile, err := os.OpenFile(filepath.Join(p.stateRoot(), "hub.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open device hub log: %w", err)
	}
	_, _ = fmt.Fprintf(logFile, "\n--- device hub starting %s ---\n", time.Now().Format(time.RFC3339))
	hubCtx, cancel := context.WithCancel(context.Background())
	// Port 0: the hub binds a free port itself and prints it. A port picked
	// here and handed over could be taken by another local process in between,
	// which would then pass the readiness probe and receive the hub's token.
	cmd := process.CommandContext(hubCtx, nodePath, entry, "--port", "0", "--host", "127.0.0.1")
	process.ConfigureTreeCancellation(cmd)
	// The hub's helpers keep state under the temp dir and a capture CA under
	// ~/Library/Application Support by default; both stay under AO's data
	// dir instead, so a dev daemon and the app's own do not share them.
	cmd.Env = append(os.Environ(),
		"EXPO_DEVICE_HUB_SESSION_TOKEN="+token,
		"EXPO_DEVICE_HUB_STATE_DIR="+hubState,
		"TMPDIR="+tmp,
		"SERVE_SIM_STATE_DIR="+filepath.Join(hubState, "serve-sim"),
		"SERVE_SIM_CAPTURE_CA_DIR="+filepath.Join(hubState, "capture-ca"),
		// serve-emu seeds the emulator's camera feed from here on every boot.
		"SERVE_EMU_CAMERA_DIR="+filepath.Join(hubState, "serve-emu-camera"),
		"NO_COLOR=1",
	)
	// The SDK the daemon uses for adb is the one the hub must use too.
	if sdk := androidSDK(); sdk != "" {
		cmd.Env = append(cmd.Env, "ANDROID_HOME="+sdk, "ANDROID_SDK_ROOT="+sdk)
	}
	announced := newPortWatcher()
	cmd.Stdout, cmd.Stderr = io.MultiWriter(logFile, announced), logFile
	if err := cmd.Start(); err != nil {
		cancel()
		_ = logFile.Close()
		return nil, fmt.Errorf("start device hub: %w", err)
	}
	hub := &hubProcess{token: token, cancel: cancel, done: make(chan struct{})}
	pid := cmd.Process.Pid
	p.recordHubPID(pid)
	go func() {
		err := cmd.Wait()
		p.forgetHubPID(pid)
		_ = logFile.Close()
		close(hub.done)
		switch {
		case hubCtx.Err() != nil:
		case hub.ready.Load():
			p.log.Warn("device hub exited; restarting it", "err", err)
			go p.reviveHub()
		default:
			p.log.Warn("device hub exited during startup", "err", err)
		}
	}()
	// Each probe carries the deadline: a hub that accepts the connection but
	// never answers would otherwise hold this wait, and p.mu with it, forever.
	probeCtx, stopProbing := context.WithTimeout(ctx, hubReadyTimeout)
	defer stopProbing()
	// The address comes from the hub's own output, which only this child writes.
	select {
	case port := <-announced.port:
		hub.origin = "http://127.0.0.1:" + strconv.Itoa(port)
	case <-hub.done:
		return nil, errors.New("device hub exited during startup; see " + filepath.Join(p.stateRoot(), "hub.log"))
	case <-probeCtx.Done():
		cancel()
		return nil, errors.New("device hub did not say which port it listens on")
	}
	for {
		select {
		case <-hub.done:
			return nil, errors.New("device hub exited during startup; see " + filepath.Join(p.stateRoot(), "hub.log"))
		default:
		}
		if ready(probeCtx, hub.origin+"/readyz") {
			hub.ready.Store(true)
			return hub, nil
		}
		select {
		case <-probeCtx.Done():
			cancel()
			return nil, errors.New("device hub did not become ready in time")
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// hubPortLine is the hub's startup line naming its address. The port counts
// only once something follows it: output can split a write mid-number.
var hubPortLine = regexp.MustCompile(`Local:\s+http://(?:localhost|127\.0\.0\.1):(\d+)[/\s]`)

// portWatcher reads the hub's stdout for the port it bound and passes the
// first one on; the rest of the output only goes to the log.
type portWatcher struct {
	mu   sync.Mutex
	buf  []byte
	sent bool
	port chan int
}

func newPortWatcher() *portWatcher { return &portWatcher{port: make(chan int, 1)} }

func (w *portWatcher) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.sent {
		return len(b), nil
	}
	w.buf = append(w.buf, b...)
	if m := hubPortLine.FindSubmatch(w.buf); m != nil {
		if port, err := strconv.Atoi(string(m[1])); err == nil && port > 0 && port <= 65535 {
			w.port <- port
			w.sent, w.buf = true, nil
			return len(b), nil
		}
	}
	if len(w.buf) > 64<<10 {
		w.buf = w.buf[len(w.buf)-4<<10:]
	}
	return len(b), nil
}

// ready reports whether one readiness probe answered 200 before ctx ended.
func ready(ctx context.Context, probe string) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, probe, http.NoBody)
	if err != nil {
		return false
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	_ = res.Body.Close()
	return res.StatusCode == http.StatusOK
}

// Stream presets. Android's encoder takes a size cap, a bitrate and a frame
// rate; iOS's MJPEG stream takes a size cap, a frame rate and a JPEG quality.
// "high" is sharper than Android's default (1280 px at 8 Mbit/s). iOS's frames
// are capped near the phone's screen width: at the simulator's full 1206x2622
// a scrolling screen is ~240 KB a frame (~90 Mbit/s), more than Wi-Fi carries
// steadily, and the stalls show as jitter; at 1600 px it is ~105 KB (~41 Mbit/s)
// with the worst gap 34 ms instead of 139.
var (
	androidQuality = map[ports.DeviceStreamQuality]map[string]any{
		ports.DeviceQualityHigh:     {"maxDimension": 1920, "h264Bitrate": 12_000_000, "h264Fps": 60},
		ports.DeviceQualityBalanced: {"maxDimension": 960, "h264Bitrate": 3_000_000, "h264Fps": 30},
		ports.DeviceQualityLow:      {"maxDimension": 720, "h264Bitrate": 800_000, "h264Fps": 20},
	}
	// A simulator streams both MJPEG (the desktop) and H.264 (the phone,
	// ~5 Mbit/s for fast text scrolling against ~112 for MJPEG); the size cap
	// is shared.
	iosQuality = map[ports.DeviceStreamQuality]map[string]any{
		ports.DeviceQualityHigh:     {"mjpegFps": 60, "mjpegQuality": 0.7, "maxDimension": 1600, "h264Bitrate": 6_000_000, "h264Fps": 60},
		ports.DeviceQualityBalanced: {"mjpegFps": 30, "mjpegQuality": 0.6, "maxDimension": 960, "h264Bitrate": 2_000_000, "h264Fps": 30},
		ports.DeviceQualityLow:      {"mjpegFps": 10, "mjpegQuality": 0.4, "maxDimension": 640, "h264Bitrate": 600_000, "h264Fps": 20},
	}
)

// SetStreamQuality retunes the device's stream through the hub.
func (p *Provider) SetStreamQuality(ctx context.Context, device ports.DeviceInfo, quality ports.DeviceStreamQuality) error {
	var path string
	var preset map[string]any
	switch device.Platform {
	case ports.DevicePlatformIOS:
		path, preset = "/vendor/serve-sim/helper/"+url.PathEscape(device.ID)+"/stream-settings", iosQuality[quality]
	case ports.DevicePlatformAndroid:
		path, preset = "/vendor/serve-emu/api/stream-settings?device="+url.QueryEscape(device.ID), androidQuality[quality]
	}
	if preset == nil {
		return fmt.Errorf("no %q stream preset for %s", quality, device.Platform)
	}
	var applied map[string]any
	if err := p.hubJSON(ctx, http.MethodPatch, path, preset, &applied); err != nil {
		return err
	}
	p.mu.Lock()
	if p.tuned == nil {
		p.tuned = map[string]tunedStream{}
	}
	p.tuned[device.ID] = tunedStream{device: device, quality: quality}
	p.saveTunedLocked()
	p.mu.Unlock()
	return nil
}

// PressButton presses an iOS Home or app switcher button. The hub's own
// buttons do nothing on a Face ID simulator (there is no Home button to press,
// and Home does not leave the switcher), so the daemon makes the home
// indicator swipe a person would (pressByGesture). The agent driver is the
// fallback: it works too, but brings its runner app to the front first.
// Android and everything else is left to the stream socket.
func (p *Provider) PressButton(ctx context.Context, device ports.DeviceInfo, sessionID, button string) error {
	command := map[string]string{"home": "home", "recents": "app-switcher"}[button]
	if device.Platform != ports.DevicePlatformIOS || command == "" {
		return ports.ErrDeviceButtonUnsupported
	}
	gestureErr := p.pressByGesture(ctx, device, button)
	if gestureErr == nil {
		return nil
	}
	p.log.Debug("device: home indicator gesture failed, using the agent driver", "button", button, "err", gestureErr)
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	control, err := p.AgentControl(device, sessionID)
	if err != nil {
		return err
	}
	press := func() error {
		return p.runTool(ctx, control.Command, append(append([]string(nil), control.Args...), command)...)
	}
	err = press()
	if err != nil && strings.Contains(err.Error(), "SESSION_NOT_FOUND") {
		// The driver only presses buttons inside a session, and no agent has
		// opened one yet (a person is driving the device first). Attaching with
		// no app opens it without launching anything.
		if openErr := p.runTool(ctx, control.Command, append(append([]string(nil), control.Args...), "open")...); openErr != nil {
			return openErr
		}
		err = press()
	}
	return err
}

// reviveHub restarts a hub that exited on its own (it crashed or was killed),
// so the streams being watched come back without anyone reopening a device:
// viewers reconnect through the daemon, which asks Hub for the new endpoint.
// It retries with a growing pause until the hub runs or the daemon closes.
func (p *Provider) reviveHub() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if p.reviving {
		p.reviveAgain = true
		p.mu.Unlock()
		return
	}
	p.reviving = true
	p.mu.Unlock()
	defer func() {
		p.mu.Lock()
		p.reviving = false
		again := p.reviveAgain && !p.closed
		p.reviveAgain = false
		p.mu.Unlock()
		if again {
			go p.reviveHub()
		}
	}()
	for attempt := 0; ; attempt++ {
		time.Sleep(hubRestartDelay(attempt))
		p.mu.Lock()
		closed := p.closed
		p.mu.Unlock()
		if closed {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := p.EnsureReady(ctx)
		cancel()
		if err == nil {
			p.log.Info("device hub restarted", "attempt", attempt+1)
			return
		}
		p.log.Warn("restart device hub", "attempt", attempt+1, "err", err)
	}
}

// hubRestartDelay is the pause before restart attempt n: 1 s, doubling to 30 s.
func hubRestartDelay(attempt int) time.Duration {
	d := time.Second << min(attempt, 5)
	return min(d, 30*time.Second)
}

// Hub returns the running hub's endpoint.
func (p *Provider) Hub(context.Context) (ports.DeviceHubEndpoint, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hub == nil {
		return ports.DeviceHubEndpoint{}, errors.New("device support is not running")
	}
	select {
	case <-p.hub.done:
		p.hub = nil
		return ports.DeviceHubEndpoint{}, errors.New("device hub stopped")
	default:
	}
	return ports.DeviceHubEndpoint{Origin: p.hub.origin, Headers: map[string]string{"Authorization": "Bearer " + p.hub.token}}, nil
}

type hubDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Platform string `json:"platform"`
	Booted   bool   `json:"booted"`
	Physical bool   `json:"physical"`
	// DeviceFrame is the hub's frame for the model, e.g. "ios:iphone-17-pro"
	// or "ios:ipad-pro-11".
	DeviceFrame string `json:"deviceFrame"`
}

// tabletModel reports whether a hub frame or simulator device type names an
// iPad or a tablet.
func tabletModel(model string) bool {
	model = strings.ToLower(model)
	return strings.Contains(model, "ipad") || strings.Contains(model, "tablet")
}

// List returns every simulator and emulator the hub sees, physical devices
// excluded.
func (p *Provider) List(ctx context.Context) ([]ports.DeviceInfo, error) {
	devices, _, err := p.listReport(ctx)
	return devices, err
}

// ListPlatform lists one platform's devices and fails when that platform's
// listing reported an error, so a device it lacks is really gone rather than
// missed (List still succeeds then, with whatever else listed).
func (p *Provider) ListPlatform(ctx context.Context, platform ports.DevicePlatform) ([]ports.DeviceInfo, error) {
	devices, failed, err := p.listReport(ctx)
	if err != nil {
		return nil, err
	}
	if why := failed[platform]; why != "" {
		return nil, fmt.Errorf("list %s devices: %s", platform, why)
	}
	out := make([]ports.DeviceInfo, 0, len(devices))
	for _, d := range devices {
		if d.Platform == platform {
			out = append(out, d)
		}
	}
	return out, nil
}

// listReport lists every device and, for each platform whose listing failed
// (and so may be missing devices), why.
func (p *Provider) listReport(ctx context.Context) ([]ports.DeviceInfo, map[ports.DevicePlatform]string, error) {
	var body struct {
		Simulators []hubDevice `json:"simulators"`
		Emulators  []hubDevice `json:"emulators"`
		Errors     hubErrors   `json:"errors"`
	}
	if err := p.hubJSON(ctx, http.MethodGet, "/api/devices", nil, &body); err != nil {
		return nil, nil, err
	}
	failed := map[ports.DevicePlatform]string{}
	fail := func(platform ports.DevicePlatform, why string) {
		if failed[platform] == "" {
			failed[platform] = why
		} else {
			failed[platform] += "; " + why
		}
	}
	for _, e := range body.Errors {
		for _, platform := range e.platforms() {
			fail(platform, hubErrors{e}.String())
		}
	}
	out := make([]ports.DeviceInfo, 0, len(body.Simulators)+len(body.Emulators))
	for _, d := range append(body.Simulators, body.Emulators...) {
		if d.Physical {
			continue
		}
		out = append(out, ports.DeviceInfo{ID: d.ID, Name: d.Name, Platform: ports.DevicePlatform(d.Platform), Version: d.Version, Booted: d.Booted, Tablet: tabletModel(d.DeviceFrame)})
	}
	sims, bootIDs, simErr := p.unlistedSimulators(ctx, out)
	if simErr != nil {
		fail(ports.DevicePlatformIOS, simErr.Error())
	}
	for i := range out {
		if out[i].Platform == ports.DevicePlatformIOS {
			out[i].BootID = bootIDs[out[i].ID]
		}
	}
	emus, emuErr := p.unlistedEmulators(ctx, out)
	if emuErr != nil {
		fail(ports.DevicePlatformAndroid, emuErr.Error())
	}
	out = append(append(out, sims...), emus...)
	// Nothing listed and the hub says why (no avdmanager, simctl failing):
	// that is an error to show, not an empty machine.
	if len(out) == 0 && len(body.Errors) > 0 {
		return nil, nil, fmt.Errorf("list devices: %s", body.Errors)
	}
	return out, failed, nil
}

// unlistedSimulators returns the iOS Simulators `simctl` knows and the hub
// does not, and each simulator's boot (its lastUsedAt, which simctl moves on
// every boot) by UDID. The hub lists a simulator only once it has been booted,
// so on a fresh Xcode install, or for one just created, its list is empty; the
// hub still boots any simulator by id.
func (p *Provider) unlistedSimulators(ctx context.Context, listed []ports.DeviceInfo) ([]ports.DeviceInfo, map[string]string, error) {
	if runtime.GOOS != "darwin" {
		return nil, nil, nil
	}
	out, err := p.run(ctx, "xcrun", "simctl", "list", "devices", "available", "-j")
	if err != nil {
		return nil, nil, fmt.Errorf("simctl list devices: %w", err)
	}
	var parsed struct {
		Devices map[string][]struct {
			UDID       string `json:"udid"`
			Name       string `json:"name"`
			State      string `json:"state"`
			DeviceType string `json:"deviceTypeIdentifier"`
			LastUsedAt string `json:"lastUsedAt"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, nil, fmt.Errorf("simctl list devices: %w", err)
	}
	bootIDs := map[string]string{}
	known := make(map[string]bool, len(listed))
	for _, d := range listed {
		known[d.ID] = true
	}
	var extra []ports.DeviceInfo
	for runtimeID, sims := range parsed.Devices {
		version, ok := iosRuntimeVersion(runtimeID)
		if !ok {
			continue
		}
		for _, sim := range sims {
			bootIDs[sim.UDID] = sim.LastUsedAt
			if !known[sim.UDID] {
				extra = append(extra, ports.DeviceInfo{ID: sim.UDID, Name: sim.Name, Platform: ports.DevicePlatformIOS, Version: version, Booted: sim.State == "Booted", Tablet: tabletModel(sim.DeviceType), BootID: sim.LastUsedAt})
			}
		}
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Name < extra[j].Name })
	return extra, bootIDs, nil
}

// unlistedEmulators returns the Android Virtual Devices the SDK knows and the
// hub does not: like simulators, the hub lists an AVD only after it has booted
// it itself, so one made by Android Studio or `avdmanager` is missing until
// then. A running one is reported by its serial, as the hub does, so it is
// never offered twice.
func (p *Provider) unlistedEmulators(ctx context.Context, listed []ports.DeviceInfo) ([]ports.DeviceInfo, error) {
	sdk := androidSDK()
	if sdk == "" {
		return nil, nil
	}
	emulator := filepath.Join(sdk, "emulator", "emulator")
	if !executable(emulator) {
		return nil, nil
	}
	avds, err := p.run(ctx, emulator, "-list-avds")
	if err != nil {
		return nil, fmt.Errorf("emulator -list-avds: %w", err)
	}
	known := make(map[string]bool, len(listed))
	for _, d := range listed {
		known[d.ID] = true
		known[d.Name] = true
	}
	running := p.runningAVDs(ctx)
	var extra []ports.DeviceInfo
	for _, name := range strings.Fields(string(avds)) {
		if known[name] {
			continue
		}
		device := ports.DeviceInfo{ID: name, Name: name, Platform: ports.DevicePlatformAndroid, Version: avdVersion(name)}
		if serial, ok := running[name]; ok {
			if known[serial] {
				continue
			}
			device.ID, device.Booted = serial, true
		}
		extra = append(extra, device)
	}
	sort.Slice(extra, func(i, j int) bool { return extra[i].Name < extra[j].Name })
	return extra, nil
}

// runningAVDs maps the name of each running emulator's AVD to its serial.
func (p *Provider) runningAVDs(ctx context.Context) map[string]string {
	out, err := p.run(ctx, adbPath(), "devices")
	if err != nil {
		return nil
	}
	running := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[1] != "device" || !strings.HasPrefix(fields[0], "emulator-") {
			continue
		}
		name, err := p.run(ctx, adbPath(), "-s", fields[0], "emu", "avd", "name")
		if err != nil {
			continue
		}
		if first := strings.TrimSpace(strings.SplitN(string(name), "\n", 2)[0]); first != "" {
			running[first] = fields[0]
		}
	}
	return running
}

// avdVersion reads the Android API level an AVD targets ("target=android-35"
// in its .ini) as "API 35"; empty when it cannot.
func avdVersion(name string) string {
	home := os.Getenv("ANDROID_AVD_HOME")
	if home == "" {
		user, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(user, ".android", "avd")
	}
	raw, err := os.ReadFile(filepath.Join(home, name+".ini"))
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if level, ok := strings.CutPrefix(strings.TrimSpace(line), "target=android-"); ok && level != "" {
			return "API " + level
		}
	}
	return ""
}

// iosRuntimeVersion turns "com.apple.CoreSimulator.SimRuntime.iOS-26-5" into
// "iOS 26.5"; other runtimes (tvOS, watchOS, visionOS) report false.
func iosRuntimeVersion(runtimeID string) (string, bool) {
	rest, ok := strings.CutPrefix(runtimeID, "com.apple.CoreSimulator.SimRuntime.iOS-")
	if !ok {
		return "", false
	}
	return "iOS " + strings.ReplaceAll(rest, "-", "."), true
}

type hubActionResult struct {
	OK    bool   `json:"ok"`
	ID    string `json:"id"`
	Error string `json:"error"`
	// Errors explain a failure, and can come with a success too (a boot whose
	// camera feed could not be set up): ok decides, errors only explain.
	Errors hubErrors `json:"errors"`
}

// hubError is one entry of a hub answer's errors: {id, message, error}, where
// message is the readable part and error the underlying detail.
type hubError struct {
	Message string `json:"message"`
	Error   string `json:"error"`
}

// platforms says which platform's listing an error came from. The hub tags
// none, but its Android errors name avdmanager, adb or the emulator and its
// iOS ones simctl or a simulator; one that names neither counts for both.
func (e hubError) platforms() []ports.DevicePlatform {
	text := strings.ToLower(e.Message + " " + e.Error)
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(text, w) {
				return true
			}
		}
		return false
	}
	android := has("android", "avd", "adb", "emulator")
	ios := has("simctl", "simulator", "xcrun", "coresimulator", "apple", "ios")
	switch {
	case android && !ios:
		return []ports.DevicePlatform{ports.DevicePlatformAndroid}
	case ios && !android:
		return []ports.DevicePlatform{ports.DevicePlatformIOS}
	default:
		return []ports.DevicePlatform{ports.DevicePlatformIOS, ports.DevicePlatformAndroid}
	}
}

// hubErrors decodes the hub's error list, also accepting plain strings.
type hubErrors []hubError

func (e *hubErrors) UnmarshalJSON(raw []byte) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	out := make(hubErrors, 0, len(items))
	for _, item := range items {
		var text string
		if json.Unmarshal(item, &text) == nil {
			out = append(out, hubError{Message: text})
			continue
		}
		var entry hubError
		if err := json.Unmarshal(item, &entry); err != nil {
			return err
		}
		out = append(out, entry)
	}
	*e = out
	return nil
}

// String joins each error's message with the first line of its detail: the
// hub fills error with inspect(err), whose first line says what failed (for
// instance "spawn …/avdmanager ENOENT") while the message often stops at a
// colon before it.
func (e hubErrors) String() string {
	parts := make([]string, 0, len(e))
	for _, item := range e {
		msg := strings.TrimSpace(item.Message)
		detail := firstLine(item.Error)
		switch {
		case msg == "":
			if detail != "" {
				parts = append(parts, detail)
			}
		case detail == "" || strings.Contains(msg, detail):
			parts = append(parts, msg)
		case strings.HasSuffix(msg, ":"):
			parts = append(parts, msg+" "+detail)
		default:
			parts = append(parts, msg+": "+detail)
		}
	}
	return strings.Join(parts, "; ")
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func (r hubActionResult) err(action string) error {
	if r.OK {
		return nil
	}
	msg := r.Error
	if why := r.Errors.String(); why != "" {
		if msg == "" {
			msg = why
		} else {
			msg += ": " + why
		}
	}
	if msg == "" {
		msg = "unknown error"
	}
	return fmt.Errorf("%s device: %s", action, msg)
}

// Boot boots through the hub so its device list and the stream helper both
// see the device come up.
func (p *Provider) Boot(ctx context.Context, device ports.DeviceInfo) (ports.DeviceInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, bootTimeout)
	defer cancel()
	// A running emulator is already up: asking the hub to boot it by serial
	// starts its AVD a second time (on another port, which then exits). A
	// stopped one is listed by AVD name and does need the boot.
	if device.Platform == ports.DevicePlatformAndroid && device.Booted {
		return device, nil
	}
	startedHere := !device.Booted
	// A boot that does not finish leaves no expected boot behind, or a later
	// boot outside AO would pass as this one.
	expected, finished := false, false
	defer func() {
		if expected && !finished {
			p.mu.Lock()
			delete(p.ownBoots, device.ID)
			p.mu.Unlock()
		}
	}()
	if device.Platform == ports.DevicePlatformIOS && startedHere {
		// The listed state can be seconds old: a simulator another open just
		// booted, or one started from Xcode, must not be reset (the reset shuts
		// down a running simulator). Ask simctl what it is now.
		if booted, known := p.simulatorBooted(ctx, device.ID); known && booted {
			startedHere = false
			device.Booted = true
		} else {
			// This boot gets a fresh helper below, so it must not count as one
			// that happened behind AO's back (see NoteBooted).
			p.expectBoot(device.ID)
			expected = true
			p.resetSimulatorHelper(ctx, device)
		}
	}
	var res hubActionResult
	body := map[string]string{"platform": string(device.Platform), "id": device.ID, "name": device.Name}
	if err := p.hubJSON(ctx, http.MethodPost, "/api/devices/boot", body, &res); err != nil {
		return device, err
	}
	if err := res.err("boot"); err != nil {
		// An emulator the hub spawned before failing is left running otherwise.
		if device.Platform == ports.DevicePlatformAndroid && startedHere && res.ID != "" {
			stopCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			if stopErr := p.Shutdown(stopCtx, ports.DeviceInfo{ID: res.ID, Name: device.Name, Platform: device.Platform}); stopErr != nil {
				p.log.Warn("shut down an emulator whose boot failed", "device", res.ID, "err", stopErr)
			}
			stop()
		}
		return device, err
	}
	if res.ID != "" {
		device.ID = res.ID
	}
	device.Booted = true
	if device.Platform == ports.DevicePlatformIOS {
		// Booting alone does not attach a stream helper; the grid start does
		// both and is idempotent for a booted simulator.
		var started hubActionResult
		err := p.hubJSON(ctx, http.MethodPost, "/vendor/serve-sim/grid/api/start", map[string]string{"udid": device.ID}, &started)
		if err == nil {
			err = started.err("stream")
		}
		if err != nil {
			// Nobody gets this device, so nothing would ever shut down one this
			// request powered on. One that was running before is left running.
			if startedHere {
				stopCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
				if stopErr := p.Shutdown(stopCtx, device); stopErr != nil {
					p.log.Warn("shut down a simulator whose stream did not start", "device", device.ID, "err", stopErr)
				}
				stop()
				device.Booted = false
			}
			return device, err
		}
		p.awaitLiveStream(ctx, device)
	}
	p.restoreQuality(ctx, device)
	finished = true
	return device, nil
}

// simulatorBooted asks simctl whether a simulator is booted now; known is
// false when simctl cannot say.
func (p *Provider) simulatorBooted(ctx context.Context, udid string) (booted, known bool) {
	// Off macOS xcrun is missing, and the error below reads as unknown.
	out, err := p.run(ctx, "xcrun", "simctl", "list", "devices", "-j")
	if err != nil {
		return false, false
	}
	var parsed struct {
		Devices map[string][]struct {
			UDID  string `json:"udid"`
			State string `json:"state"`
		} `json:"devices"`
	}
	if json.Unmarshal(out, &parsed) != nil {
		return false, false
	}
	for _, sims := range parsed.Devices {
		for _, sim := range sims {
			if sim.UDID == udid {
				return sim.State == "Booted", true
			}
		}
	}
	return false, false
}

// expectBoot marks a simulator this provider is about to boot.
func (p *Provider) expectBoot(udid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.ownBoots == nil {
		p.ownBoots = map[string]bool{}
	}
	p.ownBoots[udid] = true
}

// NoteBooted is told which boot a leased device is on each time the device
// service looks at it. The hub's stream helper for a simulator outlives its
// shutdown and keeps that boot's touch input; resetSimulatorHelper replaces it
// for boots that go through Boot, but a simulator booted any other way (an
// agent's `xcrun simctl boot` or `expo run:ios`, Xcode) got the old helper
// back: video came, and every touch went nowhere until the hub restarted. So
// a running simulator found on a boot this provider did not start gets the
// hub replaced, which gives every simulator a fresh helper; viewers reconnect.
// The first sighting of a simulator only records its boot.
func (p *Provider) NoteBooted(device ports.DeviceInfo) {
	if device.Platform != ports.DevicePlatformIOS || !device.Booted || device.BootID == "" {
		return
	}
	p.mu.Lock()
	if p.bootIDs == nil {
		p.bootIDs = map[string]string{}
	}
	previous, seen := p.bootIDs[device.ID]
	p.bootIDs[device.ID] = device.BootID
	changed := seen && previous != device.BootID
	own := p.ownBoots[device.ID]
	if own && (changed || !seen) {
		delete(p.ownBoots, device.ID)
	}
	replace := changed && !own && p.hub != nil && !p.closed && !p.replacingHub
	if replace {
		p.replacingHub = true
	}
	p.mu.Unlock()
	if replace {
		p.log.Info("simulator booted outside AO; restarting the device hub for fresh touch input", "device", device.ID)
		go p.replaceHub() //nolint:gosec // G118: outlives the request that noticed the boot
	}
}

// restartHub stops the running hub and starts a new one, whose stream helpers
// are fresh. Each device's stream quality is put back by EnsureReady.
func (p *Provider) restartHub() {
	defer func() {
		p.mu.Lock()
		p.replacingHub = false
		p.mu.Unlock()
	}()
	p.mu.Lock()
	hub, closed := p.hub, p.closed
	p.mu.Unlock()
	if hub == nil || closed {
		return
	}
	// Cancelled on purpose: the exit watcher does not revive it.
	hub.cancel()
	select {
	case <-hub.done:
	case <-time.After(5 * time.Second):
	}
	p.mu.Lock()
	if p.hub == hub {
		p.hub = nil
	}
	p.mu.Unlock()
	ctx, cancel := context.WithTimeout(p.life, 2*time.Minute)
	defer cancel()
	if err := p.EnsureReady(ctx); err != nil {
		if errors.Is(err, errProviderClosed) {
			return
		}
		p.log.Warn("restart device hub after an outside boot", "err", err)
		go p.reviveHub()
	}
}

// resetSimulatorHelper drops the hub's stream helper for a simulator that is
// off. The helper outlives a shutdown and keeps the old boot's touch input:
// after the next boot its video came back but every touch went nowhere, until
// the hub restarted. The grid's shutdown disposes it (and shuts down a
// simulator that is already off, which does nothing), so the boot's grid start
// makes a fresh one. Best effort: a failure only costs the old behaviour.
func (p *Provider) resetSimulatorHelper(ctx context.Context, device ports.DeviceInfo) {
	var res hubActionResult
	if err := p.hubJSON(ctx, http.MethodPost, "/vendor/serve-sim/grid/api/shutdown", map[string]string{"udid": device.ID}, &res); err != nil {
		p.log.Debug("reset simulator stream helper", "device", device.ID, "err", err)
		return
	}
	if err := res.err("reset stream helper"); err != nil {
		p.log.Debug("reset simulator stream helper", "device", device.ID, "err", err)
	}
}

// restoreQuality gives a freshly booted device's stream its quality again: a
// boot restarts the hub's stream helper, which comes back at full size (~90
// Mbit/s for a scrolling simulator). Best effort.
func (p *Provider) restoreQuality(ctx context.Context, device ports.DeviceInfo) {
	quality := ports.DeviceQualityHigh
	p.mu.Lock()
	if t, ok := p.tuned[device.ID]; ok {
		quality = t.quality
	}
	p.mu.Unlock()
	if err := p.SetStreamQuality(ctx, device, quality); err != nil {
		p.log.Debug("restore stream quality after boot", "device", device.ID, "err", err)
	}
}

// Shutdown powers the device off through the hub.
func (p *Provider) Shutdown(ctx context.Context, device ports.DeviceInfo) error {
	var res hubActionResult
	body := map[string]string{"platform": string(device.Platform), "id": device.ID, "name": device.Name}
	if err := p.hubJSON(ctx, http.MethodPost, "/api/devices/shutdown", body, &res); err != nil {
		return err
	}
	return res.err("shut down")
}

// Install puts a built .app (iOS) or .apk (Android) on the device.
func (p *Provider) Install(ctx context.Context, device ports.DeviceInfo, artifactPath string) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	switch device.Platform {
	case ports.DevicePlatformIOS:
		return p.runTool(ctx, "xcrun", "simctl", "install", device.ID, artifactPath)
	case ports.DevicePlatformAndroid:
		return p.runTool(ctx, adbPath(), "-s", device.ID, "install", "-r", artifactPath)
	}
	return fmt.Errorf("unsupported platform %q", device.Platform)
}

// Launch starts an installed app by bundle id (iOS) or package (Android).
func (p *Provider) Launch(ctx context.Context, device ports.DeviceInfo, appID string) error {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	switch device.Platform {
	case ports.DevicePlatformIOS:
		// simctl's own error for an app that is not there is an opaque
		// FBSOpenApplicationServiceErrorDomain code; say what is wrong.
		if _, err := p.run(ctx, "xcrun", "simctl", "get_app_container", device.ID, appID); err != nil {
			return fmt.Errorf("%s is not installed on %s", appID, device.Name)
		}
		return p.runTool(ctx, "xcrun", "simctl", "launch", device.ID, appID)
	case ports.DevicePlatformAndroid:
		return p.launchAndroid(ctx, device, appID)
	}
	return fmt.Errorf("unsupported platform %q", device.Platform)
}

// launchAndroid resolves the package's launcher activity and starts it.
// `monkey -p` exits 0 without starting anything for some packages, so the
// activity is named explicitly and the command's own error text is checked.
func (p *Provider) launchAndroid(ctx context.Context, device ports.DeviceInfo, pkg string) error {
	if !androidPackagePattern.MatchString(pkg) {
		return fmt.Errorf("%q is not an Android package name", pkg)
	}
	out, err := p.run(ctx, adbPath(), "-s", device.ID, "shell", "cmd", "package", "resolve-activity", "--brief", "-c", "android.intent.category.LAUNCHER", pkg)
	if err != nil {
		return fmt.Errorf("resolve %s launcher: %w: %s", pkg, err, lastLines(string(out), 3))
	}
	component := ""
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, pkg+"/") {
			component = line
		}
	}
	if component == "" {
		return fmt.Errorf("%s is not installed on %s or has no launcher activity", pkg, device.Name)
	}
	started, err := p.run(ctx, adbPath(), "-s", device.ID, "shell", "am", "start", "-n", component)
	if err != nil {
		return fmt.Errorf("start %s: %w: %s", component, err, lastLines(string(started), 3))
	}
	if strings.Contains(string(started), "Error") {
		return fmt.Errorf("start %s: %s", component, lastLines(string(started), 3))
	}
	return nil
}

// Screenshot returns a PNG of the device screen.
func (p *Provider) Screenshot(ctx context.Context, device ports.DeviceInfo) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	switch device.Platform {
	case ports.DevicePlatformIOS:
		// The hub takes the device as a query parameter and no body.
		return p.hubRaw(ctx, http.MethodPost, "/vendor/serve-sim/api/screenshot?device="+url.QueryEscape(device.ID), nil)
	case ports.DevicePlatformAndroid:
		out, err := p.run(ctx, adbPath(), "-s", device.ID, "exec-out", "screencap", "-p")
		if err != nil {
			return nil, fmt.Errorf("adb screencap: %w", err)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported platform %q", device.Platform)
}

// AgentControl writes the agent-device command bound to this device and
// session. The agent runs it with a subcommand and nothing else: passing the
// platform, device and session flags by hand was the step agents got wrong
// (a shell that does not word-split a flags variable ran the command unbound).
func (p *Provider) AgentControl(device ports.DeviceInfo, sessionID string) (ports.DeviceAgentControl, error) {
	var selector string
	switch device.Platform {
	case ports.DevicePlatformIOS:
		selector = "--udid"
	case ports.DevicePlatformAndroid:
		selector = "--serial"
	default:
		return ports.DeviceAgentControl{}, fmt.Errorf("unsupported platform %q", device.Platform)
	}
	if !safeLeaseToken.MatchString(sessionID) || !safeLeaseToken.MatchString(device.ID) {
		return ports.DeviceAgentControl{}, fmt.Errorf("session or device id %q has characters a lease command cannot carry", sessionID)
	}
	path := filepath.Join(p.stateRoot(), "leases", sessionID, "agent-device")
	if err := writeLeaseWrapper(path, p.ShimPath(), []string{"--platform", string(device.Platform), selector, device.ID, "--session", "ao-" + sessionID}); err != nil {
		return ports.DeviceAgentControl{}, err
	}
	return ports.DeviceAgentControl{Command: path}, nil
}

// RevokeControl replaces the session's bound agent-device command with one
// that refuses: the lease it was written for has ended, and run again it would
// open a new driver session on a device another session may now hold.
func (p *Provider) RevokeControl(sessionID string) error {
	if !safeLeaseToken.MatchString(sessionID) {
		return fmt.Errorf("session id %q has characters a lease command cannot carry", sessionID)
	}
	path := filepath.Join(p.stateRoot(), "leases", sessionID, "agent-device")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return writeRevokedWrapper(path)
}

// ReleaseControl closes agent-device sessions AO opened on this device for
// other AO sessions. agent-device claims a device per session machine-wide,
// so a session that ended without closing would otherwise lock the device
// against the next lease holder. Sessions outside AO's ao- prefix are left
// alone; the agent sees them as DEVICE_IN_USE with the owner named.
func (p *Provider) ReleaseControl(ctx context.Context, device ports.DeviceInfo, keepSessionID string) error {
	shim := p.ShimPath()
	if !executable(shim) {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := p.run(ctx, shim, "session", "list", "--json")
	if err != nil {
		return fmt.Errorf("list agent-device sessions: %w: %s", err, lastLines(string(out), 3))
	}
	var listed struct {
		Data struct {
			Sessions []struct {
				Name string `json:"name"`
				ID   string `json:"id"`
			} `json:"sessions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		return fmt.Errorf("decode agent-device sessions: %w", err)
	}
	keep := ""
	if keepSessionID != "" {
		keep = "ao-" + keepSessionID
	}
	var errs []error
	for _, session := range listed.Data.Sessions {
		if session.ID != device.ID || !strings.HasPrefix(session.Name, "ao-") || session.Name == keep {
			continue
		}
		if closeOut, closeErr := p.run(ctx, shim, "close", "--session", session.Name); closeErr != nil {
			errs = append(errs, fmt.Errorf("close agent-device session %s: %w: %s", session.Name, closeErr, lastLines(string(closeOut), 3)))
		}
	}
	return errors.Join(errs...)
}

// Close stops the hub and the agent-device daemon the agents' commands
// started. Simulators and emulators keep running; the user owns those.
func (p *Provider) Close() error {
	p.stopLife()
	p.mu.Lock()
	p.closed = true
	hub := p.hub
	p.hub = nil
	p.mu.Unlock()
	var err error
	if shim := p.ShimPath(); executable(shim) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		if out, stopErr := p.run(ctx, shim, "daemon", "stop"); stopErr != nil {
			err = fmt.Errorf("stop agent-device daemon: %w: %s", stopErr, lastLines(string(out), 3))
		}
		cancel()
	}
	if hub == nil {
		return err
	}
	hub.cancel()
	select {
	case <-hub.done:
	case <-time.After(5 * time.Second):
	}
	return err
}

func (p *Provider) runTool(ctx context.Context, name string, args ...string) error {
	out, err := p.run(ctx, name, args...)
	if err != nil {
		return fmt.Errorf("%s %s: %w: %s", filepath.Base(name), args[0], err, lastLines(string(out), 4))
	}
	return nil
}

func (p *Provider) hubJSON(ctx context.Context, method, path string, in, out any) error {
	var raw []byte
	if in != nil {
		var err error
		if raw, err = json.Marshal(in); err != nil {
			return err
		}
	}
	body, err := p.hubRaw(ctx, method, path, raw)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode device hub %s: %w", path, err)
	}
	return nil
}

func (p *Provider) hubRaw(ctx context.Context, method, path string, raw []byte) ([]byte, error) {
	endpoint, err := p.Hub(ctx)
	if err != nil {
		return nil, err
	}
	var reader io.Reader
	if raw != nil {
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint.Origin+path, reader)
	if err != nil {
		return nil, err
	}
	for name, value := range endpoint.Headers {
		req.Header.Set(name, value)
	}
	if raw != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("device hub %s: %w", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("read device hub %s: %w", path, err)
	}
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("device hub %s: %s: %s", path, res.Status, lastLines(string(body), 3))
	}
	return body, nil
}

// androidSDK finds the Android SDK: the first candidate that has both adb and
// the emulator (as the hub looks for the tools, not just a folder), else the
// first that exists. adb on PATH counts as a candidate too.
func androidSDK() string {
	candidates := []string{os.Getenv("ANDROID_HOME"), os.Getenv("ANDROID_SDK_ROOT")}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "Library", "Android", "sdk"), filepath.Join(home, "Android", "Sdk"))
	}
	if adb, err := exec.LookPath("adb"); err == nil {
		if resolved, err := filepath.EvalSymlinks(adb); err == nil {
			adb = resolved
		}
		candidates = append(candidates, filepath.Dir(filepath.Dir(adb)))
	}
	existing := ""
	for _, dir := range candidates {
		if dir == "" {
			continue
		}
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		if executable(filepath.Join(dir, "platform-tools", "adb")) && executable(filepath.Join(dir, "emulator", "emulator")) {
			return dir
		}
		if existing == "" {
			existing = dir
		}
	}
	return existing
}

func adbPath() string {
	if sdk := androidSDK(); sdk != "" {
		if path := filepath.Join(sdk, "platform-tools", "adb"); executable(path) {
			return path
		}
	}
	return "adb"
}

func executable(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir() && info.Mode()&0o111 != 0
}

func randomToken() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("mint device hub token: %w", err)
	}
	return hex.EncodeToString(b), nil
}
