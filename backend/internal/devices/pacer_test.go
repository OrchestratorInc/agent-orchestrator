package devices

import (
	"encoding/json"
	"testing"
	"time"
)

func ms(n float64) time.Duration { return time.Duration(n * float64(time.Millisecond)) }

func TestInputPacerForwardsASteadyDragAsItComes(t *testing.T) {
	var p InputPacer
	start := time.Unix(100, 0)
	for i := 0; i < 20; i++ {
		arrival := start.Add(ms(float64(i) * 8))
		if due := p.Due(arrival, 5000+float64(i)*8); due.Sub(arrival) > ms(1) {
			t.Fatalf("move %d waited %v on a steady link", i, due.Sub(arrival))
		}
	}
}

func TestInputPacerSpreadsABurstAtThePhonesSpacing(t *testing.T) {
	var p InputPacer
	start := time.Unix(100, 0)
	p.Due(start, 5000)
	// The radio was away for 100 ms: the next five moves, made 8 ms apart,
	// all land at once.
	land := start.Add(ms(100))
	var prev time.Time
	for i := 1; i <= 5; i++ {
		due := p.Due(land, 5000+float64(i)*8)
		if i > 1 {
			if gap := due.Sub(prev); gap < ms(7) || gap > ms(8) {
				t.Fatalf("move %d went %v after the last, want the phone's ~8 ms", i, gap)
			}
		}
		prev = due
	}
}

func TestInputPacerNeverHoldsATouchPastTheCap(t *testing.T) {
	var p InputPacer
	start := time.Unix(100, 0)
	p.Due(start, 5000)
	land := start.Add(ms(10))
	var due time.Time
	for i := 1; i <= 60; i++ {
		due = p.Due(land, 5000+float64(i)*8)
	}
	if wait := due.Sub(land); wait > MaxInputDelay {
		t.Fatalf("waited %v, cap is %v", wait, MaxInputDelay)
	}
}

func TestInputPacerKeepsUnstampedMessagesInLine(t *testing.T) {
	var p InputPacer
	start := time.Unix(100, 0)
	p.Due(start, 5000)
	queued := p.Due(start.Add(ms(100)), 5000+120) // held behind the burst
	button := p.Due(start.Add(ms(100)), 0)
	if button.Before(queued) {
		t.Fatalf("a button press jumped ahead of a queued touch")
	}
}

func TestInputPacerTreatsALongGapAsANewGesture(t *testing.T) {
	var p InputPacer
	start := time.Unix(100, 0)
	p.Due(start, 5000)
	arrival := start.Add(5 * time.Second)
	if due := p.Due(arrival, 10000); !due.Equal(arrival) {
		t.Fatalf("a new gesture waited %v", due.Sub(arrival))
	}
}

func TestTakeInputTimeStripsTheStamp(t *testing.T) {
	out, touch := TakeInputTime([]byte(`{"type":"touch","action":"move","x":0.5,"y":0.25,"t":1234.5}`), false)
	if touch != 1234.5 {
		t.Fatalf("time = %v", touch)
	}
	var fields map[string]any
	if err := json.Unmarshal(out, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["t"]; ok || fields["action"] != "move" || fields["x"] != 0.5 {
		t.Fatalf("forwarded %s", out)
	}

	ios := append([]byte{iosTouchTag}, []byte(`{"type":"move","x":0.1,"y":0.2,"t":99}`)...)
	out, touch = TakeInputTime(ios, true)
	if touch != 99 || out[0] != iosTouchTag {
		t.Fatalf("ios: time %v, frame %q", touch, out)
	}
	if err := json.Unmarshal(out[1:], &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["t"]; ok {
		t.Fatalf("ios frame kept the stamp: %s", out[1:])
	}
}

func TestTakeInputTimeLeavesOtherMessagesAlone(t *testing.T) {
	for _, tc := range []struct {
		msg    []byte
		binary bool
	}{
		{[]byte(`{"type":"home"}`), false},
		{append([]byte{0x04}, []byte(`{"button":"home","t":5}`)...), true},
		{[]byte("not json"), false},
		{[]byte{iosTouchTag}, true},
	} {
		out, touch := TakeInputTime(tc.msg, tc.binary)
		if touch != 0 || string(out) != string(tc.msg) {
			t.Fatalf("changed %q to %q (time %v)", tc.msg, out, touch)
		}
	}
}
