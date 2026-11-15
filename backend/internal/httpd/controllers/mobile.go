package controllers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/requestscope"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

const mobileUnencryptedWarning = "Traffic on this connection is not encrypted. Only use it on a network you trust."

type mobileBridge interface {
	Status() MobileStatusResponse
	Enable() (MobileStatusResponse, error)
	EnableLANOnly() (MobileStatusResponse, error)
	EnableTunnelOnly() (MobileStatusResponse, error)
	Disable() error
	Regenerate() (MobileStatusResponse, error)
	StartRemoteAccess() (MobileStatusResponse, error)
	SetSecurePairing(on bool) (MobileStatusResponse, error)
	SetKeepAwake(on bool) (MobileStatusResponse, error)
	SetBrowserRemoteAccess(on bool) (MobileStatusResponse, error)
	SetKeepAwakeLid(on, lidClosed bool) (MobileStatusResponse, error)
}

type MobileController struct{ Bridge mobileBridge }

type RemoteHostAccountTokenResponse struct {
	HostID  string `json:"hostId"`
	Token   string `json:"token"`
	HostKey string `json:"hostKey"`
}

func (c *MobileController) IssueAccountToken(w http.ResponseWriter, r *http.Request) {
	issuer, ok := c.Bridge.(interface {
		IssueAccountToken(string, string) (RemoteHostAccountTokenResponse, error)
	})
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "REMOTE_HOST_ACCOUNT_TOKEN", "account pairing is unavailable", nil)
		return
	}
	password := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	accountID := strings.TrimSpace(r.Header.Get("X-AO-Account-ID"))
	var result RemoteHostAccountTokenResponse
	var err error
	if device, ok := c.Bridge.(interface {
		IssueAccountTokenForDevice(string) (RemoteHostAccountTokenResponse, error)
	}); ok && requestscope.CredentialFrom(r.Context()).Kind == requestscope.CredentialDevice {
		result, err = device.IssueAccountTokenForDevice(accountID)
	} else {
		result, err = issuer.IssueAccountToken(password, accountID)
	}
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "REMOTE_HOST_ACCOUNT_TOKEN", "connection password required", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, result)
}

func (c *MobileController) NewPairingCode(w http.ResponseWriter, r *http.Request) {
	pairing, ok := c.Bridge.(interface {
		NewPairingCode() (PairingCodeResponse, error)
	})
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "DEVICE_PAIRING", "device pairing is unavailable", nil)
		return
	}
	result, err := pairing.NewPairingCode()
	if errors.Is(err, errRemoteAccessOff) {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "REMOTE_ACCESS_OFF", err.Error(), nil)
		return
	}
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DEVICE_PAIRING", err.Error(), nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, result)
}

func (c *MobileController) PairDevice(w http.ResponseWriter, r *http.Request) {
	pairing, ok := c.Bridge.(interface {
		PairDevice(code, name, platform string) (PairDeviceResponse, error)
	})
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "DEVICE_PAIRING", "device pairing is unavailable", nil)
		return
	}
	credential := requestscope.CredentialFrom(r.Context())
	if credential.Kind != requestscope.CredentialPairingCode && credential.Kind != requestscope.CredentialPassword {
		envelope.WriteAPIError(w, r, http.StatusForbidden, "forbidden", "DEVICE_PAIRING", "a pairing code is required", nil)
		return
	}
	var req PairDeviceRequest
	if r.ContentLength != 0 {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&req); err != nil {
			envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
			return
		}
	}
	code := ""
	if credential.Kind == requestscope.CredentialPairingCode {
		code = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	}
	result, err := pairing.PairDevice(code, req.Name, req.Platform)
	switch {
	case errors.Is(err, errPairingCodeUsed):
		envelope.WriteAPIError(w, r, http.StatusUnauthorized, "unauthorized", "PAIRING_CODE_USED", err.Error(), nil)
		return
	case errors.Is(err, mobilebridge.ErrTooManyDevices):
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "TOO_MANY_DEVICES", err.Error(), nil)
		return
	case err != nil:
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "DEVICE_PAIRING", "could not pair the device", nil)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	envelope.WriteJSON(w, http.StatusOK, result)
}

