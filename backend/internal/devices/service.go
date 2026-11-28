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

// Lease is one session's device.
type Lease struct {
	SessionID    domain.SessionID
	Provider     string
	Device       ports.DeviceInfo
	Reason       string
	AgentControl ports.DeviceAgentControl
	OpenedAt     time.Time
	// Suspended is set while the device is shut down for going unused: the
	// lease still holds it, and the next use starts it again.
	Suspended bool
	// Starting is set, in a live lease, while AO boots the device.
	Starting bool
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
	// MemoryBudget is how much memory running devices may hold together, in
	// bytes; zero is half the machine's memory.
	MemoryBudget int64
	// IdleSuspendAfter is how long a leased device may go unused before it is
	// shut down; zero is defaultIdleSuspendAfter. Memory pressure shortens it.
	IdleSuspendAfter time.Duration
	// MemoryPressure reads the machine's memory pressure; nil reads the OS.
	MemoryPressure func() MemoryPressure
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
	// ownedBoot holds, for an owned simulator, the boot AO started it on (its
	// BootID). A device someone shut down and started again outside AO is on
	// another boot: it is theirs now, and AO no longer sleeps, parks, evicts or
	// powers it off (forgetRebootedLocked).
	ownedBoot map[string]string
	// parked holds the owned devices (by deviceKey) whose lease ended with the
	// device still running, and the session that last held each. A parked
	// device waits for another session to take it; once its session has ended
	// and nobody did, retireParked powers it off, so a simulator AO started
	// does not run on for nobody.
	parked map[string]parkedDevice
	// listed caches the provider's device list for LiveLease, which viewers
	// poll every couple of seconds.
	listed   []ports.DeviceInfo
	listedAt time.Time
	// booting holds the devices (by deviceKey) an Open is booting. The
	// simulator reports booted as soon as it starts, but its stream is only
	// ready when the boot returns; a viewer that connected in between got a
	// black picture.
	booting map[string]bool
	// bootGen counts finished boots of devices that were off. Open compares it
	// across its device listing: a boot in between makes that listing's "off"
	// stale, so it must not drop ownership on it.
	bootGen uint64
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
	// used is when each session last used its device through the daemon, and
	// viewers how many stream and touch connections watch it now; together
	// with the agent's own command they tell an unused device (suspendIdle).
	used    map[domain.SessionID]time.Time
	viewers map[domain.SessionID]int
	// bootDone holds, per deviceKey, a channel closed when the boot under way
	// ends (see boot).
	bootDone map[string]chan struct{}
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

// parkedDevice is an owned device no session holds, and the session that
// last held it.
type parkedDevice struct {
	session domain.SessionID
	device  ports.DeviceInfo
	// at is when it was parked (or, after a daemon restart, when the daemon
	// started): a parked device unused for the idle timeout is powered off.
	at time.Time
}

// retireTimeout bounds one reaper pass of powering off parked devices: a
// simulator can take a while to shut down.
const retireTimeout = 2 * time.Minute

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
	if l.Suspended {
		// Shut down already: nothing to park or power off, and whoever boots
		// it next owns it.
		s.disownLocked(deviceKey(l.Device))
	} else {
		// In the same save as the lease removal: a daemon that stops before
		// finishRelease must still power the device off once the session ends.
		s.parkLocked(id, l.Device)
	}
	s.saveLocked()
	return l, true
}

// parkLocked records an owned device whose lease by session id ended, for
// retireParked. A device this service did not boot is never parked. Callers
// hold s.mu and save.
func (s *Service) parkLocked(id domain.SessionID, device ports.DeviceInfo) {
	key := deviceKey(device)
	if !s.owned[key] {
		return
	}
	if s.parked == nil {
		s.parked = map[string]parkedDevice{}
	}
	s.parked[key] = parkedDevice{session: id, device: device, at: s.cfg.Now()}
}

// disownLocked forgets that this service booted the device under key.
// Callers hold s.mu and save.
func (s *Service) disownLocked(key string) {
	delete(s.owned, key)
	delete(s.ownedBoot, key)
}

// noteOwnBootLocked records the boot this service just started the owned
// device under key on. Callers hold s.mu and save.
func (s *Service) noteOwnBootLocked(key string, booted ports.DeviceInfo) {
	if booted.BootID == "" {
		return
	}
	if s.ownedBoot == nil {
		s.ownedBoot = map[string]string{}
	}
	s.ownedBoot[key] = booted.BootID
}

