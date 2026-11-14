package conpty

import (
	"slices"
	"sync"
	"time"
)

type foregroundReader interface {
	ForegroundPGID() (pgid int, ok bool)
}

var (
	foregroundMinInterval  = 100 * time.Millisecond
	foregroundBusyWindow   = 2 * time.Second
	foregroundEchoWindow   = 250 * time.Millisecond
	foregroundResizeWindow = 600 * time.Millisecond
	foregroundStartGrace   = 2500 * time.Millisecond
)

var processArgvFunc = processArgv

const (
	maxForegroundArgs   = 8
	maxForegroundArgLen = 512
)

type foregroundTracker struct {
	reader foregroundReader
	poke   chan struct{}

	mu            sync.Mutex
	lastInput     time.Time
	lastResize    time.Time
	lastOutput    time.Time
	lastDraw      time.Time
	screenPending bool
	screenTyped   bool
	since         time.Time
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
	echo := now.Sub(t.lastInput) <= foregroundEchoWindow
	if !echo {
		t.screenTyped = true
	}
	if !echo && now.Sub(t.lastResize) > foregroundResizeWindow {
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

func (t *foregroundTracker) evaluate(now time.Time) (next time.Duration, changed bool, state ForegroundPayload) {
	pgid, _ := t.reader.ForegroundPGID()
	argv := capForegroundArgv(processArgvFunc(pgid))
	t.mu.Lock()
	defer t.mu.Unlock()
	state.PGID, state.Argv = pgid, argv
	if pgid != t.state.PGID || firstArg(argv) != firstArg(t.state.Argv) {
		t.since = now
	}
	quiet := now.Sub(t.lastOutput)
	state.Busy = t.lastOutput.After(t.since.Add(foregroundStartGrace)) && quiet < foregroundBusyWindow
	if state.Busy {
		next = foregroundBusyWindow - quiet + 10*time.Millisecond
	}
	changed = !t.hasState || state.PGID != t.state.PGID || state.Busy != t.state.Busy || !slices.Equal(state.Argv, t.state.Argv)
	if !state.Busy && t.screenPending {
		if wait := foregroundEchoWindow - now.Sub(t.lastDraw); wait > 0 {
			next = wait
		} else {
			state.EchoOnly = !changed && !t.screenTyped
			changed, t.screenPending, t.screenTyped = true, false, false
		}
	}
	t.state, t.hasState = state, true
	return next, changed, state
}

func firstArg(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return argv[0]
}

func capForegroundArgv(argv []string) []string {
	argv = argv[:min(len(argv), maxForegroundArgs)]
	out := make([]string, len(argv))
	for i, arg := range argv {
		out[i] = arg[:min(len(arg), maxForegroundArgLen)]
	}
	return out
}
