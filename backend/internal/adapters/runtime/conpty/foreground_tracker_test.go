package conpty

import (
	"testing"
	"time"
)

type fakeForegroundReader struct{ pgid int }

func (f *fakeForegroundReader) ForegroundPGID() (int, bool) { return f.pgid, f.pgid > 0 }

func TestForegroundTrackerBusyFollowsOutputAfterStartup(t *testing.T) {
	tracker := newForegroundTracker(&fakeForegroundReader{pgid: 42})
	start := time.Now()

	tracker.noteOutput(start)
	if _, changed, state := tracker.evaluate(start); !changed || state.PGID != 42 || state.Busy {
		t.Fatalf("first screen = %+v (changed %v), want pgid 42, not busy", state, changed)
	}

	working := start.Add(foregroundStartGrace + time.Second)
	tracker.noteOutput(working)
	next, changed, state := tracker.evaluate(working)
	if !changed || !state.Busy || next <= 0 || next > foregroundBusyWindow+time.Second {
		t.Fatalf("output after startup = %+v (changed %v, next %v), want busy with a wake for the quiet edge", state, changed, next)
	}

	if _, changed, state = tracker.evaluate(working.Add(foregroundBusyWindow)); !changed || state.Busy {
		t.Fatalf("after the quiet window = %+v (changed %v), want not busy", state, changed)
	}
}

func TestForegroundTrackerIgnoresEchoAndResizeRedraws(t *testing.T) {
	tracker := newForegroundTracker(&fakeForegroundReader{pgid: 7})
	now := time.Now()
	tracker.evaluate(now)
	now = now.Add(foregroundStartGrace + time.Second)

	tracker.noteInput(now)
	tracker.noteOutput(now.Add(10 * time.Millisecond))
	tracker.noteResize(now.Add(time.Second))
	tracker.noteOutput(now.Add(time.Second + 100*time.Millisecond))
	if _, _, state := tracker.evaluate(now.Add(time.Second + 150*time.Millisecond)); state.Busy {
		t.Fatal("an echo or a resize redraw counted as the program working")
	}
}

type fakeForegroundPTY struct {
	ptyConn
	fakeForegroundReader
}

func TestDeferredPTYForwardsForeground(t *testing.T) {
	d := newDeferredPTY(nil)
	if _, ok := d.ForegroundPGID(); ok {
		t.Fatal("unstarted PTY reported a foreground")
	}
	d.conn = &fakeForegroundPTY{fakeForegroundReader: fakeForegroundReader{pgid: 31}}
	if pgid, ok := d.ForegroundPGID(); !ok || pgid != 31 {
		t.Fatalf("ForegroundPGID() = %d, %v; want 31, true", pgid, ok)
	}
}
