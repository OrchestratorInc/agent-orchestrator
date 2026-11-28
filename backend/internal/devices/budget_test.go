package devices

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type testClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *testClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// budgetService is a ready service with the given memory budget, a clock the
// test moves, and sessions that all live on.
func budgetService(t *testing.T, p *fakeProvider, budget int64) (*Service, *testClock) {
	t.Helper()
	clock := &testClock{now: time.Unix(1_800_000_000, 0)}
	svc := newAvailable(Config{
		DataDir: t.TempDir(), Provider: p, MemoryBudget: budget, Now: clock.Now,
		Sessions: func(context.Context, domain.SessionID) (SessionInfo, error) { return SessionInfo{}, nil },
	})
	t.Cleanup(func() { _ = svc.Shutdown() })
	if _, err := svc.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	return svc, clock
}

// openOn opens a device for the session and marks it running in the fake's
// list, as a real boot would.
func openOn(t *testing.T, svc *Service, p *fakeProvider, id domain.SessionID, req OpenRequest) Lease {
	t.Helper()
	l, err := svc.Open(context.Background(), id, req)
	if err != nil {
		t.Fatalf("open for %s: %v", id, err)
	}
	p.setBooted(l.Device.ID, true)
	return l
}

func (f *fakeProvider) shutdownsSoFar() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.shutdowns...)
}

func (f *fakeProvider) isAsleep(session string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asleep[session]
}

// A parked device holds memory nobody uses: a new device that does not fit
// powers it off first.
func TestOpenPowersOffAParkedDeviceToMakeRoom(t *testing.T) {
	p := iosFleet()
	svc, _ := budgetService(t, p, iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	if err := svc.Close(context.Background(), "s-1", false); err != nil {
		t.Fatal(err)
	}
	if lease := openOn(t, svc, p, "s-2", OpenRequest{DeviceID: "C"}); lease.Device.ID != "C" {
		t.Fatalf("lease = %+v, want C", lease.Device)
	}
	if got := p.shutdownsSoFar(); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("shut down %v, want the parked A", got)
	}
	if _, parked := svc.parked["ios:A"]; parked {
		t.Fatal("A is still parked after it was powered off")
	}
}

// Another session's device that went unused a while is put to sleep, not
// released: its session keeps it, and it starts again on that session's next
// use.
func TestOpenPutsAnIdleDeviceToSleepToMakeRoom(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(evictIdleAfter + time.Second)
	openOn(t, svc, p, "s-2", OpenRequest{DeviceID: "C"})
	held, ok := svc.Lease("s-1")
	if !ok || !held.Suspended || held.Device.ID != "A" {
		t.Fatalf("s-1 lease = %+v (held %v), want A asleep", held, ok)
	}
	if got := p.shutdownsSoFar(); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("shut down %v, want A", got)
	}
	if !p.isAsleep("s-1") {
		t.Fatal("s-1's agent command was not marked asleep")
	}
}

// A device someone watches, or used moments ago, is never taken from under
// them: the open is refused and says what fills the machine.
func TestOpenLeavesWatchedAndRecentlyUsedDevicesRunning(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	done := svc.Watch("s-1")
	clock.advance(10 * time.Minute)
	_, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "C"})
	if apiCode(err) != "NO_DEVICE_ROOM" || !strings.Contains(err.Error(), "iPhone A (session s-1)") || !strings.Contains(err.Error(), "2.5 GB") {
		t.Fatalf("open while A is watched = %v, want NO_DEVICE_ROOM naming iPhone A and the budget", err)
	}
	done()
	clock.advance(time.Minute)
	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "C"}); apiCode(err) != "NO_DEVICE_ROOM" {
		t.Fatalf("open a minute after A was watched = %v, want NO_DEVICE_ROOM", err)
	}
	clock.advance(evictIdleAfter)
	openOn(t, svc, p, "s-2", OpenRequest{DeviceID: "C"})
	if got := p.shutdownsSoFar(); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("shut down %v, want A once it went unused", got)
	}
}

// A machine with nothing running may always start one device, whatever its
// budget.
func TestOneDeviceAlwaysFits(t *testing.T) {
	p := iosFleet()
	svc, _ := budgetService(t, p, 1)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	if _, err := svc.Open(context.Background(), "s-2", OpenRequest{DeviceID: "C"}); apiCode(err) != "NO_DEVICE_ROOM" {
		t.Fatalf("second open = %v, want NO_DEVICE_ROOM", err)
	}
}

