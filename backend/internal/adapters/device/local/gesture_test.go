package local

import (
	"encoding/json"
	"testing"
	"time"
)

func TestHomeIndicatorGestureStartsOnTheBottomEdge(t *testing.T) {
	for _, button := range []string{"home", "recents"} {
		steps := homeIndicatorGesture(button)
		if len(steps) < 3 || steps[0].phase != "begin" || steps[len(steps)-1].phase != "end" {
			t.Fatalf("%s: steps %+v", button, steps)
		}
		frame := encodeGestureStep(steps[0])
		if frame[0] != iosTouchFrame {
			t.Fatalf("%s: frame tag %#x", button, frame[0])
		}
		var touch map[string]any
		if err := json.Unmarshal(frame[1:], &touch); err != nil {
			t.Fatal(err)
		}
		if touch["edge"] != float64(bottomEdge) || touch["y"].(float64) < 0.99 || touch["type"] != "begin" {
			t.Fatalf("%s: first touch %v, want a begin on the bottom edge", button, touch)
		}
		for i := 1; i < len(steps); i++ {
			if steps[i].y > steps[i-1].y {
				t.Fatalf("%s: step %d moves down", button, i)
			}
		}
	}
	if homeIndicatorGesture("back") != nil {
		t.Fatal("back has no iOS gesture")
	}
}

func TestRecentsGestureRestsBeforeLifting(t *testing.T) {
	home, recents := homeIndicatorGesture("home"), homeIndicatorGesture("recents")
	if rest := recents[len(recents)-1].wait; rest < 500*time.Millisecond {
		t.Fatalf("recents lifts after %v; the switcher opens only when the finger rests", rest)
	}
	if home[len(home)-1].y > 0.6 || recents[len(recents)-1].y < 0.6 {
		t.Fatalf("home should flick past the middle and recents stop short of it")
	}
}
