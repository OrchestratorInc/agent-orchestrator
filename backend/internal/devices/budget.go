package devices

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Devices are limited by the memory they hold, not by how many sessions hold
// one: a simulator holds about 2.2 GB and an emulator 3.5 to 4.9 GB (measured
// on Apple silicon, Nov 2026), and a session that switched devices can keep
// its last one running. Each running device counts at its platform's weight
// against a budget of half the machine's memory.
const (
	iosDeviceMemory     int64 = 2560 << 20
	androidDeviceMemory int64 = 4608 << 20
	// fallbackMemory stands in for the machine's memory when it cannot be read.
	fallbackMemory int64 = 16 << 30
)

// defaultIdleSuspendAfter is how long a leased device may go unused (no
// viewer, no command) before it is shut down to free its memory. Its lease
// stays, and the next use starts it again.
const defaultIdleSuspendAfter = 15 * time.Minute

// evictIdleAfter is how long another session's device must have gone unused
// before an open may shut it down early to make room.
const evictIdleAfter = 2 * time.Minute

// MemoryPressure is how hard the machine is pressed for memory, as its OS
// reports it. Half the machine's memory is the budget at most; under pressure
// devices give memory back sooner, whatever their share of it.
type MemoryPressure int

// The pressure levels.
const (
	PressureNormal MemoryPressure = iota
	// PressureWarning: unused devices sleep after warningIdleSuspendAfter, and
	// a new device only starts in place of an idle one.
	PressureWarning
	// PressureCritical: devices unused for criticalIdleSuspendAfter sleep at
	// once, and no new device starts.
	PressureCritical
)

const (
	warningIdleSuspendAfter  = 5 * time.Minute
	criticalIdleSuspendAfter = time.Minute
)

// idleAfter is how long a device may go unused before it sleeps, shorter
// under memory pressure.
func (s *Service) idleAfter(pressure MemoryPressure) time.Duration {
	after := s.cfg.IdleSuspendAfter
	switch pressure {
	case PressureCritical:
		after = min(after, criticalIdleSuspendAfter)
	case PressureWarning:
		after = min(after, warningIdleSuspendAfter)
	}
	return after
}

// codeNoRoom refuses a device because the devices running now fill the
// machine's room for them: the refusal clears by itself once one of them goes
// idle, sleeps or is released, so a view may wait for it rather than ask the
// user to try again. Anything else that stops a start is not worth waiting out.
const codeNoRoom = "NO_DEVICE_ROOM"

// deviceMemory is what a running device of this platform counts as.
func deviceMemory(platform ports.DevicePlatform) int64 {
	if platform == ports.DevicePlatformAndroid {
		return androidDeviceMemory
	}
	return iosDeviceMemory
}

// defaultMemoryBudget is half the machine's memory.
func defaultMemoryBudget() int64 {
	total := physicalMemory()
	if total <= 0 {
		total = fallbackMemory
	}
	return total / 2
}

// platformOfKey reads the platform back from a deviceKey.
func platformOfKey(key string) ports.DevicePlatform {
	platform, _, _ := strings.Cut(key, ":")
	return ports.DevicePlatform(platform)
}

// runningDevice is one device counted against the budget.
type runningDevice struct {
	key     string
	name    string
	session domain.SessionID
	leased  bool
}

// usageLocked sums the memory of the devices AO keeps running: leased ones
// that are not suspended, parked ones, and ones an open reserved or is
// booting. A leased or parked device the listing (when given) shows off holds
// nothing: shut down outside AO, or left from an earlier run. Callers hold
// s.mu.
func (s *Service) usageLocked(listed []ports.DeviceInfo) (int64, []runningDevice) {
	on := func(ports.DeviceInfo) bool { return true }
	if listed != nil {
		booted := map[string]bool{}
		for _, d := range listed {
			if d.Booted {
				booted[deviceKey(d)] = true
			}
		}
		on = func(d ports.DeviceInfo) bool { return booted[deviceKey(d)] }
	}
	seen := map[string]bool{}
	var running []runningDevice
	var total int64
	add := func(key, name string, session domain.SessionID, leased bool) {
		if seen[key] {
			return
		}
		seen[key] = true
		total += deviceMemory(platformOfKey(key))
		running = append(running, runningDevice{key: key, name: name, session: session, leased: leased})
	}
	for _, l := range s.sortedLeasesLocked() {
		if !l.Suspended && on(l.Device) {
			add(deviceKey(l.Device), l.Device.Name, l.SessionID, true)
		}
	}
	for key, p := range s.parked {
		if on(p.device) {
			add(key, p.device.Name, p.session, false)
		}
	}
	for key, owner := range s.reserved {
		add(key, "", owner, false)
	}
	for key := range s.booting {
		add(key, "", "", false)
	}
	return total, running
}