// Taking a device that is already running costs no memory, so it is never
// refused for the budget.
func TestTakingARunningDeviceNeedsNoRoom(t *testing.T) {
	p := iosFleet()
	svc, _ := budgetService(t, p, iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	if lease := openOn(t, svc, p, "s-2", OpenRequest{DeviceID: "B"}); lease.Device.ID != "B" {
		t.Fatalf("lease = %+v, want the running B", lease.Device)
	}
	if got := p.shutdownsSoFar(); len(got) != 0 {
		t.Fatalf("shut down %v for a device that was already running", got)
	}
}

// On a full machine, a session moving to another device shuts its old one
// down instead of parking it.
func TestSwitchingDevicesOnAFullMachineShutsTheOldOneDown(t *testing.T) {
	p := iosFleet()
	svc, _ := budgetService(t, p, iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	if lease := openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "C"}); lease.Device.ID != "C" {
		t.Fatalf("lease = %+v, want C", lease.Device)
	}
	if got := p.shutdownsSoFar(); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("shut down %v, want the old A", got)
	}
	if len(svc.parked) != 0 {
		t.Fatalf("parked %v, want nothing", svc.parked)
	}
}

// With room to spare, switching keeps parking the old device, so switching
// back is instant.
func TestSwitchingDevicesWithRoomParksTheOldOne(t *testing.T) {
	p := iosFleet()
	svc, _ := budgetService(t, p, 3*iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "C"})
	if got := p.shutdownsSoFar(); len(got) != 0 {
		t.Fatalf("shut down %v, want A parked", got)
	}
	if _, parked := svc.parked["ios:A"]; !parked {
		t.Fatal("A was not parked")
	}
}

// Android weighs more than iOS: an emulator does not fit where a simulator
// would.
func TestAnEmulatorCountsForMoreThanASimulator(t *testing.T) {
	p := &fakeProvider{
		platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformIOS, Available: true}, {Platform: ports.DevicePlatformAndroid, Available: true}},
		devices: []ports.DeviceInfo{
			{ID: "A", Name: "iPhone A", Platform: ports.DevicePlatformIOS},
			{ID: "C", Name: "iPhone C", Platform: ports.DevicePlatformIOS},
			{ID: "pixel", Name: "pixel", Platform: ports.DevicePlatformAndroid},
		},
	}
	svc, _ := budgetService(t, p, 2*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	if _, err := svc.Open(ctx, "s-2", OpenRequest{Platform: ports.DevicePlatformAndroid}); apiCode(err) != "NO_DEVICE_ROOM" {
		t.Fatalf("emulator open = %v, want NO_DEVICE_ROOM", err)
	}
	openOn(t, svc, p, "s-2", OpenRequest{Platform: ports.DevicePlatformIOS})
}

// A device nobody used for the idle timeout is shut down; its lease holds,
// says the device is off, and the session's next use starts it again.
func TestAnUnusedDeviceSleepsAndWakesOnUse(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(defaultIdleSuspendAfter - time.Second)
	svc.suspendIdle(ctx)
	if held, _ := svc.Lease("s-1"); held.Suspended {
		t.Fatal("put to sleep before the idle timeout")
	}
	clock.advance(2 * time.Second)
	svc.suspendIdle(ctx)
	held, ok := svc.Lease("s-1")
	if !ok || !held.Suspended {
		t.Fatalf("lease = %+v (held %v), want A asleep", held, ok)
	}
	if got := p.shutdownsSoFar(); !slices.Equal(got, []string{"A"}) || !p.isAsleep("s-1") {
		t.Fatalf("shut down %v, asleep %v; want A off and the command asleep", got, p.isAsleep("s-1"))
	}
	if live, _ := svc.LiveLease(ctx, "s-1"); live.Device.Booted {
		t.Fatal("a sleeping device reads as on")
	}
	if _, _, _, err := svc.HubTarget(ctx, "s-1"); apiCode(err) != "DEVICE_ASLEEP" {
		t.Fatalf("stream of a sleeping device = %v, want DEVICE_ASLEEP", err)
	}
	if reloaded := New(Config{DataDir: svc.cfg.DataDir, Provider: p}); !reloaded.leases["s-1"].Suspended {
		t.Fatal("the sleep was not saved")
	}

	p.setBooted("A", false)
	if _, err := svc.Launch(ctx, "s-1", "com.example.app"); err != nil {
		t.Fatal(err)
	}
	held, _ = svc.Lease("s-1")
	if held.Suspended || held.Device.ID != "A" || p.isAsleep("s-1") {
		t.Fatalf("after use: lease %+v, asleep %v; want A awake", held, p.isAsleep("s-1"))
	}
	p.mu.Lock()
	lastRelease := p.released[len(p.released)-1]
	p.mu.Unlock()
	if lastRelease != "A keep=s-1" {
		t.Fatalf("last control release %q, want A's stale claims freed for s-1 after the wake", lastRelease)
	}
	p.mu.Lock()
	booted := append([]string(nil), p.booted...)
	p.mu.Unlock()
	if !slices.Equal(booted, []string{"A", "A"}) {
		t.Fatalf("booted %v, want A started again", booted)
	}
}