// forgetRebootedLocked disowns each listed device that runs on another boot
// than the one this service started it on: shut down and started again
// outside AO (by its user in Xcode, or by another tool), it is not AO's to
// put to sleep or power off any more. A lease on it stays, as on any device
// AO did not boot. Devices an open or release is busy with are skipped: their
// boot is AO's own and is recorded when it ends. Callers hold s.mu.
func (s *Service) forgetRebootedLocked(devices []ports.DeviceInfo) {
	changed := false
	for _, d := range devices {
		key := deviceKey(d)
		own, recorded := s.ownedBoot[key]
		if !recorded || !d.Booted || d.BootID == "" || d.BootID == own || s.busyLocked(key) {
			continue
		}
		s.log.Info("leave a device restarted outside AO running", "device", d.ID)
		s.disownLocked(key)
		delete(s.parked, key)
		changed = true
	}
	if changed {
		s.saveLocked()
	}
}

// forgetOrphansLocked disowns devices that no lease or parked record holds,
// which nothing would ever release: an older daemon saved them so. Left
// owned, a session later handed one that someone else started meanwhile
// would put it to sleep. Callers hold s.mu.
func (s *Service) forgetOrphansLocked() {
	held := map[string]bool{}
	for _, l := range s.leases {
		held[deviceKey(l.Device)] = true
	}
	for key := range s.parked {
		held[key] = true
	}
	changed := false
	for key := range s.owned {
		if !held[key] {
			s.disownLocked(key)
			changed = true
		}
	}
	if changed {
		s.saveLocked()
	}
}

// stopWatchLocked closes the hub connections opened under the session's lease.
func (s *Service) stopWatchLocked(id domain.SessionID) {
	if w, ok := s.watches[id]; ok {
		w.cancel()
		delete(s.watches, id)
	}
}

// finishRelease frees the driver's claims on a device whose lease (held by
// session id) ended, shuts it down when asked, and only then lets another
// session take it. A device this service booted that is still running is
// parked under the session, for retireParked to power off once that session
// ends.
func (s *Service) finishRelease(ctx context.Context, id domain.SessionID, device ports.DeviceInfo, shutdown bool) error {
	key := deviceKey(device)
	defer func() {
		s.mu.Lock()
		delete(s.releasing, key)
		s.mu.Unlock()
	}()
	s.releaseControl(ctx, device, "")
	var err error
	if shutdown {
		err = s.cfg.Provider.Shutdown(ctx, device)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case shutdown && err == nil:
		// Off now: whoever boots it next owns it, not this service.
		s.disownLocked(key)
		delete(s.parked, key)
	case s.owned[key]:
		s.parkLocked(id, device)
	default:
		return err
	}
	s.saveLocked()
	return err
}

// New builds the service. Nothing starts until SetAvailable turns devices on.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.MemoryBudget <= 0 {
		cfg.MemoryBudget = defaultMemoryBudget()
	}
	if cfg.IdleSuspendAfter <= 0 {
		cfg.IdleSuspendAfter = defaultIdleSuspendAfter
	}
	if cfg.MemoryPressure == nil {
		cfg.MemoryPressure = readMemoryPressure
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	leases, owned, ownedBoot, parked := loadLeases(cfg.DataDir, cfg.Now())
	s := &Service{cfg: cfg, log: cfg.Log, state: SetupOff, leases: leases, owned: owned, ownedBoot: ownedBoot, parked: parked}
	s.forgetOrphansLocked()
	// When each device was last used is not saved: a restart (an app update)
	// counts as a use, so it does not put every device in use to sleep.
	for id := range leases {
		s.touchLocked(id)
	}
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
			suspend, cancel := context.WithTimeout(ctx, retireTimeout)
			s.suspendIdle(suspend)
			cancel()
			// Here, not in pruneEnded: shutting a simulator down takes seconds,
			// and Status and Open must not wait for it.
			retire, cancel := context.WithTimeout(ctx, retireTimeout)
			s.retireParked(retire)
			cancel()
		}
	}
}