// roomPlan is what an open shuts down to fit its device in the budget.
type roomPlan struct {
	// previous is the deviceKey of the session's own last device, which this
	// open replaces, when it is shut down rather than parked.
	previous string
	parked   []parkedDevice
	// suspend holds other sessions' idle leases, marked suspended already.
	suspend []Lease
}

// planRoomLocked decides whether device fits in the memory budget for session
// id, given the provider's current listing, and what to shut down when it
// does not: the session's own device this
// open replaces (replacing, when set), then parked devices oldest first, then
// other sessions' devices unused for evictIdleAfter, least recently used
// first. A machine running nothing may always start one device. Nothing is
// marked; markRoomLocked does that. Callers hold s.mu.
func (s *Service) planRoomLocked(id domain.SessionID, device ports.DeviceInfo, replacing *Lease, listed []ports.DeviceInfo) (roomPlan, error) {
	key := deviceKey(device)
	used, running := s.usageLocked(listed)
	counted := make(map[string]bool, len(running))
	for _, r := range running {
		counted[r.key] = true
	}
	for _, r := range running {
		if r.key == key {
			return roomPlan{}, nil // running and counted already (parked, or this open's own)
		}
	}
	if device.Booted {
		return roomPlan{}, nil // running already: taking it adds nothing
	}
	pressure := s.cfg.MemoryPressure()
	if pressure == PressureCritical {
		return roomPlan{}, apierr.Conflict("NO_DEVICE_AVAILABLE", "This machine is critically low on memory, so AO is not starting another device. Close other apps, or release a device, and try again.", map[string]any{"platform": string(device.Platform), "memoryPressure": "critical"})
	}
	need, budget := deviceMemory(device.Platform), s.cfg.MemoryBudget
	if pressure == PressureWarning && used > 0 {
		// Short on memory: a new device only starts in place of an idle one.
		budget = min(budget, used)
	}
	fits := func() bool { return used == 0 || used+need <= budget }
	var plan roomPlan
	if fits() {
		return plan, nil
	}
	if replacing != nil && !replacing.Suspended {
		prevKey := deviceKey(replacing.Device)
		if prevKey != key && counted[prevKey] && s.owned[prevKey] && !s.busyLocked(prevKey) {
			plan.previous = prevKey
			used -= deviceMemory(replacing.Device.Platform)
		}
	}
	if !fits() {
		parked := make([]parkedDevice, 0, len(s.parked))
		for k, p := range s.parked {
			if k != key && counted[k] && s.idleLocked(k) {
				parked = append(parked, p)
			}
		}
		sort.Slice(parked, func(i, j int) bool { return parked[i].at.Before(parked[j].at) })
		for _, p := range parked {
			if fits() {
				break
			}
			plan.parked = append(plan.parked, p)
			used -= deviceMemory(p.device.Platform)
		}
	}
	if !fits() {
		now := s.cfg.Now()
		var idle []Lease
		for owner, l := range s.leases {
			k := deviceKey(l.Device)
			if owner == id || !counted[k] || l.Suspended || !s.owned[k] || s.viewers[owner] > 0 || s.busyLocked(k) {
				continue
			}
			if now.Sub(s.lastActivityLocked(owner, l)) >= evictIdleAfter {
				idle = append(idle, l)
			}
		}
		sort.Slice(idle, func(i, j int) bool {
			return s.lastActivityLocked(idle[i].SessionID, idle[i]).Before(s.lastActivityLocked(idle[j].SessionID, idle[j]))
		})
		for _, l := range idle {
			if fits() {
				break
			}
			plan.suspend = append(plan.suspend, l)
			used -= deviceMemory(l.Device.Platform)
		}
	}
	if !fits() {
		if pressure == PressureWarning {
			return roomPlan{}, apierr.Conflict(codeNoRoom, "This machine is short on memory, so a new device only starts in place of an idle one, and none is idle: "+s.runningNames(running)+". Release one of them, or end its session, to open another.", map[string]any{"platform": string(device.Platform), "memoryPressure": "warning"})
		}
		return roomPlan{}, s.fullError(device.Platform, budget, running)
	}
	return plan, nil
}

// fullError says what fills the budget and who to ask.
func (s *Service) fullError(platform ports.DevicePlatform, budget int64, running []runningDevice) error {
	return apierr.Conflict(codeNoRoom, s.fullReason(budget, running)+" Release one of them, or end its session, to open another.", map[string]any{"platform": string(platform)})
}