func (c *MobileController) RequireDeviceCredentials(w http.ResponseWriter, r *http.Request) {
	setter, ok := c.Bridge.(interface {
		SetRequireDeviceCredentials(bool) (MobileStatusResponse, error)
	})
	if !ok {
		envelope.WriteAPIError(w, r, http.StatusNotImplemented, "not_implemented", "DEVICE_PAIRING", "device pairing is unavailable", nil)
		return
	}
	var req SetRequireDeviceCredentialsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	res, err := setter.SetRequireDeviceCredentials(req.Enabled)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_REQUIRE_DEVICE_CREDENTIALS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

func withWarning(res MobileStatusResponse) MobileStatusResponse {
	res.Warning = mobileUnencryptedWarning
	return res
}

func (c *MobileController) Status(w http.ResponseWriter, r *http.Request) {
	envelope.WriteJSON(w, http.StatusOK, withWarning(c.Bridge.Status()))
}

func (c *MobileController) StartRemoteAccess(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.StartRemoteAccess()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_REMOTE_ACCESS", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

func (c *MobileController) Enable(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.Enable()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_ENABLE", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

func (c *MobileController) EnableLANOnly(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.EnableLANOnly()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_ENABLE_LAN_ONLY", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

func (c *MobileController) EnableTunnelOnly(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.EnableTunnelOnly()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusConflict, "conflict", "MOBILE_ENABLE_TUNNEL_ONLY", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

func (c *MobileController) Disable(w http.ResponseWriter, r *http.Request) {
	if err := c.Bridge.Disable(); err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_DISABLE", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(c.Bridge.Status()))
}

func (c *MobileController) Regenerate(w http.ResponseWriter, r *http.Request) {
	res, err := c.Bridge.Regenerate()
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_REGEN", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

func (c *MobileController) SecurePairing(w http.ResponseWriter, r *http.Request) {
	var body SetSecurePairingRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_SECURE_PAIRING", "invalid body", nil)
		return
	}
	res, err := c.Bridge.SetSecurePairing(body.Enabled)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_SECURE_PAIRING", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

func (c *MobileController) KeepAwake(w http.ResponseWriter, r *http.Request) {
	var body SetKeepAwakeRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_KEEP_AWAKE", "invalid body", nil)
		return
	}
	var res MobileStatusResponse
	var err error
	if body.LidClosed != nil {
		res, err = c.Bridge.SetKeepAwakeLid(body.Enabled, *body.LidClosed)
	} else {
		res, err = c.Bridge.SetKeepAwake(body.Enabled)
	}
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_KEEP_AWAKE", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

func (c *MobileController) BrowserRemoteAccess(w http.ResponseWriter, r *http.Request) {
	var body SetBrowserRemoteAccessRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "invalid_request", "MOBILE_BROWSER_CONTROL", "invalid body", nil)
		return
	}
	res, err := c.Bridge.SetBrowserRemoteAccess(body.Enabled)
	if err != nil {
		envelope.WriteAPIError(w, r, http.StatusInternalServerError, "internal", "MOBILE_BROWSER_CONTROL", err.Error(), nil)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, withWarning(res))
}

type LANController interface {
	Start(port int) (int, error)
	StartLoopback(port int) (int, error)
	Stop(ctx context.Context) error
	Running() bool
	BoundPort() int
	SetPasswordHash(hash string)
	PasswordHash() string
}

type TunnelController interface {
	Start(localPort int)
	Stop()
	Endpoint() *mobilebridge.TunnelEndpoint
	Status() mobilebridge.TunnelStatus
}

type KeepAwakeController interface {
	Start() error
	Stop()
	Active() bool
	HasBattery() bool
	ConfigureLid(on, allowSetup bool) error
	LidStatus() (supported, active bool, reason string)
}

type BridgeService struct {
	LAN                LANController
	ConfigPath         string
	DefaultPort        int
	PickLANHosts       func() []string
	PickTailscaleHosts func() []string
	ApplyServe         func(port int) error
	ClearServe         func() error
	QueryTS            func() mobilebridge.TailscaleInfo
	ServeTarget        func() int

	HostID string

	Tunnel TunnelController

	ResolveTunnel func() TunnelController

	ConnectorInstalling func() bool

	KeepAwake KeepAwakeController

	Devices     *mobilebridge.AccessDevices
	Codes       *mobilebridge.PairingCodes
	PushDevices DeviceRoster

	OnBrowserRemoteAccessDisabled func()

	transitionMu sync.Mutex

	tunnelMu sync.RWMutex

	refusedMu          sync.Mutex
	refusedAccountHash string
	listingNudgeOnce   sync.Once
	listingNudge       chan struct{}
	tunnelResolving    chan struct{}

	stateMu  sync.RWMutex
	serveErr error
}

func (b *BridgeService) lanHosts() []string {
	if b.PickLANHosts != nil {
		return b.PickLANHosts()
	}
	return mobilebridge.LocalPrivateIPv4s()
}

func (b *BridgeService) tailscaleHosts() []string {
	if b.PickTailscaleHosts != nil {
		return b.PickTailscaleHosts()
	}
	return mobilebridge.LocalTailscaleIPv4s()
}

func first(hosts []string) string {
	if len(hosts) == 0 {
		return ""
	}
	return hosts[0]
}

func (b *BridgeService) NoteAccountListing(hash string, accepted bool) {
	b.refusedMu.Lock()
	defer b.refusedMu.Unlock()
	if accepted {
		if b.refusedAccountHash == hash {
			b.refusedAccountHash = ""
		}
		return
	}
	b.refusedAccountHash = hash
}

func (b *BridgeService) accountLinkRefused(hash string) bool {
	b.refusedMu.Lock()
	defer b.refusedMu.Unlock()
	return hash != "" && b.refusedAccountHash == hash
}

func (b *BridgeService) ListingNudges() <-chan struct{} {
	b.listingNudgeOnce.Do(func() { b.listingNudge = make(chan struct{}, 1) })
	return b.listingNudge
}

func (b *BridgeService) nudgeListing() {
	b.listingNudgeOnce.Do(func() { b.listingNudge = make(chan struct{}, 1) })
	select {
	case b.listingNudge <- struct{}{}:
	default:
	}
}

func (b *BridgeService) Status() MobileStatusResponse {
	st, _ := mobilebridge.Load(b.ConfigPath)
	enabled := st.Enabled && b.LAN.Running()
	lan := b.lanHosts()
	ts := b.tailscaleHosts()
	if st.LoopbackOnly {
		lan, ts = nil, nil
	}
	res := MobileStatusResponse{
		Enabled:             enabled,
		AccountLinked:       st.AccountTokenHash != "",
		AccountLinkRefused:  b.accountLinkRefused(st.AccountTokenHash),
		LoopbackOnly:        st.LoopbackOnly,
		NoPublicTunnel:      st.NoPublicTunnel,
		Host:                first(lan),
		TailscaleHost:       first(ts),
		Port:                b.LAN.BoundPort(),
		Warning:             mobileUnencryptedWarning,
		BrowserRemoteAccess: st.BrowserRemoteAccess,
		Endpoints: mobilebridge.Endpoints(mobilebridge.EndpointInputs{
			LANHosts:       lan,
			TailscaleHosts: ts,
			Port:           b.LAN.BoundPort(),
			Tunnel:         b.tunnelEndpoint(),
		}),
		Tunnel:                   b.tunnelStatus(),
		HostID:                   b.HostID,
		RequireDeviceCredentials: st.RequireDeviceCredentials,
	}
	if b.Devices != nil {
		if used := b.Devices.SharedLastUsed(mobilebridge.SharedPassword); !used.IsZero() {
			res.PasswordLastUsedAt = &used
		}
	}
	if enabled {
		res.Password = st.Password
	}
	res.SecurePairing = b.securePairingStatus(st.SecurePairing, enabled)
	if st.ServeCleanupPending && res.SecurePairing.Reason == "" {
		res.SecurePairing.Reason = "clear_failed"
	}
	res.KeepAwake = b.keepAwakeStatus(st.KeepAwake, st.KeepAwakeLid)
	return res
}

func (b *BridgeService) SetBrowserRemoteAccess(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	st.BrowserRemoteAccess = on
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	if !on && b.OnBrowserRemoteAccessDisabled != nil {
		b.OnBrowserRemoteAccessDisabled()
	}
	return b.Status(), nil
}

func (b *BridgeService) AdvertisedEndpoints() []mobilebridge.Endpoint {
	st, _ := mobilebridge.Load(b.ConfigPath)
	var lan, ts []string
	if !st.LoopbackOnly {
		lan, ts = b.lanHosts(), b.tailscaleHosts()
	}
	return mobilebridge.Endpoints(mobilebridge.EndpointInputs{
		LANHosts:       lan,
		TailscaleHosts: ts,
		Port:           b.LAN.BoundPort(),
		Tunnel:         b.tunnelEndpoint(),
	})
}

func (b *BridgeService) tunnel() TunnelController {
	b.tunnelMu.RLock()
	defer b.tunnelMu.RUnlock()
	return b.Tunnel
}

func (b *BridgeService) ensureTunnel() TunnelController {
	b.awaitBootTunnel()
	if t := b.tunnel(); t != nil {
		return t
	}
	if b.ResolveTunnel == nil {
		return nil
	}
	resolved := b.ResolveTunnel()
	if resolved == nil {
		return nil
	}
	b.tunnelMu.Lock()
	defer b.tunnelMu.Unlock()
	if b.Tunnel == nil {
		b.Tunnel = resolved
	}
	return b.Tunnel
}

func (b *BridgeService) ResolveTunnelInBackground(resolve func() TunnelController) {
	done := make(chan struct{})
	b.tunnelMu.Lock()
	b.tunnelResolving = done
	b.tunnelMu.Unlock()
	go func() {
		defer close(done)
		t := resolve()
		if t == nil {
			return
		}
		b.tunnelMu.Lock()
		defer b.tunnelMu.Unlock()
		if b.Tunnel == nil {
			b.Tunnel = t
		}
	}()
}

func (b *BridgeService) awaitBootTunnel() {
	b.tunnelMu.RLock()
	done := b.tunnelResolving
	b.tunnelMu.RUnlock()
	if done != nil {
		<-done
	}
}

func (b *BridgeService) tunnelEndpoint() *mobilebridge.TunnelEndpoint {
	t := b.tunnel()
	if t == nil {
		return nil
	}
	return t.Endpoint()
}

func (b *BridgeService) tunnelStatus() mobilebridge.TunnelStatus {
	b.awaitBootTunnel()
	t := b.tunnel()
	if t == nil {
		return mobilebridge.TunnelStatus{Installing: b.ConnectorInstalling != nil && b.ConnectorInstalling()}
	}
	st := t.Status()
	st.Supported = true
	return st
}

func (b *BridgeService) queryTS() mobilebridge.TailscaleInfo {
	if b.QueryTS != nil {
		return b.QueryTS()
	}
	return mobilebridge.QueryTailscale(context.Background())
}

func (b *BridgeService) applyServe(port int) error {
	if b.ApplyServe != nil {
		return b.ApplyServe(port)
	}
	return mobilebridge.NewServe().Apply(context.Background(), port)
}

func (b *BridgeService) clearServe() error {
	if b.ClearServe != nil {
		return b.ClearServe()
	}
	return mobilebridge.NewServe().Clear(context.Background())
}

func (b *BridgeService) serveTarget() int {
	if b.ServeTarget != nil {
		return b.ServeTarget()
	}
	return mobilebridge.NewServe().Target(context.Background())
}

func (b *BridgeService) serveError() error {
	b.stateMu.RLock()
	defer b.stateMu.RUnlock()
	return b.serveErr
}

func (b *BridgeService) setServeError(err error) {
	b.stateMu.Lock()
	defer b.stateMu.Unlock()
	b.serveErr = err
}

func (b *BridgeService) securePairingStatus(on, bridgeUp bool) SecurePairingStatus {
	serveErr := b.serveError()
	sp := SecurePairingStatus{Enabled: on}
	if !on {
		if serveErr != nil {
			sp.Reason = "clear_failed"
		}
		return sp
	}
	info := b.queryTS()
	switch {
	case info.Name == "":
		sp.Reason = "no_cli"
		return sp
	case !info.CertsEnabled:
		sp.Host = info.Name
		sp.Reason = "no_certs"
		return sp
	}
	sp.Available, sp.Host = true, info.Name
	if !bridgeUp {
		return sp
	}
	if serveErr != nil {
		sp.Reason = "serve_failed"
		return sp
	}
	if b.serveTarget() != b.LAN.BoundPort() {
		sp.Reason = "port_mismatch"
		return sp
	}
	sp.Active, sp.Port = true, 443
	return sp
}

func (b *BridgeService) keepAwakeStatus(on, lidClosed bool) KeepAwakeStatus {
	if b.KeepAwake == nil {
		return KeepAwakeStatus{}
	}
	res := KeepAwakeStatus{
		Supported:  true,
		Enabled:    on,
		Active:     b.KeepAwake.Active(),
		HasBattery: b.KeepAwake.HasBattery(),
	}
	res.LidClosed = lidClosed
	res.LidSupported, res.LidActive, res.LidReason = b.KeepAwake.LidStatus()
	return res
}

func (b *BridgeService) applyKeepAwakeLocked(on, lidClosed bool) error {
	if b.KeepAwake == nil {
		return nil
	}
	if on {
		if err := b.KeepAwake.Start(); err != nil {
			return err
		}
		_ = b.KeepAwake.ConfigureLid(lidClosed, false)
		return nil
	}
	b.KeepAwake.Stop()
	return nil
}

func (b *BridgeService) SetKeepAwake(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	if b.KeepAwake == nil {
		return MobileStatusResponse{}, errors.New("keeping this machine awake is only supported on macOS")
	}
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return MobileStatusResponse{}, err
	}
	previous := st
	st.KeepAwake = on
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	if err := b.applyKeepAwakeLocked(on, st.KeepAwakeLid); err != nil {
		return MobileStatusResponse{}, errors.Join(err, mobilebridge.Save(b.ConfigPath, previous))
	}
	return b.Status(), nil
}

func (b *BridgeService) SetKeepAwakeLid(on, lidClosed bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	if b.KeepAwake == nil {
		return MobileStatusResponse{}, errors.New("keeping this machine awake is only supported on macOS")
	}
	if lidClosed {
		supported, _, reason := b.KeepAwake.LidStatus()
		if !supported {
			if reason == "isolated_data_dir" {
				return MobileStatusResponse{}, errors.New("closed-lid wakefulness is disabled for custom or isolated AO_DATA_DIR directories; use the default ~/.ao/data directory to enable it")
			}
			return MobileStatusResponse{}, errors.New("closed-lid wakefulness requires a supported Apple Silicon MacBook (macOS 13–27) and the desktop helper")
		}
	}
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil && !os.IsNotExist(err) {
		return MobileStatusResponse{}, err
	}
	if lidClosed && !st.KeepAwakeLid {
		if err := b.KeepAwake.ConfigureLid(false, true); err != nil {
			return MobileStatusResponse{}, err
		}
	}
	previous := st
	st.KeepAwake, st.KeepAwakeLid = on, lidClosed
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	if err := b.applyKeepAwakeLocked(on, st.KeepAwakeLid); err != nil {
		return MobileStatusResponse{}, errors.Join(err, mobilebridge.Save(b.ConfigPath, previous))
	}
	return b.Status(), nil
}

func (b *BridgeService) SetSecurePairing(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, _ := mobilebridge.Load(b.ConfigPath)
	if on && st.LoopbackOnly {
		return MobileStatusResponse{}, errors.New("tailscale secure pairing is unavailable in tunnel-only mode")
	}
	ownedServe := st.SecurePairing || st.ServeCleanupPending
	st.SecurePairing = on
	if on {
		st.ServeCleanupPending = false
	} else if ownedServe {
		st.ServeCleanupPending = true
	}
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return MobileStatusResponse{}, err
	}
	b.setServeError(nil)
	if on {
		if b.LAN.Running() {
			b.setServeError(b.applyServe(b.LAN.BoundPort()))
		}
	} else if ownedServe {
		err := b.clearServe()
		b.setServeError(err)
		if err == nil && st.ServeCleanupPending {
			st.ServeCleanupPending = false
			if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
				return MobileStatusResponse{}, err
			}
		}
	}
	return b.Status(), nil
}

func (b *BridgeService) enableWithPasswordLocked(pw string, noPublicTunnel, loopbackOnly bool) (MobileStatusResponse, error) {
	prevHash := b.LAN.PasswordHash()
	wasRunning := b.LAN.Running()
	prevSt, _ := mobilebridge.Load(b.ConfigPath)

	b.applyPasswordLogin(prevSt)

	nextHash := mobilebridge.HashPassword(pw)
	if !wasRunning {
		b.LAN.SetPasswordHash(nextHash)
	}
	start := b.LAN.Start
	if loopbackOnly {
		start = b.LAN.StartLoopback
	}
	port, err := start(b.DefaultPort)
	if err != nil {
		if !wasRunning {
			b.LAN.SetPasswordHash(prevHash)
		}
		return MobileStatusResponse{}, err
	}
	retired := prevSt.RetiredPasswordHash
	accountTokenHash := prevSt.AccountTokenHash
	accountToken := prevSt.AccountToken
	accountTokenOwner := prevSt.AccountTokenOwner
	if prevSt.Password != "" && prevSt.Password != pw {
		retired = mobilebridge.HashPassword(prevSt.Password)
		accountTokenHash = ""
		accountToken = ""
		accountTokenOwner = ""
	}
	nextSt := mobilebridge.State{Enabled: true, Password: pw, LastPort: port, RetiredPasswordHash: retired, AccountTokenHash: accountTokenHash, AccountToken: accountToken, AccountTokenOwner: accountTokenOwner, RequireDeviceCredentials: prevSt.RequireDeviceCredentials, SecurePairing: prevSt.SecurePairing, ServeCleanupPending: prevSt.ServeCleanupPending, KeepAwake: prevSt.KeepAwake, KeepAwakeLid: prevSt.KeepAwakeLid, BrowserRemoteAccess: prevSt.BrowserRemoteAccess, NoPublicTunnel: noPublicTunnel, LoopbackOnly: loopbackOnly}
	if err := mobilebridge.Save(b.ConfigPath, nextSt); err != nil {
		if !wasRunning {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = b.LAN.Stop(ctx)
			b.LAN.SetPasswordHash(prevHash)
		}
		return MobileStatusResponse{}, err
	}
	var revokeErr error
	if prevSt.Password != "" && prevSt.Password != pw {
		revokeErr = b.revokeAllDevices()
	}
	if wasRunning {
		b.LAN.SetPasswordHash(nextHash)
	}
	if accountTokenHash != prevSt.AccountTokenHash {
		if setter, ok := b.LAN.(interface{ SetAccountTokenHash(string) }); ok {
			setter.SetAccountTokenHash(accountTokenHash)
		}
		b.nudgeTunnel()
	}
	b.startConnectorsLocked(port, nextSt)
	if revokeErr != nil {
		return MobileStatusResponse{}, fmt.Errorf("the password changed, but device tokens were not revoked: %w", revokeErr)
	}
	return b.Status(), nil
}

func (b *BridgeService) startConnectorsLocked(port int, st mobilebridge.State) {
	b.setServeError(nil)
	if st.SecurePairing && !st.LoopbackOnly {
		b.setServeError(b.applyServe(port))
	}
	if !st.NoPublicTunnel {
		if t := b.ensureTunnel(); t != nil {
			t.Start(port)
		}
	}
}

func (b *BridgeService) RestoreOnBoot(state mobilebridge.State) error {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	wakeErr := b.applyKeepAwakeLocked(state.KeepAwake, state.KeepAwakeLid)
	if !state.Enabled {
		return wakeErr
	}

	prevHash := b.LAN.PasswordHash()
	accountSetter, hasAccountSetter := b.LAN.(interface {
		SetAccountTokenHash(string)
		AccountTokenHash() string
	})
	previousAccountHash := ""
	if hasAccountSetter {
		previousAccountHash = accountSetter.AccountTokenHash()
	}
	if state.RetiredPasswordHash != "" {
		b.LAN.SetPasswordHash(state.RetiredPasswordHash)
	}
	b.LAN.SetPasswordHash(mobilebridge.HashPassword(state.Password))
	if setter, ok := b.LAN.(interface{ SetAccountTokenHash(string) }); ok {
		setter.SetAccountTokenHash(state.AccountTokenHash)
	}
	b.applyPasswordLogin(state)
	start := b.LAN.Start
	if state.LoopbackOnly {
		start = b.LAN.StartLoopback
	}
	port, err := start(state.LastPort)
	if err != nil {
		b.LAN.SetPasswordHash(prevHash)
		if hasAccountSetter {
			accountSetter.SetAccountTokenHash(previousAccountHash)
		}
		return errors.Join(err, wakeErr)
	}
	b.startConnectorsLocked(port, state)
	return wakeErr
}

func (b *BridgeService) hostPublicKey() string {
	key, err := mobilebridge.HostPublicKey(b.ConfigPath)
	if err != nil {
		return ""
	}
	return key
}

func (b *BridgeService) IssueAccountToken(password, accountID string) (RemoteHostAccountTokenResponse, error) {
	return b.issueAccountToken(password, false, accountID)
}

func (b *BridgeService) IssueAccountTokenForDevice(accountID string) (RemoteHostAccountTokenResponse, error) {
	return b.issueAccountToken("", true, accountID)
}

func (b *BridgeService) issueAccountToken(password string, device bool, accountID string) (RemoteHostAccountTokenResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	state, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return RemoteHostAccountTokenResponse{}, err
	}
	if !state.Enabled {
		return RemoteHostAccountTokenResponse{}, errors.New("connection password required")
	}
	if state.AccountToken != "" && mobilebridge.PasswordMatches(state.AccountTokenHash, password) {
		return RemoteHostAccountTokenResponse{HostID: b.HostID, Token: state.AccountToken, HostKey: b.hostPublicKey()}, nil
	}
	if !device && !mobilebridge.PasswordMatches(mobilebridge.HashPassword(state.Password), password) {
		return RemoteHostAccountTokenResponse{}, errors.New("connection password required")
	}
	accountID = strings.TrimSpace(accountID)
	if len(accountID) > 256 {
		accountID = ""
	}
	if accountID != "" && accountID == state.AccountTokenOwner && state.AccountToken != "" &&
		mobilebridge.PasswordMatches(state.AccountTokenHash, state.AccountToken) {
		return RemoteHostAccountTokenResponse{HostID: b.HostID, Token: state.AccountToken, HostKey: b.hostPublicKey()}, nil
	}
	setter, ok := b.LAN.(interface{ SetAccountTokenHash(string) })
	if !ok {
		return RemoteHostAccountTokenResponse{}, errors.New("account pairing is unavailable")
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		return RemoteHostAccountTokenResponse{}, err
	}
	token := hex.EncodeToString(secret)
	state.AccountTokenHash = mobilebridge.HashPassword(token)
	state.AccountToken = token
	state.AccountTokenOwner = accountID
	if err := mobilebridge.Save(b.ConfigPath, state); err != nil {
		return RemoteHostAccountTokenResponse{}, err
	}
	setter.SetAccountTokenHash(state.AccountTokenHash)
	b.nudgeTunnel()
	return RemoteHostAccountTokenResponse{HostID: b.HostID, Token: token, HostKey: b.hostPublicKey()}, nil
}

