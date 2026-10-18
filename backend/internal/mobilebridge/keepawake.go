package mobilebridge

import (
	"context"
	"errors"
	"math"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// KeepAwakeSupported reports whether this platform can hold the machine awake
// for Connect Mobile. Only macOS: it is where idle sleep cuts a paired phone
// off, and caffeinate ships with every install.
func KeepAwakeSupported() bool { return runtime.GOOS == "darwin" }

// KeepAwake prevents idle system sleep while Connect Mobile is on, so a phone
// can keep reaching the daemon after the user walks away from the desk.
//
// It runs `caffeinate -i -w <daemon pid>`: -i holds only the idle-system-sleep
// assertion, so the display can still turn off and the screen can still lock,
// and -w ends caffeinate on its own if the daemon dies without calling Stop.
// A closed MacBook lid still sleeps; nothing here overrides that.
type KeepAwake struct {
	binary     string
	ownerPID   int
	hasBattery bool
	deadline   time.Time
	display    bool
	lid        *LidAwake

	mu              sync.Mutex
	cmd             *exec.Cmd
	done            chan struct{}
	runningDeadline time.Time
	runningDisplay  bool
	retiring        map[*exec.Cmd]idleRetirement
	now             func() time.Time
}

const (
	// Renew between the first and second five-second policy ticks. Using exactly
	// one tick as the margin could skip renewal just before that boundary.
	idleRenewalMargin  = 7500 * time.Millisecond
	idleHandoffOverlap = 5 * time.Second
)

type idleRetirement struct {
	expires time.Time
	done    chan struct{}
	timer   *time.Timer
}

func (k *KeepAwake) clock() time.Time {
	if k.now != nil {
		return k.now()
	}
	return time.Now()
}

// NewKeepAwake returns a controller that ties its assertion to ownerPID.
func NewKeepAwake(ownerPID int) *KeepAwake {
	return &KeepAwake{binary: "caffeinate", ownerPID: ownerPID, hasBattery: detectBattery()}
}

// detectBattery reports whether this Mac has an internal battery, which is how
// the settings page knows to warn that closing the lid still sleeps it.
func detectBattery() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()
	out, err := aoprocess.CommandContext(ctx, "pmset", "-g", "batt").Output()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "InternalBattery")
}

// HasBattery reports whether this machine is a laptop.
func (k *KeepAwake) HasBattery() bool { return k.hasBattery }

// Active reports whether the sleep assertion is currently held.
func (k *KeepAwake) Active() bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.cmd != nil || len(k.retiring) != 0
}

// Start holds the assertion. Idempotent.
func (k *KeepAwake) Start() error {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.cmd != nil {
		return nil
	}
	return k.startLocked()
}

func (k *KeepAwake) startLocked() error {
	args := []string{"-i", "-w", strconv.Itoa(k.ownerPID)}
	if k.display {
		args = append(args, "-d")
	}
	if !k.deadline.IsZero() {
		seconds := int(math.Ceil(k.deadline.Sub(k.clock()).Seconds()))
		if seconds < 1 {
			return errors.New("awake deadline has expired")
		}
		args = append(args, "-t", strconv.Itoa(seconds))
	}
	cmd := aoprocess.Command(k.binary, args...)
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	k.cmd, k.done = cmd, done
	k.runningDeadline, k.runningDisplay = k.deadline, k.display
	go func() {
		_ = cmd.Wait()
		k.mu.Lock()
		if k.cmd == cmd {
			k.cmd, k.done = nil, nil
		}
		if retired, ok := k.retiring[cmd]; ok {
			retired.timer.Stop()
			delete(k.retiring, cmd)
		}
		k.mu.Unlock()
		close(done)
	}()
	return nil
}

// Stop releases the assertion. Idempotent.
func (k *KeepAwake) Stop() {
	if k.lid != nil {
		k.lid.Stop()
	}
	k.stopIdle()
}

