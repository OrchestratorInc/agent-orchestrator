package browserruntime

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"
)

type wedgeHarness struct {
	broker *Broker
	addr   string
	conn   net.Conn
	enc    *json.Encoder
	dec    *json.Decoder
}

func newWedgeHarness(t *testing.T) *wedgeHarness {
	return newTunedWedgeHarness(t, nil)
}

func newTunedWedgeHarness(t *testing.T, tune func(*Broker)) *wedgeHarness {
	t.Helper()
	broker := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if tune != nil {
		tune(broker)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = broker.Serve(ctx, ln) }()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	enc := json.NewEncoder(conn)
	if err := enc.Encode(wireMessage{Type: "hello", Version: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(conn)
	expectHelloOK(t, dec)
	waitConnected(t, broker)
	return &wedgeHarness{
		broker: broker,
		addr:   ln.Addr().String(),
		conn:   conn,
		enc:    enc,
		dec:    dec,
	}
}

func expectHelloOK(t *testing.T, dec *json.Decoder) {
	t.Helper()
	var reply wireMessage
	if err := dec.Decode(&reply); err != nil {
		t.Fatalf("read hello reply: %v", err)
	}
	if reply.Type != "hello-ok" {
		t.Fatalf("hello reply = %#v, want hello-ok", reply)
	}
}

type executeOutcome struct {
	result Result
	err    error
}

func startWedgeCommand(t *testing.T, h *wedgeHarness, action string, timeout time.Duration) (<-chan executeOutcome, wireMessage, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	outcomes := make(chan executeOutcome, 1)
	go func() {
		result, err := h.broker.Execute(ctx, "session-1", action, nil)
		outcomes <- executeOutcome{result: result, err: err}
	}()
	_ = h.conn.SetReadDeadline(time.Now().Add(browserRuntimeTestTimeout()))
	var command wireMessage
	if err := h.dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	waitPendingRequest(t, h.broker, command.RequestID)
	return outcomes, command, cancel
}

func expectWedgeTimeout(t *testing.T, h *wedgeHarness, outcomes <-chan executeOutcome, command wireMessage) {
	t.Helper()
	var cancelMessage wireMessage
	if err := h.dec.Decode(&cancelMessage); err != nil {
		t.Fatal(err)
	}
	if cancelMessage.Type != "cancel" || cancelMessage.RequestID != command.RequestID {
		t.Fatalf("cancel message = %#v, command = %#v", cancelMessage, command)
	}
	select {
	case outcome := <-outcomes:
		if !errors.Is(outcome.err, context.DeadlineExceeded) {
			t.Fatalf("Execute error = %v, want context deadline", outcome.err)
		}
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("Execute did not return after the request deadline")
	}
	assertPendingEmpty(t, h.broker)
}

func assertPendingEmpty(t *testing.T, broker *Broker) {
	t.Helper()
	broker.mu.Lock()
	pending := len(broker.pending)
	broker.mu.Unlock()
	if pending != 0 {
		t.Fatalf("pending registry holds %d entries, want 0", pending)
	}
}

func TestBrokerExecuteDeadlineWithSilentRuntime(t *testing.T) {
	h := newWedgeHarness(t)
	outcomes, command, cancel := startWedgeCommand(t, h, "open", 100*time.Millisecond)
	defer cancel()
	expectWedgeTimeout(t, h, outcomes, command)
}

func TestBrokerNextCommandBoundedAfterWedge(t *testing.T) {
	h := newWedgeHarness(t)

	outcomes1, command1, cancel1 := startWedgeCommand(t, h, "open", 100*time.Millisecond)
	defer cancel1()
	expectWedgeTimeout(t, h, outcomes1, command1)

	outcomes2, command2, cancel2 := startWedgeCommand(t, h, "snapshot", 100*time.Millisecond)
	defer cancel2()
	if command2.RequestID == command1.RequestID {
		t.Fatal("broker reused the wedged request id")
	}
	expectWedgeTimeout(t, h, outcomes2, command2)

	if err := h.enc.Encode(wireMessage{
		Type:      "result",
		RequestID: command1.RequestID,
		OK:        true,
		Result:    json.RawMessage(`{"late":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	outcomes3, command3, cancel3 := startWedgeCommand(t, h, "snapshot", 2*browserRuntimeTestTimeout())
	defer cancel3()
	if err := h.enc.Encode(wireMessage{
		Type:      "result",
		RequestID: command3.RequestID,
		OK:        true,
		Result:    json.RawMessage(`{"ok":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-outcomes3:
		if outcome.err != nil {
			t.Fatalf("command after wedge failed: %v", outcome.err)
		}
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("command after wedge did not complete")
	}
	assertPendingEmpty(t, h.broker)
}

func TestBrokerStatusReportsConnectedDuringWedge(t *testing.T) {
	h := newWedgeHarness(t)
	outcomes, command, cancel := startWedgeCommand(t, h, "open", 2*browserRuntimeTestTimeout())
	defer cancel()

	status := h.broker.Status()
	if !status.Connected {
		t.Fatal("status dropped connected while the runtime socket is attached")
	}
	if status.ConnectedAt.IsZero() {
		t.Fatal("connectedAt is zero while the runtime socket is attached")
	}

	if err := h.enc.Encode(wireMessage{
		Type:      "result",
		RequestID: command.RequestID,
		OK:        true,
		Result:    json.RawMessage(`{"ok":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-outcomes:
		if outcome.err != nil {
			t.Fatalf("wedged command failed after the runtime answered: %v", outcome.err)
		}
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("wedged command did not complete after the runtime answered")
	}
}

func TestBrokerNewConnectionFailsStalePending(t *testing.T) {
	h := newWedgeHarness(t)
	outcomes, _, cancel := startWedgeCommand(t, h, "open", 2*browserRuntimeTestTimeout())
	defer cancel()

	replacement, err := net.Dial("tcp", h.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = replacement.Close() }()
	replacementEnc := json.NewEncoder(replacement)
	if err := replacementEnc.Encode(wireMessage{Type: "hello", Version: ProtocolVersion}); err != nil {
		t.Fatal(err)
	}
	replacementDec := json.NewDecoder(replacement)
	expectHelloOK(t, replacementDec)
	waitConnected(t, h.broker)

	select {
	case outcome := <-outcomes:
		if !errors.Is(outcome.err, ErrUnavailable) {
			t.Fatalf("stale command error = %v, want ErrUnavailable", outcome.err)
		}
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("stale in-flight command was not failed by the replacement connection")
	}
	if !h.broker.Status().Connected {
		t.Fatal("replacement connection did not keep the broker connected")
	}

	resultCh := make(chan executeOutcome, 1)
	go func() {
		result, err := h.broker.Execute(context.Background(), "session-1", "snapshot", nil)
		resultCh <- executeOutcome{result: result, err: err}
	}()
	_ = replacement.SetReadDeadline(time.Now().Add(browserRuntimeTestTimeout()))
	var command wireMessage
	if err := replacementDec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	if err := replacementEnc.Encode(wireMessage{
		Type:      "result",
		RequestID: command.RequestID,
		OK:        true,
		Result:    json.RawMessage(`{"ok":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-resultCh:
		if outcome.err != nil {
			t.Fatalf("command on replacement connection failed: %v", outcome.err)
		}
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("command on replacement connection did not complete")
	}
}

func startPonger(t *testing.T, h *wedgeHarness) <-chan wireMessage {
	t.Helper()
	frames := make(chan wireMessage, 16)
	go func() {
		dec := h.dec
		for {
			var msg wireMessage
			if err := dec.Decode(&msg); err != nil {
				return
			}
			if msg.Type == "ping" {
				_ = h.enc.Encode(wireMessage{Type: "pong"})
				continue
			}
			select {
			case frames <- msg:
			case <-time.After(browserRuntimeTestTimeout()):
				return
			}
		}
	}()
	return frames
}

func TestBrokerUnresponsiveRuntimeFailsPendingFast(t *testing.T) {
	h := newTunedWedgeHarness(t, func(b *Broker) {
		b.liveness = livenessConfig{interval: 20 * time.Millisecond, stallLimit: 80 * time.Millisecond}
	})
	requestCtx, cancel := context.WithTimeout(context.Background(), 2*browserRuntimeTestTimeout())
	defer cancel()
	outcomes := make(chan executeOutcome, 1)
	go func() {
		_, err := h.broker.Execute(requestCtx, "session-1", "open", nil)
		outcomes <- executeOutcome{err: err}
	}()
	_ = h.conn.SetReadDeadline(time.Now().Add(browserRuntimeTestTimeout()))
	var command wireMessage
	if err := h.dec.Decode(&command); err != nil {
		t.Fatal(err)
	}
	waitPendingRequest(t, h.broker, command.RequestID)

	select {
	case outcome := <-outcomes:
		if !errors.Is(outcome.err, ErrUnavailable) {
			t.Fatalf("stale command error = %v, want ErrUnavailable", outcome.err)
		}
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("unresponsive runtime was not failed by liveness detection")
	}
	if h.broker.Status().Connected {
		t.Fatal("unresponsive runtime still reported connected")
	}
}

func TestBrokerResponsiveRuntimeSurvivesLiveness(t *testing.T) {
	h := newTunedWedgeHarness(t, func(b *Broker) {
		b.liveness = livenessConfig{interval: 20 * time.Millisecond, stallLimit: 80 * time.Millisecond}
	})
	frames := startPonger(t, h)

	requestCtx, cancel := context.WithTimeout(context.Background(), 2*browserRuntimeTestTimeout())
	defer cancel()
	outcomes := make(chan executeOutcome, 1)
	go func() {
		result, err := h.broker.Execute(requestCtx, "session-1", "wait", nil)
		outcomes <- executeOutcome{result: result, err: err}
	}()

	var command wireMessage
	select {
	case command = <-frames:
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("timed out waiting for the command frame")
	}
	if command.Type != "command" {
		t.Fatalf("command = %#v", command)
	}
	time.Sleep(200 * time.Millisecond)
	if err := h.enc.Encode(wireMessage{
		Type:      "result",
		RequestID: command.RequestID,
		OK:        true,
		Result:    json.RawMessage(`{"ok":true}`),
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case outcome := <-outcomes:
		if outcome.err != nil {
			t.Fatalf("responsive runtime command failed: %v", outcome.err)
		}
	case <-time.After(browserRuntimeTestTimeout()):
		t.Fatal("responsive runtime command did not complete")
	}
	if !h.broker.Status().Connected {
		t.Fatal("responsive runtime was disconnected by liveness detection")
	}
}

func TestBrokerHelloVersionNegotiation(t *testing.T) {
	broker := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	broker.liveness = livenessConfig{interval: 20 * time.Millisecond, stallLimit: 80 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = broker.Serve(ctx, ln) }()

	stale, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(stale).Encode(wireMessage{Type: "hello", Version: 1}); err != nil {
		t.Fatal(err)
	}
	_ = stale.SetReadDeadline(time.Now().Add(time.Second))
	var rejection wireMessage
	if err := json.NewDecoder(stale).Decode(&rejection); err != nil {
		t.Fatalf("read hello reply: %v", err)
	}
	if rejection.Type != "hello-rejected" {
		t.Fatalf("hello reply = %#v, want hello-rejected", rejection)
	}
	if _, err := stale.Read(make([]byte, 1)); err == nil {
		t.Fatal("protocol v1 runtime connection remained open")
	}
	_ = stale.Close()
	if broker.Status().Connected {
		t.Fatal("protocol v1 runtime was accepted")
	}

	current, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = current.Close() }()
	if err := json.NewEncoder(current).Encode(wireMessage{Type: "hello", Version: 3}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(browserRuntimeTestTimeout())
	for !broker.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("protocol v3 runtime was not accepted")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestBrokerLegacyRuntimeGetsNoPings(t *testing.T) {
	broker := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	broker.liveness = livenessConfig{interval: 20 * time.Millisecond, stallLimit: 80 * time.Millisecond}
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
	if err := json.NewEncoder(conn).Encode(wireMessage{Type: "hello", Version: 2}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(browserRuntimeTestTimeout())
	for !broker.Status().Connected {
		if time.Now().After(deadline) {
			t.Fatal("protocol v2 runtime was not accepted")
		}
		time.Sleep(time.Millisecond)
	}

	dec := json.NewDecoder(conn)
	_ = conn.SetReadDeadline(time.Now().Add(browserRuntimeTestTimeout()))
	expectHelloOK(t, dec)

	_ = conn.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	var frame wireMessage
	if err := dec.Decode(&frame); err == nil {
		t.Fatalf("legacy runtime received an unexpected frame: %#v", frame)
	} else {
		var netErr net.Error
		if !errors.As(err, &netErr) || !netErr.Timeout() {
			t.Fatalf("legacy runtime connection failed unexpectedly: %v", err)
		}
	}
	if !broker.Status().Connected {
		t.Fatal("legacy runtime was disconnected by liveness detection")
	}
}
