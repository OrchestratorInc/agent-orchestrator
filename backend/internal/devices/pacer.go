package devices

import (
	"encoding/json"
	"time"
)

// MaxInputDelay is the most a touch waits behind its arrival.
const MaxInputDelay = 150 * time.Millisecond

// inputDrain replays touches this much faster than they were made, so the
// delay a pause builds drains away on a steady link.
const inputDrain = 0.98

// iosTouchTag starts a serve-sim touch frame; the JSON follows it.
const iosTouchTag = 0x03

// InputPacer spaces a phone's touches out again before they reach the
// device. An iPhone's Wi-Fi leaves the channel for ~100 ms twice a second
// while AWDL (AirDrop, Continuity) is active, so the moves of a drag stop and
// then land together: forwarded as they come, the device scrolls in jerks and
// reads a flick's speed from the squeezed timing. Each touch the phone stamped
// is sent one gap (as the phone measured it) after the one before, never before
// it arrived and never more than MaxInputDelay late. Messages without a stamp
// keep their place in line.
type InputPacer struct {
	lastDue   time.Time
	lastTouch float64
}

// Due reports when a message that arrived at arrival should go on. touchMS is
// the phone's time for the touch in milliseconds, or 0 when it carries none.
func (p *InputPacer) Due(arrival time.Time, touchMS float64) time.Time {
	due := arrival
	if touchMS > 0 && p.lastTouch > 0 {
		// A gap of a second or more is a new gesture, not a delayed move.
		if gap := touchMS - p.lastTouch; gap > 0 && gap < 1000 {
			next := p.lastDue.Add(time.Duration(gap * inputDrain * float64(time.Millisecond)))
			if next.After(due) {
				due = next
			}
		}
	}
	if p.lastDue.After(due) {
		due = p.lastDue
	}
	if due.Sub(arrival) > MaxInputDelay {
		due = arrival.Add(MaxInputDelay)
	}
	if touchMS > 0 {
		p.lastTouch = touchMS
	}
	p.lastDue = due
	return due
}

// TakeInputTime removes the phone's touch time ("t", milliseconds) from an
// input message and returns the message the hub expects with it. Android's
// socket takes plain JSON text; serve-sim takes binary frames whose first byte
// is a tag, and only touches carry a time. Anything else passes unchanged.
func TakeInputTime(msg []byte, binary bool) ([]byte, float64) {
	prefix := 0
	if binary {
		if len(msg) < 2 || msg[0] != iosTouchTag {
			return msg, 0
		}
		prefix = 1
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(msg[prefix:], &fields); err != nil {
		return msg, 0
	}
	raw, ok := fields["t"]
	if !ok {
		return msg, 0
	}
	var touchMS float64
	if err := json.Unmarshal(raw, &touchMS); err != nil || touchMS <= 0 {
		return msg, 0
	}
	delete(fields, "t")
	body, err := json.Marshal(fields)
	if err != nil {
		return msg, 0
	}
	return append(append([]byte{}, msg[:prefix]...), body...), touchMS
}
