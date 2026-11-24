// Package devices gives AO sessions iOS Simulators and Android Emulators. It
// owns the user's opt-in, picks a provider and device for a session, and keeps
// one lease per session so parallel agents never share a device.
package devices

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// SetupState is where the user's opt-in stands.
type SetupState string

// Setup states, in the order a first opt-in passes through them.
const (
	SetupOff        SetupState = "off"
	SetupInstalling SetupState = "installing"
	SetupReady      SetupState = "ready"
	SetupError      SetupState = "error"
)

// defaultMaxLocalLeases caps concurrent local devices; each simulator or
// emulator holds several GB of RAM.
const defaultMaxLocalLeases = 3

// Lease is one session's device.
type Lease struct {
	SessionID    domain.SessionID
	Provider     string
	Device       ports.DeviceInfo
	Reason       string
	AgentControl ports.DeviceAgentControl
	OpenedAt     time.Time
}

// Status is the device picture shown in the Device tab.
type Status struct {
	// Available is false until the desktop app's Developer mode turns devices
	// on; see SetAvailable.
	Available bool
	Enabled   bool
	State     SetupState
	Error     string
	Platforms []ports.DevicePlatformAvailability
	Devices   []ports.DeviceInfo
	Leases    []Lease
}

// OpenRequest narrows what a session wants; both fields are optional.
type OpenRequest struct {
	Platform ports.DevicePlatform
	DeviceID string
}

// SessionInfo is what the service needs to know about a session.
type SessionInfo struct {
	WorkspacePath string
	Terminated    bool
}

// SessionLookup reads a session. It returns an error when the session does
// not exist.
type SessionLookup func(ctx context.Context, id domain.SessionID) (SessionInfo, error)

// Config wires the service.
type Config struct {
	DataDir  string
	Provider ports.DeviceProvider
	// Sessions detects project platforms and frees the leases of sessions
	// that ended. Nil skips both.
	Sessions SessionLookup
	Log      *slog.Logger
	// MaxLocalLeases defaults to 3.
	MaxLocalLeases int
	Now            func() time.Time
}

// Service is the device service.
type Service struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	state    SetupState
	setupErr string
	ready    chan struct{}
	leases   map[domain.SessionID]Lease
	// owned holds the devices this service booted (by deviceKey), which a
	// later session may reuse. A device someone else booted is not assumed
	// free to take.
	owned map[string]bool
	// listed caches the provider's device list for LiveLease, which viewers
	// poll every couple of seconds.
	listed   []ports.DeviceInfo
	listedAt time.Time
	// booting holds the devices (by deviceKey) an Open is booting. The
	// simulator reports booted as soon as it starts, but its stream is only
	// ready when the boot returns; a viewer that connected in between got a
	// black picture.
	booting map[string]bool
	// reserved holds the devices (by deviceKey) an Open picked and is booting
	// for a session that has no lease on it yet. Each counts against the cap
	// and keeps other opens off the device until the lease is written or the
	// open fails.
	reserved map[string]domain.SessionID
	// releasing holds the devices (by deviceKey) whose lease just ended while their driver
	// claims are released (and the device shut down, when asked). Until that
	// finishes no other session may take them, or the cleanup would hit the
	// new holder's device.
	releasing map[string]bool
	// watches ends the hub connections opened under a lease when it is
	// released or replaced, so a viewer cannot keep driving a device another
	// session now holds.
	watches map[domain.SessionID]leaseWatch
	// stopReaper ends the loop that frees ended sessions' devices.
	stopReaper context.CancelFunc
	// available is the Developer mode gate, off on every daemon boot until the
	// desktop app turns it on.
	available atomic.Bool
}

// errUnavailable refuses device work while the desktop app's Developer mode is off.
var errUnavailable = apierr.Conflict("DEVICES_UNAVAILABLE", "Devices are a Developer mode preview. Turn on Developer mode in Orchestrator's Settings > General to use them.", nil)

// reapEvery is how often leases of ended sessions are freed when nothing else
// asks about them. A variable for tests.
var reapEvery = 30 * time.Second

type leaseWatch struct {
	deviceID string
	ctx      context.Context
	cancel   context.CancelFunc
}

// Enabled reports whether the user opted into device support. Session prompts
// read it to decide whether to describe the device tools.
func Enabled(dataDir string) bool {
	_, err := os.Stat(markerPath(dataDir))
	return err == nil
}

func markerPath(dataDir string) string { return filepath.Join(dataDir, "devices", "enabled") }

// sessionGone reports whether a session lookup proves the session ended: it
// terminated or it does not exist. Any other lookup error (a cancelled
// request, a store hiccup) proves nothing, and the lease is kept.
func sessionGone(info SessionInfo, err error) bool {
	if err == nil {
		return info.Terminated
	}
	var apiErr *apierr.Error
	if errors.As(err, &apiErr) && apiErr.Code == "SESSION_NOT_FOUND" {
		return true
	}
	return errors.Is(err, ports.ErrSessionNotFound)
}

