package browserruntime

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	ProtocolVersion             = 3
	minSupportedProtocolVersion = 2
	RuntimeTokenStdinEnv        = "AO_BROWSER_RUNTIME_TOKEN_STDIN" //nolint:gosec // Environment variable name, not a credential.
	RuntimeAddressEnv           = "AO_BROWSER_RUNTIME_ADDRESS"
	helloTimeout                = 5 * time.Second
	helloReplyTimeout           = time.Second
	runtimeReconnectGrace       = 2 * time.Second
	maxRuntimeFrameBytes        = 8 << 20
)

func ReadRuntimeToken(r io.Reader) (string, error) {
	const maxTokenBytes = 256
	line, err := bufio.NewReader(io.LimitReader(r, maxTokenBytes+1)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read browser runtime token: %w", err)
	}
	token := strings.TrimSpace(line)
	if token == "" {
		return "", errors.New("browser runtime token handoff was empty")
	}
	if len(token) > maxTokenBytes {
		return "", errors.New("browser runtime token handoff was too long")
	}
	return token, nil
}

var ErrUnavailable = errors.New("browser runtime is unavailable")

var ErrReconnecting = errors.New("browser runtime is reconnecting")

var ErrUnresponsive = errors.New("browser runtime is unresponsive")

type livenessConfig struct {
	interval   time.Duration
	stallLimit time.Duration
}

var defaultLiveness = livenessConfig{interval: 5 * time.Second, stallLimit: 15 * time.Second}

type Status struct {
	Connected     bool
	ConnectedAt   time.Time
	Transport     string
	Client        string
	ClientVersion string
	Target        string
}

type Command struct {
	RequestID string                 `json:"requestId"`
	SessionID domain.SessionID       `json:"sessionId"`
	Action    string                 `json:"action"`
	Args      map[string]interface{} `json:"args,omitempty"`
}

type CommandError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e CommandError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return fmt.Sprintf("%s (%s)", e.Message, e.Code)
}

type Result struct {
	RequestID string
	Value     interface{}
}

type wireMessage struct {
	Type          string                 `json:"type"`
	Version       int                    `json:"version,omitempty"`
	Token         string                 `json:"token,omitempty"`
	Client        string                 `json:"client,omitempty"`
	ClientVersion string                 `json:"clientVersion,omitempty"`
	RequestID     string                 `json:"requestId,omitempty"`
	SessionID     domain.SessionID       `json:"sessionId,omitempty"`
	Action        string                 `json:"action,omitempty"`
	Args          map[string]interface{} `json:"args,omitempty"`
	OK            bool                   `json:"ok,omitempty"`
	Result        json.RawMessage        `json:"result,omitempty"`
	Error         *CommandError          `json:"error,omitempty"`
}

type pendingResult struct {
	value interface{}
	err   error
}

type Broker struct {
	log *slog.Logger

	mu                sync.Mutex
	conn              net.Conn
	connectedAt       time.Time
	pending           map[string]chan pendingResult
	writeGate         chan struct{}
	connectionChanged chan struct{}
	token             string
	transport         string
	client            string
	clientVersion     string
	verifyPeer        func(net.Conn) error
	exclusive         bool
	pongAt            time.Time

	liveness livenessConfig
}

func New(log *slog.Logger, token ...string) *Broker {
	if log == nil {
		log = slog.Default()
	}
	gate := make(chan struct{}, 1)
	gate <- struct{}{}
	runtimeToken := ""
	if len(token) > 0 {
		runtimeToken = token[0]
	}
	return &Broker{log: log, pending: make(map[string]chan pendingResult), writeGate: gate, connectionChanged: make(chan struct{}), token: runtimeToken, transport: TransportElectron, liveness: defaultLiveness}
}

const TransportElectron = "electron-webcontents-debugger"

func (b *Broker) SetTransport(name string) {
	b.mu.Lock()
	b.transport = name
	b.mu.Unlock()
}