// The agent's own command, a viewer, or a call through the daemon each keep
// a device awake.
func TestADeviceInUseStaysAwake(t *testing.T) {
	ctx := context.Background()
	t.Run("agent command", func(t *testing.T) {
		p := iosFleet()
		svc, clock := budgetService(t, p, 3*iosDeviceMemory)
		openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
		clock.advance(defaultIdleSuspendAfter + time.Minute)
		p.mu.Lock()
		p.agentUsed = map[string]time.Time{"s-1": clock.Now().Add(-time.Minute)}
		p.mu.Unlock()
		svc.suspendIdle(ctx)
		if held, _ := svc.Lease("s-1"); held.Suspended {
			t.Fatal("slept while the agent drove it")
		}
	})
	t.Run("viewer", func(t *testing.T) {
		p := iosFleet()
		svc, clock := budgetService(t, p, 3*iosDeviceMemory)
		openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
		done := svc.Watch("s-1")
		defer done()
		clock.advance(time.Hour)
		svc.suspendIdle(ctx)
		if held, _ := svc.Lease("s-1"); held.Suspended {
			t.Fatal("slept while someone watched it")
		}
	})
	t.Run("daemon call", func(t *testing.T) {
		p := iosFleet()
		svc, clock := budgetService(t, p, 3*iosDeviceMemory)
		openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
		clock.advance(defaultIdleSuspendAfter - time.Minute)
		if _, err := svc.Screenshot(ctx, "s-1"); err != nil {
			t.Fatal(err)
		}
		clock.advance(2 * time.Minute)
		svc.suspendIdle(ctx)
		if held, _ := svc.Lease("s-1"); held.Suspended {
			t.Fatal("slept right after a screenshot")
		}
	})
}

// Only devices AO started are put to sleep: one the user booted is theirs.
func TestADeviceTheUserStartedNeverSleeps(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "B"})
	clock.advance(time.Hour)
	svc.suspendIdle(context.Background())
	if held, _ := svc.Lease("s-1"); held.Suspended || len(p.shutdownsSoFar()) != 0 {
		t.Fatalf("the user's B was put to sleep: %+v", held)
	}
}

// A device that would not shut down stays awake, and its command is not left
// marked asleep.
func TestAFailedSleepTakesItBack(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	p.mu.Lock()
	p.shutdownErr = errors.New("simulator busy")
	p.mu.Unlock()
	clock.advance(time.Hour)
	svc.suspendIdle(context.Background())
	if held, _ := svc.Lease("s-1"); held.Suspended || p.isAsleep("s-1") {
		t.Fatalf("lease %+v asleep %v after a failed shutdown; want it awake", held, p.isAsleep("s-1"))
	}
}

// Releasing a sleeping device does not try to shut it down again, and leaves
// nothing parked.
func TestReleasingASleepingDevice(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(time.Hour)
	svc.suspendIdle(ctx)
	if err := svc.Close(ctx, "s-1", true); err != nil {
		t.Fatalf("release with shutdown = %v", err)
	}
	if got := p.shutdownsSoFar(); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("shut down %v, want A once", got)
	}
	if len(svc.parked) != 0 || svc.owned["ios:A"] {
		t.Fatalf("parked %v owned %v, want neither", svc.parked, svc.owned)
	}
}