// endLeaseLocked removes the session's lease, closes the hub connections
// opened under it, and holds the device until finishRelease runs. Callers
// hold s.mu and must call finishRelease with the returned lease when ok.
func (s *Service) endLeaseLocked(id domain.SessionID) (Lease, bool) {
	l, ok := s.leases[id]
	if !ok {
		return Lease{}, false
	}
	delete(s.leases, id)
	s.stopWatchLocked(id)
	// The agent's command was bound to this lease; left as is, it would open a
	// new driver session on a device another session may take next. Under s.mu,
	// like every bind, so it cannot undo a newer lease's command.
	if revoker, ok := s.cfg.Provider.(ports.DeviceControlRevoker); ok {
		if err := revoker.RevokeControl(string(id)); err != nil {
			s.log.Warn("revoke device agent command", "session", id, "err", err)
		}
	}
	if s.releasing == nil {
		s.releasing = map[string]bool{}
	}
	s.releasing[deviceKey(l.Device)] = true
	s.saveLocked()
	return l, true
}

// stopWatchLocked closes the hub connections opened under the session's lease.
func (s *Service) stopWatchLocked(id domain.SessionID) {
	if w, ok := s.watches[id]; ok {
		w.cancel()
		delete(s.watches, id)
	}
}

// finishRelease frees the driver's claims on a device whose lease ended,
// shuts it down when asked, and only then lets another session take it.
func (s *Service) finishRelease(ctx context.Context, device ports.DeviceInfo, shutdown bool) error {
	defer func() {
		s.mu.Lock()
		delete(s.releasing, deviceKey(device))
		s.mu.Unlock()
	}()
	s.releaseControl(ctx, device, "")
	if !shutdown {
		return nil
	}
	return s.cfg.Provider.Shutdown(ctx, device)
}

// New builds the service. Nothing starts until SetAvailable turns devices on.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.MaxLocalLeases <= 0 {
		cfg.MaxLocalLeases = defaultMaxLocalLeases
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	leases, owned := loadLeases(cfg.DataDir)
	s := &Service{cfg: cfg, log: cfg.Log, state: SetupOff, leases: leases, owned: owned}
	if cfg.Sessions != nil {
		// A killed or archived session frees its device within half a minute
		// even when nobody opens the Device tab or another device.
		ctx, stop := context.WithCancel(context.Background())
		s.stopReaper = stop
		go s.reap(ctx, reapEvery)
	}
	return s
}

func (s *Service) reap(ctx context.Context, every time.Duration) {
	tick := time.NewTicker(every)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			pass, cancel := context.WithTimeout(ctx, every)
			s.pruneEnded(pass)
			cancel()
		}
	}
}

// SetAvailable turns device support on or off for every client: the desktop
// app sends its Developer mode here whenever it connects to the daemon. While
// off, everything but reading status and releasing a device is refused, and
// live streams end. Leases are kept, so turning it back on resumes them. When
// the user already opted in, turning it on starts the provider in the
// background so the first open does not pay for it.
func (s *Service) SetAvailable(available bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.available.Store(available)
	if !available {
		for id := range s.watches {
			s.stopWatchLocked(id)
		}
		return
	}
	if Enabled(s.cfg.DataDir) && s.state == SetupOff {
		s.startLocked()
	}
}

// Available reports whether device support is on (Developer mode).
func (s *Service) Available() bool { return s.available.Load() }

// Offered reports whether sessions should be told about the device tools:
// devices are available and the user opted in.
func (s *Service) Offered() bool { return s.Available() && Enabled(s.cfg.DataDir) }

// Setup records the opt-in and starts the provider. It returns at once; the
// Device tab polls Status while tools install.
func (s *Service) Setup(ctx context.Context) (Status, error) {
	if !s.Available() {
		return Status{}, errUnavailable
	}
	if err := nothingRuns(s.cfg.Provider.Platforms(ctx)); err != nil {
		return Status{}, err
	}
	if err := os.MkdirAll(filepath.Dir(markerPath(s.cfg.DataDir)), 0o750); err != nil {
		return Status{}, fmt.Errorf("record device opt-in: %w", err)
	}
	if err := os.WriteFile(markerPath(s.cfg.DataDir), []byte(s.cfg.Now().UTC().Format(time.RFC3339)+"\n"), 0o600); err != nil {
		return Status{}, fmt.Errorf("record device opt-in: %w", err)
	}
	s.mu.Lock()
	if s.state == SetupOff || s.state == SetupError {
		s.startLocked()
	}
	s.mu.Unlock()
	return s.Status(ctx), nil
}

// newerVersion reports whether OS version a ("iOS 26.5", "Android 15.0") is
// newer than b, comparing their numbers part by part. Unparseable sorts last.
func newerVersion(a, b string) bool {
	x, y := versionParts(a), versionParts(b)
	for i := 0; i < len(x) && i < len(y); i++ {
		if x[i] != y[i] {
			return x[i] > y[i]
		}
	}
	return len(x) > len(y)
}

func versionParts(v string) []int {
	var parts []int
	for _, field := range strings.FieldsFunc(v, func(r rune) bool { return r < '0' || r > '9' }) {
		n, err := strconv.Atoi(field)
		if err != nil {
			break
		}
		parts = append(parts, n)
	}
	return parts
}

