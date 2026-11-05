package conpty

import (
	"slices"
	"sync"
	"time"
)

// Timing for foreground tracking. Variables so tests can shorten them.
var (
	// foregroundMinInterval coalesces a burst of output into one evaluation.
	foregroundMinInterval = 100 * time.Millisecond
	// foregroundBusyWindow is how long after its last output a program still
	// counts as busy. Agent TUIs animate a spinner or timer several times a
	// second while they work and draw nothing while they wait, so a quiet
	// window this long is the end of a burst of work.
	foregroundBusyWindow = 2 * time.Second
	// foregroundEchoWindow treats output this soon after typed input as the
	// terminal echoing it, not the program working.
	foregroundEchoWindow = 250 * time.Millisecond
	// foregroundResizeWindow treats output this soon after a resize as the
	// program redrawing for the new grid.
	foregroundResizeWindow = 600 * time.Millisecond
	// foregroundSafetyInterval re-reads the foreground while nothing is
	// printed, for a program that starts or exits without drawing anything.
	foregroundSafetyInterval = 5 * time.Second
	// foregroundArgvRefresh re-reads the leader's argv for an unchanged group,
	// which catches `exec agent` replacing the shell in place.
	foregroundArgvRefresh = 2 * time.Second
)

// foregroundTracker follows which program owns the terminal and whether it is
// drawing. It is driven by the host's own events (output, input, resize), so a
// program that starts is reported as soon as it draws its first frame, without
// polling the process table. A nil tracker ignores every call.
type foregroundTracker struct {
	reader foregroundReader
	poke   chan struct{}

	mu              sync.Mutex
	lastInput       time.Time
	lastResize      time.Time
	lastAgentOutput time.Time
	state           ForegroundPayload
	hasState        bool
	argvPGID        int
	argvReadAt      time.Time
	argv            []string
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
	if now.Sub(t.lastInput) > foregroundEchoWindow && now.Sub(t.lastResize) > foregroundResizeWindow {
		t.lastAgentOutput = now
	}
	t.mu.Unlock()
	t.wake()
}

func (t *foregroundTracker) noteInput(now time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.lastInput = now
	t.mu.Unlock()
}

func (t *foregroundTracker) noteResize(now time.Time) {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.lastResize = now
	t.mu.Unlock()
}

// current returns the last evaluated state, if any.
func (t *foregroundTracker) current() (ForegroundPayload, bool) {
	if t == nil {
		return ForegroundPayload{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.state, t.hasState
}

// run evaluates on every wake, at most once per foregroundMinInterval, and
// publishes each change. It returns when done closes.
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
			resetTimer(timer, wait)
			continue
		}
		lastEval = now
		next, changed, state := t.evaluate(now)
		if changed {
			publish(state)
		}
		resetTimer(timer, next)
	}
}

func (t *foregroundTracker) evaluate(now time.Time) (next time.Duration, changed bool, state ForegroundPayload) {
	pgid, ok := t.reader.ForegroundPGID()
	t.mu.Lock()
	defer t.mu.Unlock()
	if ok {
		if pgid != t.argvPGID || now.Sub(t.argvReadAt) >= foregroundArgvRefresh {
			t.argv = capForegroundArgv(processArgv(pgid))
			t.argvPGID, t.argvReadAt = pgid, now
		}
		state.PGID, state.Argv = pgid, t.argv
	}
	sinceOutput := now.Sub(t.lastAgentOutput)
	state.Busy = !t.lastAgentOutput.IsZero() && sinceOutput < foregroundBusyWindow
	next = foregroundSafetyInterval
	if state.Busy {
		// Wake just after the window closes to report the quiet edge.
		next = foregroundBusyWindow - sinceOutput + 10*time.Millisecond
	}
	changed = !t.hasState || state.PGID != t.state.PGID || state.Busy != t.state.Busy || !slices.Equal(state.Argv, t.state.Argv)
	t.state, t.hasState = state, true
	return next, changed, state
}

func resetTimer(timer *time.Timer, d time.Duration) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
	timer.Reset(d)
}
