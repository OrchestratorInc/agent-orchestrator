package conpty

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func runtimeForFixture(id string, f *serveFixture) *Runtime {
	r := New(Options{})
	r.mu.Lock()
	r.sessions[id] = &hostSession{addr: f.addr, pid: f.pty.PID()}
	r.mu.Unlock()
	return r
}

func readUntil(t *testing.T, s io.Reader, want string, timeout time.Duration) string {
	t.Helper()
	type res struct {
		out string
	}
	done := make(chan res, 1)
	go func() {
		var buf []byte
		tmp := make([]byte, 4096)
		for {
			n, err := s.Read(tmp)
			if n > 0 {
				buf = append(buf, tmp[:n]...)
				if bytes.Contains(buf, []byte(want)) {
					done <- res{string(buf)}
					return
				}
			}
			if err != nil {
				done <- res{string(buf)}
				return
			}
		}
	}()
	select {
	case r := <-done:
		return r.out
	case <-time.After(timeout):
		t.Fatalf("timed out reading for %q", want)
		return ""
	}
}

func TestAttachReplaysScrollback(t *testing.T) {
	f := startServe(t, 300)
	defer f.cancel()
	f.ring.Append([]byte("scrollback-line\n"))

	r := runtimeForFixture("sess", f)
	s, err := r.Attach(context.Background(), nameHandle("sess"), 0, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer s.Close()

	out := readUntil(t, s, "scrollback-line", 2*time.Second)
	if !bytes.Contains([]byte(out), []byte("scrollback-line")) {
		t.Fatalf("scrollback not replayed on Read; got %q", out)
	}
}

func TestAttachWriteReachesPTY(t *testing.T) {
	f := startServe(t, 301)
	defer f.cancel()

	r := runtimeForFixture("sess", f)
	s, err := r.Attach(context.Background(), nameHandle("sess"), 0, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer s.Close()

	keystrokes := []byte("ls -la\r")
	if _, err := s.Write(keystrokes); err != nil {
		t.Fatalf("Write: %v", err)
	}
	buf := make([]byte, len(keystrokes))
	if _, err := io.ReadFull(f.pty.inR, buf); err != nil {
		t.Fatalf("read pty input: %v", err)
	}
	if string(buf) != string(keystrokes) {
		t.Fatalf("pty input = %q, want %q", buf, keystrokes)
	}
}

func TestAttachResizeReachesPTY(t *testing.T) {
	f := startServe(t, 302)
	defer f.cancel()

	r := runtimeForFixture("sess", f)
	s, err := r.Attach(context.Background(), nameHandle("sess"), 40, 132)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer s.Close()

	if err := s.Resize(50, 160); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		f.pty.resizeMu.Lock()
		n := len(f.pty.resizes)
		var last ResizePayload
		if n > 0 {
			last = f.pty.resizes[n-1]
		}
		f.pty.resizeMu.Unlock()
		if n >= 2 && last.Cols == 160 && last.Rows == 50 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	f.pty.resizeMu.Lock()
	defer f.pty.resizeMu.Unlock()
	t.Fatalf("resizes did not reach pty as expected: %+v", f.pty.resizes)
}

func TestAttachReportsHostedCommandExit(t *testing.T) {
	f := startServe(t, 303)
	defer f.cancel()

	r := runtimeForFixture("command", f)
	s, err := r.Attach(context.Background(), nameHandle("command"), 0, 0)
	if err != nil {
		t.Fatalf("Attach: %v", err)
	}
	defer s.Close()

	f.pty.signalExit(0)
	readDone := make(chan error, 1)
	go func() {
		_, readErr := s.Read(make([]byte, 1))
		readDone <- readErr
	}()
	select {
	case readErr := <-readDone:
		if !errors.Is(readErr, ports.ErrRuntimeProcessExited) {
			t.Fatalf("Read error = %v, want ErrRuntimeProcessExited", readErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("attached stream did not report hosted command exit")
	}
}

func TestAttachUnknownSession(t *testing.T) {
	r := New(Options{})
	if _, err := r.Attach(context.Background(), nameHandle("nope"), 0, 0); err == nil {
		t.Fatal("expected error attaching to unknown session")
	}
}

func nameHandle(id string) ports.RuntimeHandle {
	return ports.RuntimeHandle{ID: id}
}
