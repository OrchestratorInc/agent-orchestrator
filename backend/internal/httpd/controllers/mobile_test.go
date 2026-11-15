package controllers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

type fakeBridge struct{ enabled bool }

func (f *fakeBridge) Status() MobileStatusResponse {
	return MobileStatusResponse{Enabled: f.enabled, Host: "192.168.1.42", Port: 3011}
}
func (f *fakeBridge) StartRemoteAccess() (MobileStatusResponse, error) {
	return MobileStatusResponse{}, nil
}

func (f *fakeBridge) Enable() (MobileStatusResponse, error) {
	f.enabled = true
	r := f.Status()
	r.Password = "abcd1234"
	return r, nil
}
func (f *fakeBridge) EnableLANOnly() (MobileStatusResponse, error)    { return f.Enable() }
func (f *fakeBridge) EnableTunnelOnly() (MobileStatusResponse, error) { return f.Enable() }
func (f *fakeBridge) Disable() error                                  { f.enabled = false; return nil }
func (f *fakeBridge) Regenerate() (MobileStatusResponse, error) {
	r := f.Status()
	r.Password = "wxyz5678"
	return r, nil
}
func (f *fakeBridge) SetKeepAwake(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.KeepAwake.Enabled = on
	return r, nil
}
func (f *fakeBridge) SetKeepAwakeLid(bool, bool) (MobileStatusResponse, error) {
	return MobileStatusResponse{}, errors.New("closed-lid wakefulness unavailable")
}
func (f *fakeBridge) SetSecurePairing(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.SecurePairing.Enabled = on
	return r, nil
}
func (f *fakeBridge) SetBrowserRemoteAccess(on bool) (MobileStatusResponse, error) {
	r := f.Status()
	r.BrowserRemoteAccess = on
	return r, nil
}

func TestMobileBrowserRemoteAccess(t *testing.T) {
	c := &MobileController{Bridge: &fakeBridge{}}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mobile/browser-control", strings.NewReader(`{"enabled":true}`))
	rr := httptest.NewRecorder()
	c.BrowserRemoteAccess(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rr.Code, rr.Body.String())
	}
	var got MobileStatusResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.BrowserRemoteAccess {
		t.Fatal("browser remote access was not enabled")
	}
}

type fakeLAN struct {
	running     bool
	hash        string
	accountHash string
	stopCalls   int
	bindHost    string
}

func (f *fakeLAN) Start(port int) (int, error) {
	f.running = true
	f.bindHost = "0.0.0.0"
	return port, nil
}
func (f *fakeLAN) StartLoopback(port int) (int, error) {
	f.running = true
	f.bindHost = "127.0.0.1"
	return port, nil
}
func (f *fakeLAN) Stop(ctx context.Context) error {
	f.stopCalls++
	f.running = false
	return nil
}
func (f *fakeLAN) Running() bool { return f.running }
func (f *fakeLAN) BoundPort() int {
	if !f.running {
		return 0
	}
	return 3011
}
func (f *fakeLAN) SetPasswordHash(h string)     { f.hash = h }
func (f *fakeLAN) PasswordHash() string         { return f.hash }
func (f *fakeLAN) SetAccountTokenHash(h string) { f.accountHash = h }
func (f *fakeLAN) AccountTokenHash() string     { return f.accountHash }

func TestIssueAccountTokenRequiresPairingPasswordAndPersistsHash(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobile", "config.json")
	if err := mobilebridge.Save(path, mobilebridge.State{Enabled: true, Password: "secret12"}); err != nil {
		t.Fatal(err)
	}
	lan := &fakeLAN{running: true}
	bridge := &BridgeService{LAN: lan, ConfigPath: path, HostID: "h_test"}
	if _, err := bridge.IssueAccountToken("wrong", "user-a"); err == nil {
		t.Fatal("wrong password accepted")
	}
	issued, err := bridge.IssueAccountToken("secret12", "user-a")
	if err != nil {
		t.Fatal(err)
	}
	if issued.HostID != "h_test" || len(issued.Token) != 64 {
		t.Fatalf("bad token: %+v", issued)
	}
	if want, err := mobilebridge.HostPublicKey(path); err != nil || issued.HostKey != want || len(want) != 43 {
		t.Fatalf("host key = %q, want %q (%v)", issued.HostKey, want, err)
	}
	state, err := mobilebridge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !mobilebridge.PasswordMatches(state.AccountTokenHash, issued.Token) || state.AccountToken != issued.Token || state.AccountTokenOwner != "user-a" || lan.accountHash != state.AccountTokenHash {
		t.Fatal("account credential was not persisted and armed")
	}
	if state.Password != "secret12" {
		t.Fatal("pairing password changed")
	}
}

