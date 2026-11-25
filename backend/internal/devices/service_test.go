package devices

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type fakeProvider struct {
	mu        sync.Mutex
	platforms []ports.DevicePlatformAvailability
	devices   []ports.DeviceInfo
	readyErr  error
	booted    []string
	shutdowns []string
	// bootGate, when set, holds Boot until it is closed; bootEntered, when
	// set, hears each Boot as it starts.
	bootGate    chan struct{}
	bootEntered chan string
	// gateDevice, when set, limits bootGate and bootEntered to that device.
	gateDevice string
	// bootID, when set, renames a booted device (an emulator back under a new
	// serial).
	bootID func(ports.DeviceInfo) string
	// releaseGate, when set, holds a full release (no session kept) until closed.
	releaseGate chan struct{}
	installed   []string
	released    []string
	// bound records each agent command written, as session=device; revoked
	// each session whose command was turned off.
	bound   []string
	revoked []string
	// bootErr fails Boot for a device id; controlErr fails AgentControl;
	// shutdownErr fails Shutdown; readyGate holds EnsureReady until closed.
	bootErr     map[string]error
	controlErr  error
	shutdownErr error
	readyGate   chan struct{}
}

func (f *fakeProvider) removeDevice(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.devices[:0]
	for _, d := range f.devices {
		if d.ID != id {
			kept = append(kept, d)
		}
	}
	f.devices = kept
}

func (f *fakeProvider) RevokeControl(session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.revoked = append(f.revoked, session)
	return nil
}

func (f *fakeProvider) Name() string { return "local" }
func (f *fakeProvider) Platforms(context.Context) []ports.DevicePlatformAvailability {
	return f.platforms
}
func (f *fakeProvider) EnsureReady(context.Context) error {
	if f.readyGate != nil {
		<-f.readyGate
	}
	return f.readyErr
}
func (f *fakeProvider) List(context.Context) ([]ports.DeviceInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ports.DeviceInfo(nil), f.devices...), nil
}
func (f *fakeProvider) setBooted(id string, booted bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range f.devices {
		if f.devices[i].ID == id {
			f.devices[i].Booted = booted
		}
	}
}
func (f *fakeProvider) Boot(_ context.Context, d ports.DeviceInfo) (ports.DeviceInfo, error) {
	gated := f.gateDevice == "" || f.gateDevice == d.ID
	if f.bootEntered != nil && gated {
		f.bootEntered <- d.ID
	}
	if f.bootGate != nil && gated {
		<-f.bootGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.bootErr[d.ID]; err != nil {
		return ports.DeviceInfo{}, err
	}
	f.booted = append(f.booted, d.ID)
	if f.bootID != nil {
		d.ID = f.bootID(d)
	}
	d.Booted = true
	return d, nil
}
func (f *fakeProvider) Shutdown(_ context.Context, d ports.DeviceInfo) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.shutdowns = append(f.shutdowns, d.ID)
	return f.shutdownErr
}
func (f *fakeProvider) Install(_ context.Context, _ ports.DeviceInfo, path string) error {
	f.installed = append(f.installed, path)
	return nil
}
func (f *fakeProvider) Launch(context.Context, ports.DeviceInfo, string) error { return nil }
func (f *fakeProvider) Screenshot(context.Context, ports.DeviceInfo) ([]byte, error) {
	return []byte("png"), nil
}
func (f *fakeProvider) Hub(context.Context) (ports.DeviceHubEndpoint, error) {
	return ports.DeviceHubEndpoint{Origin: "http://127.0.0.1:1", Headers: map[string]string{"Authorization": "Bearer t"}}, nil
}
func (f *fakeProvider) AgentControl(d ports.DeviceInfo, session string) (ports.DeviceAgentControl, error) {
	f.mu.Lock()
	f.bound = append(f.bound, session+"="+d.ID)
	controlErr := f.controlErr
	f.mu.Unlock()
	if controlErr != nil {
		return ports.DeviceAgentControl{}, controlErr
	}
	return ports.DeviceAgentControl{Command: "/bin/agent-device", Args: []string{"--udid", d.ID, "--session", "ao-" + session}}, nil
}
func (f *fakeProvider) ReleaseControl(_ context.Context, d ports.DeviceInfo, keep string) error {
	if keep == "" && f.releaseGate != nil {
		<-f.releaseGate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.released = append(f.released, d.ID+" keep="+keep)
	return nil
}
func (f *fakeProvider) Close() error { return nil }

// newAvailable builds a service with devices available, as with Developer mode on.
func newAvailable(cfg Config) *Service {
	svc := New(cfg)
	svc.SetAvailable(true)
	return svc
}

func newTestService(t *testing.T, p *fakeProvider, maxLocal int) *Service {
	t.Helper()
	dir := t.TempDir()
	svc := newAvailable(Config{DataDir: dir, Provider: p, MaxLocalLeases: maxLocal, Now: func() time.Time { return time.Unix(1, 0) }})
	if _, err := svc.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc
}

func iosFleet() *fakeProvider {
	return &fakeProvider{
		platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformIOS, Available: true}, {Platform: ports.DevicePlatformAndroid, Reason: "no sdk"}},
		devices: []ports.DeviceInfo{
			{ID: "A", Name: "iPhone A", Platform: ports.DevicePlatformIOS},
			{ID: "B", Name: "iPhone B", Platform: ports.DevicePlatformIOS, Booted: true},
			{ID: "C", Name: "iPhone C", Platform: ports.DevicePlatformIOS},
		},
	}
}

func apiCode(err error) string {
	var e *apierr.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

func TestOpenPrefersBootedFreeDeviceAndKeepsOnePerSession(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 3)

	first, err := svc.Open(ctx, "s-1", OpenRequest{Platform: ports.DevicePlatformIOS})
	if err != nil {
		t.Fatal(err)
	}
	if first.Device.ID != "A" || first.Provider != "local" || first.Reason == "" {
		t.Fatalf("first lease = %+v, want the shut-down device A (B is booted by someone else) from local with a reason", first)
	}
	if got := first.AgentControl.Args; len(got) == 0 || got[len(got)-1] != "ao-s-1" {
		t.Fatalf("agent control not pinned to the session: %v", got)
	}

	again, err := svc.Open(ctx, "s-1", OpenRequest{})
	if err != nil || again.Device.ID != "A" {
		t.Fatalf("reopen = %+v, %v; want the same device", again, err)
	}

	second, err := svc.Open(ctx, "s-2", OpenRequest{Platform: ports.DevicePlatformIOS})
	if err != nil {
		t.Fatal(err)
	}
	if second.Device.ID == "A" || second.Device.ID == "B" {
		t.Fatalf("second session got %s: the first session's device, or one booted by someone else", second.Device.ID)
	}

	if _, err := svc.Open(ctx, "s-3", OpenRequest{DeviceID: "A"}); apiCode(err) != "DEVICE_IN_USE" {
		t.Fatalf("open leased device err = %v, want DEVICE_IN_USE", err)
	}
}

