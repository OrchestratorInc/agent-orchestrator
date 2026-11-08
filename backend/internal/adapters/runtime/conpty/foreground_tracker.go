package conpty

import (
	"slices"
	"sync"
	"time"
)

// foregroundReader is implemented by PTYs that can name their foreground
// process group. Windows ConPTY has no process groups, so it never reports one.
type foregroundReader interface {
	ForegroundPGID() (pgid int, ok bool)
}

// Timing for foreground tracking. Variables so tests can shorten them.
var (
	// foregroundMinInterval coalesces a burst of output into one evaluation.
	foregroundMinInterval = 100 * time.Millisecond
	// foregroundBusyWindow: agent TUIs redraw several times a second while
	// they work and draw nothing while they wait.
	foregroundBusyWindow = 2 * time.Second
	// Output this soon after typed input is its echo, and this soon after a
	// resize is a redraw; neither means the program is working.
	foregroundEchoWindow   = 250 * time.Millisecond
	foregroundResizeWindow = 600 * time.Millisecond
	// foregroundStartGrace: a program drawing its first screen is not working.
	foregroundStartGrace = 2500 * time.Millisecond
)

// maxForegroundArgs and maxForegroundArgLen bound the argv sent to watchers.
const (
	maxForegroundArgs   = 8
	maxForegroundArgLen = 512
)

// foregroundTracker follows which program owns the terminal and whether it is
// drawing. The host's own output, input and resize events drive it, so a new
// program is reported as soon as it draws, without polling. A nil tracker
// ignores every call.
type foregroundTracker struct {
	reader foregroundReader
	poke   chan struct{}

	mu            sync.Mutex
	lastInput     time.Time
	lastResize    time.Time
	lastOutput    time.Time // output that was not an echo or a resize redraw
	lastDraw      time.Time
	screenPending bool
	since         time.Time // when the current foreground group appeared
	state         ForegroundPayload
	hasState      bool
}

func newForegroundTracker(reader foregroundReader) *foregroundTracker {
	return &foregroundTracker{reader: reader, poke: make(chan struct{}, 1)}
}

func (t *foregroundTracker) wake() {
	if t == nil {
		return
	}
	select {
	case t.poke <- struct{}{}:
	default:
	}
}

func (t *foregroundTracker) noteOutput(now time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.lastDraw, t.screenPending = now, true
	if now.Sub(t.lastInput) > foregroundEchoWindow && now.Sub(t.lastResize) > foregroundResizeWindow {
		t.lastOutput = now
	}
	t.mu.Unlock()
	t.wake()
}

func (t *foregroundTracker) noteInput(now time.Time) {
	if t != nil {
		t.mu.Lock()
		t.lastInput = now
		t.mu.Unlock()
	}
}

func (t *foregroundTracker) noteResize(now time.Time) {
	if t != nil {
		t.mu.Lock()
		t.lastResize = now
		t.mu.Unlock()
	}
}

func (t *foregroundTracker) current() (ForegroundPayload, bool) {
	if t == nil {
		return ForegroundPayload{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state, t.hasState
}

// run evaluates on every wake, at most once per foregroundMinInterval, and
// publishes state changes and settled screen updates until done closes.
func (t *foregroundTracker) run(done <-chan struct{}, publish func(ForegroundPayload)) {
	if t == nil {
		return
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastEval time.Time
	for {
		select {
		case <-done:
			return
		case <-t.poke:
		case <-timer.C:
		}
		now := time.Now()
		if wait := foregroundMinInterval - now.Sub(lastEval); wait > 0 {
			timer.Reset(wait)
			continue
		}
		lastEval = now
		next, changed, state := t.evaluate(now)
		if changed {
			publish(state)
		}
		if next > 0 {
			timer.Reset(next)
		}
	}
}

// evaluate reports whether a state or settled-screen update is due and when
// to evaluate again (0: only on the next event).
func (t *foregroundTracker) evaluate(now time.Time) (next time.Duration, changed bool, state ForegroundPayload) {
	pgid, _ := t.reader.ForegroundPGID()
	// exec can replace the program without changing its process group. Wakes
	// are output-driven and coalesced, so read argv on each evaluation.
	argv := capForegroundArgv(processArgv(pgid))
	t.mu.Lock()
	defer t.mu.Unlock()
	state.PGID, state.Argv = pgid, argv
	if pgid != t.state.PGID {
		t.since = now
	}
	quiet := now.Sub(t.lastOutput)
	state.Busy = t.lastOutput.After(t.since.Add(foregroundStartGrace)) && quiet < foregroundBusyWindow
	if state.Busy {
		next = foregroundBusyWindow - quiet + 10*time.Millisecond
	}
	changed = !t.hasState || state.PGID != t.state.PGID || state.Busy != t.state.Busy || !slices.Equal(state.Argv, t.state.Argv)
	if !state.Busy && t.screenPending {
		// Echo and resize output must not show as working, but may contain a
		// permission prompt. Inspect it once it settles, even if state is equal.
		if wait := foregroundEchoWindow - now.Sub(t.lastDraw); wait > 0 {
			next = wait
		} else {
			changed, t.screenPending = true, false
		}
	}
	t.state, t.hasState = state, true
	return next, changed, state
}

func capForegroundArgv(argv []string) []string {
	argv = argv[:min(len(argv), maxForegroundArgs)]
	out := make([]string, len(argv))
	for i, arg := range argv {
		out[i] = arg[:min(len(arg), maxForegroundArgLen)]
	}
	return out
}
