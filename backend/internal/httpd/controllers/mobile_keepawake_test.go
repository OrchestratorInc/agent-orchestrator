package controllers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type fakeKeepAwake struct {
	active  bool
	battery bool
	starts  int
}

func (f *fakeKeepAwake) Start() error     { f.active = true; f.starts++; return nil }
func (f *fakeKeepAwake) Stop()            { f.active = false }
func (f *fakeKeepAwake) Active() bool     { return f.active }
func (f *fakeKeepAwake) HasBattery() bool { return f.battery }

func (f *fakeKeepAwake) ConfigureLid(on, _ bool) error {
	if on {
		return errors.New("closed-lid wakefulness unavailable")
	}
	return nil
}
func (f *fakeKeepAwake) LidStatus() (bool, bool, string) { return false, false, "helper_missing" }

func newKeepAwakeBridge(t *testing.T, ka KeepAwakeController) (*BridgeService, *fakeLAN) {
	t.Helper()
	lan := &fakeLAN{}
	return &BridgeService{
		LAN:                lan,
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		KeepAwake:          ka,
	}, lan
}

// Unsupported platforms report nothing to toggle and refuse the switch.
func TestKeepAwakeUnsupportedWithoutController(t *testing.T) {
	b, _ := newKeepAwakeBridge(t, nil)
	if got := b.Status().KeepAwake; got != (KeepAwakeStatus{}) {
		t.Fatalf("KeepAwake = %+v, want zero value", got)
	}
	if _, err := b.SetKeepAwake(true); err == nil {
		t.Fatal("expected SetKeepAwake to fail without a controller")
	}
}

// Turning the option on while the bridge is off only records the choice; the
// assertion is taken when the bridge comes up and released when it goes down.
func TestKeepAwakeFollowsBridge(t *testing.T) {
	ka := &fakeKeepAwake{battery: true}
	b, _ := newKeepAwakeBridge(t, ka)

	res, err := b.SetKeepAwake(true)
	if err != nil {
		t.Fatalf("SetKeepAwake: %v", err)
	}
	if !res.KeepAwake.Supported || !res.KeepAwake.Enabled || res.KeepAwake.Active || !res.KeepAwake.HasBattery {
		t.Fatalf("with bridge off: %+v", res.KeepAwake)
	}

	if _, err := b.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if !ka.active {
		t.Fatal("enable should take the assertion when the option is on")
	}
	// Enable rewrites persisted state; the choice must survive it.
	if st, _ := mobilebridge.Load(b.ConfigPath); !st.KeepAwake {
		t.Fatal("Enable dropped the persisted KeepAwake choice")
	}

	if err := b.Disable(); err != nil {
		t.Fatalf("Disable: %v", err)
	}
	if ka.active {
		t.Fatal("disable should release the assertion")
	}
	if !b.Status().KeepAwake.Enabled {
		t.Fatal("disable should keep the preference")
	}
}

func TestKeepAwakeToggleWhileBridgeRuns(t *testing.T) {
	ka := &fakeKeepAwake{}
	b, _ := newKeepAwakeBridge(t, ka)
	if _, err := b.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if ka.active {
		t.Fatal("option is off by default; enable must not take the assertion")
	}
	if res, _ := b.SetKeepAwake(true); !res.KeepAwake.Active {
		t.Fatal("turning the option on with the bridge up should take the assertion")
	}
	if res, _ := b.SetKeepAwake(false); res.KeepAwake.Active || res.KeepAwake.Enabled {
		t.Fatalf("turning the option off: %+v", res.KeepAwake)
	}
}

func TestKeepAwakeRestoredOnBoot(t *testing.T) {
	ka := &fakeKeepAwake{}
	b, _ := newKeepAwakeBridge(t, ka)
	if err := b.RestoreOnBoot(mobilebridge.State{Enabled: true, Password: "pw", LastPort: 3011, KeepAwake: true}); err != nil {
		t.Fatalf("RestoreOnBoot: %v", err)
	}
	if !ka.active {
		t.Fatal("boot restore should take the assertion when the option was on")
	}
	b.ShutdownKeepAwake()
	if ka.active {
		t.Fatal("shutdown should release the assertion")
	}
}