func TestOpenRespectsLocalCapacity(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, iosFleet(), 1)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{Platform: ports.DevicePlatformIOS}); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Open(ctx, "s-2", OpenRequest{Platform: ports.DevicePlatformIOS})
	if apiCode(err) != "NO_DEVICE_AVAILABLE" {
		t.Fatalf("over-capacity open err = %v, want NO_DEVICE_AVAILABLE", err)
	}
}

func TestOpenExplainsUnsupportedPlatform(t *testing.T) {
	svc := newTestService(t, iosFleet(), 3)
	_, err := svc.Open(context.Background(), "s-1", OpenRequest{Platform: ports.DevicePlatformAndroid})
	if apiCode(err) != "NO_DEVICE_AVAILABLE" {
		t.Fatalf("android open err = %v, want NO_DEVICE_AVAILABLE", err)
	}
}

func TestOpenRequiresOptIn(t *testing.T) {
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: iosFleet()})
	_, err := svc.Open(context.Background(), "s-1", OpenRequest{})
	if apiCode(err) != "DEVICES_DISABLED" {
		t.Fatalf("open before setup err = %v, want DEVICES_DISABLED", err)
	}
	if Enabled(svc.cfg.DataDir) {
		t.Fatal("Enabled before setup")
	}
}

// Devices are a Developer mode preview: until the desktop turns them on, and
// again after it turns them off, every device action is refused, but status
// reads and giving a device back still work and the lease survives.
func TestDevicesNeedDeveloperMode(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := New(Config{DataDir: t.TempDir(), Provider: p, MaxLocalLeases: 3, Now: func() time.Time { return time.Unix(1, 0) }})
	if _, err := svc.Setup(ctx); apiCode(err) != "DEVICES_UNAVAILABLE" {
		t.Fatalf("Setup while unavailable = %v, want DEVICES_UNAVAILABLE", err)
	}
	if Enabled(svc.cfg.DataDir) {
		t.Fatal("the opt-in was recorded while devices were unavailable")
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); apiCode(err) != "DEVICES_UNAVAILABLE" {
		t.Fatalf("Open while unavailable = %v, want DEVICES_UNAVAILABLE", err)
	}
	if st := svc.Status(ctx); st.Available || st.State != SetupOff {
		t.Fatalf("status while unavailable = %+v, want unavailable and off", st)
	}

	svc.SetAvailable(true)
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !svc.Offered() {
		t.Fatal("not offered with devices available and opted in")
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); err != nil {
		t.Fatalf("Open: %v", err)
	}

	svc.SetAvailable(false)
	if svc.Offered() {
		t.Fatal("offered after Developer mode turned off")
	}
	if st := svc.Status(ctx); st.Available || len(st.Leases) != 1 {
		t.Fatalf("status after turning off = %+v, want unavailable with the lease kept", st)
	}
	if _, err := svc.Screenshot(ctx, "s-1"); apiCode(err) != "DEVICES_UNAVAILABLE" {
		t.Fatalf("Screenshot while unavailable = %v, want DEVICES_UNAVAILABLE", err)
	}
	if _, _, _, err := svc.HubTarget(ctx, "s-1"); apiCode(err) != "DEVICES_UNAVAILABLE" {
		t.Fatalf("HubTarget while unavailable = %v, want DEVICES_UNAVAILABLE", err)
	}

	svc.SetAvailable(true)
	if _, err := svc.Screenshot(ctx, "s-1"); err != nil {
		t.Fatalf("Screenshot after turning back on: %v", err)
	}
	svc.SetAvailable(false)
	if err := svc.Close(ctx, "s-1", false); err != nil {
		t.Fatalf("Close while unavailable: %v", err)
	}
	if _, ok := svc.Lease("s-1"); ok {
		t.Fatal("lease kept after Close")
	}
}

// Turning devices off ends a live stream: the hub connection's context is
// cancelled, so a viewer cannot keep driving the device.
func TestTurningDevicesOffEndsStreams(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, iosFleet(), 3)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); err != nil {
		t.Fatal(err)
	}
	_, _, watch, err := svc.HubTarget(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	svc.SetAvailable(false)
	select {
	case <-watch.Done():
	default:
		t.Fatal("stream still open after devices turned off")
	}
}

// The provider starts on boot only once devices are available: a machine with
// Developer mode off never runs the device tools.
func TestProviderStartsWhenAvailable(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	first := newAvailable(Config{DataDir: dir, Provider: iosFleet()})
	if _, err := first.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	svc := New(Config{DataDir: dir, Provider: iosFleet()})
	if st := svc.Status(ctx); st.State != SetupOff {
		t.Fatalf("state on boot = %q, want off until devices are available", st.State)
	}
	svc.SetAvailable(true)
	if st := svc.Status(ctx); st.State == SetupOff {
		t.Fatal("provider did not start when devices became available")
	}
}

func TestSetupFailureIsReportedAndRetried(t *testing.T) {
	p := iosFleet()
	p.readyErr = errors.New("npm missing")
	svc := newTestService(t, p, 3)
	_, err := svc.Open(context.Background(), "s-1", OpenRequest{})
	if apiCode(err) != "DEVICES_SETUP_FAILED" {
		t.Fatalf("open after failed setup err = %v, want DEVICES_SETUP_FAILED", err)
	}
	p.readyErr = nil
	if _, err := svc.Open(context.Background(), "s-1", OpenRequest{}); err != nil {
		t.Fatalf("open after fixing setup: %v", err)
	}
}