func TestPasswordRotationRevokesAccountToken(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobile", "config.json")
	lan := &fakeLAN{}
	bridge := &BridgeService{LAN: lan, ConfigPath: path, HostID: "h_test", DefaultPort: 3011}
	status, err := bridge.Enable()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bridge.IssueAccountToken(status.Password, "user-a"); err != nil {
		t.Fatal(err)
	}
	if lan.accountHash == "" {
		t.Fatal("account token was not armed")
	}
	if _, err := bridge.Regenerate(); err != nil {
		t.Fatal(err)
	}
	state, err := mobilebridge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.AccountTokenHash != "" || lan.accountHash != "" {
		t.Fatal("password rotation left the account token valid")
	}
}

func TestIssueAccountTokenReusesCredentialForSameAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobile", "config.json")
	if err := mobilebridge.Save(path, mobilebridge.State{Enabled: true, Password: "secret12"}); err != nil {
		t.Fatal(err)
	}
	lan := &fakeLAN{running: true}
	bridge := &BridgeService{LAN: lan, ConfigPath: path, HostID: "h_test"}
	first, err := bridge.IssueAccountToken("secret12", "user-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := bridge.IssueAccountToken("secret12", "user-a")
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != first.Token {
		t.Fatalf("same-account retry rotated token: first=%q second=%q", first.Token, second.Token)
	}
	third, err := bridge.IssueAccountToken("secret12", "user-b")
	if err != nil {
		t.Fatal(err)
	}
	if third.Token == first.Token {
		t.Fatal("different account did not rotate token")
	}
	if got, err := bridge.IssueAccountToken(third.Token, "user-b"); err != nil || got.Token != third.Token {
		t.Fatalf("current token retry = %+v, %v; want current credential", got, err)
	}
}

func TestMobileEnableRollsBackListenerWhenSaveFails(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	lan := &fakeLAN{}
	b := &BridgeService{LAN: lan, ConfigPath: filepath.Join(blocker, "mobile", "config.json"), DefaultPort: 3011}

	if _, err := b.Enable(); err == nil {
		t.Fatal("expected enable to fail on Save error")
	}
	if lan.Running() {
		t.Fatal("listener still running after failed enable; must be stopped")
	}
	if lan.stopCalls == 0 {
		t.Fatal("expected Stop to be called on rollback")
	}
	if lan.hash != "" {
		t.Fatalf("expected hash rolled back to empty, got %q", lan.hash)
	}
}