var (
	errRemoteAccessOff = errors.New("remote access is off; turn it on before pairing a device")
	errPairingCodeUsed = errors.New("the pairing code is used or expired; make a new one")
)

func (b *BridgeService) applyPasswordLogin(st mobilebridge.State) {
	if setter, ok := b.LAN.(interface{ SetPasswordLoginDisabled(bool) }); ok {
		setter.SetPasswordLoginDisabled(st.RequireDeviceCredentials)
	}
}

func (b *BridgeService) revokeAllDevices() error {
	if b.Codes != nil {
		b.Codes.Clear()
	}
	if b.Devices != nil {
		devices := b.Devices.List()
		if err := b.Devices.RevokeAll(); err != nil {
			return err
		}
		if lan, ok := b.LAN.(interface{ DisconnectCredential(string) }); ok {
			for _, device := range devices {
				lan.DisconnectCredential(device.TokenHash)
			}
		}
	}
	if b.PushDevices != nil {
		for _, device := range b.PushDevices.List() {
			_ = b.PushDevices.Delete(device.InstallID)
		}
	}
	return nil
}

func (b *BridgeService) NewPairingCode() (PairingCodeResponse, error) {
	if b.Codes == nil || b.Devices == nil {
		return PairingCodeResponse{}, errors.New("device pairing is unavailable")
	}
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return PairingCodeResponse{}, err
	}
	if !st.Enabled || !b.LAN.Running() {
		return PairingCodeResponse{}, errRemoteAccessOff
	}
	code, expiresAt, err := b.Codes.Issue()
	if err != nil {
		return PairingCodeResponse{}, err
	}
	return PairingCodeResponse{Code: code, ExpiresAt: expiresAt.UTC()}, nil
}