func TestEndedSessionsAndCloseFreeTheDevice(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	var mu sync.Mutex
	terminated := map[domain.SessionID]bool{}
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: p, Sessions: func(_ context.Context, id domain.SessionID) (SessionInfo, error) {
		mu.Lock()
		defer mu.Unlock()
		return SessionInfo{Terminated: terminated[id]}, nil
	}})
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	lease, err := svc.Open(ctx, "s-1", OpenRequest{Platform: ports.DevicePlatformIOS})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	terminated["s-1"] = true
	mu.Unlock()
	if status := svc.Status(ctx); len(status.Leases) != 0 {
		t.Fatalf("terminated session still holds %v", status.Leases)
	}
	if len(p.shutdowns) != 0 {
		t.Fatal("freeing an ended session's lease shut the device down")
	}
	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: lease.Device.ID}); err != nil {
		t.Fatalf("released device not reusable: %v", err)
	}
	if err := svc.Close(ctx, "s-2", true); err != nil {
		t.Fatal(err)
	}
	if len(p.shutdowns) != 1 || p.shutdowns[0] != lease.Device.ID {
		t.Fatalf("shutdowns = %v, want [%s]", p.shutdowns, lease.Device.ID)
	}
}

func TestInstallValidatesPathAndLease(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 3)
	if _, err := svc.Install(ctx, "s-1", "/tmp/x.app"); apiCode(err) != "NO_DEVICE_LEASE" {
		t.Fatalf("install without lease err = %v, want NO_DEVICE_LEASE", err)
	}
	if _, err := svc.Open(ctx, domain.SessionID("s-1"), OpenRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(ctx, "s-1", "relative.app"); apiCode(err) != "ARTIFACT_PATH_INVALID" {
		t.Fatalf("relative install err = %v, want ARTIFACT_PATH_INVALID", err)
	}
	app := filepath.Join(t.TempDir(), "App.app")
	if err := os.Mkdir(app, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Install(ctx, "s-1", app); err != nil {
		t.Fatal(err)
	}
	if len(p.installed) != 1 || p.installed[0] != app {
		t.Fatalf("installed = %v", p.installed)
	}
}

func TestInstallRejectsAnotherPlatformsBuild(t *testing.T) {
	dir := t.TempDir()
	touch := func(name string) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	iphone := ports.DeviceInfo{Name: "iPhone 17", Platform: ports.DevicePlatformIOS}
	pixel := ports.DeviceInfo{Name: "Pixel 9", Platform: ports.DevicePlatformAndroid}
	for _, tc := range []struct {
		device ports.DeviceInfo
		path   string
		isDir  bool
		want   string
	}{
		{iphone, touch("app-release.apk"), false, "Android build"},
		{iphone, touch("App.ipa"), false, "physical iPhones"},
		{iphone, touch("notes.txt"), false, "not an iOS Simulator app"},
		{pixel, filepath.Join(dir, "App.app"), true, "iOS build"},
		{pixel, touch("app.aab"), false, "app bundle"},
	} {
		if got := artifactMismatch(tc.device, tc.path, tc.isDir); !strings.Contains(got, tc.want) {
			t.Errorf("%s on %s: %q, want it to mention %q", filepath.Base(tc.path), tc.device.Name, got, tc.want)
		}
	}
	if got := artifactMismatch(iphone, filepath.Join(dir, "App.app"), true); got != "" {
		t.Errorf("a simulator .app was refused: %q", got)
	}
	if got := artifactMismatch(pixel, touch("app.apk"), false); got != "" {
		t.Errorf("an .apk was refused: %q", got)
	}
}

func TestOpenCloseAndEndedSessionsReleaseAgentClaims(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	ended := false
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: p, Sessions: func(_ context.Context, id domain.SessionID) (SessionInfo, error) {
		return SessionInfo{Terminated: id == "s-1" && ended}, nil
	}})
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "B"}); err != nil {
		t.Fatal(err)
	}
	ended = true
	svc.Status(ctx)
	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "B"}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(ctx, "s-2", false); err != nil {
		t.Fatal(err)
	}
	want := []string{"B keep=s-1", "B keep=", "B keep=s-2", "B keep="}
	if len(p.released) != len(want) {
		t.Fatalf("released = %v, want %v", p.released, want)
	}
	for i := range want {
		if p.released[i] != want[i] {
			t.Fatalf("released = %v, want %v", p.released, want)
		}
	}
}

func TestReusesOwnedDeviceBeforeBootingAnother(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 3)
	first, err := svc.Open(ctx, "s-1", OpenRequest{Platform: ports.DevicePlatformIOS})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(ctx, "s-1", false); err != nil {
		t.Fatal(err)
	}
	// The device stays booted after its session ends; the next session reuses
	// it rather than booting a third while B belongs to someone else.
	for i := range p.devices {
		if p.devices[i].ID == first.Device.ID {
			p.devices[i].Booted = true
		}
	}
	second, err := svc.Open(ctx, "s-2", OpenRequest{Platform: ports.DevicePlatformIOS})
	if err != nil {
		t.Fatal(err)
	}
	if second.Device.ID != first.Device.ID {
		t.Fatalf("second lease = %s, want the device this service already booted (%s)", second.Device.ID, first.Device.ID)
	}
}

type pressingProvider struct {
	*fakeProvider
	pressed []string
	err     error
}

func (p *pressingProvider) PressButton(_ context.Context, d ports.DeviceInfo, session, button string) error {
	p.pressed = append(p.pressed, d.ID+" "+session+" "+button)
	return p.err
}

func newServiceWith(t *testing.T, p ports.DeviceProvider) *Service {
	t.Helper()
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: p, MaxLocalLeases: 3, Now: func() time.Time { return time.Unix(1, 0) }})
	if _, err := svc.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc
}

