package devices

import (
	"bytes"
	"io"
	"strconv"
	"time"
)

// A browser paints a multipart/x-mixed-replace part only once the next part
// begins, and the hub writes a part only when the screen changes. Left alone,
// an <img> viewer never shows a screen that is standing still and always
// hides the last frame of a change. Sending the last part again after a short
// quiet period makes it paint.
const (
	stillFrameDelay = 100 * time.Millisecond
	maxPartHeader   = 16 << 10
	maxPartBytes    = 16 << 20
)

// StillFrameStream wraps a hub MJPEG body so the last complete part is sent
// once more after the stream has been quiet for delay (zero uses the default).
// It follows the hub's framing ("--frame", Content-Length, CRLF CRLF, body,
// CRLF); framing it does not recognise turns the repeat off and the rest of
// the stream passes through untouched.
func StillFrameStream(body io.ReadCloser, delay time.Duration) io.ReadCloser {
	if delay <= 0 {
		delay = stillFrameDelay
	}
	pr, pw := io.Pipe()
	chunks := make(chan []byte)
	readErr := make(chan error, 1)
	go func() {
		defer close(chunks)
		buf := make([]byte, 64<<10)
		for {
			n, err := body.Read(buf)
			if n > 0 {
				chunk := append([]byte(nil), buf[:n]...)
				select {
				case chunks <- chunk:
				case <-time.After(time.Minute):
					readErr <- io.ErrClosedPipe
					return
				}
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()
	go func() {
		t := newPartTracker()
		timer := time.NewTimer(delay)
		timer.Stop()
		defer timer.Stop()
		repeat := false
		for {
			select {
			case chunk, ok := <-chunks:
				if !ok {
					_ = pw.CloseWithError(<-readErr)
					return
				}
				if _, err := pw.Write(chunk); err != nil {
					_ = body.Close()
					return
				}
				completed := t.feed(chunk)
				switch {
				case t.partial():
					// A part is in flight (perhaps right after one that completed in
					// the same chunk): repeating now would land inside it, and the
					// next complete part is the news anyway.
					repeat = false
					timer.Stop()
				case completed:
					repeat = true
					timer.Reset(delay)
				}
			case <-timer.C:
				if repeat {
					repeat = false
					if last := t.lastPart(); last != nil {
						if _, err := pw.Write(last); err != nil {
							_ = body.Close()
							return
						}
					}
				}
			}
		}
	}()
	return &stillFrameBody{reader: pr, closer: body}
}

type stillFrameBody struct {
	reader *io.PipeReader
	closer io.Closer
}

func (b *stillFrameBody) Read(p []byte) (int, error) { return b.reader.Read(p) }

func (b *stillFrameBody) Close() error {
	_ = b.reader.Close()
	return b.closer.Close()
}

// partTracker splits a byte stream into the hub's multipart parts.
type partTracker struct {
	pending []byte
	last    []byte
	off     bool
}

func newPartTracker() *partTracker { return &partTracker{} }

// feed adds bytes and reports whether at least one part completed.
func (t *partTracker) feed(chunk []byte) bool {
	if t.off {
		return false
	}
	t.pending = append(t.pending, chunk...)
	completed := false
	for {
		end := bytes.Index(t.pending, []byte("\r\n\r\n"))
		if end < 0 {
			if len(t.pending) > maxPartHeader {
				t.disable()
			}
			return completed
		}
		header := t.pending[:end]
		if !bytes.HasPrefix(header, []byte("--")) {
			t.disable()
			return completed
		}
		length := -1
		for _, line := range bytes.Split(header, []byte("\r\n")) {
			if name, value, ok := bytes.Cut(line, []byte(":")); ok && bytes.EqualFold(bytes.TrimSpace(name), []byte("content-length")) {
				if n, err := strconv.Atoi(string(bytes.TrimSpace(value))); err == nil {
					length = n
				}
			}
		}
		if length < 0 || length > maxPartBytes {
			t.disable()
			return completed
		}
		total := end + 4 + length + 2 // header, blank line, body, trailing CRLF
		if len(t.pending) < total {
			return completed
		}
		t.last = append(t.last[:0], t.pending[:total]...)
		t.pending = append(t.pending[:0], t.pending[total:]...)
		completed = true
	}
}

func (t *partTracker) partial() bool { return !t.off && len(t.pending) > 0 }

func (t *partTracker) lastPart() []byte {
	if t.off || len(t.last) == 0 {
		return nil
	}
	return append([]byte(nil), t.last...)
}

func (t *partTracker) disable() {
	t.off = true
	t.pending, t.last = nil, nil
}