func TestMobileEnableReturnsPassword(t *testing.T) {
	c := &MobileController{Bridge: &fakeBridge{}}
	w := httptest.NewRecorder()
	c.Enable(w, httptest.NewRequest(http.MethodPost, "/api/v1/mobile/enable", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("got %d", w.Code)
	}
	var got MobileStatusResponse
	json.NewDecoder(w.Body).Decode(&got)
	if !got.Enabled || got.Password != "abcd1234" || got.Warning == "" {
		t.Fatalf("bad response: %+v", got)
	}
}

func TestMobileStatusSurfacesBothHosts(t *testing.T) {
	lan := &fakeLAN{running: true}
	b := &BridgeService{
		LAN:                lan,
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return []string{"100.72.46.7"} },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	got := b.Status()
	if got.Host != "192.168.1.42" {
		t.Errorf("Host = %q want 192.168.1.42", got.Host)
	}
	if got.TailscaleHost != "100.72.46.7" {
		t.Errorf("TailscaleHost = %q want 100.72.46.7", got.TailscaleHost)
	}
}

func TestMobileStatusTailscaleHostEmptyWhenAbsent(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := b.Status().TailscaleHost; got != "" {
		t.Errorf("TailscaleHost = %q want empty", got)
	}
}

func TestMobileStatusHostPickersDefaultWhenUnset(t *testing.T) {
	b := &BridgeService{
		LAN:         &fakeLAN{running: true},
		ConfigPath:  filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort: 3011,
	}
	got := b.Status()
	if got.Port != 3011 {
		t.Errorf("Port = %d want 3011", got.Port)
	}
}

func TestSecurePairingAppliesActualBoundPort(t *testing.T) {
	lan := &portOverrideLAN{startPort: 54014, boundPortStale: 3011}
	var applied []int
	b := &BridgeService{
		LAN:         lan,
		ConfigPath:  filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort: 3011,
		ApplyServe:  func(port int) error { applied = append(applied, port); return nil },
		ClearServe:  func() error { return nil },
		QueryTS:     func() mobilebridge.TailscaleInfo { return tsUp },
		ServeTarget: func() int { return 54014 },
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatalf("SetSecurePairing: %v", err)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("Enable: %v", err)
	}
	if len(applied) == 0 || applied[len(applied)-1] != 54014 {
		t.Errorf("applied = %v, want the bound port 54014", applied)
	}
}

func TestSecurePairingStatusActive(t *testing.T) {
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Enable(); err != nil {
		t.Fatal(err)
	}
	sp := b.Status().SecurePairing
	if !sp.Enabled || !sp.Available || !sp.Active {
		t.Fatalf("securePairing = %+v, want enabled/available/active", sp)
	}
	if sp.Host != "prasads-macbook-pro.tail057d04.ts.net" || sp.Port != 443 {
		t.Errorf("host/port = %q/%d", sp.Host, sp.Port)
	}
	if sp.Reason != "" {
		t.Errorf("Reason = %q, want empty when available", sp.Reason)
	}
}

func TestSecurePairingReasons(t *testing.T) {
	cases := []struct {
		name   string
		info   mobilebridge.TailscaleInfo
		target func() int
		want   string
	}{
		{"no cli", mobilebridge.TailscaleInfo{}, func() int { return 0 }, "no_cli"},
		{"no certs", mobilebridge.TailscaleInfo{Name: "h.tail1.ts.net"}, func() int { return 0 }, "no_certs"},
		{"port mismatch", tsUp, func() int { return 9999 }, "port_mismatch"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := newSecureBridge(t, tc.info, tc.target)
			if _, err := b.SetSecurePairing(true); err != nil {
				t.Fatal(err)
			}
			if _, err := b.Enable(); err != nil {
				t.Fatal(err)
			}
			if got := b.Status().SecurePairing.Reason; got != tc.want {
				t.Errorf("Reason = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSecurePairingServeFailureKeepsBridgeUp(t *testing.T) {
	b := newSecureBridge(t, tsUp, func() int { return 0 })
	b.ApplyServe = func(int) error { return errors.New("port 443 in use") }
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatal(err)
	}
	res, err := b.Enable()
	if err != nil {
		t.Fatalf("Enable must succeed despite serve failure: %v", err)
	}
	if !res.Enabled {
		t.Error("bridge not enabled")
	}
	if got := b.Status().SecurePairing.Reason; got != "serve_failed" {
		t.Errorf("Reason = %q, want serve_failed", got)
	}
}

func TestSecurePairingOffClearsProxy(t *testing.T) {
	cleared := 0
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	b.ClearServe = func() error { cleared++; return nil }
	if _, err := b.SetSecurePairing(false); err != nil || cleared != 0 {
		t.Fatalf("turning off unused secure pairing cleared an unrelated Serve route: clears=%d err=%v", cleared, err)
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatal(err)
	}
	if _, err := b.SetSecurePairing(false); err != nil {
		t.Fatal(err)
	}
	if cleared != 1 {
		t.Errorf("Clear called %d times, want 1", cleared)
	}
}

func TestSecurePairingOffClearFailureReported(t *testing.T) {
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatal(err)
	}
	b.ClearServe = func() error { return errors.New("tailscale serve clear: exit 1") }
	if _, err := b.SetSecurePairing(false); err != nil {
		t.Fatalf("SetSecurePairing(false) must not return an error: %v", err)
	}
	sp := b.Status().SecurePairing
	if sp.Enabled {
		t.Error("Enabled = true, want false")
	}
	if sp.Reason != "clear_failed" {
		t.Errorf("Reason = %q, want clear_failed", sp.Reason)
	}
}

var tsUp = mobilebridge.TailscaleInfo{
	Name:         "prasads-macbook-pro.tail057d04.ts.net",
	CertsEnabled: true,
}

type portOverrideLAN struct {
	fakeLAN
	startPort      int
	boundPortStale int
}

func (f *portOverrideLAN) BoundPort() int         { return f.boundPortStale }
func (f *portOverrideLAN) Start(int) (int, error) { f.running = true; return f.startPort, nil }

func newSecureBridge(t *testing.T, info mobilebridge.TailscaleInfo, target func() int) *BridgeService {
	t.Helper()
	return &BridgeService{
		LAN:         &fakeLAN{},
		ConfigPath:  filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort: 3011,
		ApplyServe:  func(int) error { return nil },
		ClearServe:  func() error { return nil },
		QueryTS:     func() mobilebridge.TailscaleInfo { return info },
		ServeTarget: target,
	}
}

func TestDisableLeavesServeAloneWhenSecurePairingNeverEnabled(t *testing.T) {
	cleared := 0
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	b.ClearServe = func() error { cleared++; return nil }
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := b.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if cleared != 0 {
		t.Errorf("clearServe called %d times, want 0 — AO must not touch a proxy it never installed", cleared)
	}
}

func TestDisableClearsServeWhenSecurePairingEnabled(t *testing.T) {
	cleared := 0
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	b.ClearServe = func() error { cleared++; return nil }
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatalf("set secure: %v", err)
	}
	if err := b.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if cleared != 1 {
		t.Errorf("clearServe called %d times, want 1", cleared)
	}
}

func TestShutdownServeClearsProxyWhenSecurePairingEnabled(t *testing.T) {
	cleared := 0
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	b.ClearServe = func() error { cleared++; return nil }
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatalf("set secure: %v", err)
	}
	b.ShutdownServe()
	if cleared != 1 {
		t.Errorf("clearServe called %d times on shutdown, want 1", cleared)
	}
}

func TestShutdownServeKeepsSecurePairingPreference(t *testing.T) {
	b := newSecureBridge(t, tsUp, func() int { return 3011 })
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := b.SetSecurePairing(true); err != nil {
		t.Fatalf("set secure: %v", err)
	}
	b.ShutdownServe()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !st.SecurePairing {
		t.Error("SecurePairing was cleared by shutdown; boot restore would not re-apply the proxy")
	}
}

func TestShutdownServeNoopWhenNotOwned(t *testing.T) {
	for name, setup := range map[string]func(*BridgeService){
		"bridge disabled":         func(b *BridgeService) {},
		"secure pairing never on": func(b *BridgeService) { _, _ = b.Enable() },
	} {
		t.Run(name, func(t *testing.T) {
			cleared := 0
			b := newSecureBridge(t, tsUp, func() int { return 3011 })
			b.ClearServe = func() error { cleared++; return nil }
			setup(b)
			b.ShutdownServe()
			if cleared != 0 {
				t.Errorf("clearServe called %d times, want 0", cleared)
			}
		})
	}
}

func TestMobileStatusAdvertisesEveryEndpoint(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42", "10.0.0.5"} },
		PickTailscaleHosts: func() []string { return []string{"100.72.46.7"} },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	got := b.Status()

	want := []mobilebridge.Endpoint{
		{Kind: mobilebridge.KindLAN, Host: "192.168.1.42", Port: 3011},
		{Kind: mobilebridge.KindLAN, Host: "10.0.0.5", Port: 3011},
		{Kind: mobilebridge.KindTailscale, Host: "100.72.46.7", Port: 3011},
	}
	if len(got.Endpoints) != len(want) {
		t.Fatalf("got %d endpoints %+v, want %d", len(got.Endpoints), got.Endpoints, len(want))
	}
	for i := range want {
		if got.Endpoints[i] != want[i] {
			t.Errorf("endpoint %d: got %+v want %+v", i, got.Endpoints[i], want[i])
		}
	}

	if got.Host != "192.168.1.42" {
		t.Errorf("Host = %q want the first LAN candidate", got.Host)
	}
	if got.TailscaleHost != "100.72.46.7" {
		t.Errorf("TailscaleHost = %q want the first Tailscale candidate", got.TailscaleHost)
	}
}

func TestMobileStatusEndpointsEmptyWithoutNetwork(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return nil },
		PickTailscaleHosts: func() []string { return nil },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	got := b.Status()
	if len(got.Endpoints) != 0 {
		t.Fatalf("got %+v, want no endpoints", got.Endpoints)
	}
	if got.Host != "" {
		t.Errorf("Host = %q want empty", got.Host)
	}
}

