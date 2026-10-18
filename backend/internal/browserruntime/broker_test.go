package browserruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadRuntimeToken(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		want   string
		failed bool
	}{
		{name: "newline-delimited", input: "secret-token\n", want: "secret-token"},
		{name: "eof-delimited", input: "secret-token", want: "secret-token"},
		{name: "empty", input: "\n", failed: true},
		{name: "too-long", input: strings.Repeat("x", 257), failed: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ReadRuntimeToken(strings.NewReader(tt.input))
			if tt.failed {
				if err == nil {
					t.Fatalf("ReadRuntimeToken(%q) succeeded", tt.input)
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("ReadRuntimeToken(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
			}
		})
	}
}

func TestBrokerExecuteRoundTrip(t *testing.T) {
	broker := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = broker.Serve(ctx, ln) }()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	handshake(t, enc, dec, "")
	waitConnected(t, broker)

	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Execute(context.Background(), "session-1", "snapshot", map[string]interface{}{"interactive": true})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	var command wireMessage
	if err := dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	if command.Type != "command" || command.SessionID != "session-1" || command.Action != "snapshot" {
		t.Fatalf("command = %#v", command)
	}
	if err := enc.Encode(wireMessage{
		Type:      "result",
		RequestID: command.RequestID,
		OK:        true,
		Result:    json.RawMessage(`{"text":"button Save [ref=e1]"}`),
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		t.Fatal(err)
	case result := <-resultCh:
		value := result.Value.(map[string]interface{})
		if value["text"] != "button Save [ref=e1]" {
			t.Fatalf("result = %#v", result.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for result")
	}
}

func TestBrokerExecuteWaitsForRuntimeReconnect(t *testing.T) {
	broker := New(nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = broker.Serve(ctx, ln) }()

	resultCh := make(chan Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Execute(context.Background(), "session-1", "snapshot", nil)
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	handshake(t, enc, dec, "")
	var command wireMessage
	if err := dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	if command.Type != "command" || command.Action != "snapshot" {
		t.Fatalf("command = %#v", command)
	}
	if err := enc.Encode(wireMessage{Type: "result", RequestID: command.RequestID, OK: true, Result: json.RawMessage(`{"ok":true}`)}); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-errCh:
		t.Fatal(err)
	case result := <-resultCh:
		if result.Value.(map[string]interface{})["ok"] != true {
			t.Fatalf("result = %#v", result.Value)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for result")
	}
}

func TestBrokerExecuteHonorsContextWhileWaitingForRuntime(t *testing.T) {
	broker := New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := broker.Execute(ctx, "session-1", "snapshot", nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline", err)
	}
}

func TestBrokerMapsRuntimeError(t *testing.T) {
	broker := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = broker.Serve(ctx, ln) }()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	enc := json.NewEncoder(conn)
	dec := json.NewDecoder(conn)
	handshake(t, enc, dec, "")
	waitConnected(t, broker)

	errCh := make(chan error, 1)
	go func() {
		_, err := broker.Execute(context.Background(), "session-1", "click", map[string]interface{}{"ref": "e1"})
		errCh <- err
	}()
	var command wireMessage
	if err := dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	_ = enc.Encode(wireMessage{
		Type:      "result",
		RequestID: command.RequestID,
		Error:     &CommandError{Code: "STALE_REFERENCE", Message: "snapshot again"},
	})

	select {
	case err := <-errCh:
		var commandErr CommandError
		if !errors.As(err, &commandErr) || commandErr.Code != "STALE_REFERENCE" {
			t.Fatalf("error = %#v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for error")
	}
}

func TestBrokerUnavailableWithoutElectron(t *testing.T) {
	broker := New(nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := broker.Execute(ctx, "session-1", "snapshot", nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context deadline", err)
	}
}

func TestBrokerRejectsInvalidRuntimeToken(t *testing.T) {
	broker := New(nil, "expected-token")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = broker.Serve(ctx, ln) }()

	for _, tc := range []struct {
		name  string
		hello string
		code  string
	}{
		{"wrong token", `{"type":"hello","version":2,"token":"wrong-token"}`, "BROWSER_RUNTIME_TOKEN_REJECTED"},
		{"missing token", `{"type":"hello","version":2}`, "BROWSER_RUNTIME_TOKEN_REJECTED"},
		{"other protocol", `{"type":"hello","version":3,"token":"expected-token"}`, "BROWSER_RUNTIME_PROTOCOL_MISMATCH"},
		{"not a hello", `{"type":"result","requestId":"r1"}`, "BROWSER_RUNTIME_BAD_HELLO"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid, err := net.Dial("tcp", ln.Addr().String())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = invalid.Close() }()
			if _, err := io.WriteString(invalid, tc.hello+"\n"); err != nil {
				t.Fatal(err)
			}
			// The runtime learns it was refused, and why, before the daemon closes.
			_ = invalid.SetReadDeadline(time.Now().Add(browserRuntimeTestTimeout()))
			dec := json.NewDecoder(invalid)
			var reply wireMessage
			if err := dec.Decode(&reply); err != nil {
				t.Fatal(err)
			}
			if reply.Type != "hello-rejected" || reply.Error == nil || reply.Error.Code != tc.code {
				t.Fatalf("reply = %#v, want hello-rejected with %s", reply, tc.code)
			}
			// Only EOF proves the daemon closed the connection; a deadline timeout
			// would mean it stayed open.
			if err := dec.Decode(&reply); !errors.Is(err, io.EOF) {
				t.Fatalf("read after hello-rejected: err = %v, reply = %#v; want io.EOF from the daemon closing", err, reply)
			}
			if broker.Status().Connected {
				t.Fatal("broker accepted an invalid hello")
			}
		})
	}

	valid, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = valid.Close() }()
	handshake(t, json.NewEncoder(valid), json.NewDecoder(valid), "expected-token")
	waitConnected(t, broker)
}

