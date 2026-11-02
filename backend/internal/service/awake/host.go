package awake

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// HostAdapter uses a distinct lease from mobile. Ending desktop wake cannot
// withdraw mobile's lease, or another daemon's native ownership.
type HostAdapter struct {
	control            *mobilebridge.KeepAwake
	binary             string
	mu                 sync.Mutex
	applications       []domain.AwakeApplication
	observed           time.Time
	native             *mobilebridge.LidAwake
	nativeGuard        bool
	nativeDegraded     atomic.Bool // registered helper needs an upgrade; idle wake uses caffeinate
	lowPowerControl    bool
	lowPowerAuthorized bool
	lid                bool
	display            bool
	audioControl       bool
	displayControl     bool
	keyboardControl    bool
	appPause           bool
	browsers           []domain.AwakeApplication
	knownNetworks      []string
}

// NewHost creates a desktop adapter with ownership separate from mobile.
func NewHost(pid int, binary, dataDir string) *HostAdapter {
	control := mobilebridge.NewKeepAwake(pid)
	native := mobilebridge.NewLidAwake(pid, binary, dataDir)
	control.SetLidController(native)
	supported, _, _ := native.Status()
	return &HostAdapter{control: control, binary: binary, native: native, nativeGuard: supported}
}

// Supported reports desktop wake availability.
func (h *HostAdapter) Supported() bool { return runtime.GOOS == "darwin" }

// Active reports current native wake ownership.
func (h *HostAdapter) Active() bool {
	if h.useNative() {
		return h.native.WakeActive()
	}
	return h.control.Active()
}

// Start starts a wake request.
func (h *HostAdapter) Start() error {
	if h.useNative() {
		return h.native.Start(false)
	}
	return h.control.Start()
}

// Stop releases this adapter's wake ownership.
func (h *HostAdapter) Stop() { h.control.Stop() }

// ConfigureLid updates this owner's closed-lid policy.
func (h *HostAdapter) ConfigureLid(on, setup bool) error {
	if h.useNative() {
		h.lid = on
		return h.native.ConfigurePolicy(on, h.display)
	}
	return h.control.ConfigureLid(on, setup)
}

// useNative reports whether wake requests go through the native coordinator.
func (h *HostAdapter) useNative() bool { return h.nativeGuard && !h.nativeDegraded.Load() }

// ErrLidHelperUpgrade marks a registered wake helper that must be upgraded
// before the native coordinator can be used.
var ErrLidHelperUpgrade = errors.New("registered wake helper needs an upgrade")

// SetupLid registers the supported native coordinator. When an older helper is
// registered, idle-only requests fall back to the unprivileged assertion path
// until setup succeeds again; the returned error wraps ErrLidHelperUpgrade.
func (h *HostAdapter) SetupLid() error {
	err := h.control.SetupLid()
	if mobilebridge.IsLidHelperUpgrade(err) {
		h.nativeDegraded.Store(true)
		return errors.Join(ErrLidHelperUpgrade, err)
	}
	if err == nil {
		h.nativeDegraded.Store(false)
	}
	return err
}

// LidStatus reports lid support, ownership, and failure reason.
func (h *HostAdapter) LidStatus() (bool, bool, string) { return h.control.LidStatus() }

// ConfigureIdle updates idle and display wake policy.
func (h *HostAdapter) ConfigureIdle(deadline time.Time, display bool) error {
	h.display = display
	if h.useNative() {
		return h.native.ConfigurePolicy(h.lid, display)
	}
	return h.control.ConfigureIdle(deadline, display)
}

// SetDeadline bounds native ownership by a policy deadline.
func (h *HostAdapter) SetDeadline(deadline time.Time) error { return h.control.SetDeadline(deadline) }