func TestPressButton(t *testing.T) {
	ctx := context.Background()
	newBase := func() *fakeProvider {
		return &fakeProvider{devices: []ports.DeviceInfo{{ID: "UDID-1", Name: "iPhone", Platform: ports.DevicePlatformIOS, Booted: true}}, platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformIOS, Available: true}}}
	}
	p := &pressingProvider{fakeProvider: newBase()}
	svc := newServiceWith(t, p)

	if err := svc.PressButton(ctx, "aa-1", "recents"); err == nil {
		t.Fatal("pressed a button with no lease")
	}
	if _, err := svc.Open(ctx, "aa-1", OpenRequest{Platform: ports.DevicePlatformIOS}); err != nil {
		t.Fatal(err)
	}
	if err := svc.PressButton(ctx, "aa-1", "volume"); err == nil {
		t.Fatal("accepted an unknown button")
	}
	if err := svc.PressButton(ctx, "aa-1", "recents"); err != nil {
		t.Fatal(err)
	}
	if len(p.pressed) != 1 || p.pressed[0] != "UDID-1 aa-1 recents" {
		t.Fatalf("pressed = %v", p.pressed)
	}
	p.err = ports.ErrDeviceButtonUnsupported
	var apiErr *apierr.Error
	if err := svc.PressButton(ctx, "aa-1", "home"); !errors.As(err, &apiErr) || apiErr.Code != "DEVICE_BUTTON_UNSUPPORTED" {
		t.Fatalf("unsupported button = %v, want DEVICE_BUTTON_UNSUPPORTED so the caller uses the socket", err)
	}

	// A provider without the capability leaves every button to the socket.
	plain := newServiceWith(t, newBase())
	if _, err := plain.Open(ctx, "aa-2", OpenRequest{Platform: ports.DevicePlatformIOS}); err != nil {
		t.Fatal(err)
	}
	if err := plain.PressButton(ctx, "aa-2", "home"); !errors.As(err, &apiErr) || apiErr.Code != "DEVICE_BUTTON_UNSUPPORTED" {
		t.Fatalf("plain provider = %v", err)
	}
}

type tuningProvider struct {
	*fakeProvider
	mu    sync.Mutex
	calls []string
}

func (p *tuningProvider) SetStreamQuality(_ context.Context, d ports.DeviceInfo, q ports.DeviceStreamQuality) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, d.ID+" "+string(q))
	return nil
}

func (p *tuningProvider) seen() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func TestSetQualityAppliesAndResetsWhenTheLeaseEnds(t *testing.T) {
	ctx := context.Background()
	base := &fakeProvider{devices: []ports.DeviceInfo{{ID: "UDID-1", Name: "iPhone", Platform: ports.DevicePlatformIOS, Booted: true}}, platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformIOS, Available: true}}}
	p := &tuningProvider{fakeProvider: base}
	svc := newServiceWith(t, p)

	if err := svc.SetQuality(ctx, "aa-1", ports.DeviceQualityLow); err == nil {
		t.Fatal("tuned a device the session does not hold")
	}
	if _, err := svc.Open(ctx, "aa-1", OpenRequest{Platform: ports.DevicePlatformIOS}); err != nil {
		t.Fatal(err)
	}
	if err := svc.SetQuality(ctx, "aa-1", "turbo"); err == nil {
		t.Fatal("accepted an unknown quality")
	}
	if err := svc.SetQuality(ctx, "aa-1", ports.DeviceQualityLow); err != nil {
		t.Fatal(err)
	}
	if err := svc.Close(ctx, "aa-1", false); err != nil {
		t.Fatal(err)
	}
	if got := p.seen(); len(got) != 2 || got[0] != "UDID-1 low" || got[1] != "UDID-1 high" {
		t.Fatalf("tuned %v, want low while leased and high once released", got)
	}

	// A provider without a tuner says so instead of pretending.
	plain := newServiceWith(t, &fakeProvider{devices: base.devices, platforms: base.platforms})
	if _, err := plain.Open(ctx, "aa-2", OpenRequest{Platform: ports.DevicePlatformIOS}); err != nil {
		t.Fatal(err)
	}
	var apiErr *apierr.Error
	if err := plain.SetQuality(ctx, "aa-2", ports.DeviceQualityLow); !errors.As(err, &apiErr) || apiErr.Code != "DEVICE_QUALITY_UNSUPPORTED" {
		t.Fatalf("plain provider = %v", err)
	}
}

// A device shut down outside AO reads as off on the session's lease, so the
// Device tab and the phone can say so instead of holding the last frame.
func TestLiveLeaseReportsADeviceShutDownOutsideAO(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	now := time.Unix(100, 0)
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: p, Now: func() time.Time { return now }})
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	lease, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"})
	if err != nil {
		t.Fatal(err)
	}
	p.setBooted(lease.Device.ID, true)
	if live, _ := svc.LiveLease(ctx, "s-1"); !live.Device.Booted {
		t.Fatal("a running device reads as off")
	}
	p.setBooted(lease.Device.ID, false)
	now = now.Add(liveListTTL + time.Second)
	if live, _ := svc.LiveLease(ctx, "s-1"); live.Device.Booted {
		t.Fatal("a device shut down outside AO still reads as running")
	}
	if _, ok := svc.LiveLease(ctx, "s-2"); ok {
		t.Fatal("a session without a device has a live lease")
	}
}

// While AO boots a device that was off, its lease reads as off: the stream is
// only ready when the boot returns, and a viewer that connected earlier got a
// black picture. Reopening a running device never reads as off.
func TestLiveLeaseReadsOffUntilTheBootReturns(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	gate := make(chan struct{})
	p.bootGate = gate
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: p})
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); done <- err }()
	waitFor(t, func() bool {
		svc.mu.Lock()
		defer svc.mu.Unlock()
		return svc.booting[deviceKey(ports.DeviceInfo{ID: "A", Platform: ports.DevicePlatformIOS})]
	})
	p.setBooted("A", true) // the simulator says booted as soon as it starts
	svc.mu.Lock()
	svc.leases["s-1"] = Lease{SessionID: "s-1", Device: ports.DeviceInfo{ID: "A", Platform: ports.DevicePlatformIOS}}
	svc.mu.Unlock()
	if live, _ := svc.LiveLease(ctx, "s-1"); live.Device.Booted {
		t.Fatal("a device still booting reads as running")
	}
	close(gate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if live, _ := svc.LiveLease(ctx, "s-1"); !live.Device.Booted {
		t.Fatal("a booted device reads as off")
	}
	// Reopening the running device does not mark it as starting.
	p.bootGate = make(chan struct{})
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{}); done <- err }()
	time.Sleep(50 * time.Millisecond)
	svc.mu.Lock()
	marked := svc.booting[deviceKey(ports.DeviceInfo{ID: "A", Platform: ports.DevicePlatformIOS})]
	svc.mu.Unlock()
	close(p.bootGate)
	<-done
	if marked {
		t.Fatal("reopening a running device marked it as starting")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for i := 0; i < 200; i++ {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition never held")
}

// An emulator that stops is listed by its AVD name instead of the serial it
// ran under: its lease still reads as off, and reopening boots it by name.
func TestStoppedEmulatorReadsOffAndReopensByName(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{
		platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformAndroid, Available: true}},
		devices:   []ports.DeviceInfo{{ID: "emulator-5554", Name: "Pixel_9", Platform: ports.DevicePlatformAndroid, Booted: true}},
	}
	now := time.Unix(100, 0)
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: p, Now: func() time.Time { return now }})
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "emulator-5554"}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.devices = []ports.DeviceInfo{{ID: "Pixel_9", Name: "Pixel_9", Platform: ports.DevicePlatformAndroid}}
	p.booted = nil
	p.mu.Unlock()
	now = now.Add(liveListTTL + time.Second)
	if live, _ := svc.LiveLease(ctx, "s-1"); live.Device.Booted {
		t.Fatal("a stopped emulator still reads as running")
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.booted) != 1 || p.booted[0] != "Pixel_9" {
		t.Fatalf("reopen booted %v, want the AVD name Pixel_9", p.booted)
	}
}