func (b *BridgeService) PairDevice(code, name, platform string) (PairDeviceResponse, error) {
	if b.Codes == nil || b.Devices == nil {
		return PairDeviceResponse{}, errors.New("device pairing is unavailable")
	}
	credential := string(requestscope.CredentialPassword)
	if code != "" {
		if !b.Codes.Consume(code) {
			return PairDeviceResponse{}, errPairingCodeUsed
		}
		credential = string(requestscope.CredentialPairingCode)
	}
	device, token, err := b.Devices.Issue(name, platform, time.Now())
	if err != nil {
		return PairDeviceResponse{}, err
	}
	return PairDeviceResponse{HostID: b.HostID, DeviceID: device.ID, Token: token, Credential: credential}, nil
}

func (b *BridgeService) SetRequireDeviceCredentials(on bool) (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	if st.RequireDeviceCredentials != on {
		st.RequireDeviceCredentials = on
		if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
			return MobileStatusResponse{}, err
		}
	}
	b.applyPasswordLogin(st)
	return b.Status(), nil
}

func (b *BridgeService) AccessDevices() []mobilebridge.AccessDevice {
	if b.Devices == nil {
		return nil
	}
	return b.Devices.List()
}

func (b *BridgeService) ConnectedCredentials() map[string]bool {
	if lan, ok := b.LAN.(interface{ ConnectedCredentials() map[string]bool }); ok {
		return lan.ConnectedCredentials()
	}
	return map[string]bool{}
}