// Capabilities reports host operation availability.
func (h *HostAdapter) Capabilities() []domain.AwakeCapability {
	lid, _, reason := h.LidStatus()
	core := h.Supported()
	h.mu.Lock()
	lowPowerControl, lowPowerAuthorized := h.lowPowerControl, h.lowPowerAuthorized
	audio, apps := h.audioControl, h.appPause
	lights, keyboard := h.displayControl, h.keyboardControl
	browsers := len(h.browsers) > 0
	networks := len(h.knownNetworks) > 0
	fresh := time.Since(h.observed) < 15*time.Second
	h.mu.Unlock()
	return []domain.AwakeCapability{
		{ID: "native_guard", Available: h.nativeGuard, Reason: "Independent safety monitoring requires the desktop native helper and the default data directory."},
		{ID: "idle", Available: core, Reason: "Stay Awake requires macOS."},
		{ID: "lid", Available: lid, Reason: reason},
		{ID: "display_awake", Available: core, Reason: "Display assertions require macOS."},
		{ID: "low_power", Available: h.nativeGuard && lowPowerControl && lowPowerAuthorized && fresh, Reason: "Authorize Low Power Mode in Stay Awake settings first."},
		{ID: "low_power_revocation", Available: h.nativeGuard, Reason: "Revocation requires the supported default desktop helper."},
		{ID: "low_power_authorization", Available: h.nativeGuard && lowPowerControl && fresh, Reason: "Automatic Low Power Mode requires readable battery and charger power profiles on a supported Mac."},
		{ID: "display_restore", Available: lid && lights && fresh, Reason: "Owned display brightness control requires compatible native APIs on macOS 26–27."},
		{ID: "keyboard", Available: lid && keyboard && fresh, Reason: "Keyboard control requires compatible native APIs and a built-in backlight on macOS 26–27."},
		{ID: "audio", Available: lid && audio && fresh, Reason: "Owned audio-output recovery is not available on this host."},
		{ID: "pause_apps", Available: lid && apps && fresh, Reason: "Application pausing requires verified per-process recovery support."},
		{ID: "hotspot", Available: h.nativeGuard && networks && fresh, Reason: "Hotspot recovery requires a configured network and macOS Wi-Fi permission."},
		{ID: "agent_reconnect", Reason: "Automatic agent reconnection requires a verified interrupted transport and target consent."},
		{ID: "browser_actions", Available: h.nativeGuard && browsers && fresh, Reason: "Browser relaunch requires selecting a supported browser and confirming window closure."},
	}
}