func TestLiveLeaseReleasesAnEndedSessionsDevice(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	ended := false
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: p, Sessions: func(context.Context, domain.SessionID) (SessionInfo, error) {
		return SessionInfo{Terminated: ended}, nil
	}})
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := svc.LiveLease(ctx, "s-1"); !ok {
		t.Fatal("a live session lost its device")
	}
	ended = true
	if _, ok := svc.LiveLease(ctx, "s-1"); ok {
		t.Fatal("an ended session's viewer still reads its device")
	}
	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatalf("the ended session's device was not freed: %v", err)
	}
}

// A machine with neither Xcode nor the Android SDK has nothing to run: turning
// device support on says what to install instead of downloading the tools, and
// does not record the opt-in.
func TestSetupRefusesWhenNothingCanRun(t *testing.T) {
	p := &fakeProvider{platforms: []ports.DevicePlatformAvailability{
		{Platform: ports.DevicePlatformIOS, Reason: "Install Xcode."},
		{Platform: ports.DevicePlatformAndroid, Reason: "Install the Android SDK."},
	}}
	dir := t.TempDir()
	svc := newAvailable(Config{DataDir: dir, Provider: p, MaxLocalLeases: 1, Now: func() time.Time { return time.Unix(1, 0) }})
	_, err := svc.Setup(context.Background())
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "NO_DEVICE_PLATFORM" {
		t.Fatalf("Setup = %v, want NO_DEVICE_PLATFORM", err)
	}
	if !strings.Contains(apiErr.Message, "iOS: Install Xcode.") || !strings.Contains(apiErr.Message, "Android: Install the Android SDK.") {
		t.Fatalf("message %q does not say what to install", apiErr.Message)
	}
	if Enabled(dir) {
		t.Fatal("the opt-in was recorded although nothing can run")
	}
	if st := svc.Status(context.Background()); st.State != SetupOff {
		t.Fatalf("state = %q, want off (no tools downloaded)", st.State)
	}
	// One platform is enough.
	p.platforms[1].Available = true
	if _, err := svc.Setup(context.Background()); err != nil {
		t.Fatalf("Setup with Android available = %v", err)
	}
}

// Left to choose, an agent gets a phone on the newest OS: an app is mostly
// built for one, and an iPad or an old runtime is a surprise. The ladder still
// comes first (one this service booted, then one that is off).
func TestPickDevicePrefersAPhoneOnTheNewestOS(t *testing.T) {
	ios := func(id, version string, tablet, booted bool) ports.DeviceInfo {
		return ports.DeviceInfo{ID: id, Name: id, Platform: ports.DevicePlatformIOS, Version: version, Tablet: tablet, Booted: booted}
	}
	devices := []ports.DeviceInfo{
		ios("ipad-new", "iOS 26.5", true, false),
		ios("iphone-old", "iOS 18.2", false, false),
		ios("iphone-new", "iOS 26.5", false, false),
		ios("iphone-mid", "iOS 26.1", false, false),
	}
	got, err := pickDevice(devices, ports.DevicePlatformIOS, "", nil, nil, nil)
	if err != nil || got.ID != "iphone-new" {
		t.Fatalf("picked %q, %v; want the iPhone on iOS 26.5", got.ID, err)
	}
	// Taken: the next newest phone, still not the iPad.
	got, _ = pickDevice(devices, ports.DevicePlatformIOS, "", map[string]domain.SessionID{deviceKey(ios("iphone-new", "", false, false)): "other"}, nil, nil)
	if got.ID != "iphone-mid" {
		t.Fatalf("picked %q, want iphone-mid", got.ID)
	}
	// Only tablets left: a tablet still beats nothing.
	got, _ = pickDevice(devices[:1], ports.DevicePlatformIOS, "", nil, nil, nil)
	if got.ID != "ipad-new" {
		t.Fatalf("picked %q, want the iPad when it is all there is", got.ID)
	}
	// The ladder first: an old iPhone this service booted beats a newer one that is off.
	withOwned := append([]ports.DeviceInfo{ios("iphone-ours", "iOS 17.0", false, true)}, devices...)
	got, _ = pickDevice(withOwned, ports.DevicePlatformIOS, "", nil, map[string]bool{deviceKey(ios("iphone-ours", "", false, true)): true}, nil)
	if got.ID != "iphone-ours" {
		t.Fatalf("picked %q, want the running simulator this service booted", got.ID)
	}
	// A named device is taken as named.
	got, _ = pickDevice(devices, ports.DevicePlatformIOS, "ipad-new", nil, nil, nil)
	if got.ID != "ipad-new" {
		t.Fatalf("picked %q, want the named iPad", got.ID)
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"iOS 26.5", "iOS 26.1", true},
		{"iOS 26.1", "iOS 26.5", false},
		{"iOS 26.0.1", "iOS 26.0", true},
		{"Android 15.0", "Android 9.0", true},
		{"iOS 18.2", "", true},
		{"", "iOS 18.2", false},
	} {
		if got := newerVersion(c.a, c.b); got != c.want {
			t.Errorf("newerVersion(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// A reopen waits on a boot; if the lease is closed meanwhile (and the device
// handed to someone else), the reopen must not bring the old lease back.
func TestReopenDoesNotBringBackALeaseClosedDuringItsBoot(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 3)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	p.setBooted("A", false) // shut down outside AO: the reopen boots it
	p.bootGate, p.bootEntered = make(chan struct{}), make(chan string, 1)
	done := make(chan error, 1)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{}); done <- err }()
	<-p.bootEntered
	if err := svc.Close(ctx, "s-1", false); err != nil {
		t.Fatal(err)
	}
	close(p.bootGate)
	if err := <-done; apiCode(err) != "DEVICE_LEASE_CHANGED" {
		t.Fatalf("reopen = %v, want DEVICE_LEASE_CHANGED", err)
	}
	if _, ok := svc.Lease("s-1"); ok {
		t.Fatal("a closed lease came back")
	}
}