// nothingRuns refuses setup on a machine where no platform can run: without
// Xcode or the Android SDK there is nothing to install the device tools for,
// so say what to install instead of downloading them.
func nothingRuns(platforms []ports.DevicePlatformAvailability) error {
	reasons := make([]string, 0, len(platforms))
	details := make([]map[string]any, 0, len(platforms))
	for _, p := range platforms {
		if p.Available {
			return nil
		}
		reasons = append(reasons, platformLabel(p.Platform)+": "+p.Reason)
		details = append(details, map[string]any{"platform": string(p.Platform), "available": false, "reason": p.Reason})
	}
	return apierr.Conflict("NO_DEVICE_PLATFORM",
		"This machine can't run a simulator or emulator yet. "+strings.Join(reasons, " "),
		map[string]any{"platforms": details})
}

// startLocked runs EnsureReady in the background. Callers hold s.mu, except
// New, which runs before the service is shared.
func (s *Service) startLocked() {
	s.state, s.setupErr = SetupInstalling, ""
	ready := make(chan struct{})
	s.ready = ready
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := s.cfg.Provider.EnsureReady(ctx)
		s.mu.Lock()
		if err != nil {
			s.state, s.setupErr = SetupError, err.Error()
			s.log.Warn("device support setup failed", "err", err)
		} else {
			s.state = SetupReady
		}
		s.mu.Unlock()
		close(ready)
	}()
}

// Status reports opt-in, platforms, devices and leases.
func (s *Service) Status(ctx context.Context) Status {
	s.pruneEnded(ctx)
	s.mu.Lock()
	out := Status{Available: s.Available(), Enabled: Enabled(s.cfg.DataDir), State: s.state, Error: s.setupErr, Leases: s.sortedLeasesLocked()}
	s.mu.Unlock()
	out.Platforms = s.cfg.Provider.Platforms(ctx)
	if out.State == SetupReady {
		if devices, err := s.cfg.Provider.List(ctx); err == nil {
			out.Devices = devices
		} else {
			out.Error = err.Error()
		}
	}
	return out
}

// pruneEnded frees the leases of sessions that terminated or no longer
// exist, so a killed session's device is free for the next one. The device
// keeps running.
func (s *Service) pruneEnded(ctx context.Context) {
	if s.cfg.Sessions == nil {
		return
	}
	s.mu.Lock()
	owners := make([]domain.SessionID, 0, len(s.leases))
	for id := range s.leases {
		owners = append(owners, id)
	}
	s.mu.Unlock()
	for _, id := range owners {
		info, err := s.cfg.Sessions(ctx, id)
		if !sessionGone(info, err) {
			if err != nil {
				s.log.Debug("keep device lease: session lookup failed", "session", id, "err", err)
			}
			continue
		}
		s.mu.Lock()
		lease, ok := s.endLeaseLocked(id)
		s.mu.Unlock()
		if ok {
			_ = s.finishRelease(ctx, lease.Device, false)
		}
	}
}

// releaseControl frees the agent driver's claims on a device. A failure only
// costs the next holder a DEVICE_IN_USE retry, so it is logged, not returned.
func (s *Service) releaseControl(ctx context.Context, device ports.DeviceInfo, keep domain.SessionID) {
	if err := s.cfg.Provider.ReleaseControl(ctx, device, string(keep)); err != nil {
		s.log.Warn("release device control", "device", device.ID, "err", err)
	}
	// A device handed back is full quality again, whatever a phone on a slow
	// link asked of it. Best effort: the next phone asks for its own.
	if keep == "" {
		if tuner, ok := s.cfg.Provider.(ports.DeviceStreamTuner); ok {
			if err := tuner.SetStreamQuality(ctx, device, ports.DeviceQualityHigh); err != nil {
				s.log.Debug("reset device stream quality", "device", device.ID, "err", err)
			}
		}
	}
}

func (s *Service) sortedLeasesLocked() []Lease {
	out := make([]Lease, 0, len(s.leases))
	for _, l := range s.leases {
		out = append(out, l)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OpenedAt.Before(out[j].OpenedAt) })
	return out
}

