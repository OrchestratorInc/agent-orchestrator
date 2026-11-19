package daemon

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// fakeLAN is a minimal httpd.LANController fake for exercising
// restoreMobileOnBoot without a real listener.
type fakeLAN struct {
	started bool
	hash    string
	port    int
	// returnPort, when non-zero, is what Start returns instead of the port it
	// was asked for — simulating LANManager's ephemeral-port fallback when the
	// requested port is already taken (e.g. by another AO instance). Left zero,
	// Start behaves like a normal listener and echoes the requested port back.
	returnPort int
}

func (f *fakeLAN) Start(port int) (int, error) {
	f.started = true
	if f.returnPort != 0 {
		f.port = f.returnPort
		return f.returnPort, nil
	}
	f.port = port
	return port, nil
}
func (f *fakeLAN) StartLoopback(port int) (int, error) {
	return f.Start(port)
}
func (f *fakeLAN) Stop(ctx context.Context) error { return nil }
func (f *fakeLAN) Running() bool                  { return f.started }
func (f *fakeLAN) BoundPort() int                 { return f.port }
func (f *fakeLAN) SetPasswordHash(hash string)    { f.hash = hash }
func (f *fakeLAN) PasswordHash() string           { return f.hash }

func TestRestoreEnabledStartsListener(t *testing.T) {
	dir := t.TempDir()
	path := mobilebridge.Path(dir)
	if err := mobilebridge.Save(path, mobilebridge.State{Enabled: true, Password: "secret12", LastPort: 3011}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	lan := &fakeLAN{}
	bs := &controllers.BridgeService{LAN: lan}
	if err := restoreMobileOnBoot(path, bs); err != nil {
		t.Fatalf("restoreMobileOnBoot: %v", err)
	}
	if !lan.started {
		t.Fatal("expected LAN listener started from persisted enabled state")
	}
	// Restore derives the auth hash from the persisted plaintext password (no
	// rotation), so the fake must have received HashPassword(persisted password).
	if want := mobilebridge.HashPassword("secret12"); lan.hash != want {
		t.Fatalf("expected hash derived from persisted password %q, got %q", want, lan.hash)
	}
	if lan.port != 3011 {
		t.Fatalf("expected persisted port reused, got %d", lan.port)
	}
}

func TestRestoreDisabledDoesNotStart(t *testing.T) {
	dir := t.TempDir()
	path := mobilebridge.Path(dir)
	if err := mobilebridge.Save(path, mobilebridge.State{Enabled: false}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	lan := &fakeLAN{}
	bs := &controllers.BridgeService{LAN: lan}
	if err := restoreMobileOnBoot(path, bs); err != nil {
		t.Fatalf("restoreMobileOnBoot: %v", err)
	}
	if lan.started {
		t.Fatal("expected LAN listener NOT started when persisted state is disabled")
	}
}

type restoreWake struct {
	active, lid bool
	setups      int
}

func (f *restoreWake) Start() error     { f.active = true; return nil }
func (f *restoreWake) Stop()            { f.active, f.lid = false, false }
func (f *restoreWake) Active() bool     { return f.active }
func (f *restoreWake) HasBattery() bool { return true }
func (f *restoreWake) ConfigureLid(on, setup bool) error {
	f.lid = on
	if setup {
		f.setups++
	}
	return nil
}
func (f *restoreWake) LidStatus() (bool, bool, string) { return true, f.lid, "" }

func TestRestoreWakeWithoutEnablingMobile(t *testing.T) {
	for _, lid := range []bool{false, true} {
		path := mobilebridge.Path(t.TempDir())
		state := mobilebridge.State{KeepAwake: true, KeepAwakeLid: lid, SecurePairing: true}
		if err := mobilebridge.Save(path, state); err != nil {
			t.Fatal(err)
		}
		lan, wake := &fakeLAN{}, &restoreWake{}
		bs := &controllers.BridgeService{LAN: lan, KeepAwake: wake,
			ApplyServe: func(int) error { t.Fatal("wake restoration enabled network access"); return nil },
		}
		if err := restoreMobileOnBoot(path, bs); err != nil {
			t.Fatal(err)
		}
		if !wake.active || wake.lid != lid || wake.setups != 0 || lan.started || lan.hash != "" {
			t.Fatalf("wake-only restore: wake=%+v lan=%+v", wake, lan)
		}
		bs.ShutdownKeepAwake()
		if wake.active || wake.lid {
			t.Fatal("daemon shutdown did not release awake ownership")
		}
		persisted, err := mobilebridge.Load(path)
		if err != nil || persisted != state {
			t.Fatalf("shutdown changed saved preferences: %+v, %v", persisted, err)
		}
	}
}

// TestRestoreAppliesServeToStartReturnedPort is the regression test for the
// bridge's named primary failure mode: LANManager.Start falls back to an
// ephemeral port when its persisted default is taken (observed live: asked
// for 3011, got 54014 because another AO instance held 3011). A `tailscale
// serve` config pinned to the stale persisted port is exactly what secure
// pairing exists to prevent — and serve's config persists inside tailscaled
// across AO restarts, so a stale entry silently proxies the tailnet at this
// machine's hostname to whatever now holds that dead port.
//
// fakeLAN.returnPort is deliberately set to diverge from the requested port
// here — do not "simplify" it back to echoing the request, or this test stops
// being able to catch a regression to the old lan.Start(state.LastPort)-then-
// applyServe(state.LastPort) bug.
func TestRestoreAppliesServeToStartReturnedPort(t *testing.T) {
	dir := t.TempDir()
	path := mobilebridge.Path(dir)
	if err := mobilebridge.Save(path, mobilebridge.State{
		Enabled:       true,
		Password:      "secret12",
		LastPort:      3011,
		SecurePairing: true,
	}); err != nil {
		t.Fatalf("save state: %v", err)
	}
	lan := &fakeLAN{returnPort: 54014}
	var appliedPort int
	bs := &controllers.BridgeService{
		LAN: lan,
		ApplyServe: func(port int) error {
			appliedPort = port
			return nil
		},
	}
	if err := restoreMobileOnBoot(path, bs); err != nil {
		t.Fatalf("restoreMobileOnBoot: %v", err)
	}
	if appliedPort != 54014 {
		t.Fatalf("expected ApplyServe called with Start's returned port 54014, got %d (state.LastPort was 3011)", appliedPort)
	}
}