// A parked device nobody takes back is powered off after the idle timeout,
// even while its session lives on.
func TestAParkedDeviceRetiresAfterTheIdleTimeout(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	if err := svc.Close(ctx, "s-1", false); err != nil {
		t.Fatal(err)
	}
	svc.retireParked(ctx)
	if got := p.shutdownsSoFar(); len(got) != 0 {
		t.Fatalf("shut down %v right after parking", got)
	}
	clock.advance(defaultIdleSuspendAfter)
	svc.retireParked(ctx)
	if got := p.shutdownsSoFar(); !slices.Equal(got, []string{"A"}) {
		t.Fatalf("shut down %v, want the parked A", got)
	}
}

// A leased device that is off (shut down outside AO, or left from an earlier
// run) holds no memory and fills no room.
func TestALeasedDeviceThatIsOffHoldsNoRoom(t *testing.T) {
	p := iosFleet()
	svc, _ := budgetService(t, p, iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	p.setBooted("A", false)
	openOn(t, svc, p, "s-2", OpenRequest{DeviceID: "C"})
	if got := p.shutdownsSoFar(); len(got) != 0 {
		t.Fatalf("shut down %v to make room nobody used", got)
	}
}

// A daemon restart does not make every device look unused: when each was last
// used is not saved, so the restart counts as a use.
func TestARestartDoesNotPutDevicesInUseToSleep(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(time.Hour)
	restarted := newAvailable(Config{DataDir: svc.cfg.DataDir, Provider: p, MemoryBudget: 3 * iosDeviceMemory, Now: clock.Now,
		Sessions: func(context.Context, domain.SessionID) (SessionInfo, error) { return SessionInfo{}, nil }})
	t.Cleanup(func() { _ = restarted.Shutdown() })
	if _, err := restarted.Setup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := restarted.awaitReady(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted.suspendIdle(context.Background())
	if held, _ := restarted.Lease("s-1"); held.Suspended {
		t.Fatal("a restart put a device to sleep that was in use an hour ago, by the old clock")
	}
	clock.advance(defaultIdleSuspendAfter)
	restarted.suspendIdle(context.Background())
	if held, _ := restarted.Lease("s-1"); !held.Suspended {
		t.Fatal("unused for the idle timeout after the restart, but still awake")
	}
}

// Putting a device to sleep binds the agent's command again, so a lease from
// an older daemon gets a command that starts the device.
func TestSleepRebindsTheAgentCommand(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	p.mu.Lock()
	before := len(p.bound)
	p.mu.Unlock()
	clock.advance(time.Hour)
	svc.suspendIdle(context.Background())
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Equal(p.bound[before:], []string{"s-1=A"}) {
		t.Fatalf("bound %v when sleeping, want s-1's command bound again", p.bound[before:])
	}
}

// Two views waking the same sleeping device at once (the Device tab and the
// phone) boot it once: the second open waits and finds it running. Booted
// twice, the second boot's helper reset shut the first one's device down.
func TestConcurrentWakesBootTheDeviceOnce(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(time.Hour)
	svc.suspendIdle(ctx)
	p.setBooted("A", false)
	p.mu.Lock()
	p.coldBoots = nil
	p.gateDevice, p.bootGate, p.bootEntered = "A", make(chan struct{}), make(chan string, 2)
	p.mu.Unlock()
	results := make(chan error, 2)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{}); results <- err }()
	<-p.bootEntered
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{}); results <- err }()
	time.Sleep(50 * time.Millisecond)
	close(p.bootGate)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Equal(p.coldBoots, []string{"A"}) {
		t.Fatalf("booted from off %v, want A once", p.coldBoots)
	}
	if held, _ := svc.Lease("s-1"); held.Suspended {
		t.Fatal("still asleep after both wakes")
	}
}