func TestBrokerCancellationSendsCancelFrame(t *testing.T) {
	broker := New(nil)
	brokerConn, runtimeConn := net.Pipe()
	pausedConn := newPauseAfterFirstWriteConn(brokerConn)
	t.Cleanup(func() {
		pausedConn.release()
		_ = brokerConn.Close()
		_ = runtimeConn.Close()
	})
	broker.mu.Lock()
	broker.conn = pausedConn
	broker.connectedAt = time.Now().UTC()
	broker.mu.Unlock()
	dec := json.NewDecoder(runtimeConn)

	requestCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() {
		_, err := broker.Execute(requestCtx, "session-1", "wait", nil)
		errCh <- err
	}()
	var command wireMessage
	_ = runtimeConn.SetReadDeadline(time.Now().Add(browserRuntimeTestTimeout()))
	if err := dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	<-pausedConn.firstWriteDone
	waitPendingRequest(t, broker, command.RequestID)
	cancel()
	pausedConn.release()
	var cancelMessage wireMessage
	_ = runtimeConn.SetReadDeadline(time.Now().Add(browserRuntimeTestTimeout()))
	if err := dec.Decode(&cancelMessage); err != nil {
		t.Fatal(err)
	}
	_ = runtimeConn.SetReadDeadline(time.Time{})
	if cancelMessage.Type != "cancel" || cancelMessage.RequestID != command.RequestID {
		t.Fatalf("cancel message = %#v, command = %#v", cancelMessage, command)
	}
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Execute error = %v", err)
		}
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("Execute did not return after request cancellation")
	}
}

type pauseAfterFirstWriteConn struct {
	net.Conn
	firstWriteDone chan struct{}
	releaseWrite   chan struct{}
	pauseOnce      sync.Once
	releaseOnce    sync.Once
}

func newPauseAfterFirstWriteConn(conn net.Conn) *pauseAfterFirstWriteConn {
	return &pauseAfterFirstWriteConn{
		Conn:           conn,
		firstWriteDone: make(chan struct{}),
		releaseWrite:   make(chan struct{}),
	}
}

func (c *pauseAfterFirstWriteConn) Write(frame []byte) (int, error) {
	written, err := c.Conn.Write(frame)
	c.pauseOnce.Do(func() {
		close(c.firstWriteDone)
		<-c.releaseWrite
	})
	return written, err
}

func (c *pauseAfterFirstWriteConn) release() {
	c.releaseOnce.Do(func() { close(c.releaseWrite) })
}

func TestBrokerWriteObservesContext(t *testing.T) {
	broker := New(nil)
	server, client := net.Pipe()
	defer func() {
		_ = server.Close()
		_ = client.Close()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	started := time.Now()
	err := broker.write(ctx, client, wireMessage{Type: "command", RequestID: "blocked"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("write error = %v", err)
	}
	if time.Since(started) > time.Second {
		t.Fatal("context cancellation did not bound browser write")
	}
}

// handshake sends a hello and requires hello-ok as the daemon's first frame.
func handshake(t *testing.T, enc *json.Encoder, dec *json.Decoder, token string) {
	t.Helper()
	if err := enc.Encode(wireMessage{Type: "hello", Version: ProtocolVersion, Token: token}); err != nil {
		t.Fatal(err)
	}
	var reply wireMessage
	if err := dec.Decode(&reply); err != nil {
		t.Fatal(err)
	}
	if reply.Type != "hello-ok" {
		t.Fatalf("first daemon frame = %#v, want hello-ok", reply)
	}
}

func waitConnected(t *testing.T, broker *Broker) {
	t.Helper()
	deadline := time.Now().Add(browserRuntimeTestTimeout())
	for !broker.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("browser runtime did not connect")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitPendingRequest(t *testing.T, broker *Broker, requestID string) {
	t.Helper()
	deadline := time.Now().Add(browserRuntimeTestTimeout())
	for {
		broker.mu.Lock()
		_, ok := broker.pending[requestID]
		broker.mu.Unlock()
		if ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("browser request %q was not registered as pending", requestID)
		}
		time.Sleep(time.Millisecond)
	}
}

func browserRuntimeTestTimeout() time.Duration {
	if testing.Short() {
		return time.Second
	}
	return 5 * time.Second
}