// Observe reads fresh host conditions.
func (h *HostAdapter) Observe(ctx context.Context) (domain.AwakeObservation, error) {
	var result domain.AwakeObservation
	if runtime.GOOS != "darwin" || !filepath.IsAbs(h.binary) {
		return result, errors.New("desktop observation helper unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out, err := aoprocess.CommandContext(ctx, h.binary, "observe").Output()
	if err != nil {
		return result, err
	}
	if len(out) > 1048576 {
		return result, errors.New("oversized observation")
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return result, err
	}
	if addresses, err := net.InterfaceAddrs(); err == nil {
		result.NetworkAddresses = []string{}
		for _, address := range addresses {
			ip, _, err := net.ParseCIDR(address.String())
			if err == nil && !ip.IsLoopback() {
				result.NetworkAddresses = append(result.NetworkAddresses, ip.String())
			}
		}
	}
	if volumes, err := os.ReadDir("/Volumes"); err == nil {
		result.Devices = []string{}
		for _, volume := range volumes {
			if !strings.HasPrefix(volume.Name(), ".") {
				result.Devices = append(result.Devices, volume.Name())
			}
		}
	}
	h.mu.Lock()
	h.applications = append([]domain.AwakeApplication{}, result.Applications...)
	h.audioControl, h.appPause = result.AudioControl, result.AppPause
	h.displayControl, h.keyboardControl = result.DisplayControl, result.KeyboardControl
	h.browsers = append([]domain.AwakeApplication{}, result.Browsers...)
	h.knownNetworks = append([]string{}, result.KnownNetworks...)
	h.lowPowerControl, h.lowPowerAuthorized = result.LowPowerControl, result.LowPowerAuthorized
	h.observed = time.Now()
	h.mu.Unlock()
	return result, nil
}

// AppAlive checks the observed process identity.
func (h *HostAdapter) AppAlive(_ context.Context, id, identity string) (bool, string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.observed.IsZero() || time.Since(h.observed) > 15*time.Second {
		return false, "", errors.New("application observation unavailable")
	}
	for _, app := range h.applications {
		if app.ID == id && (identity == "" || identity == app.Identity) {
			return true, app.Identity, nil
		}
	}
	return false, "", nil
}

// BindingFuncs adapts daemon lifecycle callbacks.
type BindingFuncs struct {
	Interrupted func() []domain.AwakeAgentTarget
	Reconnect   func(context.Context, domain.AwakeAgentTarget) error
	Session     func(context.Context, string) (bool, error)
	Agent       func(context.Context) (bool, error)
	App         func(context.Context, string, string) (bool, string, error)
}

// Alive resolves a session or application lifetime.
func (b BindingFuncs) Alive(ctx context.Context, kind, id, identity string) (bool, string, error) {
	if kind == "session" {
		alive, err := b.Session(ctx, id)
		return alive, "", err
	}
	return b.App(ctx, id, identity)
}

// AgentActive reports whether an AO agent is working.
func (b BindingFuncs) AgentActive(ctx context.Context) (bool, error) { return b.Agent(ctx) }

// SetProtection sets the minimum battery level for native policy.
func (h *HostAdapter) SetProtection(percent int) error { return h.native.SetProtection(percent) }

// ConfigureEffects sets optional journaled native effects.
func (h *HostAdapter) ConfigureEffects(settings domain.AwakeSettings) error {
	if err := h.native.ConfigureLowPower(settings.LowPowerBatteryPercent, settings.LowPowerOnSeriousHeat); err != nil {
		return err
	}
	return h.native.ConfigureEffects(settings.MuteAudio, settings.PauseApps, settings.RestoreDisplay, settings.DarkenKeyboard)
}

// RequestRecovery requests restoration of AO-owned native effects.
func (h *HostAdapter) RequestRecovery() error { return h.native.RequestRecovery() }

// RecoveryReason reports pending restoration failures.
func (h *HostAdapter) RecoveryReason() string { return h.native.RecoveryReason() }

// RelaunchBrowser invokes the native supported-browser relaunch action.
func (h *HostAdapter) RelaunchBrowser(ctx context.Context, id string) error {
	if !h.nativeGuard {
		return errors.New("browser actions are disabled for custom or unsupported installations")
	}
	return h.runAction(ctx, "relaunch-browser", struct {
		BrowserID string `json:"browserID"`
	}{id})
}

// JoinNetwork attempts association with a saved macOS Wi-Fi profile.
func (h *HostAdapter) JoinNetwork(ctx context.Context, ssid string) error {
	if !h.nativeGuard {
		return errors.New("network actions are disabled for isolated or unsupported installations")
	}
	return h.runAction(ctx, "join-known-network", struct {
		SSID string `json:"ssid"`
	}{ssid})
}

// AuthorizeLowPower opens an explicit macOS approval for fixed power commands.
func (h *HostAdapter) AuthorizeLowPower(ctx context.Context) error {
	if !h.nativeGuard {
		return errors.New("low power mode authorization requires the default desktop installation")
	}
	return h.runActionTimeout(ctx, "authorize-low-power", struct{}{}, 120*time.Second)
}

// RevokeLowPower ensures the coordinator participates in the permission lock.
func (h *HostAdapter) RevokeLowPower(ctx context.Context) error {
	if !h.nativeGuard {
		return errors.New("revocation requires the default desktop installation")
	}
	if err := h.native.Setup(); err != nil {
		return err
	}
	return h.runActionTimeout(ctx, "revoke-low-power", struct{}{}, 135*time.Second)
}

func (h *HostAdapter) runAction(ctx context.Context, action string, request any) error {
	return h.runActionTimeout(ctx, action, request, 12*time.Second)
}
func (h *HostAdapter) runActionTimeout(ctx context.Context, action string, request any, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	command := aoprocess.CommandContext(ctx, h.binary, action)
	command.Stdin = bytes.NewReader(data)
	aoprocess.ConfigureTreeCancellation(command)
	command.WaitDelay = 3 * time.Second
	out, err := command.Output()
	var result struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	if json.Unmarshal(out, &result) != nil {
		return errors.New(action + " could not be confirmed")
	}
	if err != nil || !result.OK {
		if result.Error != "" {
			return errors.New(result.Error)
		}
		return errors.New(action + " failed")
	}
	return nil
}

// AgentRecoverySupported reports guarded reconnect callback availability.
func (b BindingFuncs) AgentRecoverySupported() bool {
	return b.Interrupted != nil && b.Reconnect != nil
}

// InterruptedAgents returns transport-loss recovery candidates.
func (b BindingFuncs) InterruptedAgents() []domain.AwakeAgentTarget {
	if b.Interrupted == nil {
		return nil
	}
	return b.Interrupted()
}

// ReconnectAgent delegates a guarded controller reconnect.
func (b BindingFuncs) ReconnectAgent(ctx context.Context, target domain.AwakeAgentTarget) error {
	return b.Reconnect(ctx, target)
}