// A device AO booted that its user then shut down and started again (in
// Xcode, say) is theirs: AO leaves it running rather than putting it to sleep
// when the session stops using it.
func TestADeviceRestartedOutsideAOIsNotPutToSleep(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	p.rebootOutside("A")
	clock.advance(time.Hour)
	svc.suspendIdle(context.Background())
	if held, _ := svc.Lease("s-1"); held.Suspended {
		t.Fatal("put a device restarted outside AO to sleep")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if slices.Contains(p.shutdowns, "A") {
		t.Fatal("shut down a device restarted outside AO")
	}
}

// AO's own wake is a new boot too, and the device stays AO's: unused again,
// it goes back to sleep.
func TestAWokenDeviceStaysAOsToPutToSleep(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(time.Hour)
	svc.suspendIdle(ctx)
	p.setBooted("A", false)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); err != nil {
		t.Fatal(err)
	}
	svc.LiveLease(ctx, "s-1") // a viewer's poll lists the new boot
	clock.advance(time.Hour)
	svc.suspendIdle(ctx)
	if held, _ := svc.Lease("s-1"); !held.Suspended {
		t.Fatal("a device AO woke did not sleep again")
	}
}

// A parked device (its session released it, still running) that someone
// started again outside AO is not powered off when its session ends.
func TestAParkedDeviceRestartedOutsideAOIsLeftRunning(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	if err := svc.Close(ctx, "s-1", false); err != nil {
		t.Fatal(err)
	}
	p.rebootOutside("A")
	clock.advance(time.Hour)
	svc.retireParked(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	if slices.Contains(p.shutdowns, "A") {
		t.Fatal("powered off a parked device restarted outside AO")
	}
}

// Making room never shuts down another session's device that was started
// again outside AO: it is no longer AO's to stop, so it counts as full.
func TestMakingRoomLeavesADeviceRestartedOutsideAO(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	p.rebootOutside("A")
	p.setBooted("B", false)
	clock.advance(time.Hour)
	if _, err := svc.Open(context.Background(), "s-2", OpenRequest{DeviceID: "C"}); apiCode(err) != codeNoRoom {
		t.Fatalf("open with only a restarted device to stop = %v, want %s", err, codeNoRoom)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if slices.Contains(p.shutdowns, "A") {
		t.Fatal("shut down a device restarted outside AO to make room")
	}
}

// An owned device no lease or parked record holds (saved so by an older
// daemon) is forgotten on load: nothing would ever release it, and a session
// later handed it while someone else ran it would put it to sleep.
func TestOwnedDevicesNothingHoldsAreForgottenOnLoad(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "devices"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(leasesPath(dir), []byte(`{"leases":[],"owned":["ios:A"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := newAvailable(Config{DataDir: dir, Provider: iosFleet()})
	t.Cleanup(func() { _ = svc.Shutdown() })
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.owned) != 0 {
		t.Fatalf("owned %v after load, want none", svc.owned)
	}
}

// A caller that stops waiting (the phone gives up on a request after 12 s,
// the room it waited for having just freed) does not cancel a boot already
// under way: the device comes up and the lease is awake, rather than the
// half-booted device being shut down again.
func TestAWakeFinishesWhenTheCallerGivesUp(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(time.Hour)
	svc.suspendIdle(context.Background())
	p.setBooted("A", false)
	p.mu.Lock()
	p.coldBoots, p.shutdowns = nil, nil // the sleep shut it down
	p.gateDevice, p.bootGate, p.bootEntered = "A", make(chan struct{}), make(chan string, 1)
	p.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{}); result <- err }()
	<-p.bootEntered
	cancel()
	close(p.bootGate)
	if err := <-result; err != nil {
		t.Fatalf("wake = %v, want it to finish", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Equal(p.coldBoots, []string{"A"}) {
		t.Fatalf("booted from off %v, want A", p.coldBoots)
	}
	if held, _ := svc.Lease("s-1"); held.Suspended {
		t.Fatal("still asleep after the wake")
	}
	if slices.Contains(p.shutdowns, "A") {
		t.Fatal("shut A down after its caller left")
	}
}

// The same for a first open: the boot finishes and the lease is written.
func TestAFirstOpenFinishesWhenTheCallerGivesUp(t *testing.T) {
	p := iosFleet()
	svc, _ := budgetService(t, p, 3*iosDeviceMemory)
	p.mu.Lock()
	p.gateDevice, p.bootGate, p.bootEntered = "A", make(chan struct{}), make(chan string, 1)
	p.mu.Unlock()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{DeviceID: "A"}); result <- err }()
	<-p.bootEntered
	cancel()
	close(p.bootGate)
	if err := <-result; err != nil {
		t.Fatalf("open = %v, want it to finish", err)
	}
	if _, ok := svc.Lease("s-1"); !ok {
		t.Fatal("no lease after the boot finished")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if slices.Contains(p.shutdowns, "A") {
		t.Fatal("shut A down after its caller left")
	}
}

// Stream settings and button presses are not device work: on a sleeping
// device they are refused, not a reason to start it (the phone sends its
// stream quality as it opens the device, alongside its own wake).
func TestStreamCallsDoNotWakeASleepingDevice(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(time.Hour)
	svc.suspendIdle(ctx)
	p.mu.Lock()
	before := len(p.booted)
	p.mu.Unlock()
	if err := svc.SetQuality(ctx, "s-1", ports.DeviceQualityLow); apiCode(err) != "DEVICE_ASLEEP" {
		t.Fatalf("quality on a sleeping device = %v, want DEVICE_ASLEEP", err)
	}
	if err := svc.PressButton(ctx, "s-1", "home"); apiCode(err) != "DEVICE_ASLEEP" {
		t.Fatalf("button on a sleeping device = %v, want DEVICE_ASLEEP", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.booted) != before {
		t.Fatalf("booted %v after stream calls, want nothing", p.booted[before:])
	}
}

// An emulator asleep is its AVD, not the serial it last ran under: when
// another emulator took that serial meanwhile, waking the lease starts the
// leased AVD, and the lease never reads the other one's state.
func TestWakingAnEmulatorWhoseSerialAnotherTook(t *testing.T) {
	p := &fakeProvider{
		platforms: []ports.DevicePlatformAvailability{{Platform: ports.DevicePlatformAndroid, Available: true}},
		devices:   []ports.DeviceInfo{{ID: "pixel", Name: "pixel", Platform: ports.DevicePlatformAndroid}},
		bootID:    func(ports.DeviceInfo) string { return "emulator-5556" },
	}
	svc, clock := budgetService(t, p, 3*androidDeviceMemory)
	ctx := context.Background()
	if lease := openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "pixel"}); lease.Device.ID != "emulator-5556" {
		t.Fatalf("lease = %+v, want pixel on emulator-5556", lease.Device)
	}
	p.mu.Lock()
	p.devices = []ports.DeviceInfo{{ID: "emulator-5556", Name: "pixel", Platform: ports.DevicePlatformAndroid, Booted: true}}
	p.bootID = func(ports.DeviceInfo) string { return "emulator-5558" }
	p.mu.Unlock()
	clock.advance(time.Hour)
	svc.suspendIdle(ctx)
	// Asleep, and another AVD now runs as emulator-5556.
	p.mu.Lock()
	p.devices = []ports.DeviceInfo{
		{ID: "pixel", Name: "pixel", Platform: ports.DevicePlatformAndroid},
		{ID: "emulator-5556", Name: "other", Platform: ports.DevicePlatformAndroid, Booted: true},
	}
	p.coldBoots = nil
	p.mu.Unlock()
	clock.advance(time.Minute)
	if live, _ := svc.LiveLease(ctx, "s-1"); live.Device.Booted {
		t.Fatal("the sleeping lease reads the other emulator on its old serial as its own")
	}
	if _, err := svc.Launch(ctx, "s-1", "com.example.app"); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !slices.Equal(p.coldBoots, []string{"pixel"}) {
		t.Fatalf("woke %v, want the leased AVD pixel", p.coldBoots)
	}
	if held, _ := svc.Lease("s-1"); held.Device.ID != "emulator-5558" || held.Device.Name != "pixel" {
		t.Fatalf("lease after the wake = %+v, want pixel on its new serial", held.Device)
	}
}

func setPressure(svc *Service, level MemoryPressure) {
	svc.mu.Lock()
	defer svc.mu.Unlock()
	svc.cfg.MemoryPressure = func() MemoryPressure { return level }
}

// Under memory pressure unused devices give their memory back sooner: after
// 5 minutes when the machine is short, after a minute when it is critical.
func TestUnderMemoryPressureUnusedDevicesSleepSooner(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		level MemoryPressure
		idle  time.Duration
	}{{PressureWarning, warningIdleSuspendAfter}, {PressureCritical, criticalIdleSuspendAfter}} {
		p := iosFleet()
		svc, clock := budgetService(t, p, 3*iosDeviceMemory)
		openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
		clock.advance(tc.idle + time.Second)
		svc.suspendIdle(ctx)
		if held, _ := svc.Lease("s-1"); held.Suspended {
			t.Fatalf("level %d: slept after %v with no pressure", tc.level, tc.idle)
		}
		setPressure(svc, tc.level)
		svc.suspendIdle(ctx)
		if held, _ := svc.Lease("s-1"); !held.Suspended {
			t.Fatalf("level %d: still awake after %v unused under pressure", tc.level, tc.idle)
		}
	}
}

// A device someone watches stays awake under any pressure.
func TestMemoryPressureLeavesAWatchedDeviceRunning(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	done := svc.Watch("s-1")
	defer done()
	setPressure(svc, PressureCritical)
	clock.advance(time.Hour)
	svc.suspendIdle(context.Background())
	if held, _ := svc.Lease("s-1"); held.Suspended {
		t.Fatal("put a watched device to sleep under memory pressure")
	}
}

// Short on memory, a new device only starts in place of an idle one, whatever
// room the budget has; an empty machine still starts one.
func TestShortOnMemoryANewDeviceStartsOnlyInPlaceOfAnIdleOne(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	setPressure(svc, PressureWarning)
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	_, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "C"})
	if apiCode(err) != "NO_DEVICE_ROOM" || !strings.Contains(err.Error(), "short on memory") || !strings.Contains(err.Error(), "iPhone A (session s-1)") {
		t.Fatalf("open while short on memory = %v, want a refusal naming iPhone A", err)
	}
	clock.advance(evictIdleAfter + time.Second)
	openOn(t, svc, p, "s-2", OpenRequest{DeviceID: "C"})
	if held, _ := svc.Lease("s-1"); !held.Suspended {
		t.Fatal("C started without putting the idle A to sleep")
	}
}

// Critically low on memory, nothing starts: not a new device, not a wake.
func TestCriticallyLowOnMemoryStartsNoDevice(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(time.Hour)
	svc.suspendIdle(ctx)
	setPressure(svc, PressureCritical)
	if _, err := svc.Open(ctx, "s-2", OpenRequest{DeviceID: "C"}); apiCode(err) != "NO_DEVICE_AVAILABLE" || !strings.Contains(err.Error(), "critically low on memory") {
		t.Fatalf("open = %v, want a critical-memory refusal", err)
	}
	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); apiCode(err) != "NO_DEVICE_AVAILABLE" {
		t.Fatalf("wake = %v, want a critical-memory refusal", err)
	}
	setPressure(svc, PressureNormal)
	if _, err := svc.Open(ctx, "s-1", OpenRequest{}); err != nil {
		t.Fatalf("wake once the pressure eased = %v", err)
	}
}

// While AO boots a device its live lease says so, so a view can tell a start
// under way from one still waiting for room.
func TestALiveLeaseSaysWhenItsDeviceIsStarting(t *testing.T) {
	p := iosFleet()
	svc, clock := budgetService(t, p, 3*iosDeviceMemory)
	ctx := context.Background()
	openOn(t, svc, p, "s-1", OpenRequest{DeviceID: "A"})
	clock.advance(time.Hour)
	svc.suspendIdle(ctx)
	p.setBooted("A", false)
	p.mu.Lock()
	p.gateDevice, p.bootGate, p.bootEntered = "A", make(chan struct{}), make(chan string, 1)
	p.mu.Unlock()
	done := make(chan error, 1)
	go func() { _, err := svc.Open(ctx, "s-1", OpenRequest{}); done <- err }()
	<-p.bootEntered
	if live, _ := svc.LiveLease(ctx, "s-1"); !live.Starting || live.Device.Booted {
		t.Fatalf("live lease during the boot = %+v, want starting and not booted", live)
	}
	close(p.bootGate)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if live, _ := svc.LiveLease(ctx, "s-1"); live.Starting {
		t.Fatal("still starting after the boot")
	}
}
