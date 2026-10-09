package terminal

import (
	"context"
	"encoding/base64"
	"testing"
	"time"
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

// A frame queued after a quiet period (a keystroke's echo) is written at once;
// output that keeps arriving within burstFlushInterval of the previous write
// waits for it and leaves as one merged frame.
func TestWriteLoopPacesBurstsButNotIsolatedFrames(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn := newFakeConn()
	c := &connState{conn: conn, ctx: ctx, cancel: cancel, out: newOutQueue(), terms: map[string]*attachment{}}
	go c.writeLoop(ctx)

	start := time.Now()
	c.enqueue(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: []byte("e")})
	first := <-conn.out
	if elapsed := time.Since(start); elapsed >= burstFlushInterval {
		t.Fatalf("isolated frame took %v; it must not wait for the burst interval", elapsed)
	}
	if first.Data != base64.StdEncoding.EncodeToString([]byte("e")) {
		t.Fatalf("first frame = %q", first.Data)
	}

	// Right behind that write: these chunks must wait out the interval and merge.
	for _, chunk := range []string{"1", "2", "3"} {
		c.enqueue(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: []byte(chunk)})
	}
	select {
	case burst := <-conn.out:
		if burst.Data != base64.StdEncoding.EncodeToString([]byte("123")) {
			t.Fatalf("burst frame = %q, want the three chunks merged", burst.Data)
		}
	case <-time.After(time.Second):
		t.Fatal("paced burst was never written")
	}

	// After a quiet period the next frame is immediate again.
	time.Sleep(2 * burstFlushInterval)
	start = time.Now()
	c.enqueue(serverMsg{Ch: chTerminal, ID: "a", Type: msgData, raw: []byte("x")})
	<-conn.out
	if elapsed := time.Since(start); elapsed >= burstFlushInterval {
		t.Fatalf("frame after a quiet period took %v", elapsed)
	}
}