type fakeTunnel struct {
	startedOn int
	stops     int
	endpoint  *mobilebridge.TunnelEndpoint
	status    mobilebridge.TunnelStatus
}

func (f *fakeTunnel) Start(localPort int) { f.startedOn = localPort }
func (f *fakeTunnel) Stop()               { f.stops++ }
func (f *fakeTunnel) Endpoint() *mobilebridge.TunnelEndpoint {
	return f.endpoint
}
func (f *fakeTunnel) Status() mobilebridge.TunnelStatus { return f.status }

func TestMobileEnableStartsTheTunnelOnTheBoundPort(t *testing.T) {
	tun := &fakeTunnel{}
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if tun.startedOn != 3011 {
		t.Fatalf("tunnel started on %d, want the bound port 3011", tun.startedOn)
	}
}

type stopErrorLAN struct {
	fakeLAN
	err          error
	stillRunning bool
}

func (f *stopErrorLAN) Stop(context.Context) error {
	f.stopCalls++
	f.running = f.stillRunning
	return f.err
}

func TestMobileDisableFinishesCleanupAfterStopErrorWhenListenerStopped(t *testing.T) {
	stopErr := errors.New("shutdown deadline exceeded")
	path := filepath.Join(t.TempDir(), "mobile.json")
	if err := mobilebridge.Save(path, mobilebridge.State{
		Enabled:       true,
		Password:      "paired-password",
		LastPort:      3011,
		SecurePairing: true,
	}); err != nil {
		t.Fatal(err)
	}
	cleared := 0
	lan := &stopErrorLAN{
		fakeLAN: fakeLAN{running: true, hash: mobilebridge.HashPassword("paired-password")},
		err:     stopErr,
	}
	bridge := &BridgeService{
		LAN:         lan,
		ConfigPath:  path,
		ClearServe:  func() error { cleared++; return nil },
		DefaultPort: 3011,
	}

	if err := bridge.Disable(); !errors.Is(err, stopErr) {
		t.Fatalf("Disable error = %v, want %v", err, stopErr)
	}
	state, err := mobilebridge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled {
		t.Fatal("persisted bridge remained enabled after the listener stopped")
	}
	if cleared != 1 {
		t.Fatalf("owned secure-pairing proxy cleared %d times, want 1", cleared)
	}
}