// awaitReady waits for an in-flight setup and reports its outcome.
func (s *Service) awaitReady(ctx context.Context) error {
	if !s.Available() {
		return errUnavailable
	}
	if !Enabled(s.cfg.DataDir) {
		return apierr.Conflict("DEVICES_DISABLED", "Device support is off. Turn it on in the Device tab or run `ao device setup`.", nil)
	}
	s.mu.Lock()
	if s.state == SetupOff || (s.state == SetupError && s.ready == nil) {
		s.startLocked()
	}
	ready := s.ready
	s.mu.Unlock()
	select {
	case <-ready:
	case <-ctx.Done():
		return apierr.Unavailable("DEVICES_NOT_READY", "Device support is still starting. Try again in a moment.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != SetupReady {
		// A failed setup retries on the next request.
		msg := s.setupErr
		s.ready = nil
		return apierr.Unavailable("DEVICES_SETUP_FAILED", "Device support could not start: "+msg)
	}
	return nil
}

// Open leases a device to a session, booting it when needed. Reopening returns
// the session's existing lease unless the request names a different device.
func (s *Service) Open(ctx context.Context, id domain.SessionID, req OpenRequest) (Lease, error) {
	if err := s.awaitReady(ctx); err != nil {
		return Lease{}, err
	}
	s.pruneEnded(ctx)
	s.mu.Lock()
	existing, has := s.leases[id]
	localInUse := len(s.leases)
	holders := make([]string, 0, len(s.leases))
	for owner := range s.leases {
		if owner != id {
			holders = append(holders, string(owner))
		}
	}
	s.mu.Unlock()
	sort.Strings(holders)
	if has && (req.DeviceID == "" || req.DeviceID == existing.Device.ID) && (req.Platform == "" || req.Platform == existing.Device.Platform) {
		current := existing.Device
		if listed, ok := s.listedDevice(ctx, existing.Device); ok {
			// As listed now: a stopped emulator goes by its AVD name, not the
			// serial it ran under, and boots by that name.
			current = listed
		}
		device, err := s.boot(ctx, current)
		if err == nil {
			return s.confirmReopen(id, existing, device)
		}
		// A device deleted since it was leased never boots again: let the lease
		// go and open another, rather than fail every open until a close.
		if !s.releaseIfGone(ctx, id, existing) {
			return Lease{}, apierr.Unavailable("DEVICE_BOOT_FAILED", err.Error())
		}
		has, localInUse = false, localInUse-1
	}
	var workspace string
	if s.cfg.Sessions != nil {
		if info, err := s.cfg.Sessions(ctx, id); err == nil {
			workspace = info.WorkspacePath
		}
	}
	decision := Resolve(ResolveInput{
		Requested:        req.Platform,
		ProjectPlatforms: DetectPlatforms(workspace),
		Local:            s.cfg.Provider.Platforms(ctx),
		LocalSlotFree:    has || localInUse < s.cfg.MaxLocalLeases,
		LocalCap:         s.cfg.MaxLocalLeases,
		LocalHolders:     holders,
	})
	if !decision.OK() {
		return Lease{}, apierr.Conflict("NO_DEVICE_AVAILABLE", decision.Fallback, map[string]any{"platform": string(decision.Platform)})
	}
	devices, err := s.cfg.Provider.List(ctx)
	if err != nil {
		return Lease{}, apierr.Unavailable("DEVICE_LIST_FAILED", err.Error())
	}
	// Pick, check the cap and reserve under one lock, before booting: two opens
	// racing for the last slot or the same device must not both boot one.
	s.mu.Lock()
	device, err := s.pickLocked(devices, decision.Platform, req.DeviceID, id)
	if err == nil {
		err = s.capLocked(id, decision.Platform)
	}
	if err != nil {
		s.mu.Unlock()
		return Lease{}, err
	}
	key := deviceKey(device)
	if s.reserved == nil {
		s.reserved = map[string]domain.SessionID{}
	}
	s.reserved[key] = id
	s.mu.Unlock()
	wasBooted := device.Booted
	// Until the lease is written, a failure gives back the reservation and
	// shuts down a device this open started, unless a lease now holds it.
	committed := false
	var started ports.DeviceInfo
	defer func() {
		if committed {
			return
		}
		s.mu.Lock()
		if s.reserved[key] == id {
			delete(s.reserved, key)
		}
		_, held := s.ownerOfLocked(key)
		stop := started.ID != "" && !wasBooted && !held
		if stop {
			delete(s.owned, key)
		}
		s.mu.Unlock()
		if stop {
			stopCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			defer cancel()
			if err := s.cfg.Provider.Shutdown(stopCtx, started); err != nil {
				s.log.Warn("shut down a device whose open failed", "device", started.ID, "err", err)
			}
		}
	}()
	booted, err := s.boot(ctx, device)
	if err != nil {
		return Lease{}, apierr.Unavailable("DEVICE_BOOT_FAILED", err.Error())
	}
	started = booted
	lease := Lease{
		SessionID: id,
		Provider:  decision.Provider,
		Device:    booted,
		Reason:    decision.Reason,
		OpenedAt:  s.cfg.Now().UTC(),
	}
	s.mu.Lock()
	if owner, clash := s.ownerOfLocked(key); clash && owner != id {
		s.mu.Unlock()
		return Lease{}, apierr.Conflict("DEVICE_IN_USE", booted.Name+" was just taken by session "+string(owner)+".", nil)
	}
	if s.releasing[key] {
		s.mu.Unlock()
		return Lease{}, apierr.Conflict("DEVICE_IN_USE", booted.Name+" is being released by another session. Try again in a moment.", nil)
	}
	previous, hadPrevious := s.leases[id]
	// Bound under s.mu, after the checks, like a reopen's rebind: the two
	// write the same per-session command file and must not interleave.
	control, err := s.cfg.Provider.AgentControl(booted, string(id))
	if err != nil {
		s.mu.Unlock()
		return Lease{}, agentControlFailed(booted, err)
	}
	lease.AgentControl = control
	replaced := hadPrevious && deviceKey(previous.Device) != key
	if replaced {
		s.stopWatchLocked(id)
		if s.releasing == nil {
			s.releasing = map[string]bool{}
		}
		s.releasing[deviceKey(previous.Device)] = true
	}
	if !wasBooted {
		s.owned[key] = true
	}
	delete(s.reserved, key)
	committed = true
	s.leases[id] = lease
	s.saveLocked()
	s.mu.Unlock()
	if replaced {
		_ = s.finishRelease(ctx, previous.Device, false)
	}
	// No other AO session holds this device now, so any other claim on it is
	// stale: an ended session, or one whose lease a daemon restart dropped.
	s.releaseControl(ctx, booted, id)
	return lease, nil
}

// deviceKey is a device's identity across boots, for ownership and busy
// checks: an emulator's serial (emulator-5554) changes every time it starts
// and another emulator can take it next, while its AVD name does not. A
// simulator's UDID never changes. The serial is only for addressing commands.
func deviceKey(d ports.DeviceInfo) string {
	if d.Platform == ports.DevicePlatformAndroid && d.Name != "" {
		return "android:" + d.Name
	}
	return string(d.Platform) + ":" + d.ID
}

// pickLocked picks a device for session id that no other session holds,
// reserved, is booting or is releasing. Callers hold s.mu.
func (s *Service) pickLocked(devices []ports.DeviceInfo, platform ports.DevicePlatform, wantID string, id domain.SessionID) (ports.DeviceInfo, error) {
	taken := map[string]domain.SessionID{}
	for owner, l := range s.leases {
		if owner != id {
			taken[deviceKey(l.Device)] = owner
		}
	}
	for key, owner := range s.reserved {
		if owner != id {
			taken[key] = owner
		}
	}
	// Busy: being released, or being booted (its listed state is stale, and
	// booting it again would reset it).
	busy := make(map[string]bool, len(s.releasing)+len(s.booting))
	for key := range s.releasing {
		busy[key] = true
	}
	for key := range s.booting {
		busy[key] = true
	}
	return pickDevice(devices, platform, wantID, taken, s.owned, busy)
}

// capLocked refuses a session without a lease once leases and other
// sessions' in-flight opens fill the machine's cap. Callers hold s.mu.
func (s *Service) capLocked(id domain.SessionID, platform ports.DevicePlatform) error {
	if _, has := s.leases[id]; has {
		return nil
	}
	holders := map[domain.SessionID]bool{}
	for owner := range s.leases {
		holders[owner] = true
	}
	for _, owner := range s.reserved {
		if owner != id {
			holders[owner] = true
		}
	}
	if len(holders) < s.cfg.MaxLocalLeases {
		return nil
	}
	return apierr.Conflict("NO_DEVICE_AVAILABLE", fmt.Sprintf("This machine already runs %d devices for other sessions, the most it runs at once. Release one of them, or end its session, to open another.", s.cfg.MaxLocalLeases), map[string]any{"platform": string(platform)})
}

// confirmReopen records a reopened lease once its device is up, unless the
// lease was released or its device taken while the boot ran.
func (s *Service) confirmReopen(id domain.SessionID, existing Lease, device ports.DeviceInfo) (Lease, error) {
	updated := existing
	updated.Device = device
	s.mu.Lock()
	defer s.mu.Unlock()
	// The boot took a while: the lease may have been released (and the
	// device taken by another session) meanwhile. Never bring it back.
	cur, still := s.leases[id]
	if !still || !cur.OpenedAt.Equal(existing.OpenedAt) || cur.Device.ID != existing.Device.ID {
		return Lease{}, apierr.Conflict("DEVICE_LEASE_CHANGED", "This session's device was released while it was starting. Open it again.", nil)
	}
	if owner, clash := s.ownerOfLocked(deviceKey(device)); (clash && owner != id) || s.releasing[deviceKey(device)] {
		return Lease{}, apierr.Conflict("DEVICE_IN_USE", device.Name+" is in use by another session.", nil)
	}
	if device.ID != existing.Device.ID {
		// A restarted emulator runs under a new serial: the agent's command is
		// bound to the old one, so bind it again. Only now, with the lease
		// confirmed and s.mu held: the command file is shared by every lease
		// this session holds, and a stale reopen must not rewrite a newer one.
		control, err := s.cfg.Provider.AgentControl(device, string(id))
		if err != nil {
			return Lease{}, agentControlFailed(device, err)
		}
		updated.AgentControl = control
		s.stopWatchLocked(id)
	}
	s.leases[id] = updated
	s.saveLocked()
	return updated, nil
}

func agentControlFailed(device ports.DeviceInfo, err error) error {
	return apierr.Unavailable("DEVICE_CONTROL_FAILED", "Could not set up the agent's command for "+device.Name+": "+err.Error())
}

// releaseIfGone ends the session's lease when its device no longer exists (a
// simulator deleted in Xcode, an AVD removed) and reports whether it did.
// Only a clean list of the device's platform that lacks it counts as gone.
func (s *Service) releaseIfGone(ctx context.Context, id domain.SessionID, held Lease) bool {
	var devices []ports.DeviceInfo
	var err error
	// A list where the device's own platform failed (simctl erroring while
	// emulators still list) says nothing about whether the device exists.
	if lister, ok := s.cfg.Provider.(ports.DevicePlatformLister); ok {
		devices, err = lister.ListPlatform(ctx, held.Device.Platform)
	} else {
		devices, err = s.cfg.Provider.List(ctx)
	}
	if err != nil {
		return false
	}
	for _, d := range devices {
		if sameDevice(d, held.Device) {
			return false
		}
	}
	s.mu.Lock()
	cur, ok := s.leases[id]
	if !ok || !cur.OpenedAt.Equal(held.OpenedAt) || cur.Device.ID != held.Device.ID {
		s.mu.Unlock()
		return false
	}
	delete(s.owned, deviceKey(held.Device))
	ended, _ := s.endLeaseLocked(id)
	s.listed = nil
	s.mu.Unlock()
	_ = s.finishRelease(ctx, ended.Device, false)
	s.log.Info("released the lease of a device that no longer exists", "session", id, "device", held.Device.ID)
	return true
}

// sameDevice says whether a listed device is the leased one. A running
// emulator is listed by its serial and a stopped one by its AVD name, so
// Android also matches on the name.
func sameDevice(listed, leased ports.DeviceInfo) bool {
	if listed.ID == leased.ID {
		return true
	}
	return leased.Platform == ports.DevicePlatformAndroid && listed.Platform == leased.Platform && listed.Name == leased.Name
}

// ownerOfLocked returns the session holding the device with this deviceKey.
func (s *Service) ownerOfLocked(key string) (domain.SessionID, bool) {
	for owner, l := range s.leases {
		if deviceKey(l.Device) == key {
			return owner, true
		}
	}
	return "", false
}

// pickDevice takes the named device. taken, owned and busy are by deviceKey. Otherwise it prefers a device this
// service already booted (cheap to reuse), then one that is shut down (booting
// it makes it ours), and only last one somebody else has running: other
// sessions and the user's own work share these machines, and a running
// simulator may be mid-use. Within each, a phone beats a tablet and the newest
// OS wins: what an app is mostly built for and tested on.
func pickDevice(devices []ports.DeviceInfo, platform ports.DevicePlatform, wantID string, taken map[string]domain.SessionID, owned, busy map[string]bool) (ports.DeviceInfo, error) {
	if wantID != "" {
		for _, d := range devices {
			if d.ID != wantID && d.Name != wantID {
				continue
			}
			if owner, ok := taken[deviceKey(d)]; ok {
				return ports.DeviceInfo{}, apierr.Conflict("DEVICE_IN_USE", d.Name+" is in use by session "+string(owner)+".", nil)
			}
			if busy[deviceKey(d)] {
				return ports.DeviceInfo{}, apierr.Conflict("DEVICE_IN_USE", d.Name+" is busy with another session (starting or being released). Try again in a moment.", nil)
			}
			return d, nil
		}
		return ports.DeviceInfo{}, apierr.NotFound("DEVICE_NOT_FOUND", "No simulator or emulator matches "+wantID+".")
	}
	var free []ports.DeviceInfo
	for _, d := range devices {
		if d.Platform != platform {
			continue
		}
		if _, ok := taken[deviceKey(d)]; ok || busy[deviceKey(d)] {
			continue
		}
		free = append(free, d)
	}
	sort.SliceStable(free, func(i, j int) bool {
		if free[i].Tablet != free[j].Tablet {
			return !free[i].Tablet
		}
		return newerVersion(free[i].Version, free[j].Version)
	})
	for _, want := range []func(ports.DeviceInfo) bool{
		func(d ports.DeviceInfo) bool { return d.Booted && owned[deviceKey(d)] },
		func(d ports.DeviceInfo) bool { return !d.Booted },
		func(ports.DeviceInfo) bool { return true },
	} {
		for _, d := range free {
			if want(d) {
				return d, nil
			}
		}
	}
	hint := "Create an iOS Simulator in Xcode (Window → Devices and Simulators)."
	if platform == ports.DevicePlatformAndroid {
		hint = "Create a virtual device in Android Studio's Device Manager."
	}
	return ports.DeviceInfo{}, apierr.Conflict("NO_FREE_DEVICE", "No free "+platformLabel(platform)+" device. "+hint, nil)
}

// Lease returns the session's lease.
func (s *Service) Lease(id domain.SessionID) (Lease, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.leases[id]
	return l, ok
}

// boot boots a device. One that was off reads as still starting until the
// boot returns; a running one (an agent reopening its device) is not touched,
// or its viewers would drop and reconnect.
func (s *Service) boot(ctx context.Context, device ports.DeviceInfo) (ports.DeviceInfo, error) {
	if device.Booted {
		return s.cfg.Provider.Boot(ctx, device)
	}
	s.mu.Lock()
	if s.booting == nil {
		s.booting = map[string]bool{}
	}
	// By deviceKey: a stopped emulator boots by its AVD name while its lease
	// still holds the serial it last ran under.
	key := deviceKey(device)
	s.booting[key] = true
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.booting, key)
		s.listed = nil // the next LiveLease reads the device as it is now
		s.mu.Unlock()
	}()
	return s.cfg.Provider.Boot(ctx, device)
}