func (b *BridgeService) AccountDevices() (tokenHash string, lastUsed time.Time) {
	st, _ := mobilebridge.Load(b.ConfigPath)
	if st.AccountTokenHash == "" || b.Devices == nil {
		return st.AccountTokenHash, time.Time{}
	}
	return st.AccountTokenHash, b.Devices.SharedLastUsed(mobilebridge.SharedAccount)
}

func (b *BridgeService) RevokeDevice(id string) (installID string, found bool, err error) {
	if b.Devices == nil {
		return "", false, nil
	}
	device, found, err := b.Devices.Revoke(id)
	if err != nil || !found {
		return "", found, err
	}
	if lan, ok := b.LAN.(interface{ DisconnectCredential(string) }); ok {
		lan.DisconnectCredential(device.TokenHash)
	}
	return device.InstallID, true, nil
}

func (b *BridgeService) UnlinkAccount() error {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return err
	}
	if st.AccountTokenHash == "" {
		return nil
	}
	st.AccountTokenHash, st.AccountToken, st.AccountTokenOwner = "", "", ""
	if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
		return err
	}
	if setter, ok := b.LAN.(interface{ SetAccountTokenHash(string) }); ok {
		setter.SetAccountTokenHash("")
	}
	b.nudgeTunnel()
	return nil
}