func TestMobileDisablePreservesEnabledStateWhenStopErrorLeavesListenerRunning(t *testing.T) {
	stopErr := errors.New("listener still running")
	path := filepath.Join(t.TempDir(), "mobile.json")
	if err := mobilebridge.Save(path, mobilebridge.State{
		Enabled:       true,
		Password:      "paired-password",
		LastPort:      3011,
		SecurePairing: true,
	}); err != nil {
		t.Fatal(err)
	}
	cleared := 0
	lan := &stopErrorLAN{
		fakeLAN:      fakeLAN{running: true, hash: mobilebridge.HashPassword("paired-password")},
		err:          stopErr,
		stillRunning: true,
	}
	bridge := &BridgeService{
		LAN:         lan,
		ConfigPath:  path,
		ClearServe:  func() error { cleared++; return nil },
		DefaultPort: 3011,
	}

	if err := bridge.Disable(); !errors.Is(err, stopErr) {
		t.Fatalf("Disable error = %v, want %v", err, stopErr)
	}
	state, err := mobilebridge.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !state.Enabled {
		t.Fatal("persisted bridge was disabled while the listener remained running")
	}
	if cleared != 0 {
		t.Fatalf("secure-pairing proxy cleared %d times while listener remained live", cleared)
	}
}

func TestMobileDisableStopsTheTunnel(t *testing.T) {
	tun := &fakeTunnel{}
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if err := b.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if tun.stops != 1 {
		t.Fatalf("tunnel stopped %d times, want 1", tun.stops)
	}
}