func (b *Broker) SetExclusive(exclusive bool) {
	b.mu.Lock()
	b.exclusive = exclusive
	b.mu.Unlock()
}

func (b *Broker) SetPeerVerifier(verify func(net.Conn) error) {
	b.mu.Lock()
	b.verifyPeer = verify
	b.mu.Unlock()
}

func NewToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate browser runtime token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func (b *Broker) Status() Status {
	b.mu.Lock()
	defer b.mu.Unlock()
	return Status{
		Connected:     b.conn != nil,
		ConnectedAt:   b.connectedAt,
		Transport:     b.transport,
		Client:        b.client,
		ClientVersion: b.clientVersion,
	}
}

func (b *Broker) Execute(ctx context.Context, sessionID domain.SessionID, action string, args map[string]interface{}) (Result, error) {
	requestID := uuid.NewString()
	resultCh := make(chan pendingResult, 1)

	var conn net.Conn
	for {
		var err error
		conn, err = b.waitForConnection(ctx)
		if err != nil {
			return Result{}, err
		}
		b.mu.Lock()
		if b.conn == conn {
			b.pending[requestID] = resultCh
			b.mu.Unlock()
			break
		}
		b.mu.Unlock()
	}

	msg := wireMessage{
		Type:      "command",
		RequestID: requestID,
		SessionID: sessionID,
		Action:    action,
		Args:      args,
	}
	if err := b.write(ctx, conn, msg); err != nil {
		b.removePending(requestID)
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		b.disconnect(conn, fmt.Errorf("write browser command: %w", err))
		return Result{}, ErrUnavailable
	}

	select {
	case <-ctx.Done():
		b.removePending(requestID)
		cancelCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = b.write(cancelCtx, conn, wireMessage{Type: "cancel", RequestID: requestID})
		cancel()
		return Result{}, ctx.Err()
	case result := <-resultCh:
		if result.err != nil {
			return Result{}, result.err
		}
		return Result{RequestID: requestID, Value: result.value}, nil
	}
}

func (b *Broker) DestroySession(ctx context.Context, sessionID domain.SessionID) error {
	_, err := b.Execute(ctx, sessionID, "__destroy-session", nil)
	if errors.Is(err, ErrUnavailable) {
		return nil
	}
	return err
}

func (b *Broker) Serve(ctx context.Context, ln net.Listener) error {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil //nolint:nilerr // listener closure is the expected shutdown path
			}
			return fmt.Errorf("accept browser runtime: %w", err)
		}
		go b.serveConn(ctx, conn)
	}
}

func (b *Broker) serveConn(ctx context.Context, conn net.Conn) {
	b.mu.Lock()
	verify := b.verifyPeer
	b.mu.Unlock()
	if verify != nil {
		if err := verify(conn); err != nil {
			b.log.Warn("browser runtime peer rejected", "transport", b.transport, "err", err)
			_ = conn.Close()
			return
		}
	}
	_ = conn.SetReadDeadline(time.Now().Add(helloTimeout))
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), maxRuntimeFrameBytes)
	if !scanner.Scan() {
		_ = conn.Close()
		return
	}
	helloFrame := scanner.Bytes()
	if rejection := helloRejection(helloFrame, b.token); rejection != nil {
		_ = writeHelloReply(conn, wireMessage{Type: "hello-rejected", Error: rejection})
		_ = conn.Close()
		return
	}
	var hello wireMessage
	_ = json.Unmarshal(helloFrame, &hello)
	if b.busy() {
		_ = writeHelloReply(conn, wireMessage{Type: "hello-rejected", Error: errRuntimeBusy})
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if err := writeHelloReply(conn, wireMessage{Type: "hello-ok"}); err != nil {
		_ = conn.Close()
		return
	}

	b.mu.Lock()
	if b.exclusive && b.conn != nil {
		b.mu.Unlock()
		frame, _ := json.Marshal(wireMessage{Type: "rejected", Error: errRuntimeBusy})
		_ = conn.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = conn.Write(append(frame, '\n'))
		_ = conn.Close()
		return
	}
	old := b.conn
	b.conn = conn
	b.connectedAt = time.Now().UTC()
	b.client = hello.Client
	b.clientVersion = hello.ClientVersion
	b.signalConnectionChangedLocked()
	pending := b.takePendingLocked()
	if hello.Version >= ProtocolVersion {
		b.pongAt = time.Now()
	}
	b.mu.Unlock()
	if old != nil && old != conn {
		_ = old.Close()
	}
	failPending(pending, ErrUnavailable)
	b.log.Info("browser runtime connected", "transport", b.transport, "client", hello.Client, "clientVersion", hello.ClientVersion)

	if hello.Version >= ProtocolVersion {
		go b.runLiveness(ctx, conn)
	}

	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	for scanner.Scan() {
		var msg wireMessage
		if err := json.Unmarshal(scanner.Bytes(), &msg); err != nil {
			continue
		}
		if msg.Type == "pong" {
			b.mu.Lock()
			if b.conn == conn {
				b.pongAt = time.Now()
			}
			b.mu.Unlock()
			continue
		}
		if msg.Type != "result" || msg.RequestID == "" {
			continue
		}
		b.resolve(msg)
	}
	b.disconnect(conn, scanner.Err())
}

