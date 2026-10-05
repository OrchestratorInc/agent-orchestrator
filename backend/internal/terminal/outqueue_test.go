package terminal

import (
	"encoding/base64"
	"testing"
)

// PTY output arrives in many small reads; consecutive queued output for one
// terminal must leave as one message, in order, without crossing other
// terminals' output or non-data messages.
func TestOutQueueMergesConsecutiveDataForOneTerminal(t *testing.T) {
	q := newOutQueue()
	q.push(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: []byte("he")})
	q.push(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: []byte("llo")})
	q.push(serverMsg{Ch: chTerminal, ID: "b", Type: msgData, raw: []byte("other")})
	q.push(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: []byte(" ")})
	q.push(serverMsg{Ch: chTerminal, ID: "a", Type: msgExited})
	q.push(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: []byte("late")})

	frames := q.drain()
	type frame struct{ id, typ, data string }
	var got []frame
	for _, f := range frames {
		data, err := base64.StdEncoding.DecodeString(f.Data)
		if err != nil {
			t.Fatalf("frame data is not base64: %q", f.Data)
		}
		if f.raw != nil {
			t.Fatalf("drained frame still holds raw bytes")
		}
		got = append(got, frame{f.ID, f.Type, string(data)})
	}
	want := []frame{
		{"a", msgData, "hello"},
		{"b", msgData, "other"},
		{"a", msgData, " "},
		{"a", msgExited, ""},
		{"a", msgData, "late"},
	}
	if len(got) != len(want) {
		t.Fatalf("frames = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("frame %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestOutQueueBoundsAMergedFrame(t *testing.T) {
	q := newOutQueue()
	chunk := make([]byte, maxMergedData/2+1)
	q.push(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: chunk})
	q.push(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: chunk})
	if frames := q.drain(); len(frames) != 2 {
		t.Fatalf("frames = %d, want the second chunk in its own frame past maxMergedData", len(frames))
	}
}