func TestMobileStatusIncludesAReadyTunnelEndpoint(t *testing.T) {
	tun := &fakeTunnel{
		endpoint: &mobilebridge.TunnelEndpoint{Ready: true, Hostname: "abc.trycloudflare.com"},
		status:   mobilebridge.TunnelStatus{Running: true, Ready: true, Hostname: "abc.trycloudflare.com"},
	}
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	got := b.Status()
	last := got.Endpoints[len(got.Endpoints)-1]
	if last.Kind != mobilebridge.KindTunnel || last.Host != "abc.trycloudflare.com" {
		t.Fatalf("last endpoint = %+v, want the tunnel", last)
	}
	if !got.Tunnel.Ready {
		t.Error("Tunnel status not surfaced; the desktop cannot show progress")
	}
}

func TestMobileStatusOmitsATunnelThatIsNotReady(t *testing.T) {
	tun := &fakeTunnel{status: mobilebridge.TunnelStatus{Running: true}}
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	for _, e := range b.Status().Endpoints {
		if e.Kind == mobilebridge.KindTunnel {
			t.Fatalf("advertised %+v while the tunnel was not ready", e)
		}
	}
}

func TestMobileWorksWithNoTunnelConfigured(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if got := b.Status(); len(got.Endpoints) != 1 || got.Endpoints[0].Kind != mobilebridge.KindLAN {
		t.Fatalf("endpoints = %+v, want just the LAN one", got.Endpoints)
	}
	if err := b.Disable(); err != nil {
		t.Fatalf("disable: %v", err)
	}
}

func TestMobileStatusCarriesTheHostIdentity(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: true},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		HostID:             "h_b3e07f31",
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
	}
	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if got := b.Status().HostID; got != "h_b3e07f31" {
		t.Fatalf("HostID = %q want h_b3e07f31", got)
	}
}

func TestMobileAdvertisesNothingWhileTheBridgeIsOff(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{running: false},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return []string{"100.72.46.7"} },
	}

	if got := b.AdvertisedEndpoints(); len(got) != 0 {
		t.Fatalf("advertised %+v while the bridge was disabled", got)
	}
	if got := b.Status().Endpoints; len(got) != 0 {
		t.Fatalf("status advertised %+v while the bridge was disabled", got)
	}
}

func TestMobileRestoreOnBootStartsTheTunnel(t *testing.T) {
	tun := &fakeTunnel{}
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
		Tunnel:             tun,
	}

	if err := b.RestoreOnBoot(mobilebridge.State{Enabled: true, Password: "pw", LastPort: 3011}); err != nil {
		t.Fatalf("restore: %v", err)
	}

	if tun.startedOn != 3011 {
		t.Fatalf("tunnel started on %d, want the restored port 3011", tun.startedOn)
	}
}

