package conpty

import (
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// TestClientConnectionsDeclareControl: every daemon connection except Attach
// opens with MsgControlReq, so the host sends it no replay and no output.
func TestClientConnectionsDeclareControl(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probes := []struct {
		name string
		run  func(addr string)
	}{
		{"status", func(addr string) { _, _, _ = clientStatusContext(ctx, addr) }},
		{"output", func(addr string) { _, _ = clientGetOutput(ctx, addr, 10) }},
		{"styled output", func(addr string) { _, _ = clientGetStyledOutput(ctx, addr, 10) }},
		{"send message", func(addr string) { _ = clientSendMessage(addr, "") }},
		{"send input", func(addr string) { _ = clientSendInput(addr, "x") }},
		{"kill", func(addr string) { _ = clientKill(addr) }},
		{"watch foreground", func(addr string) { _ = clientWatchForeground(ctx, addr, func(ForegroundPayload) {}) }},
		{"watch title", func(addr string) { _ = clientWatchTitle(ctx, addr, func(TitlePayload) {}) }},
	}
	for _, probe := range probes {
		t.Run(probe.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ln.Close() }()
			first := make(chan byte, 1)
			go func() {
				conn, err := ln.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				header := make([]byte, 5)
				if _, err := io.ReadFull(conn, header); err == nil {
					first <- header[0]
				}
			}()
			probe.run(ln.Addr().String())
			select {
			case got := <-first:
				if got != MsgControlReq {
					t.Fatalf("first frame type = 0x%02x, want MsgControlReq", got)
				}
			case <-ctx.Done():
				t.Fatal("no frame received")
			}
		})
	}
}

// TestInputWriteFailsWhenHostStopsReading: a pty-host that stops reading must
// fail a send rather than block its caller, which may hold a lock.
func TestInputWriteFailsWhenHostStopsReading(t *testing.T) {
	client, host := net.Pipe()
	defer func() { _ = client.Close(); _ = host.Close() }()
	done := make(chan error, 1)
	go func() { done <- writeInputFrame(client, []byte("task"), 50*time.Millisecond) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("write to a host that never reads succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write to a host that never reads did not time out")
	}
}