// runningNames lists the devices counted against the budget, and who holds
// each.
func (s *Service) runningNames(running []runningDevice) string {
	names := make([]string, 0, len(running))
	for _, r := range running {
		name := r.name
		if name == "" {
			name = "a device starting"
		}
		switch {
		case r.leased:
			name += " (session " + string(r.session) + ")"
		case r.session != "" && r.name != "":
			name += " (released by session " + string(r.session) + ")"
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

// fullReason describes the devices filling the budget.
func (s *Service) fullReason(budget int64, running []runningDevice) string {
	var used int64
	for _, r := range running {
		used += deviceMemory(platformOfKey(r.key))
	}
	msg := fmt.Sprintf("Devices on this machine already use about %s of the %s AO lets them use", gigabytes(used), gigabytes(budget))
	if names := s.runningNames(running); names != "" {
		msg += ": " + names
	}
	return msg + "."
}

// gigabytes formats a byte count as "12 GB" or "2.5 GB".
func gigabytes(b int64) string {
	gb := math.Round(float64(b)/(1<<30)*10) / 10
	return strconv.FormatFloat(gb, 'f', -1, 64) + " GB"
}

// markRoomLocked claims what plan shuts down, so no other open takes or
// counts it: parked devices are held as releasing, and leases are marked
// suspended. Callers hold s.mu.
func (s *Service) markRoomLocked(plan roomPlan) {
	if s.releasing == nil {
		s.releasing = map[string]bool{}
	}
	for _, p := range plan.parked {
		s.releasing[deviceKey(p.device)] = true
	}
	for i, l := range plan.suspend {
		plan.suspend[i] = s.beginSuspendLocked(l.SessionID, l)
	}
	if len(plan.suspend) > 0 {
		s.saveLocked()
	}
}

// makeRoom shuts down what plan claimed. The session's previous device is
// shut down by the open itself, once it no longer holds it. A failed
// shutdown is logged: the open goes on, briefly over the budget.
func (s *Service) makeRoom(ctx context.Context, plan roomPlan) {
	for _, p := range plan.parked {
		key := deviceKey(p.device)
		listed, found := s.listedDevice(ctx, p.device)
		var err error
		if !found || listed.Booted {
			target := p.device
			if found {
				target = listed
			}
			err = s.cfg.Provider.Shutdown(ctx, target)
		}
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
			s.log.Warn("power off a parked device to make room", "device", p.device.ID, "err", err)
		} else {
			s.log.Info("powered off a parked device to make room", "device", p.device.ID)
		}
	}
	for _, l := range plan.suspend {
		s.finishSuspend(ctx, l.SessionID, l)
	}
}

// busyLocked reports whether an open, boot or release is under way for the
// device with this deviceKey. Callers hold s.mu.
func (s *Service) busyLocked(key string) bool {
	_, reserved := s.reserved[key]
	return reserved || s.booting[key] || s.releasing[key]
}

// touchLocked records that the session used its device now. Callers hold s.mu.
func (s *Service) touchLocked(id domain.SessionID) {
	if s.used == nil {
		s.used = map[domain.SessionID]time.Time{}
	}
	s.used[id] = s.cfg.Now()
}

func (s *Service) touch(id domain.SessionID) {
	s.mu.Lock()
	s.touchLocked(id)
	s.mu.Unlock()
}

// lastActivityLocked is when the session last used its device: the lease
// opening, a call through the daemon, a viewer, or the agent's own command.
// Callers hold s.mu.
func (s *Service) lastActivityLocked(id domain.SessionID, l Lease) time.Time {
	last := l.OpenedAt
	if u := s.used[id]; u.After(last) {
		last = u
	}
	if agent, ok := s.cfg.Provider.(ports.DeviceAgentActivity); ok {
		if a := agent.AgentLastUsed(string(id)); a.After(last) {
			last = a
		}
	}
	return last
}

// Watch records a viewer (a stream or touch socket) on the session's device
// until the returned func runs: a device someone watches is in use.
func (s *Service) Watch(id domain.SessionID) func() {
	s.mu.Lock()
	if s.viewers == nil {
		s.viewers = map[domain.SessionID]int{}
	}
	s.viewers[id]++
	s.touchLocked(id)
	s.mu.Unlock()
	done := false
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if done {
			return
		}
		done = true
		s.viewers[id]--
		if s.viewers[id] <= 0 {
			delete(s.viewers, id)
		}
		s.touchLocked(id)
	}
}

// beginSuspendLocked marks the lease suspended and holds its device until
// finishSuspend shuts it down. Callers hold s.mu and save.
func (s *Service) beginSuspendLocked(id domain.SessionID, l Lease) Lease {
	// Bound again, so the agent's command is one that starts a sleeping
	// device: a lease from before this daemon may hold an older command.
	if control, err := s.cfg.Provider.AgentControl(l.Device, string(id)); err == nil {
		l.AgentControl = control
	} else {
		s.log.Warn("rebind the agent's device command", "session", id, "err", err)
	}
	l.Suspended = true
	s.leases[id] = l
	if s.releasing == nil {
		s.releasing = map[string]bool{}
	}
	s.releasing[deviceKey(l.Device)] = true
	s.stopWatchLocked(id)
	return l
}

// finishSuspend shuts down a device whose lease beginSuspendLocked marked,
// and marks the agent's command asleep so its next run starts the device
// again. A failed shutdown takes the suspension back.
func (s *Service) finishSuspend(ctx context.Context, id domain.SessionID, l Lease) {
	key := deviceKey(l.Device)
	s.setAgentAsleep(id, true)
	listed, found := s.listedDevice(ctx, l.Device)
	var err error
	if !found || listed.Booted {
		target := l.Device
		if found {
			target = listed
		}
		err = s.cfg.Provider.Shutdown(ctx, target)
	}
	s.mu.Lock()
	delete(s.releasing, key)
	if err != nil {
		if cur, ok := s.leases[id]; ok && cur.Suspended && deviceKey(cur.Device) == key {
			cur.Suspended = false
			s.leases[id] = cur
			s.saveLocked()
		}
	}
	s.listed = nil
	s.mu.Unlock()
	if err != nil {
		s.setAgentAsleep(id, false)
		s.log.Warn("put an unused device to sleep", "session", id, "device", l.Device.ID, "err", err)
		return
	}
	s.log.Info("put an unused device to sleep", "session", id, "device", l.Device.ID)
}

func (s *Service) setAgentAsleep(id domain.SessionID, asleep bool) {
	agent, ok := s.cfg.Provider.(ports.DeviceAgentActivity)
	if !ok {
		return
	}
	if err := agent.SetAgentAsleep(string(id), asleep); err != nil {
		s.log.Warn("mark the agent's device command", "session", id, "asleep", asleep, "err", err)
	}
}

// suspendIdle shuts down the leased devices nobody used for the idle timeout
// (shorter under memory pressure).
// Only devices this service booted: one the user started is theirs to stop.
// idleLeaseLocked reports whether session id's lease is one to put to sleep:
// on a device AO booted, awake, unwatched, not busy, and unused for after.
// Callers hold s.mu.
func (s *Service) idleLeaseLocked(id domain.SessionID, l Lease, after time.Duration, now time.Time) bool {
	key := deviceKey(l.Device)
	if l.Suspended || !s.owned[key] || s.viewers[id] > 0 || s.busyLocked(key) {
		return false
	}
	return now.Sub(s.lastActivityLocked(id, l)) >= after
}

// anyIdleLocked reports whether any lease is one to put to sleep. Callers
// hold s.mu.
func (s *Service) anyIdleLocked(after time.Duration) bool {
	now := s.cfg.Now()
	for id, l := range s.leases {
		if s.idleLeaseLocked(id, l, after, now) {
			return true
		}
	}
	return false
}

func (s *Service) suspendIdle(ctx context.Context) {
	after := s.idleAfter(s.cfg.MemoryPressure())
	s.mu.Lock()
	if s.state != SetupReady {
		s.mu.Unlock()
		return
	}
	if !s.anyIdleLocked(after) {
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	// Only AO's own boot is put to sleep: a device started again outside AO
	// since is its user's. Without a list to tell, nothing sleeps this round.
	devices, err := s.cfg.Provider.List(ctx)
	if err != nil {
		s.log.Debug("keep devices awake: list failed", "err", err)
		return
	}
	s.mu.Lock()
	if s.state != SetupReady {
		s.mu.Unlock()
		return
	}
	s.forgetRebootedLocked(devices)
	now := s.cfg.Now()
	var idle []Lease
	for id, l := range s.leases {
		if s.idleLeaseLocked(id, l, after, now) {
			idle = append(idle, s.beginSuspendLocked(id, l))
		}
	}
	if len(idle) > 0 {
		s.saveLocked()
	}
	s.mu.Unlock()
	for _, l := range idle {
		s.finishSuspend(ctx, l.SessionID, l)
	}
}