func TestMobileRestoreOnBootWithoutATunnelIsHarmless(t *testing.T) {
	b := &BridgeService{
		LAN:                &fakeLAN{},
		ConfigPath:         filepath.Join(t.TempDir(), "mobile", "config.json"),
		DefaultPort:        3011,
		PickLANHosts:       func() []string { return []string{"192.168.1.42"} },
		PickTailscaleHosts: func() []string { return nil },
	}
	if err := b.RestoreOnBoot(mobilebridge.State{Enabled: true, Password: "pw", LastPort: 3011}); err != nil {
		t.Fatalf("restore: %v", err)
	}
}

func TestShutdownTunnelStopsTheConnector(t *testing.T) {
	dir := t.TempDir()
	tun := &fakeTunnel{}
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json"), Tunnel: tun}

	b.ShutdownTunnel()

	if tun.stops != 1 {
		t.Fatalf("connector stops = %d, want 1", tun.stops)
	}
}

func TestShutdownTunnelWithoutAConnector(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json")}

	b.ShutdownTunnel()
}

func TestTunnelStatusReportsWhenRemoteAccessIsUnsupported(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json")}

	st := b.tunnelStatus()

	if st.Supported {
		t.Fatal("no connector configured, so remote access is not supported")
	}
}

func TestTunnelStatusReportsSupportedWhenAConnectorExists(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{ConfigPath: filepath.Join(dir, "mobile.json"), Tunnel: &fakeTunnel{}}

	if !b.tunnelStatus().Supported {
		t.Fatal("a configured connector means remote access is supported")
	}
}

func TestEnableResolvesAConnectorInstalledSinceBoot(t *testing.T) {
	dir := t.TempDir()
	tun := &fakeTunnel{}
	resolved := 0
	b := &BridgeService{
		ConfigPath:  filepath.Join(dir, "mobile.json"),
		LAN:         &fakeLAN{},
		DefaultPort: 3011,
		ResolveTunnel: func() TunnelController {
			resolved++
			return tun
		},
	}

	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}

	if resolved != 1 {
		t.Fatalf("resolver calls = %d, want 1", resolved)
	}
	if tun.startedOn != 3011 {
		t.Fatalf("connector started on %d, want the bound port 3011", tun.startedOn)
	}
}

func TestEnableWithoutAConnectorStillWorks(t *testing.T) {
	dir := t.TempDir()
	b := &BridgeService{
		ConfigPath:    filepath.Join(dir, "mobile.json"),
		LAN:           &fakeLAN{},
		DefaultPort:   3011,
		ResolveTunnel: func() TunnelController { return nil },
	}

	if _, err := b.Enable(); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if b.tunnelStatus().Supported {
		t.Fatal("no connector resolved, so remote access is not supported")
	}
}

func TestStartRemoteAccessKeepsThePairedPhoneWorking(t *testing.T) {
	dir := t.TempDir()
	lan := &fakeLAN{}
	tun := &fakeTunnel{}
	b := &BridgeService{
		ConfigPath: filepath.Join(dir, "mobile.json"), LAN: lan, DefaultPort: 3011,
	}
	before, err := b.Enable()
	if err != nil {
		t.Fatalf("enable: %v", err)
	}

	b.ResolveTunnel = func() TunnelController { return tun }
	after, err := b.StartRemoteAccess()
	if err != nil {
		t.Fatalf("StartRemoteAccess: %v", err)
	}

	if after.Password != before.Password {
		t.Fatal("password rotated: the phone paired a moment ago can no longer authenticate")
	}
	if tun.startedOn != 3011 {
		t.Fatalf("connector started on %d, want the bound port 3011", tun.startedOn)
	}
}

func TestStartRemoteAccessWhileDisabledDoesNothing(t *testing.T) {
	dir := t.TempDir()
	tun := &fakeTunnel{}
	b := &BridgeService{
		ConfigPath: filepath.Join(dir, "mobile.json"), LAN: &fakeLAN{}, DefaultPort: 3011,
		ResolveTunnel: func() TunnelController { return tun },
	}

	if _, err := b.StartRemoteAccess(); err != nil {
		t.Fatalf("StartRemoteAccess: %v", err)
	}
	if tun.startedOn != 0 {
		t.Fatal("connector started while the bridge is disabled")
	}
}