// Two opens can both see a free slot before they boot; only one may commit.
func TestConcurrentOpensRespectTheCap(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 1)
	p.bootGate, p.bootEntered = make(chan struct{}), make(chan string, 2)
	results := make(chan error, 2)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); results <- err }()
	go func() { _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "C"}); results <- err }()
	// One open reserves the only slot and boots; the other is refused before
	// it starts a device it could never keep.
	first := <-p.bootEntered
	if err := <-results; apiCode(err) != "NO_DEVICE_AVAILABLE" {
		t.Fatalf("the losing open = %v, want NO_DEVICE_AVAILABLE", err)
	}
	close(p.bootGate)
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	booted, shutdowns := append([]string(nil), p.booted...), append([]string(nil), p.shutdowns...)
	p.mu.Unlock()
	if len(booted) != 1 || booted[0] != first || len(shutdowns) != 0 || len(svc.Status(ctx).Leases) != 1 {
		t.Fatalf("booted=%v shutdowns=%v leases=%d, want only the winner's device started and leased", booted, shutdowns, len(svc.Status(ctx).Leases))
	}
}

// While a released device's driver claims are still being closed (or it is
// shutting down), nobody else may take it: the cleanup would hit them.
func TestDeviceStaysReservedWhileItIsReleased(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 3)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	p.releaseGate = make(chan struct{})
	closed := make(chan error, 1)
	go func() { closed <- svc.Close(ctx, "s-1", true) }()
	deadline := time.Now().Add(2 * time.Second)
	var err error
	for time.Now().Before(deadline) {
		if _, err = svc.Open(ctx, "s-2", OpenRequest{DeviceID: "A"}); apiCode(err) == "DEVICE_IN_USE" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if apiCode(err) != "DEVICE_IN_USE" {
		t.Fatalf("open during release = %v, want DEVICE_IN_USE", err)
	}
	if got, _ := svc.Open(ctx, "s-3", OpenRequest{Platform: ports.DevicePlatformIOS}); got.Device.ID == "A" {
		t.Fatal("an automatic pick took the device being released")
	}
	close(p.releaseGate)
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatalf("open after the release finished = %v", err)
	}
}

// A lookup that fails for any reason but "no such session" proves nothing:
// the lease stays. Not found, or terminated, frees it.
func TestLeaseSurvivesAFailedSessionLookup(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	var lookupErr error
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: p, MaxLocalLeases: 3, Now: func() time.Time { return time.Unix(1, 0) },
		Sessions: func(context.Context, domain.SessionID) (SessionInfo, error) { return SessionInfo{}, lookupErr }})
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	lookupErr = errors.New("database is locked")
	svc.Status(ctx)
	if _, ok := svc.LiveLease(ctx, "s-1"); !ok {
		t.Fatal("a transient lookup error released the lease")
	}
	lookupErr = apierr.NotFound("SESSION_NOT_FOUND", "Unknown session")
	if _, ok := svc.LiveLease(ctx, "s-1"); ok {
		t.Fatal("a session that no longer exists kept its device")
	}
}

// An emulator restarted under a new serial: the reopened lease, and the agent
// command bound to it, follow the new serial.
func TestReopenRebindsAnEmulatorWithANewSerial(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{
		platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformAndroid, Available: true}},
		devices:   []ports.DeviceInfo{{ID: "emulator-5554", Name: "pixel", Platform: ports.DevicePlatformAndroid, Booted: true}},
	}
	svc := newTestService(t, p, 3)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.devices = []ports.DeviceInfo{{ID: "pixel", Name: "pixel", Platform: ports.DevicePlatformAndroid}}
	p.bootID = func(ports.DeviceInfo) string { return "emulator-5556" }
	p.mu.Unlock()
	lease, err := svc.Open(ctx, "s-1", OpenRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Device.ID != "emulator-5556" || !strings.Contains(strings.Join(lease.AgentControl.Args, " "), "emulator-5556") {
		t.Fatalf("lease = %+v, want the device and its agent command on emulator-5556", lease)
	}
}

// Hub connections live as long as the lease they were opened under.
func TestHubTargetEndsWithTheLease(t *testing.T) {
	ctx := context.Background()
	svc := newTestService(t, iosFleet(), 3)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	_, _, watch, err := svc.HubTarget(ctx, "s-1")
	if err != nil {
		t.Fatal(err)
	}
	if watch.Err() != nil {
		t.Fatal("the lease's connections ended while it was held")
	}
	if err := svc.Close(ctx, "s-1", false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watch.Done():
	case <-time.After(time.Second):
		t.Fatal("releasing the device left its hub connections open")
	}
}

// A reopen that finishes after its lease changed must not rewrite the
// session's agent command: it would point a newer lease's command at the old
// device.
func TestStaleReopenDoesNotRebindTheAgentCommand(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{
		platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformAndroid, Available: true}},
		devices: []ports.DeviceInfo{
			{ID: "emulator-5554", Name: "pixel", Platform: ports.DevicePlatformAndroid, Booted: true},
			{ID: "emulator-5560", Name: "tablet", Platform: ports.DevicePlatformAndroid, Booted: true},
		},
	}
	svc := newTestService(t, p, 3)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "emulator-5554"}); err != nil {
		t.Fatal(err)
	}
	// The emulator stopped; reopening boots it back under a new serial, slowly.
	p.mu.Lock()
	p.devices[0] = ports.DeviceInfo{ID: "pixel", Name: "pixel", Platform: ports.DevicePlatformAndroid}
	p.bootID = func(ports.DeviceInfo) string { return "emulator-5556" }
	p.mu.Unlock()
	p.gateDevice, p.bootGate, p.bootEntered = "pixel", make(chan struct{}), make(chan string, 1)
	stale := make(chan error, 1)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{}); stale <- err }()
	<-p.bootEntered
	// Meanwhile the session moves to the other device.
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "emulator-5560"}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	before := len(p.bound)
	p.mu.Unlock()
	close(p.bootGate)
	if err := <-stale; apiCode(err) != "DEVICE_LEASE_CHANGED" {
		t.Fatalf("stale reopen = %v, want DEVICE_LEASE_CHANGED", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.bound) != before {
		t.Fatalf("the stale reopen rebound the command: %v", p.bound[before:])
	}
}

