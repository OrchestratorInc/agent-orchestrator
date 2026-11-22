package ports

import (
	"context"
	"errors"
)

// DevicePlatform names the mobile OS a simulator or emulator runs.
type DevicePlatform string

// The platforms device providers serve.
const (
	DevicePlatformIOS     DevicePlatform = "ios"
	DevicePlatformAndroid DevicePlatform = "android"
)

// DeviceInfo is one iOS Simulator or Android Emulator a provider can serve.
// ID is the simulator UDID or the emulator serial (the AVD name while it is
// not running).
type DeviceInfo struct {
	ID       string
	Name     string
	Platform DevicePlatform
	Version  string
	Booted   bool
	// Tablet marks an iPad or tablet emulator, which a pick without a named
	// device passes over for a phone.
	Tablet bool
}

// DevicePlatformAvailability reports whether a provider can run a platform,
// with a user-facing reason when it cannot.
type DevicePlatformAvailability struct {
	Platform  DevicePlatform
	Available bool
	Reason    string
}

// DeviceHubEndpoint is where the stream server that fronts a provider's
// devices answers, and the headers its requests must carry (a bearer token
// for the local hub, a vendor credential for a hosted one). The daemon proxies
// an allowlist of its routes and adds the headers; clients never hold them.
type DeviceHubEndpoint struct {
	Origin  string
	Headers map[string]string
}

// DeviceAgentControl is the agent-device command bound to one device and
// session: the agent runs it with a subcommand and nothing else, and
// concurrent sessions never drive each other's device. Args are extra flags
// for a provider whose command cannot carry its own binding; the local
// provider's wrapper needs none.
type DeviceAgentControl struct {
	Command string
	Args    []string
}

// DeviceProvider is the contract every device source implements: local
// simulators today, remote hosts and hosted device clouds later. Callers never
// learn where the device physically runs.
type DeviceProvider interface {
	// Name is the stable provider id shown to users, e.g. "local".
	Name() string
	// Platforms reports which platforms this provider can run right now.
	Platforms(ctx context.Context) []DevicePlatformAvailability
	// EnsureReady installs pinned tools and starts helper processes. It is
	// idempotent and must only run after the user opted in.
	EnsureReady(ctx context.Context) error
	List(ctx context.Context) ([]DeviceInfo, error)
	// Boot starts the device (a no-op when already booted) and its stream,
	// returning the authoritative device: an AVD's id becomes its serial.
	Boot(ctx context.Context, device DeviceInfo) (DeviceInfo, error)
	Shutdown(ctx context.Context, device DeviceInfo) error
	Install(ctx context.Context, device DeviceInfo, artifactPath string) error
	Launch(ctx context.Context, device DeviceInfo, appID string) error
	Screenshot(ctx context.Context, device DeviceInfo) ([]byte, error)
	Hub(ctx context.Context) (DeviceHubEndpoint, error)
	AgentControl(device DeviceInfo, sessionID string) (DeviceAgentControl, error)
	// ReleaseControl drops the agent driver's hold on a device for every AO
	// session except keepSessionID (empty releases all), so a device AO hands
	// to a new session is not still claimed by an ended one.
	ReleaseControl(ctx context.Context, device DeviceInfo, keepSessionID string) error
	Close() error
}

// DeviceStreamQuality is how much a device's stream may cost the link. Over a
// home network "high" is right; a phone on mobile data asks for less.
type DeviceStreamQuality string

// The stream qualities, from most to least bandwidth.
const (
	DeviceQualityHigh     DeviceStreamQuality = "high"
	DeviceQualityBalanced DeviceStreamQuality = "balanced"
	DeviceQualityLow      DeviceStreamQuality = "low"
)

// Valid reports whether q is one of the known qualities.
func (q DeviceStreamQuality) Valid() bool {
	return q == DeviceQualityHigh || q == DeviceQualityBalanced || q == DeviceQualityLow
}

// DeviceStreamTuner is implemented by a provider whose stream encoder can be
// retuned: resolution, frame rate and bitrate or JPEG quality. The setting
// belongs to the device's stream, so it applies to every viewer of it.
type DeviceStreamTuner interface {
	SetStreamQuality(ctx context.Context, device DeviceInfo, quality DeviceStreamQuality) error
}

// DeviceButtonPresser is implemented by a provider whose hardware buttons
// need more than the stream socket's button message. The hub's app switcher
// presses a Home button that a Face ID iPhone does not have, so it does
// nothing there; the provider's own driver opens the switcher properly.
type DeviceButtonPresser interface {
	// PressButton presses "home", "back" or "recents" on the device, or
	// returns ErrDeviceButtonUnsupported when the stream socket should carry it.
	PressButton(ctx context.Context, device DeviceInfo, sessionID, button string) error
}

// ErrDeviceButtonUnsupported means the provider leaves this button to the
// stream socket.
var ErrDeviceButtonUnsupported = errors.New("device button is not handled by the provider")