// liveListTTL is how long LiveLease trusts the last device list.
const liveListTTL = 3 * time.Second

// LiveLease is the session's lease with the device's running state as it is
// now, not as it was when the lease opened: a simulator shut down outside AO
// shows as off, so viewers can say so instead of holding the last frame.
//
// A session that ended has no device: its lease is released here too, so a
// phone still showing it drops the stream instead of driving a device another
// session may now take.
func (s *Service) LiveLease(ctx context.Context, id domain.SessionID) (Lease, bool) {
	l, ok := s.Lease(id)
	if !ok {
		return l, false
	}
	if s.cfg.Sessions != nil {
		if info, err := s.cfg.Sessions(ctx, id); sessionGone(info, err) {
			s.mu.Lock()
			ended, held := s.endLeaseLocked(id)
			s.mu.Unlock()
			if held {
				_ = s.finishRelease(ctx, ended.Device, false)
			}
			return Lease{}, false
		} else if err != nil {
			s.log.Debug("keep device lease: session lookup failed", "session", id, "err", err)
		}
	}
	if listed, found := s.listedDevice(ctx, l.Device); found {
		l.Device.Booted = listed.Booted
	}
	s.mu.Lock()
	if s.booting[deviceKey(l.Device)] {
		l.Device.Booted = false
	}
	s.mu.Unlock()
	return l, true
}