func (b *BridgeService) nudgeTunnel() {
	if n, ok := b.tunnel().(interface{ Nudge() }); ok {
		n.Nudge()
	}
}

func (b *BridgeService) Enable() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, _ := mobilebridge.Load(b.ConfigPath)
	if st.LoopbackOnly && b.LAN.Running() {
		return MobileStatusResponse{}, errors.New("disable the loopback-only listener before enabling LAN access")
	}

	pw, err := mobilebridge.GeneratePassword()
	if err != nil {
		return MobileStatusResponse{}, err
	}
	return b.enableWithPasswordLocked(pw, false, false)
}

func (b *BridgeService) EnableLANOnly() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	if st.LoopbackOnly && b.LAN.Running() {
		return MobileStatusResponse{}, errors.New("disable the loopback-only listener before enabling LAN access")
	}
	if t := b.tunnel(); t != nil {
		t.Stop()
	}
	if st.Enabled && b.LAN.Running() {
		if !st.NoPublicTunnel {
			st.NoPublicTunnel = true
			if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
				return MobileStatusResponse{}, err
			}
		}
		return b.Status(), nil
	}
	pw, err := mobilebridge.GeneratePassword()
	if err != nil {
		return MobileStatusResponse{}, err
	}
	return b.enableWithPasswordLocked(pw, true, false)
}