func (b *Broker) runLiveness(ctx context.Context, conn net.Conn) {
	ticker := time.NewTicker(b.liveness.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		b.mu.Lock()
		replaced := b.conn != conn
		stalled := time.Since(b.pongAt) > b.liveness.stallLimit
		b.mu.Unlock()
		if replaced {
			return
		}
		if stalled {
			b.disconnect(conn, ErrUnresponsive)
			return
		}
		pingCtx, cancel := context.WithTimeout(ctx, time.Second)
		err := b.write(pingCtx, conn, wireMessage{Type: "ping"})
		cancel()
		if err != nil {
			b.log.Debug("browser runtime ping failed", "err", err)
		}
	}
}

func (b *Broker) write(ctx context.Context, conn net.Conn, msg wireMessage) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.writeGate:
	}
	defer func() { b.writeGate <- struct{}{} }()

	deadline := time.Now().Add(30 * time.Second)
	if requested, ok := ctx.Deadline(); ok && requested.Before(deadline) {
		deadline = requested
	}
	if err := conn.SetWriteDeadline(deadline); err != nil {
		return err
	}
	interruptDone := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		_ = conn.SetWriteDeadline(time.Now())
		close(interruptDone)
	})
	frame, err := json.Marshal(msg)
	if err == nil && len(frame)+1 > maxRuntimeFrameBytes {
		err = fmt.Errorf("browser runtime frame exceeds %d bytes", maxRuntimeFrameBytes)
	}
	if err == nil {
		frame = append(frame, '\n')
		for len(frame) > 0 && err == nil {
			var written int
			written, err = conn.Write(frame)
			if written == 0 && err == nil {
				err = io.ErrShortWrite
				break
			}
			frame = frame[written:]
		}
	}
	writeComplete := err == nil && len(frame) == 0
	if !stop() {
		<-interruptDone
	}
	_ = conn.SetWriteDeadline(time.Time{})
	if writeComplete {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if requested, ok := ctx.Deadline(); ok && !time.Now().Before(requested) {
		return context.DeadlineExceeded
	}
	return err
}

var errRuntimeBusy = &CommandError{Code: "BROWSER_RUNTIME_BUSY", Message: "Another browser runtime is already connected"}

func (b *Broker) busy() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exclusive && b.conn != nil
}

