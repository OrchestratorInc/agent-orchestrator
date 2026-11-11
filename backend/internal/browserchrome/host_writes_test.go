package browserchrome

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// splitConn is a net.Conn whose Write emits each frame in two halves with a
// yield between them, which is what a real TCP write does when the socket
// buffer fills mid-frame. Two writers without a shared lock then interleave
// those halves.
type splitConn struct {
	net.Conn
	mu  sync.Mutex
	out strings.Builder
}

func (c *splitConn) Write(p []byte) (int, error) {
	half := len(p) / 2
	if half == 0 {
		half = len(p)
	}
	if _, err := c.write(p[:half]); err != nil {
		return 0, err
	}
	runtime.Gosched()
	if _, err := c.write(p[half:]); err != nil {
		return half, err
	}
	return len(p), nil
}

func (c *splitConn) write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.out.Write(p)
}

func (c *splitConn) SetWriteDeadline(time.Time) error { return nil }
func (c *splitConn) Close() error                     { return nil }

// TestConcurrentDaemonWritesStayWhole drives the one case two writers reach in
// production: the daemon-to-extension relay answers an oversized command with a
// result frame on the same connection the extension-to-daemon relay writes
// commands to. Unsynchronized writers interleave partial writes, and the daemon
// reads every malformed line as a command it cannot parse, dropping it.
func TestConcurrentDaemonWritesStayWhole(t *testing.T) {
	sink := &splitConn{}
	writer := &daemonWriter{conn: sink, timeout: 5 * time.Second}

	const rounds = 100
	payload := strings.Repeat("x", 4096)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range rounds {
			frame, err := json.Marshal(map[string]any{"type": "command", "requestId": fmt.Sprintf("ext-%d", i), "pad": payload})
			if err != nil {
				t.Error(err)
				return
			}
			if err := writer.write(append(frame, '\n')); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := range rounds {
			writeDaemonError(writer, fmt.Sprintf("daemon-%d", i), "BROWSER_COMMAND_TOO_LARGE", payload)
		}
	}()
	wg.Wait()

	// Every line the daemon would read must parse as one complete JSON frame.
	seen := map[string]bool{}
	scanner := bufio.NewScanner(strings.NewReader(sink.out.String()))
	scanner.Buffer(make([]byte, 64*1024), maxFromExtensionBytes)
	for scanner.Scan() {
		var frame map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			t.Fatalf("daemon read a partial frame (%v): %.120q", err, scanner.Text())
		}
		if id, ok := frame["requestId"].(string); ok {
			seen[id] = true
		}
	}
	if len(seen) != rounds*2 {
		t.Fatalf("frames parsed = %d of %d: corrupt frames lost commands", len(seen), rounds*2)
	}
}

// TestDaemonWriteIsDeadlineBounded: a daemon that stops reading must fail the
// write instead of blocking a relay goroutine until Chrome gives up.
func TestDaemonWriteIsDeadlineBounded(t *testing.T) {
	connA, connB := net.Pipe()
	defer func() { _ = connA.Close() }()
	defer func() { _ = connB.Close() }() // never read

	writer := &daemonWriter{conn: connA, timeout: 50 * time.Millisecond}
	errC := make(chan error, 1)
	go func() { errC <- writer.write([]byte(strings.Repeat("y", 1<<20))) }()

	select {
	case err := <-errC:
		if err == nil {
			t.Fatal("write to a daemon that never reads must fail")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write was not deadline-bounded")
	}
}

// TestHostAnswersBothRelaysOnOneConnection exercises the real wiring: the host
// stays up while an oversized command is answered and a normal command is
// relayed, and the daemon parses every frame.
func TestHostAnswersBothRelaysOnOneConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	received := make(chan map[string]any, 8)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Hello handshake, as the daemon's broker performs it.
		_, _ = conn.Write([]byte("{\"type\":\"hello-ok\"}\n"))
		scanner := bufio.NewScanner(conn)
		scanner.Buffer(make([]byte, 64*1024), maxFromExtensionBytes)
		for scanner.Scan() {
			var frame map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
				t.Errorf("daemon read a partial frame: %v: %q", err, scanner.Text())
				return
			}
			received <- frame
		}
	}()

	endpointPath := t.TempDir() + "/endpoint.json"
	if err := WriteEndpoint(endpointPath, Endpoint{
		Address: ln.Addr().String(), Token: "secret", Protocol: 2,
	}); err != nil {
		t.Fatal(err)
	}

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ext := extensionSide{t: t, in: inW, out: outR}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errC := make(chan error, 1)
	go func() {
		errC <- RunHost(ctx, inR, outW, HostOptions{EndpointPath: endpointPath, Dial: func(ctx context.Context, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", address)
		}})
		_ = outW.Close()
	}()

	ext.send(map[string]interface{}{"type": "hello", "protocol": HostProtocol})
	if ready := ext.receive(); ready["type"] != "ready" {
		t.Fatalf("ready = %#v", ready)
	}

	// A normal command must reach the daemon whole while the relay goroutines
	// are both writing.
	for i := range 20 {
		ext.send(map[string]interface{}{"type": "command", "requestId": fmt.Sprintf("r%d", i), "action": "snapshot"})
	}

	var mu sync.Mutex
	seen := map[string]bool{}
	deadline := time.After(5 * time.Second)
	for len(seen) < 20 {
		select {
		case frame := <-received:
			mu.Lock()
			if id, ok := frame["requestId"].(string); ok {
				seen[id] = true
			}
			mu.Unlock()
		case <-deadline:
			t.Fatalf("daemon received %d of 20 commands: %v", len(seen), seen)
		}
	}
	select {
	case err := <-errC:
		t.Fatalf("host exited: %v", err)
	default:
	}
}