func TestConnectorInstalledStartsTheTunnelTheBridgeWanted(t *testing.T) {
	for _, tc := range []struct {
		name      string
		enable    func(*BridgeService) (MobileStatusResponse, error)
		wantStart bool
	}{
		{name: "public", enable: (*BridgeService).Enable, wantStart: true},
		{name: "lan only", enable: (*BridgeService).EnableLANOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tun TunnelController
			b := &BridgeService{
				ConfigPath: filepath.Join(t.TempDir(), "mobile.json"), LAN: &fakeLAN{}, DefaultPort: 3011,
				ResolveTunnel: func() TunnelController { return tun },
			}
			before, err := tc.enable(b)
			if err != nil {
				t.Fatalf("enable: %v", err)
			}

			landed := &fakeTunnel{}
			tun = landed
			b.ConnectorInstalled()

			if started := landed.startedOn == 3011; started != tc.wantStart {
				t.Fatalf("connector started on %d, want started=%v", landed.startedOn, tc.wantStart)
			}
			if b.Status().Password != before.Password {
				t.Fatal("password rotated: the phone paired a moment ago can no longer authenticate")
			}
		})
	}
}

type startErrorLAN struct {
	fakeLAN
	err error
}

func (f *startErrorLAN) Start(int) (int, error) { return 0, f.err }

func TestMobileRestoreOnBootRollsBackHashWhenStartFails(t *testing.T) {
	startErr := errors.New("bind failed")
	lan := &startErrorLAN{fakeLAN: fakeLAN{hash: "previous-hash", accountHash: "previous-account-hash"}, err: startErr}
	bridge := &BridgeService{LAN: lan}

	err := bridge.RestoreOnBoot(mobilebridge.State{Enabled: true, Password: "restored-password", AccountTokenHash: "restored-account-hash", LastPort: 3011})
	if !errors.Is(err, startErr) {
		t.Fatalf("RestoreOnBoot error = %v, want %v", err, startErr)
	}
	if got := lan.PasswordHash(); got != "previous-hash" {
		t.Fatalf("password hash after failed restore = %q, want previous-hash", got)
	}
	if got := lan.AccountTokenHash(); got != "previous-account-hash" {
		t.Fatalf("account token hash after failed restore = %q, want previous-account-hash", got)
	}
}

func TestTunnelStatusSaysWhenAConnectorIsOnItsWay(t *testing.T) {
	installing := true
	b := &BridgeService{LAN: &fakeLAN{}, ConfigPath: filepath.Join(t.TempDir(), "mobile", "config.json"), ConnectorInstalling: func() bool { return installing }}
	if st := b.tunnelStatus(); st.Supported || !st.Installing {
		t.Fatalf("installing: %+v", st)
	}
	installing = false
	if st := b.tunnelStatus(); st.Installing {
		t.Fatalf("not installing: %+v", st)
	}
}

func TestStatusReportsAnAccountLinkAOCloudRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mobile.json")
	if err := mobilebridge.Save(path, mobilebridge.State{Enabled: true, Password: "secret12", AccountTokenHash: "hash-a"}); err != nil {
		t.Fatal(err)
	}
	bridge := &BridgeService{LAN: &fakeLAN{running: true}, ConfigPath: path, HostID: "h_test"}
	if st := bridge.Status(); !st.AccountLinked || st.AccountLinkRefused {
		t.Fatalf("before any publish: linked=%v refused=%v, want linked and not known refused", st.AccountLinked, st.AccountLinkRefused)
	}
	bridge.NoteAccountListing("hash-a", false)
	if !bridge.Status().AccountLinkRefused {
		t.Fatal("a refused publish under the current token was not reported")
	}
	bridge.NoteAccountListing("hash-old", false)
	if bridge.Status().AccountLinkRefused {
		t.Fatal("a refusal of another token marked the current one refused")
	}
	bridge.NoteAccountListing("hash-a", false)
	bridge.NoteAccountListing("hash-a", true)
	if bridge.Status().AccountLinkRefused {
		t.Fatal("an accepted publish left the link refused")
	}
}