func (k *KeepAwake) stopIdle() {
	k.mu.Lock()
	processes := make(map[*exec.Cmd]chan struct{}, len(k.retiring)+1)
	if k.cmd != nil {
		processes[k.cmd] = k.done
	}
	for cmd, retired := range k.retiring {
		retired.timer.Stop()
		processes[cmd] = retired.done
	}
	k.mu.Unlock()
	for cmd := range processes {
		_ = cmd.Process.Kill()
	}
	for _, done := range processes {
		select {
		case <-done:
		case <-time.After(processProbeTimeout):
		}
	}
}

// ConfigureLid keeps idle assertions separate from native lid ownership.
func (k *KeepAwake) ConfigureLid(on, allowSetup bool) error {
	if k.lid == nil {
		if on {
			return errors.New("closed-lid wakefulness is unavailable")
		}
		return nil
	}
	if allowSetup {
		if err := k.lid.Setup(); err != nil {
			return err
		}
	}
	if on {
		return k.lid.Start(false)
	}
	k.lid.Stop()
	return nil
}

// LidStatus reports the optional native lid controller's observed state.
func (k *KeepAwake) LidStatus() (bool, bool, string) {
	if k.lid == nil {
		return false, false, "helper_missing"
	}
	return k.lid.Status()
}

// SetLidController attaches the desktop's optional native recovery client.
func (k *KeepAwake) SetLidController(lid *LidAwake) { k.lid = lid }

// SetupLid registers without withdrawing an existing live lease.
func (k *KeepAwake) SetupLid() error {
	if k.lid == nil {
		return errors.New("desktop lid helper is unavailable")
	}
	return k.lid.Setup()
}

// SetDeadline changes the native lease bound as well as caffeinate's timeout.
func (k *KeepAwake) SetDeadline(deadline time.Time) error {
	if k.lid == nil {
		return nil
	}
	return k.lid.SetDeadline(deadline)
}

// ConfigureIdle renews this controller's idle assertion without dropping the
// previous one first. Rolling deadlines reuse the running process until its
// renewal margin, and replacements overlap briefly. Each process retains its
// own OS-enforced timeout; shorter policy deadlines also cap retirement.
func (k *KeepAwake) ConfigureIdle(deadline time.Time, display bool) error {
	k.mu.Lock()
	k.deadline, k.display = deadline, display
	now := k.clock()
	// A newly shortened policy also bounds any earlier handoff still in flight.
	for cmd, retired := range k.retiring {
		if !deadline.IsZero() && deadline.Before(retired.expires) {
			retired.timer.Stop()
			retired.expires = deadline
			retired.timer = time.AfterFunc(deadline.Sub(now), func() { _ = cmd.Process.Kill() })
			k.retiring[cmd] = retired
		}
	}
	if k.cmd == nil {
		k.mu.Unlock()
		return nil
	}
	if k.runningDisplay == display && (k.runningDeadline.Equal(deadline) ||
		(!deadline.IsZero() && !k.runningDeadline.IsZero() &&
			!deadline.Before(k.runningDeadline) && k.runningDeadline.Sub(now) > idleRenewalMargin)) {
		k.mu.Unlock()
		return nil
	}
	previous, done, previousDeadline := k.cmd, k.done, k.runningDeadline
	if err := k.startLocked(); err != nil {
		k.mu.Unlock()
		// A failed replacement must not leave an older, less restrictive policy.
		k.stopIdle()
		return err
	}
	retireAfter := idleHandoffOverlap
	for _, end := range []time.Time{deadline, previousDeadline} {
		if !end.IsZero() && end.Sub(now) < retireAfter {
			retireAfter = end.Sub(now)
		}
	}
	if k.retiring == nil {
		k.retiring = make(map[*exec.Cmd]idleRetirement)
	}
	k.retiring[previous] = idleRetirement{expires: now.Add(retireAfter), done: done, timer: time.AfterFunc(retireAfter, func() {
		_ = previous.Process.Kill()
	})}
	k.mu.Unlock()
	return nil
}
