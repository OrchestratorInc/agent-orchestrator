package persistenthost

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

func TestControllerWritePreservesSlowReaderAndReleasesStoppedReader(t *testing.T) {
	original := clientWriteTimeout
	clientWriteTimeout = 500 * time.Millisecond
	t.Cleanup(func() { clientWriteTimeout = original })
	server, client := net.Pipe()
	output := &controllerConn{Conn: server}
	t.Cleanup(func() { _ = output.Close(); _ = client.Close() })
	if err := client.SetReadDeadline(time.Now().Add(20 * time.Second)); err != nil {
		t.Fatal(err)
	}
	controlServer, controlClient := net.Pipe()
	signal := &readProgress{conn: controlClient}
	t.Cleanup(signal.close)
	progressDone := make(chan struct{})
	go func() { defer close(progressDone); output.serveProgress(controlServer, bufio.NewReader(controlServer)) }()
	var hello helloResponse
	if err := json.NewDecoder(controlClient).Decode(&hello); err != nil || !hello.OK {
		t.Fatalf("progress handshake: %+v, %v", hello, err)
	}
	reader := &progressReader{reader: client, signal: signal}
	frame := bytes.Repeat([]byte("ordered replay\n"), 100000)
	done := make(chan error, 1)
	started := time.Now()
	go func() {
		err := writeClientFrame(output, frame)
		if err != nil {
			_ = output.Close()
		}
		done <- err
	}()
	got := make([]byte, 0, len(frame))
	buffer := make([]byte, 4096)
	for len(got) < len(frame) {
		n, err := reader.Read(buffer)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, buffer[:n]...)
		time.Sleep(5 * time.Millisecond)
	}
	if err := <-done; err != nil {
		t.Fatalf("progressing reader disconnected: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatal("partial, duplicated, or reordered replay")
	}
	if time.Since(started) < 2*clientWriteTimeout {
		t.Fatal("fixture did not exceed the write budget")
	}

	// Idle time must not leave a stale deadline on the next frame.
	time.Sleep(2 * clientWriteTimeout)
	go func() { done <- writeClientFrame(output, []byte("next\n")) }()
	next := make([]byte, 5)
	if _, err := io.ReadFull(reader, next); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if string(next) != "next\n" {
		t.Fatalf("next frame = %q", next)
	}

	// Now keep both sockets open but stop consuming output. The existing
	// delivery failure path must release ownership and retain the whole frame.
	h := &host{ctx: context.Background(), client: output, pendingRequests: make(map[string]*pendingRequest)}
	h.cond = sync.NewCond(&h.mu)
	blocked := []byte("unread frame\n")
	started = time.Now()
	h.mu.Lock()
	h.deliverProviderFrameLocked(blocked, false)
	h.mu.Unlock()
	if time.Since(started) > 5*clientWriteTimeout {
		t.Fatal("stalled writer did not release ownership promptly")
	}
	if h.client != nil {
		t.Fatal("stopped reader retained controller ownership")
	}
	if len(h.detached) != 1 || !bytes.Equal(h.detached[0], blocked) {
		t.Fatalf("failed frame not retained: %q", h.detached)
	}
	select {
	case <-progressDone:
	case <-time.After(time.Second):
		t.Fatal("progress connection leaked after detach")
	}
}

func TestAttachPreservesHostsWithoutProgressCapability(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	done := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			done <- err
			return
		}
		defer func() { _ = conn.Close() }()
		var request hello
		if err := json.NewDecoder(conn).Decode(&request); err != nil {
			done <- err
			return
		}
		// Old hosts do not advertise a progress token. Their provider stream
		// must remain usable without attempting a second connection.
		if err := json.NewEncoder(conn).Encode(helloResponse{OK: true}); err != nil {
			done <- err
			return
		}
		_, err = io.WriteString(conn, "provider frame\n")
		done <- err
	}()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	transport, err := attach(ctx, Descriptor{Version: ProtocolVersion, Address: listener.Addr().String(), Token: "old-host"}, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = transport.Stdin.Close() })
	got, err := io.ReadAll(transport.Stdout)
	if err != nil || string(got) != "provider frame\n" {
		t.Fatalf("legacy stream = %q, %v", got, err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestProgressCapabilityRejectsUnknownOrRetiredAttachment(t *testing.T) {
	h := &host{token: "host-secret"}
	retired := &controllerConn{}
	h.progress.Store("retired", retired)
	h.progress.Delete("retired")
	for _, token := range []string{"", "wrong", "retired"} {
		t.Run(token, func(t *testing.T) {
			server, client := net.Pipe()
			defer func() { _ = client.Close() }()
			_ = client.SetDeadline(time.Now().Add(time.Second))
			done := make(chan struct{})
			go func() { defer close(done); h.handle(server) }()
			if err := json.NewEncoder(client).Encode(hello{Version: ProtocolVersion, Token: h.token, Action: "progress", ProgressToken: token}); err != nil {
				t.Fatal(err)
			}
			var response helloResponse
			if err := json.NewDecoder(client).Decode(&response); err != nil {
				t.Fatal(err)
			}
			if response.OK || response.Error != ErrUnauthorized.Error() {
				t.Fatalf("unexpected response: %+v", response)
			}
			<-done
		})
	}
}