// listedDevice finds a leased device in the provider's (briefly cached) list.
// A running emulator is listed by its serial and a stopped one by its AVD
// name, so Android also matches on the name.
func (s *Service) listedDevice(ctx context.Context, device ports.DeviceInfo) (ports.DeviceInfo, bool) {
	s.mu.Lock()
	devices, fresh := s.listed, s.cfg.Now().Sub(s.listedAt) < liveListTTL && s.listed != nil
	ready := s.state == SetupReady
	s.mu.Unlock()
	if !fresh && ready {
		if listed, err := s.cfg.Provider.List(ctx); err == nil {
			devices = listed
			s.mu.Lock()
			s.listed, s.listedAt = listed, s.cfg.Now()
			s.mu.Unlock()
		}
	}
	for _, d := range devices {
		if d.ID == device.ID {
			return d, true
		}
	}
	if device.Platform == ports.DevicePlatformAndroid {
		for _, d := range devices {
			if d.Platform == device.Platform && d.Name == device.Name {
				return d, true
			}
		}
	}
	return ports.DeviceInfo{}, false
}

// requireLease returns the session's lease for device work, which needs
// devices available.
func (s *Service) requireLease(id domain.SessionID) (Lease, error) {
	if !s.Available() {
		return Lease{}, errUnavailable
	}
	l, ok := s.Lease(id)
	if !ok {
		return Lease{}, apierr.NotFound("NO_DEVICE_LEASE", "This session has no device. Run `ao device open` first.")
	}
	return l, nil
}