// A session whose lease ended loses its agent command; one that moved to
// another device keeps the new device's command.
func TestEndingALeaseRevokesTheAgentCommand(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 3)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "C"}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	moved := len(p.revoked)
	p.mu.Unlock()
	if moved != 0 {
		t.Fatalf("moving to another device revoked the new command: %v", p.revoked)
	}
	if err := svc.Close(ctx, "s-1", false); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.revoked) != 1 || p.revoked[0] != "s-1" {
		t.Fatalf("revoked = %v, want s-1 once its lease ended", p.revoked)
	}
}

// A viewer still connected to a session that was killed cannot keep reaching
// its device, and the device is freed.
func TestHubTargetRefusesAnEndedSession(t *testing.T) {
	ctx := context.Background()
	var terminated atomic.Bool
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: iosFleet(), MaxLocalLeases: 3, Now: func() time.Time { return time.Unix(1, 0) },
		Sessions: func(context.Context, domain.SessionID) (SessionInfo, error) {
			return SessionInfo{Terminated: terminated.Load()}, nil
		}})
	t.Cleanup(func() { _ = svc.Shutdown() })
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	terminated.Store(true)
	if _, _, _, err := svc.HubTarget(ctx, "s-1"); apiCode(err) != "NO_DEVICE_LEASE" {
		t.Fatalf("hub target for an ended session = %v, want NO_DEVICE_LEASE", err)
	}
	if _, ok := svc.Lease("s-1"); ok {
		t.Fatal("the ended session kept its device")
	}
}

// An ended session frees its device on its own, without anyone asking.
func TestEndedSessionsAreReapedInTheBackground(t *testing.T) {
	previous := reapEvery
	reapEvery = 20 * time.Millisecond
	t.Cleanup(func() { reapEvery = previous })
	ctx := context.Background()
	var terminated atomic.Bool
	svc := newAvailable(Config{DataDir: t.TempDir(), Provider: iosFleet(), MaxLocalLeases: 3, Now: func() time.Time { return time.Unix(1, 0) },
		Sessions: func(context.Context, domain.SessionID) (SessionInfo, error) {
			return SessionInfo{Terminated: terminated.Load()}, nil
		}})
	t.Cleanup(func() { _ = svc.Shutdown() })
	if _, err := svc.Setup(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	terminated.Store(true)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := svc.Lease("s-1"); !ok {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("an ended session's device was never freed")
}

// A device another open is still booting is busy: its listed state is stale,
// and opening it again would reset it.
func TestADeviceBeingBootedIsBusy(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 3)
	p.gateDevice, p.bootGate, p.bootEntered = "A", make(chan struct{}), make(chan string, 1)
	first := make(chan error, 1)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); first <- err }()
	<-p.bootEntered
	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "A"}); apiCode(err) != "DEVICE_IN_USE" {
		t.Fatalf("open of a device being booted = %v, want DEVICE_IN_USE", err)
	}
	close(p.bootGate)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
}

// A simulator deleted after it was leased must not fail every open until a
// close: the lease goes, and an open that names no device gets another.
func TestOpenReplacesALeasedDeviceThatWasDeleted(t *testing.T) {
	p := iosFleet()
	svc := newTestService(t, p, 1)
	ctx := context.Background()
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	p.removeDevice("A")
	p.bootErr = map[string]error{"A": errors.New("Invalid device: A")}

	lease, err := svc.Open(ctx, "s-1", OpenRequest{})
	if err != nil {
		t.Fatalf("reopen after the device was deleted: %v", err)
	}
	if lease.Device.ID == "A" {
		t.Fatalf("lease still on the deleted device: %+v", lease)
	}
	if got, _ := svc.Lease("s-1"); got.Device.ID != lease.Device.ID {
		t.Fatalf("stored lease = %+v, want %s", got, lease.Device.ID)
	}
	if reloaded := newAvailable(Config{DataDir: svc.cfg.DataDir, Provider: p}); reloaded.leases["s-1"].Device.ID != lease.Device.ID {
		t.Fatalf("saved lease = %+v, want %s", reloaded.leases["s-1"], lease.Device.ID)
	}
}

func TestOpenNamingADeletedDeviceSaysItIsGone(t *testing.T) {
	p := iosFleet()
	svc := newTestService(t, p, 3)
	ctx := context.Background()
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	p.removeDevice("A")
	p.bootErr = map[string]error{"A": errors.New("Invalid device: A")}

	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); apiCode(err) != "DEVICE_NOT_FOUND" {
		t.Fatalf("open of a deleted device err = %v, want DEVICE_NOT_FOUND", err)
	}
	if _, held := svc.Lease("s-1"); held {
		t.Fatal("the deleted device's lease was kept")
	}
}

// A device that is still there but fails to boot keeps its lease: the
// failure may pass, and the session's device must not change under it.
func TestOpenKeepsTheLeaseOfAListedDeviceThatFailsToBoot(t *testing.T) {
	p := iosFleet()
	svc := newTestService(t, p, 3)
	ctx := context.Background()
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	p.setBooted("A", false)
	p.bootErr = map[string]error{"A": errors.New("boot timed out")}

	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); apiCode(err) != "DEVICE_BOOT_FAILED" {
		t.Fatalf("reopen err = %v, want DEVICE_BOOT_FAILED", err)
	}
	if l, held := svc.Lease("s-1"); !held || l.Device.ID != "A" {
		t.Fatalf("lease = %+v (held %v), want A kept", l, held)
	}
}