// retireParked powers off the parked devices whose session ended, or that
// stayed parked for the idle timeout, and that no other session took since. A device that is already off is only forgotten.
// It needs the provider up to list and shut devices down, so until setup is
// ready the devices wait. A failed shutdown is retried on the next pass.
func (s *Service) retireParked(ctx context.Context) {
	if s.cfg.Sessions == nil {
		return
	}
	s.mu.Lock()
	if s.state != SetupReady {
		s.mu.Unlock()
		return
	}
	candidates := make([]parkedDevice, 0, len(s.parked))
	for key, p := range s.parked {
		if s.idleLocked(key) {
			candidates = append(candidates, p)
		}
	}
	s.mu.Unlock()
	var ended []parkedDevice
	now := s.cfg.Now()
	after := s.idleAfter(s.cfg.MemoryPressure())
	for _, p := range candidates {
		// Parked past the idle timeout, nobody took it back: it is only
		// holding memory, whether or not its session lives on.
		if now.Sub(p.at) >= after {
			ended = append(ended, p)
			continue
		}
		if info, err := s.cfg.Sessions(ctx, p.session); sessionGone(info, err) {
			ended = append(ended, p)
		}
	}
	if len(ended) == 0 {
		return
	}
	// Per platform: a list that succeeded while one platform failed lacks that
	// platform's devices, and a running device it misses must not read as off.
	lists := map[ports.DevicePlatform][]ports.DeviceInfo{}
	failed := map[ports.DevicePlatform]bool{}
	for _, p := range ended {
		key, platform := deviceKey(p.device), p.device.Platform
		if _, done := lists[platform]; !done && !failed[platform] {
			devices, err := s.listPlatform(ctx, platform)
			if err != nil {
				s.log.Debug("keep parked devices: list failed", "platform", platform, "err", err)
				failed[platform] = true
			} else {
				lists[platform] = devices
				s.mu.Lock()
				s.forgetRebootedLocked(devices)
				s.mu.Unlock()
			}
		}
		if failed[platform] {
			continue
		}
		// As listed now: an emulator runs under a new serial after a restart,
		// and the old one may belong to another emulator.
		var listed ports.DeviceInfo
		running := false
		for _, d := range lists[platform] {
			if deviceKey(d) == key {
				listed, running = d, d.Booted
				break
			}
		}
		s.mu.Lock()
		if cur, still := s.parked[key]; !still || cur.session != p.session || !cur.at.Equal(p.at) || !s.idleLocked(key) {
			s.mu.Unlock()
			continue
		}
		if !running {
			// Already off (or deleted): nothing to power off, only forget.
			delete(s.parked, key)
			s.disownLocked(key)
			s.listed = nil
			s.saveLocked()
			s.mu.Unlock()
			continue
		}
		// The records stay saved until the shutdown succeeds, so a daemon that
		// stops meanwhile retries it; releasing keeps every open off the device.
		if s.releasing == nil {
			s.releasing = map[string]bool{}
		}
		s.releasing[key] = true
		s.mu.Unlock()
		err := s.cfg.Provider.Shutdown(ctx, listed)
		s.mu.Lock()
		delete(s.releasing, key)
		if err == nil {
			delete(s.parked, key)
			s.disownLocked(key)
			s.listed = nil
			s.saveLocked()
		}
		s.mu.Unlock()
		if err != nil {
			s.log.Warn("power off a parked device", "session", p.session, "device", listed.ID, "err", err)
			continue
		}
		s.log.Info("powered off a parked device", "session", p.session, "device", listed.ID)
	}
}

// listPlatform lists one platform's devices, failing when that platform's own
// listing failed (see ports.DevicePlatformLister).
func (s *Service) listPlatform(ctx context.Context, platform ports.DevicePlatform) ([]ports.DeviceInfo, error) {
	if lister, ok := s.cfg.Provider.(ports.DevicePlatformLister); ok {
		return lister.ListPlatform(ctx, platform)
	}
	all, err := s.cfg.Provider.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]ports.DeviceInfo, 0, len(all))
	for _, d := range all {
		if d.Platform == platform {
			out = append(out, d)
		}
	}
	return out, nil
}

