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

func TestForegroundTrackerRechecksQuietScreenAfterEchoOrResize(t *testing.T) {
	for _, event := range []string{"input", "resize"} {
		t.Run(event, func(t *testing.T) {
			tracker := newForegroundTracker(&fakeForegroundReader{pgid: 42})
			now := time.Now()
			tracker.evaluate(now)
			now = now.Add(foregroundStartGrace + time.Second)
			if event == "input" {
				tracker.noteInput(now)
			} else {
				tracker.noteResize(now)
			}
			// A fast permission prompt is indistinguishable from echo here.
			tracker.noteOutput(now.Add(20 * time.Millisecond))
			next, _, state := tracker.evaluate(now.Add(100 * time.Millisecond))
			if state.Busy || next <= 0 {
				t.Fatalf("suppressed output = %+v, next %v; want idle with a screen recheck", state, next)
			}
			settled := now.Add(100*time.Millisecond + next)
			if _, changed, state := tracker.evaluate(settled); !changed || state.Busy {
				t.Fatalf("quiet screen = %+v, changed %v; want an idle update to inspect the prompt", state, changed)
			}
			if next, changed, _ := tracker.evaluate(settled.Add(time.Second)); changed || next != 0 {
				t.Fatal("quiet screen kept polling after its update")
			}
		})
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
