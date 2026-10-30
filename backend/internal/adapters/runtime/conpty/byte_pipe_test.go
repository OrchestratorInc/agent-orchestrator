package conpty

import (
	"errors"
	"testing"
	"time"
)

// Small host frames must reach the reader together: one Read per frame meant
// one WebSocket message per frame downstream.
func TestBytePipeReadReturnsAllBufferedOutput(t *testing.T) {
	p := newBytePipe()
	for _, chunk := range []string{"a", "b", "c"} {
		if _, err := p.Write([]byte(chunk)); err != nil {
			t.Fatalf("Write: %v", err)
		}
	}
	buf := make([]byte, 16)
	n, err := p.Read(buf)
	if err != nil || string(buf[:n]) != "abc" {
		t.Fatalf("Read = %q, %v; want all buffered output", buf[:n], err)
	}
}

// Back-pressure still reaches the pty-host: a full pipe holds the writer until
// the reader drains it.
func TestBytePipeWriterWaitsWhenFull(t *testing.T) {
	p := newBytePipe()
	if _, err := p.Write(make([]byte, maxPipeBuffered)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	wrote := make(chan struct{})
	go func() { _, _ = p.Write([]byte("x")); close(wrote) }()
	select {
	case <-wrote:
		t.Fatal("Write did not wait on a full pipe")
	case <-time.After(50 * time.Millisecond):
	}
	_, _ = p.Read(make([]byte, maxPipeBuffered))
	select {
	case <-wrote:
	case <-time.After(2 * time.Second):
		t.Fatal("Write stayed blocked after the reader drained the pipe")
	}
}

// Output written before the host ends (e.g. the process exited) is delivered
// before the closing error.
func TestBytePipeDeliversBufferedOutputBeforeTheCloseError(t *testing.T) {
	p := newBytePipe()
	_, _ = p.Write([]byte("last words"))
	exited := errors.New("process exited")
	_ = p.CloseWithError(exited)
	buf := make([]byte, 32)
	n, err := p.Read(buf)
	if err != nil || string(buf[:n]) != "last words" {
		t.Fatalf("first Read = %q, %v; want the buffered output", buf[:n], err)
	}
	if _, err := p.Read(buf); !errors.Is(err, exited) {
		t.Fatalf("second Read error = %v, want %v", err, exited)
	}
	if _, err := p.Write([]byte("more")); err == nil {
		t.Fatal("Write after close succeeded")
	}
}