func helloRejection(frame []byte, token string) *CommandError {
	var hello wireMessage
	switch {
	case json.Unmarshal(frame, &hello) != nil || hello.Type != "hello":
		return &CommandError{Code: "BROWSER_RUNTIME_BAD_HELLO", Message: "first frame was not a hello"}
	case hello.Version < minSupportedProtocolVersion || hello.Version > ProtocolVersion:
		return &CommandError{
			Code:    "BROWSER_RUNTIME_PROTOCOL_MISMATCH",
			Message: fmt.Sprintf("daemon speaks browser runtime protocol %d-%d, runtime sent %d", minSupportedProtocolVersion, ProtocolVersion, hello.Version),
		}
	case !validRuntimeToken(token, hello.Token):
		return &CommandError{
			Code:    "BROWSER_RUNTIME_TOKEN_REJECTED",
			Message: "browser runtime token does not match the one this daemon was started with",
		}
	}
	return nil
}

func writeHelloReply(conn net.Conn, msg wireMessage) error {
	frame, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err := conn.SetWriteDeadline(time.Now().Add(helloReplyTimeout)); err != nil {
		return err
	}
	defer func() { _ = conn.SetWriteDeadline(time.Time{}) }()
	_, err = conn.Write(append(frame, '\n'))
	return err
}

func validRuntimeToken(expected, actual string) bool {
	if expected == "" {
		return actual == ""
	}
	return hmac.Equal([]byte(expected), []byte(actual))
}

func (b *Broker) resolve(msg wireMessage) {
	b.mu.Lock()
	ch := b.pending[msg.RequestID]
	delete(b.pending, msg.RequestID)
	b.mu.Unlock()
	if ch == nil {
		return
	}
	if !msg.OK {
		if msg.Error == nil {
			msg.Error = &CommandError{Code: "BROWSER_COMMAND_FAILED", Message: "Browser command failed"}
		}
		ch <- pendingResult{err: *msg.Error}
		return
	}
	var value interface{} = map[string]interface{}{}
	if len(msg.Result) > 0 && string(msg.Result) != "null" {
		if err := json.Unmarshal(msg.Result, &value); err != nil {
			ch <- pendingResult{err: CommandError{Code: "BROWSER_RUNTIME_PROTOCOL_ERROR", Message: "Browser runtime returned an invalid result"}}
			return
		}
	}
	ch <- pendingResult{value: value}
}

func (b *Broker) disconnect(conn net.Conn, cause error) {
	b.mu.Lock()
	if b.conn != conn {
		b.mu.Unlock()
		return
	}
	b.conn = nil
	b.connectedAt = time.Time{}
	b.client = ""
	b.clientVersion = ""
	b.signalConnectionChangedLocked()
	pending := b.takePendingLocked()
	b.mu.Unlock()
	_ = conn.Close()
	failPending(pending, ErrUnavailable)
	if cause != nil && !errors.Is(cause, io.EOF) && !errors.Is(cause, net.ErrClosed) {
		b.log.Warn("browser runtime disconnected", "err", cause)
	} else {
		b.log.Info("browser runtime disconnected")
	}
}

func (b *Broker) waitForConnection(ctx context.Context) (net.Conn, error) {
	waitCtx, cancel := context.WithTimeout(ctx, runtimeReconnectGrace)
	defer cancel()
	for {
		b.mu.Lock()
		conn := b.conn
		changed := b.connectionChanged
		b.mu.Unlock()
		if conn != nil {
			return conn, nil
		}
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return nil, fmt.Errorf("%w: %w", ErrReconnecting, ErrUnavailable)
		case <-changed:
		}
	}
}

func (b *Broker) signalConnectionChangedLocked() {
	close(b.connectionChanged)
	b.connectionChanged = make(chan struct{})
}

func (b *Broker) removePending(requestID string) {
	b.mu.Lock()
	delete(b.pending, requestID)
	b.mu.Unlock()
}

func (b *Broker) takePendingLocked() map[string]chan pendingResult {
	pending := b.pending
	b.pending = make(map[string]chan pendingResult)
	return pending
}

func failPending(pending map[string]chan pendingResult, err error) {
	for _, ch := range pending {
		ch <- pendingResult{err: err}
	}
}