func (b *BridgeService) EnableTunnelOnly() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	if b.LAN.Running() {
		if !st.LoopbackOnly {
			return MobileStatusResponse{}, errors.New("listener already exposed on LAN; run `ao remote-host disable` before tunnel-only setup")
		}
		if _, err := b.LAN.StartLoopback(b.LAN.BoundPort()); err != nil {
			return MobileStatusResponse{}, err
		}
		if st.NoPublicTunnel {
			st.NoPublicTunnel = false
			if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
				return MobileStatusResponse{}, err
			}
		}
		if t := b.ensureTunnel(); t != nil {
			t.Start(b.LAN.BoundPort())
		}
		return b.Status(), nil
	}
	if st.ServeCleanupPending || (st.Enabled && st.SecurePairing) {
		return MobileStatusResponse{}, errors.New("tailscale Serve may still be active; inspect it and run `ao remote-host disable` before tunnel-only setup")
	}
	if st.SecurePairing {
		st.SecurePairing = false
		if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
			return MobileStatusResponse{}, err
		}
	}
	pw, err := mobilebridge.GeneratePassword()
	if err != nil {
		return MobileStatusResponse{}, err
	}
	return b.enableWithPasswordLocked(pw, false, true)
}

func (b *BridgeService) StartRemoteAccess() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	if !st.Enabled || !b.LAN.Running() {
		return b.Status(), nil
	}
	if st.NoPublicTunnel {
		st.NoPublicTunnel = false
		if err := mobilebridge.Save(b.ConfigPath, st); err != nil {
			return MobileStatusResponse{}, err
		}
	}
	if t := b.ensureTunnel(); t != nil {
		b.nudgeTunnel()
		t.Start(b.LAN.BoundPort())
	}
	b.nudgeListing()
	return b.Status(), nil
}

