package conpty

import (
	"testing"
	"time"
)

type fakeForegroundReader struct{ pgid int }

func (f *fakeForegroundReader) ForegroundPGID() (int, bool) { return f.pgid, f.pgid > 0 }

func TestForegroundTrackerBusyFollowsProgramOutput(t *testing.T) {
	reader := &fakeForegroundReader{pgid: 4242}
	tracker := newForegroundTracker(reader)
	start := time.Now()

	_, changed, state := tracker.evaluate(start)
	if !changed || state.PGID != 4242 || state.Busy {
		t.Fatalf("first evaluation = %+v (changed %v), want pgid 4242, not busy", state, changed)
	}

	tracker.noteOutput(start.Add(time.Second))
	next, changed, state := tracker.evaluate(start.Add(time.Second + 50*time.Millisecond))
	if !changed || !state.Busy {
		t.Fatalf("after output = %+v (changed %v), want busy", state, changed)
	}
	if next > foregroundBusyWindow {
		t.Fatalf("next wake %v, want within the busy window to report the quiet edge", next)
	}

	_, changed, state = tracker.evaluate(start.Add(time.Second + foregroundBusyWindow + time.Millisecond))
	if !changed || state.Busy {
		t.Fatalf("after quiet window = %+v (changed %v), want not busy", state, changed)
	}
}

func TestForegroundTrackerIgnoresEchoAndResizeRedraws(t *testing.T) {
	tracker := newForegroundTracker(&fakeForegroundReader{pgid: 7})
	now := time.Now()

	tracker.noteInput(now)
	tracker.noteOutput(now.Add(10 * time.Millisecond))
	if _, _, state := tracker.evaluate(now.Add(20 * time.Millisecond)); state.Busy {
		t.Fatal("echo of typed input counted as the program working")
	}

	later := now.Add(10 * time.Second)
	tracker.noteResize(later)
	tracker.noteOutput(later.Add(100 * time.Millisecond))
	if _, _, state := tracker.evaluate(later.Add(150 * time.Millisecond)); state.Busy {
		t.Fatal("redraw after a resize counted as the program working")
	}
}

func TestForegroundTrackerReportsProgramChange(t *testing.T) {
	reader := &fakeForegroundReader{pgid: 100}
	tracker := newForegroundTracker(reader)
	now := time.Now()
	tracker.evaluate(now)

	reader.pgid = 200
	_, changed, state := tracker.evaluate(now.Add(time.Second))
	if !changed || state.PGID != 200 {
		t.Fatalf("after foreground change = %+v (changed %v), want pgid 200", state, changed)
	}
	if _, changed, _ = tracker.evaluate(now.Add(time.Second + time.Millisecond)); changed {
		t.Fatal("unchanged foreground reported as a change")
	}
}

func TestNilForegroundTrackerIsInert(t *testing.T) {
	var tracker *foregroundTracker
	tracker.noteOutput(time.Now())
	tracker.noteInput(time.Now())
	tracker.noteResize(time.Now())
	tracker.wake()
	if _, ok := tracker.current(); ok {
		t.Fatal("nil tracker reported a state")
	}
}

type fakeForegroundPTY struct {
	ptyConn
	fakeForegroundReader
}

func TestDeferredPTYForwardsForeground(t *testing.T) {
	var _ foregroundReader = (*deferredPTY)(nil)
	d := newDeferredPTY(nil)
	if _, ok := d.ForegroundPGID(); ok {
		t.Fatal("unstarted PTY reported a foreground")
	}
	d.conn = &fakeForegroundPTY{fakeForegroundReader: fakeForegroundReader{pgid: 31}}
	if pgid, ok := d.ForegroundPGID(); !ok || pgid != 31 {
		t.Fatalf("ForegroundPGID() = %d, %v; want 31, true", pgid, ok)
	}
}