// idleLocked reports whether no session holds, reserves, boots or releases
// the device with this deviceKey. Callers hold s.mu.
func (s *Service) idleLocked(key string) bool {
	if _, held := s.ownerOfLocked(key); held {
		return false
	}
	_, reserved := s.reserved[key]
	return !reserved && !s.booting[key] && !s.releasing[key]
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
// keeps running here; one this service booted is parked, and the reaper
// powers it off (retireParked).
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
			_ = s.finishRelease(ctx, id, lease.Device, false)
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
	s.touchLocked(id)
	s.mu.Unlock()
	if has && (req.DeviceID == "" || req.DeviceID == existing.Device.ID) && (req.Platform == "" || req.Platform == existing.Device.Platform) {
		current := existing.Device
		if listed, ok := s.listedDevice(ctx, existing.Device); ok {
			// As listed now: a stopped emulator goes by its AVD name, not the
			// serial it ran under, and boots by that name.
			current = listed
		}
		if existing.Suspended {
			current.Booted = false
		}
		if !current.Booted {
			// Asleep, or shut down outside AO: starting it again takes memory
			// like any other boot.
			listed, err := s.cfg.Provider.List(ctx)
			if err != nil {
				return Lease{}, apierr.Unavailable("DEVICE_LIST_FAILED", err.Error())
			}
			s.mu.Lock()
			s.forgetRebootedLocked(listed)
			plan, err := s.planRoomLocked(id, current, nil, listed)
			if err == nil {
				s.markRoomLocked(plan)
			}
			s.mu.Unlock()
			if err != nil {
				return Lease{}, err
			}
			// Committed to starting it: a caller that stops waiting (the phone
			// gives up on a request after a while) must not cancel the boot half
			// way, which would shut the device down again and leave the session
			// with nothing. The provider bounds each boot itself.
			ctx = context.WithoutCancel(ctx)
			s.makeRoom(ctx, plan)
		}
		device, err := s.boot(ctx, current)
		if err == nil {
			lease, err := s.confirmReopen(id, existing, device)
			if err == nil && !current.Booted {
				// Started again (woken, or off since an outside shutdown): claims
				// from before the restart are stale, and freeing them here starts
				// the agent's driver from the daemon. Left to the agent's own first
				// command, the driver starts inside the agent's shell, where it
				// could not reach the simulators.
				s.releaseControl(ctx, lease.Device, id)
			}
			return lease, err
		}
		// A device deleted since it was leased never boots again: let the lease
		// go and open another, rather than fail every open until a close.
		if !s.releaseIfGone(ctx, id, existing) {
			return Lease{}, apierr.Unavailable("DEVICE_BOOT_FAILED", err.Error())
		}
	}
	var workspace string
	if s.cfg.Sessions != nil {
		if info, err := s.cfg.Sessions(ctx, id); err == nil {
			workspace = info.WorkspacePath
		}
	}
	in := ResolveInput{
		Requested:        req.Platform,
		ProjectPlatforms: DetectPlatforms(workspace),
		Local:            s.cfg.Provider.Platforms(ctx),
	}
	// Whether the device it would pick fits the memory budget, or can once
	// idle devices are shut down: a running one costs nothing more, a new
	// boot its platform's weight. A pick that fails (the named device is
	// taken) says why below, so it does not count as a full machine.
	platform := pickPlatform(in)
	in.LocalSlotFree = true
	var roomErr error
	var devices []ports.DeviceInfo
	var listGen uint64
	if availability(in.Local, platform).Available {
		s.mu.Lock()
		listGen = s.bootGen
		s.mu.Unlock()
		listed, err := s.cfg.Provider.List(ctx)
		if err != nil {
			return Lease{}, apierr.Unavailable("DEVICE_LIST_FAILED", err.Error())
		}
		devices = listed
		s.mu.Lock()
		s.forgetStoppedLocked(devices, listGen)
		s.forgetRebootedLocked(devices)
		if candidate, err := s.pickLocked(devices, platform, req.DeviceID, id); err == nil {
			_, roomErr = s.planRoomLocked(id, candidate, s.leasePtrLocked(id), devices)
			if apiErr := (*apierr.Error)(nil); errors.As(roomErr, &apiErr) {
				in.LocalSlotFree, in.LocalFull = false, apiErr.Message
			}
		}
		s.mu.Unlock()
	}
	decision := Resolve(in)
	if !decision.OK() {
		if roomErr != nil && decision.Fallback == in.LocalFull {
			return Lease{}, roomErr // no room right now: keep its code, which says so
		}
		return Lease{}, apierr.Conflict("NO_DEVICE_AVAILABLE", decision.Fallback, map[string]any{"platform": string(decision.Platform)})
	}
	if devices == nil || decision.Platform != platform {
		s.mu.Lock()
		listGen = s.bootGen
		s.mu.Unlock()
		listed, err := s.cfg.Provider.List(ctx)
		if err != nil {
			return Lease{}, apierr.Unavailable("DEVICE_LIST_FAILED", err.Error())
		}
		devices = listed
	}
	// Pick, check the budget and reserve under one lock, before booting: two
	// opens racing for the last room or the same device must not both boot one.
	s.mu.Lock()
	s.forgetStoppedLocked(devices, listGen)
	s.forgetRebootedLocked(devices)
	device, err := s.pickLocked(devices, decision.Platform, req.DeviceID, id)
	var plan roomPlan
	if err == nil {
		plan, err = s.planRoomLocked(id, device, s.leasePtrLocked(id), devices)
	}
	if err != nil {
		s.mu.Unlock()
		return Lease{}, err
	}
	s.markRoomLocked(plan)
	key := deviceKey(device)
	if s.reserved == nil {
		s.reserved = map[string]domain.SessionID{}
	}
	s.reserved[key] = id
	s.mu.Unlock()
	// Committed, like a reopen above: the boot finishes and the lease is
	// written even if the caller goes away meanwhile.
	ctx = context.WithoutCancel(ctx)
	s.makeRoom(ctx, plan)
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
			s.disownLocked(key)
			delete(s.parked, key)
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
		if previous.Suspended {
			// Asleep, so off already: nothing to park.
			s.disownLocked(deviceKey(previous.Device))
		} else {
			s.parkLocked(id, previous.Device)
		}
	}
	if !wasBooted {
		s.owned[key] = true
		s.noteOwnBootLocked(key, booted)
	}
	// Held again: its last session ending no longer powers it off.
	delete(s.parked, key)
	delete(s.reserved, key)
	committed = true
	s.leases[id] = lease
	s.touchLocked(id)
	s.saveLocked()
	s.mu.Unlock()
	s.setAgentAsleep(id, false)
	if replaced {
		// Shut down rather than parked when the budget needed its memory.
		shutdown := plan.previous != "" && plan.previous == deviceKey(previous.Device)
		_ = s.finishRelease(ctx, id, previous.Device, shutdown)
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

// forgetStoppedLocked drops this service's claim on idle devices it booted
// that are off now: whoever boots one next owns it. Kept, a simulator the user
// later boots themselves would read as AO's, to take over and power off.
// listGen is bootGen from before the listing began: when a boot finished
// since, "off" may be stale and nothing is dropped this time. Callers hold
// s.mu.
func (s *Service) forgetStoppedLocked(devices []ports.DeviceInfo, listGen uint64) {
	if s.bootGen != listGen {
		return
	}
	changed := false
	for _, d := range devices {
		key := deviceKey(d)
		if d.Booted || !s.owned[key] || !s.idleLocked(key) {
			continue
		}
		s.disownLocked(key)
		delete(s.parked, key)
		changed = true
	}
	if changed {
		s.saveLocked()
	}
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
	if updated.Suspended {
		updated.Suspended = false
		s.setAgentAsleep(id, false)
	}
	s.leases[id] = updated
	s.touchLocked(id)
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
	s.disownLocked(deviceKey(held.Device))
	ended, _ := s.endLeaseLocked(id)
	s.listed = nil
	s.mu.Unlock()
	_ = s.finishRelease(ctx, id, ended.Device, false)
	s.log.Info("released the lease of a device that no longer exists", "session", id, "device", held.Device.ID)
	return true
}

// sameDevice says whether a listed device is the leased one: the same
// deviceKey, so an emulator matches by its AVD name, whatever serial it runs
// under now or another emulator took since.
func sameDevice(listed, leased ports.DeviceInfo) bool {
	return deviceKey(listed) == deviceKey(leased)
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
		s.bootDone = map[string]chan struct{}{}
	}
	// By deviceKey: a stopped emulator boots by its AVD name while its lease
	// still holds the serial it last ran under.
	key := deviceKey(device)
	if done, underWay := s.bootDone[key]; underWay {
		// Another open is booting it (the Device tab and the phone waking the
		// same sleeping device at once). Booting it again would reset the
		// helper of the simulator that boot just started, which shuts it down:
		// wait, then take it as it is now.
		s.mu.Unlock()
		select {
		case <-done:
		case <-ctx.Done():
			return device, ctx.Err()
		}
		if listed, ok := s.listedDevice(ctx, device); ok {
			device = listed
		}
		return s.boot(ctx, device)
	}
	done := make(chan struct{})
	s.booting[key] = true
	s.bootDone[key] = done
	s.mu.Unlock()
	booted, err := s.cfg.Provider.Boot(ctx, device)
	s.mu.Lock()
	delete(s.booting, key)
	delete(s.bootDone, key)
	close(done)
	s.bootGen++
	s.listed = nil // the next LiveLease reads the device as it is now
	if err == nil && s.owned[key] {
		// Woken: AO's own new boot, recorded before anyone can list it as
		// a boot outside AO.
		s.noteOwnBootLocked(key, booted)
		s.saveLocked()
	}
	s.mu.Unlock()
	return booted, err
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
				_ = s.finishRelease(ctx, id, ended.Device, false)
			}
			return Lease{}, false
		} else if err != nil {
			s.log.Debug("keep device lease: session lookup failed", "session", id, "err", err)
		}
	}
	if listed, found := s.listedDevice(ctx, l.Device); found {
		l.Device.Booted = listed.Booted
		// Viewers poll this while they watch: a reboot outside AO is noticed
		// within seconds, before anyone taps into a dead touch input.
		if watcher, ok := s.cfg.Provider.(ports.DeviceBootWatcher); ok && listed.Booted {
			watcher.NoteBooted(listed)
		}
	}
	s.mu.Lock()
	l.Starting = s.booting[deviceKey(l.Device)]
	if l.Starting || l.Suspended {
		l.Device.Booted = false
	}
	s.mu.Unlock()
	return l, true
}