// Close releases the session's device, optionally shutting it down.
func (s *Service) Close(ctx context.Context, id domain.SessionID, shutdown bool) error {
	s.mu.Lock()
	l, ok := s.endLeaseLocked(id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	if err := s.finishRelease(ctx, l.Device, shutdown); err != nil {
		return apierr.Unavailable("DEVICE_SHUTDOWN_FAILED", "Released "+l.Device.Name+", but it did not shut down: "+err.Error())
	}
	return nil
}

// Install installs a built app on the session's device.
func (s *Service) Install(ctx context.Context, id domain.SessionID, artifactPath string) (Lease, error) {
	l, err := s.requireLease(id)
	if err != nil {
		return Lease{}, err
	}
	if strings.TrimSpace(artifactPath) == "" || !filepath.IsAbs(artifactPath) {
		return Lease{}, apierr.Invalid("ARTIFACT_PATH_INVALID", "path must be an absolute path to a .app or .apk", nil)
	}
	info, statErr := os.Stat(artifactPath)
	if statErr != nil {
		return Lease{}, apierr.Invalid("ARTIFACT_NOT_FOUND", "No build at "+artifactPath, nil)
	}
	if msg := artifactMismatch(l.Device, artifactPath, info.IsDir()); msg != "" {
		return Lease{}, apierr.Invalid("ARTIFACT_WRONG_PLATFORM", msg, nil)
	}
	if err := s.cfg.Provider.Install(ctx, l.Device, artifactPath); err != nil {
		return Lease{}, apierr.Unavailable("DEVICE_INSTALL_FAILED", err.Error())
	}
	return l, nil
}

// artifactMismatch says why a build cannot go on this device, or "" when it
// can: a simulator takes the .app folder a simulator build makes, an emulator
// an .apk. Caught here, the agent gets the fix instead of a simctl or adb error.
func artifactMismatch(device ports.DeviceInfo, path string, isDir bool) string {
	name := filepath.Base(path)
	ext := strings.ToLower(filepath.Ext(path))
	switch device.Platform {
	case ports.DevicePlatformIOS:
		switch {
		case ext == ".app" && isDir:
			return ""
		case ext == ".ipa":
			return name + " is built for physical iPhones. Build for the simulator (-sdk iphonesimulator) and install the .app it makes on " + device.Name + "."
		case ext == ".apk" || ext == ".aab":
			return name + " is an Android build, and this session's device is " + device.Name + " (iOS). Install the simulator .app instead."
		default:
			return name + " is not an iOS Simulator app. Install the .app folder a simulator build makes on " + device.Name + "."
		}
	case ports.DevicePlatformAndroid:
		switch {
		case ext == ".apk" && !isDir:
			return ""
		case ext == ".aab":
			return name + " is an app bundle; build an .apk (for example ./gradlew assembleDebug) to install it on " + device.Name + "."
		case ext == ".app" || ext == ".ipa":
			return name + " is an iOS build, and this session's device is " + device.Name + " (Android). Install an .apk instead."
		default:
			return name + " is not an Android .apk. Install the .apk a Gradle build makes on " + device.Name + "."
		}
	}
	return ""
}

// Launch starts an installed app on the session's device.
func (s *Service) Launch(ctx context.Context, id domain.SessionID, appID string) (Lease, error) {
	l, err := s.requireLease(id)
	if err != nil {
		return Lease{}, err
	}
	if strings.TrimSpace(appID) == "" {
		return Lease{}, apierr.Invalid("APP_ID_REQUIRED", "appId is required", nil)
	}
	if err := s.cfg.Provider.Launch(ctx, l.Device, appID); err != nil {
		return Lease{}, apierr.Unavailable("DEVICE_LAUNCH_FAILED", err.Error())
	}
	return l, nil
}

// Screenshot returns a PNG of the session's device.
func (s *Service) Screenshot(ctx context.Context, id domain.SessionID) ([]byte, error) {
	l, err := s.requireLease(id)
	if err != nil {
		return nil, err
	}
	png, err := s.cfg.Provider.Screenshot(ctx, l.Device)
	if err != nil {
		return nil, apierr.Unavailable("DEVICE_SCREENSHOT_FAILED", err.Error())
	}
	return png, nil
}

// SetQuality retunes how much the session's device stream costs the link
// (high, balanced or low). It reports DEVICE_QUALITY_UNSUPPORTED when the
// provider cannot, and the setting resets when the lease ends.
func (s *Service) SetQuality(ctx context.Context, id domain.SessionID, quality ports.DeviceStreamQuality) error {
	if !quality.Valid() {
		return apierr.Invalid("DEVICE_QUALITY_INVALID", "quality must be high, balanced or low", nil)
	}
	l, err := s.requireLease(id)
	if err != nil {
		return err
	}
	tuner, ok := s.cfg.Provider.(ports.DeviceStreamTuner)
	if !ok {
		return apierr.Conflict("DEVICE_QUALITY_UNSUPPORTED", "This provider's stream has no quality setting.", nil)
	}
	if err := tuner.SetStreamQuality(ctx, l.Device, quality); err != nil {
		return apierr.Unavailable("DEVICE_QUALITY_FAILED", err.Error())
	}
	return nil
}

// PressButton presses a hardware button through the provider when it has a
// better way than the stream socket, and reports DEVICE_BUTTON_UNSUPPORTED
// when it does not so the caller sends it down the socket.
func (s *Service) PressButton(ctx context.Context, id domain.SessionID, button string) error {
	switch button {
	case "home", "back", "recents":
	default:
		return apierr.Invalid("DEVICE_BUTTON_INVALID", "button must be home, back or recents", nil)
	}
	l, err := s.requireLease(id)
	if err != nil {
		return err
	}
	presser, ok := s.cfg.Provider.(ports.DeviceButtonPresser)
	if !ok {
		return apierr.Conflict("DEVICE_BUTTON_UNSUPPORTED", "Send this button on the device's input socket.", nil)
	}
	// A phone that gives up waiting must not kill a driver that is mid-start:
	// the press finishes (the provider bounds it) whether or not anyone waits.
	if err := presser.PressButton(context.WithoutCancel(ctx), l.Device, string(id), button); err != nil {
		if errors.Is(err, ports.ErrDeviceButtonUnsupported) {
			return apierr.Conflict("DEVICE_BUTTON_UNSUPPORTED", "Send this button on the device's input socket.", nil)
		}
		return apierr.Unavailable("DEVICE_BUTTON_FAILED", err.Error())
	}
	return nil
}

// HubTarget returns the stream endpoint, the lease it may serve, and a
// context that ends when that lease is released or replaced: a hub connection
// must close with it.
func (s *Service) HubTarget(ctx context.Context, id domain.SessionID) (ports.DeviceHubEndpoint, Lease, context.Context, error) {
	l, err := s.requireLease(id)
	if err != nil {
		return ports.DeviceHubEndpoint{}, Lease{}, nil, err
	}
	// A killed session's viewer must not keep driving the device: the stream
	// and touch socket are where it would.
	if s.cfg.Sessions != nil {
		if info, err := s.cfg.Sessions(ctx, id); sessionGone(info, err) {
			s.mu.Lock()
			ended, held := s.endLeaseLocked(id)
			s.mu.Unlock()
			if held {
				_ = s.finishRelease(ctx, ended.Device, false)
			}
			return ports.DeviceHubEndpoint{}, Lease{}, nil, apierr.NotFound("NO_DEVICE_LEASE", "This session has ended and no longer holds a device.")
		}
	}
	endpoint, err := s.cfg.Provider.Hub(ctx)
	if err != nil {
		return ports.DeviceHubEndpoint{}, Lease{}, nil, apierr.Unavailable("DEVICE_HUB_UNAVAILABLE", err.Error())
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, ok := s.leases[id]
	if !ok || current.Device.ID != l.Device.ID {
		return ports.DeviceHubEndpoint{}, Lease{}, nil, apierr.NotFound("NO_DEVICE_LEASE", "This session has no device. Run `ao device open` first.")
	}
	w, ok := s.watches[id]
	if !ok || w.deviceID != current.Device.ID {
		if ok {
			w.cancel()
		}
		watchCtx, cancel := context.WithCancel(context.Background())
		w = leaseWatch{deviceID: current.Device.ID, ctx: watchCtx, cancel: cancel}
		if s.watches == nil {
			s.watches = map[domain.SessionID]leaseWatch{}
		}
		s.watches[id] = w
	}
	return endpoint, current, w.ctx, nil
}

// Shutdown stops the provider's helpers when the daemon exits.
func (s *Service) Shutdown() error {
	if s.stopReaper != nil {
		s.stopReaper()
	}
	return s.cfg.Provider.Close()
}
