package devices

import (
	"io"
	"strconv"
	"strings"
	"testing"
	"time"
)

func part(body string) string {
	return "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body + "\r\n"
}

type gatedReader struct {
	chunks chan string
}

func (g *gatedReader) Read(p []byte) (int, error) {
	chunk, ok := <-g.chunks
	if !ok {
		return 0, io.EOF
	}
	return copy(p, chunk), nil
}

func (g *gatedReader) Close() error { return nil }

func readAtLeast(t *testing.T, r io.Reader, n int) string {
	t.Helper()
	got := make([]byte, n)
	done := make(chan error, 1)
	go func() { _, err := io.ReadFull(r, got); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %d bytes", n)
	}
	return string(got)
}

// A still screen: the hub sends one part and goes quiet. The viewer gets that
// part again so the browser paints it, once, and then nothing more.
func TestStillFrameStreamRepeatsTheLastPartOnce(t *testing.T) {
	src := &gatedReader{chunks: make(chan string, 4)}
	out := StillFrameStream(src, 20*time.Millisecond)
	t.Cleanup(func() { _ = out.Close() })

	first := part("AAAA")
	src.chunks <- first
	if got := readAtLeast(t, out, len(first)); got != first {
		t.Fatalf("first read = %q, want the part untouched", got)
	}
	if got := readAtLeast(t, out, len(first)); got != first {
		t.Fatalf("repeat = %q, want the last part again", got)
	}

	second := part("BBBBBB")
	src.chunks <- second
	if got := readAtLeast(t, out, len(second)); got != second {
		t.Fatalf("next part = %q", got)
	}
	if got := readAtLeast(t, out, len(second)); got != second {
		t.Fatalf("second repeat = %q, want the newest part", got)
	}
}

// A part split across reads is never repeated half-sent, and the repeat is of
// the whole part.
func TestStillFrameStreamWaitsForACompletePart(t *testing.T) {
	src := &gatedReader{chunks: make(chan string, 4)}
	out := StillFrameStream(src, 20*time.Millisecond)
	t.Cleanup(func() { _ = out.Close() })

	whole := part("CCCCCCCC")
	src.chunks <- whole[:15]
	if got := readAtLeast(t, out, 15); got != whole[:15] {
		t.Fatalf("partial = %q", got)
	}
	time.Sleep(60 * time.Millisecond) // quiet while a part is in flight: nothing to repeat
	src.chunks <- whole[15:]
	if got := readAtLeast(t, out, len(whole)-15); got != whole[15:] {
		t.Fatalf("rest = %q", got)
	}
	if got := readAtLeast(t, out, len(whole)); got != whole {
		t.Fatalf("repeat = %q, want the whole part", got)
	}
}

// Framing it does not recognise passes through and is never repeated.
func TestStillFrameStreamPassesThroughUnknownFraming(t *testing.T) {
	src := &gatedReader{chunks: make(chan string, 2)}
	out := StillFrameStream(src, 20*time.Millisecond)
	t.Cleanup(func() { _ = out.Close() })

	src.chunks <- "not multipart\r\n\r\nbytes"
	if got := readAtLeast(t, out, len("not multipart\r\n\r\nbytes")); got != "not multipart\r\n\r\nbytes" {
		t.Fatalf("got %q", got)
	}
	close(src.chunks)
	rest, err := io.ReadAll(out)
	if err != nil || len(rest) != 0 {
		t.Fatalf("after EOF got %q, %v; want no repeat", rest, err)
	}
}

// A chunk that completes one part and starts the next must not arm the repeat:
// it would land inside the part still arriving.
func TestStillFrameDoesNotRepeatIntoAPartInFlight(t *testing.T) {
	part := func(body string) string {
		return "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + body + "\r\n"
	}
	pr, pw := io.Pipe()
	out := StillFrameStream(pr, 20*time.Millisecond)
	defer out.Close()
	second := part("BBBB")
	go func() {
		_, _ = pw.Write([]byte(part("AAAA") + second[:20]))
		time.Sleep(100 * time.Millisecond) // quiet, mid-part
		_, _ = pw.Write([]byte(second[20:]))
		_ = pw.Close()
	}()
	got, _ := io.ReadAll(out)
	if want := part("AAAA") + second; !strings.HasPrefix(string(got), want) {
		t.Fatalf("stream = %q, want %q before any repeat", got, want)
	}
}