func (b *BridgeService) ConnectorInstalled() {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil || !st.Enabled || st.NoPublicTunnel || !b.LAN.Running() {
		return
	}
	if t := b.ensureTunnel(); t != nil {
		t.Start(b.LAN.BoundPort())
	}
}

func (b *BridgeService) Regenerate() (MobileStatusResponse, error) {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	pw, err := mobilebridge.GeneratePassword()
	if err != nil {
		return MobileStatusResponse{}, err
	}
	st, err := mobilebridge.Load(b.ConfigPath)
	if err != nil {
		return MobileStatusResponse{}, err
	}
	return b.enableWithPasswordLocked(pw, st.NoPublicTunnel, st.LoopbackOnly)
}

func (b *BridgeService) Disable() error {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if t := b.tunnel(); t != nil {
		t.Stop()
	}
	stopErr := b.LAN.Stop(ctx)
	if stopErr != nil && b.LAN.Running() {
		return stopErr
	}
	st, _ := mobilebridge.Load(b.ConfigPath)
	if st.SecurePairing || st.ServeCleanupPending {
		st.ServeCleanupPending = b.clearServe() != nil
	}
	st.Enabled = false
	return errors.Join(stopErr, mobilebridge.Save(b.ConfigPath, st))
}

func (b *BridgeService) ShutdownTunnel() {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	t := b.tunnel()
	if t == nil {
		return
	}
	t.Stop()
}

func (b *BridgeService) ShutdownKeepAwake() {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	if b.KeepAwake != nil {
		b.KeepAwake.Stop()
	}
}

func (b *BridgeService) ShutdownServe() {
	b.transitionMu.Lock()
	defer b.transitionMu.Unlock()

	st, _ := mobilebridge.Load(b.ConfigPath)
	if !st.Enabled || (!st.SecurePairing && !st.ServeCleanupPending) {
		return
	}
	_ = b.clearServe()
}