// listedDevice finds a leased device in the provider's (briefly cached) list.
// A running emulator is listed by its serial and a stopped one by its AVD
// name, so Android matches on the name (see deviceKey).
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
			s.forgetRebootedLocked(listed)
			s.mu.Unlock()
		}
	}
	// By deviceKey: an emulator is its AVD, not the serial it last ran under,
	// which another emulator may hold now (woken from sleep, the leased AVD
	// must not be taken for that one).
	key := deviceKey(device)
	for _, d := range devices {
		if deviceKey(d) == key {
			return d, true
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

// leasePtrLocked is the session's lease, or nil. Callers hold s.mu.
func (s *Service) leasePtrLocked(id domain.SessionID) *Lease {
	if l, ok := s.leases[id]; ok {
		return &l
	}
	return nil
}

// errAsleep refuses stream work on a sleeping device: only opening it, or
// device work (install, launch, screenshot, the agent's command), starts it.
func errAsleep(l Lease) error {
	return apierr.Conflict("DEVICE_ASLEEP", l.Device.Name+" was shut down while unused. Open it to start it again.", nil)
}

// awake returns the session's lease for device work with its device running:
// one put to sleep for going unused starts again first.
func (s *Service) awake(ctx context.Context, id domain.SessionID) (Lease, error) {
	l, err := s.requireLease(id)
	if err != nil {
		return Lease{}, err
	}
	s.touch(id)
	if !l.Suspended {
		return l, nil
	}
	return s.Open(ctx, id, OpenRequest{})
}

// Close releases the session's device, optionally shutting it down. One this
// service booted that is left running is powered off when the session ends,
// unless another session took it meanwhile.
func (s *Service) Close(ctx context.Context, id domain.SessionID, shutdown bool) error {
	s.mu.Lock()
	l, ok := s.endLeaseLocked(id)
	s.mu.Unlock()
	if !ok {
		return nil
	}
	// Asleep, it is off already: shutting it down again would only fail.
	if err := s.finishRelease(ctx, id, l.Device, shutdown && !l.Suspended); err != nil {
		return apierr.Unavailable("DEVICE_SHUTDOWN_FAILED", "Released "+l.Device.Name+", but it did not shut down: "+err.Error())
	}
	return nil
}

// Install installs a built app on the session's device.
func (s *Service) Install(ctx context.Context, id domain.SessionID, artifactPath string) (Lease, error) {
	l, err := s.awake(ctx, id)
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
	l, err := s.awake(ctx, id)
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
	l, err := s.awake(ctx, id)
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
	if l.Suspended {
		return errAsleep(l)
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
	if l.Suspended {
		return errAsleep(l)
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
	if l.Suspended {
		return ports.DeviceHubEndpoint{}, Lease{}, nil, errAsleep(l)
	}
	// A killed session's viewer must not keep driving the device: the stream
	// and touch socket are where it would.
	if s.cfg.Sessions != nil {
		if info, err := s.cfg.Sessions(ctx, id); sessionGone(info, err) {
			s.mu.Lock()
			ended, held := s.endLeaseLocked(id)
			s.mu.Unlock()
			if held {
				_ = s.finishRelease(ctx, id, ended.Device, false)
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