func TestMobileKeepAwakeRoute(t *testing.T) {
	c := &MobileController{Bridge: &fakeBridge{}}

	w := httptest.NewRecorder()
	c.KeepAwake(w, httptest.NewRequest(http.MethodPost, "/api/v1/mobile/keep-awake", bytes.NewBufferString(`{"enabled":true}`)))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var got MobileStatusResponse
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if !got.KeepAwake.Enabled {
		t.Fatalf("bad response: %+v", got.KeepAwake)
	}

	w = httptest.NewRecorder()
	c.KeepAwake(w, httptest.NewRequest(http.MethodPost, "/api/v1/mobile/keep-awake", bytes.NewBufferString(`not json`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("invalid body: got %d", w.Code)
	}
}

type fakeLidController struct {
	fakeKeepAwake
	supported bool
	lidActive bool
	setups    int
	setupErr  error
	startErr  error
	reason    string
}

func (f *fakeLidController) ConfigureLid(on, setup bool) error {
	if setup {
		f.setups++
		if f.setupErr != nil {
			f.reason = "setup_failed"
			return f.setupErr
		}
	}
	if on && f.startErr != nil {
		f.reason = "setup_failed"
		return f.startErr
	}
	f.lidActive = on
	return nil
}
func (f *fakeLidController) LidStatus() (bool, bool, string) {
	return f.supported, f.lidActive, f.reason
}
func (f *fakeLidController) Stop() { f.fakeKeepAwake.Stop(); f.lidActive = false }

func TestLidAwakeFollowsServingBridgeAndSurvivesLegacyRequests(t *testing.T) {
	ka := &fakeLidController{supported: true}
	b, _ := newKeepAwakeBridge(t, ka)
	res, err := b.SetKeepAwakeLid(true, true)
	if err != nil {
		t.Fatal(err)
	}
	if ka.lidActive || ka.active || !res.KeepAwake.LidClosed || ka.setups != 1 {
		t.Fatalf("bridge off: %+v %+v", res.KeepAwake, ka)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if !ka.active || !ka.lidActive {
		t.Fatal("bridge did not activate both wake controls")
	}
	// A legacy client's boolean-only request must retain the new preference.
	if _, err := b.SetKeepAwake(false); err != nil {
		t.Fatal(err)
	}
	if ka.active || ka.lidActive {
		t.Fatal("off should release both controls")
	}
	if !b.Status().KeepAwake.LidClosed {
		t.Fatal("legacy toggle erased lid preference")
	}
	if _, err := b.SetKeepAwake(true); err != nil {
		t.Fatal(err)
	}
	if !ka.lidActive {
		t.Fatal("legacy re-enable did not restore lid preference")
	}
	if err := b.Disable(); err != nil {
		t.Fatal(err)
	}
	if ka.lidActive {
		t.Fatal("mobile disable did not release lid control")
	}
	if !b.Status().KeepAwake.LidClosed {
		t.Fatal("disable erased preference")
	}
}

func TestLidAwakeUnsupportedDoesNotPersistChoice(t *testing.T) {
	for _, ka := range []KeepAwakeController{&fakeKeepAwake{}, &fakeLidController{supported: false}} {
		b, _ := newKeepAwakeBridge(t, ka)
		if _, err := b.SetKeepAwakeLid(true, true); err == nil {
			t.Fatal("unsupported opt-in accepted")
		}
		if b.Status().KeepAwake.LidClosed {
			t.Fatal("persisted unsupported choice")
		}
	}
}

func TestLidOffPreservesOrdinaryWake(t *testing.T) {
	ka := &fakeLidController{supported: true}
	b, _ := newKeepAwakeBridge(t, ka)
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetKeepAwakeLid(true, true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetKeepAwakeLid(true, false); err != nil {
		t.Fatal(err)
	}
	if !ka.active || ka.lidActive {
		t.Fatal("lid off must retain ordinary idle assertion")
	}
	b.ShutdownKeepAwake()
	if ka.active || ka.lidActive {
		t.Fatal("shutdown must release both")
	}
}

func TestLidRequestRejectsUnavailableBridge(t *testing.T) {
	c := &MobileController{Bridge: &fakeBridge{}}
	w := httptest.NewRecorder()
	c.KeepAwake(w, httptest.NewRequest(http.MethodPost, "/api/v1/mobile/keep-awake", bytes.NewBufferString(`{"enabled":true,"lidClosed":true}`)))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", w.Code)
	}
}

func TestLidAwakeRestoredFromBootStateWithoutSetup(t *testing.T) {
	ka := &fakeLidController{supported: true}
	b, _ := newKeepAwakeBridge(t, ka)
	if err := b.RestoreOnBoot(mobilebridge.State{Enabled: true, Password: "pw", KeepAwake: true, KeepAwakeLid: true}); err != nil {
		t.Fatal(err)
	}
	if !ka.lidActive || !ka.active || ka.setups != 0 {
		t.Fatalf("boot restoration: %+v", ka)
	}
	b.ShutdownKeepAwake()
	if ka.lidActive || ka.active {
		t.Fatal("shutdown left boot-restored controls active")
	}
}

func TestLidOptInWhileIdleWakeOffOnlyRegistersRecovery(t *testing.T) {
	ka := &fakeLidController{supported: true}
	b, _ := newKeepAwakeBridge(t, ka)
	if _, err := b.SetKeepAwakeLid(false, true); err != nil {
		t.Fatal(err)
	}
	if ka.setups != 1 || ka.lidActive || ka.active {
		t.Fatalf("saved opt-in must not acquire: %+v", ka)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if ka.lidActive {
		t.Fatal("mobile access alone must not override ordinary Keep awake preference")
	}
	if _, err := b.SetKeepAwake(true); err != nil {
		t.Fatal(err)
	}
	if !ka.lidActive {
		t.Fatal("saved lid preference did not take effect with idle wake on")
	}
}

type failedStopLAN struct{ *fakeLAN }

func (f *failedStopLAN) Stop(context.Context) error { return errors.New("listener stop failed") }

func TestLidAwakeReleasesOnFailedMobileDisable(t *testing.T) {
	ka := &fakeLidController{supported: true}
	b, lan := newKeepAwakeBridge(t, ka)
	b.LAN = &failedStopLAN{lan}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetKeepAwakeLid(true, true); err != nil {
		t.Fatal(err)
	}
	if err := b.Disable(); err == nil {
		t.Fatal("expected stop failure")
	}
	if !lan.Running() {
		t.Fatal("test must leave listener running")
	}
	if ka.lidActive || ka.active {
		t.Fatal("failed mobile shutdown must still release both wake controls")
	}
	if !b.Status().KeepAwake.LidClosed {
		t.Fatal("failed stop must retain the saved lid preference")
	}
}

func TestLidSetupFailurePreservesPreferences(t *testing.T) {
	for _, on := range []bool{false, true} {
		ka := &fakeLidController{supported: true, setupErr: errors.New("registration failed")}
		b, _ := newKeepAwakeBridge(t, ka)
		if _, err := b.Enable(); err != nil {
			t.Fatal(err)
		}
		if _, err := b.SetKeepAwake(on); err != nil {
			t.Fatal(err)
		}
		if _, err := b.SetKeepAwakeLid(!on, true); err == nil {
			t.Fatal("expected setup failure")
		}
		st, err := mobilebridge.Load(b.ConfigPath)
		if err != nil {
			t.Fatal(err)
		}
		if st.KeepAwake != on || st.KeepAwakeLid || ka.active != on {
			t.Fatalf("failed setup changed preferences: %+v", st)
		}
		b.ShutdownKeepAwake()
	}
}

func TestLidFailureDoesNotFailOrdinaryWake(t *testing.T) {
	for _, restored := range []bool{false, true} {
		ka := &fakeLidController{supported: true, startErr: errors.New("no GUI domain")}
		b, _ := newKeepAwakeBridge(t, ka)
		state := mobilebridge.State{Enabled: true, Password: "pw", KeepAwake: restored, KeepAwakeLid: true}
		if err := mobilebridge.Save(b.ConfigPath, state); err != nil {
			t.Fatal(err)
		}
		if err := b.RestoreOnBoot(state); err != nil {
			t.Fatal(err)
		}
		c := &MobileController{Bridge: b}
		w := httptest.NewRecorder()
		c.KeepAwake(w, httptest.NewRequest(http.MethodPost, "/api/v1/mobile/keep-awake", bytes.NewBufferString(`{"enabled":true}`)))
		if w.Code != http.StatusOK {
			t.Fatalf("ordinary wake failed: %d %s", w.Code, w.Body.String())
		}
		if got := b.Status().KeepAwake; !got.Active || !got.Enabled || got.LidActive || got.LidReason != "setup_failed" {
			t.Fatalf("wrong status: %+v", got)
		}
		b.ShutdownKeepAwake()
	}
}

func TestKeepAwakeLidIsolatedDataDirExplainsRestriction(t *testing.T) {
	ka := &fakeLidController{reason: "isolated_data_dir"}
	b, _ := newKeepAwakeBridge(t, ka)
	if err := mobilebridge.Save(b.ConfigPath, mobilebridge.State{KeepAwake: true}); err != nil {
		t.Fatal(err)
	}
	_, err := b.SetKeepAwakeLid(true, true)
	if err == nil || !strings.Contains(err.Error(), "AO_DATA_DIR") || !strings.Contains(err.Error(), "~/.ao/data") || strings.Contains(err.Error(), "Apple Silicon") {
		t.Fatalf("incorrect custom-data-dir guidance: %v", err)
	}
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil || !st.KeepAwake || st.KeepAwakeLid || ka.setups != 0 {
		t.Fatalf("unsupported request changed preferences or installed helper: state=%+v setups=%d err=%v", st, ka.setups, err)
	}
}