// Failures the spec declares as 503s must not reach the API as raw errors
// (undeclared 500s).
func TestDeviceFailuresAreServiceUnavailable(t *testing.T) {
	ctx := context.Background()
	unavailable := func(t *testing.T, err error, code string) {
		t.Helper()
		var e *apierr.Error
		if !errors.As(err, &e) || e.Code != code || e.Kind != apierr.KindUnavailable {
			t.Fatalf("err = %#v, want unavailable %s", err, code)
		}
	}
	t.Run("agent command", func(t *testing.T) {
		p := iosFleet()
		p.controlErr = errors.New("disk full")
		svc := newTestService(t, p, 3)
		_, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "B"})
		unavailable(t, err, "DEVICE_CONTROL_FAILED")
	})
	t.Run("shutdown", func(t *testing.T) {
		p := iosFleet()
		p.shutdownErr = errors.New("simctl hung")
		svc := newTestService(t, p, 3)
		if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "B"}); err != nil {
			t.Fatal(err)
		}
		unavailable(t, svc.Close(ctx, "s-1", true), "DEVICE_SHUTDOWN_FAILED")
		if _, held := svc.Lease("s-1"); held {
			t.Fatal("lease kept after a failed shutdown")
		}
	})
	t.Run("still starting", func(t *testing.T) {
		p := iosFleet()
		p.readyGate = make(chan struct{})
		t.Cleanup(func() { close(p.readyGate) })
		svc := newTestService(t, p, 3)
		short, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
		defer cancel()
		_, err := svc.Open(short, "s-1", OpenRequest{})
		unavailable(t, err, "DEVICES_NOT_READY")
	})
}

// platformListFails is a provider whose List succeeds while one platform's
// listing failed (simctl erroring while emulators still list).
type platformListFails struct {
	*fakeProvider
	failing ports.DevicePlatform
}

func (f platformListFails) ListPlatform(ctx context.Context, platform ports.DevicePlatform) ([]ports.DeviceInfo, error) {
	if platform == f.failing {
		return nil, errors.New("simctl list failed")
	}
	all, _ := f.List(ctx)
	var out []ports.DeviceInfo
	for _, d := range all {
		if d.Platform == platform {
			out = append(out, d)
		}
	}
	return out, nil
}

// A simulator missing from a list whose iOS half failed may still exist:
// its lease is kept, not dropped as deleted.
func TestOpenKeepsALeaseWhenItsPlatformFailedToList(t *testing.T) {
	p := iosFleet()
	svc := newServiceWith(t, platformListFails{fakeProvider: p, failing: ports.DevicePlatformIOS})
	ctx := context.Background()
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatal(err)
	}
	p.removeDevice("A")
	p.bootErr = map[string]error{"A": errors.New("simctl boot failed")}

	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); apiCode(err) != "DEVICE_BOOT_FAILED" {
		t.Fatalf("reopen err = %v, want DEVICE_BOOT_FAILED", err)
	}
	if l, held := svc.Lease("s-1"); !held || l.Device.ID != "A" {
		t.Fatalf("lease = %+v (held %v), want A kept", l, held)
	}
}

// An emulator's serial is not its identity: a lease taken on emulator-5554
// still holds that AVD after it stops (listed by name) and another emulator
// takes the port, while the other emulator stays free to open.
func TestAndroidLeasesFollowTheAVDNotTheSerial(t *testing.T) {
	ctx := context.Background()
	p := &fakeProvider{
		platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformAndroid, Available: true}},
		devices: []ports.DeviceInfo{
			{ID: "emulator-5554", Name: "Pixel_9", Platform: ports.DevicePlatformAndroid, Booted: true},
			{ID: "Other", Name: "Other", Platform: ports.DevicePlatformAndroid},
		},
	}
	svc := newTestService(t, p, 3)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "emulator-5554"}); err != nil {
		t.Fatal(err)
	}
	// Pixel_9 stopped and is listed by its AVD name; Other now runs as emulator-5554.
	p.mu.Lock()
	p.devices = []ports.DeviceInfo{
		{ID: "Pixel_9", Name: "Pixel_9", Platform: ports.DevicePlatformAndroid},
		{ID: "emulator-5554", Name: "Other", Platform: ports.DevicePlatformAndroid, Booted: true},
	}
	p.mu.Unlock()

	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "Pixel_9"}); apiCode(err) != "DEVICE_IN_USE" {
		t.Fatalf("second session opening the leased AVD = %v, want DEVICE_IN_USE", err)
	}
	lease, err := svc.Open(ctx, "s-2", OpenRequest{Platform: ports.DevicePlatformAndroid})
	if err != nil {
		t.Fatalf("second session opening any Android device: %v", err)
	}
	if lease.Device.Name != "Other" {
		t.Fatalf("second session got %+v, want the other AVD, never Pixel_9", lease.Device)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, id := range p.booted {
		if id == "Pixel_9" {
			t.Fatalf("booted %v: the leased AVD was booted for another session", p.booted)
		}
	}
}

// An open that fails after starting a device gives back its reservation and
// shuts the device down, so it neither runs on for nobody nor blocks the next
// open. A device that was already running is left alone.
func TestAFailedOpenRollsBackTheDeviceItStarted(t *testing.T) {
	ctx := context.Background()
	p := iosFleet()
	svc := newTestService(t, p, 3)
	p.controlErr = errors.New("disk full")
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); apiCode(err) != "DEVICE_CONTROL_FAILED" {
		t.Fatalf("open = %v, want DEVICE_CONTROL_FAILED", err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "B"}); apiCode(err) != "DEVICE_CONTROL_FAILED" {
		t.Fatalf("open of the running device = %v, want DEVICE_CONTROL_FAILED", err)
	}
	p.mu.Lock()
	shutdowns := append([]string(nil), p.shutdowns...)
	p.mu.Unlock()
	if len(shutdowns) != 1 || shutdowns[0] != "A" {
		t.Fatalf("shutdowns = %v, want only A, the device the failed open started", shutdowns)
	}
	svc.mu.Lock()
	reserved, owned := len(svc.reserved), svc.owned[deviceKey(ports.DeviceInfo{ID: "A", Platform: ports.DevicePlatformIOS})]
	svc.mu.Unlock()
	if reserved != 0 || owned {
		t.Fatalf("reserved=%d owned(A)=%v after a failed open, want nothing kept", reserved, owned)
	}
	p.mu.Lock()
	p.controlErr = nil
	p.mu.Unlock()
	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "A"}); err != nil {
		t.Fatalf("the next open of A: %v", err)
	}
}
