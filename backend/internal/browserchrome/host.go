package browserchrome

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
)

const (
	// Chrome rejects host-to-extension messages over 1 MiB.
	maxToExtensionBytes = 1 << 20
	// Results (screenshots) travel extension-to-host; cap them at the
	// daemon's runtime frame limit.
	maxFromExtensionBytes = 8 << 20
	extensionHelloTimeout = 10 * time.Second
)

// hostFrame is a frame between the extension and the host. Command, cancel
// and result frames pass through unchanged; hello, ready and error frames are
// the host's own handshake.
type hostFrame struct {
	Type             string `json:"type"`
	Protocol         int    `json:"protocol,omitempty"`
	ExtensionVersion string `json:"extensionVersion,omitempty"`
	Code             string `json:"code,omitempty"`
	Message          string `json:"message,omitempty"`
}

// Dialer connects to the daemon's Chrome runtime address.
type Dialer func(ctx context.Context, address string) (net.Conn, error)

// HostOptions configures RunHost.
type HostOptions struct {
	EndpointPath string
	Dial         Dialer
}

// RunHost relays frames between the extension (Chrome native messaging on in
// and out) and the daemon until either side closes.
func RunHost(ctx context.Context, in io.Reader, out io.Writer, opts HostOptions) error {
	writer := &nativeWriter{out: out}
	helloCh := make(chan hostFrame, 1)
	errCh := make(chan error, 1)
	go func() {
		raw, err := readNative(in, maxFromExtensionBytes)
		if err != nil {
			errCh <- err
			return
		}
		var hello hostFrame
		if err := json.Unmarshal(raw, &hello); err != nil {
			errCh <- fmt.Errorf("parse extension hello: %w", err)
			return
		}
		helloCh <- hello
	}()
	var hello hostFrame
	select {
	case hello = <-helloCh:
	case err := <-errCh:
		return err
	case <-time.After(extensionHelloTimeout):
		return errors.New("extension did not say hello")
	case <-ctx.Done():
		return ctx.Err()
	}
	if hello.Type != "hello" || hello.Protocol != HostProtocol {
		_ = writer.write(hostFrame{Type: "error", Code: "PROTOCOL_MISMATCH", Protocol: HostProtocol,
			Message: "The AO extension and AO app versions do not match; update both"})
		return fmt.Errorf("extension protocol %d, host protocol %d", hello.Protocol, HostProtocol)
	}

	endpoint, err := ReadEndpoint(opts.EndpointPath)
	if err != nil {
		_ = writer.write(hostFrame{Type: "error", Code: "DAEMON_UNAVAILABLE", Message: "AO is not running"})
		return err
	}
	dialCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	conn, err := opts.Dial(dialCtx, endpoint.Address)
	cancel()
	if err != nil {
		_ = writer.write(hostFrame{Type: "error", Code: "DAEMON_UNAVAILABLE", Message: "AO is not running"})
		return fmt.Errorf("connect to AO: %w", err)
	}
	defer func() { _ = conn.Close() }()
	daemonHello, err := json.Marshal(map[string]interface{}{
		"type":          "hello",
		"version":       browserruntime.ProtocolVersion,
		"token":         endpoint.Token,
		"client":        Client,
		"clientVersion": hello.ExtensionVersion,
	})
	if err != nil {
		return err
	}
	if _, err := conn.Write(append(daemonHello, '\n')); err != nil {
		_ = writer.write(hostFrame{Type: "error", Code: "DAEMON_UNAVAILABLE", Message: "AO closed the connection"})
		return err
	}
	scanner := bufio.NewScanner(conn)
	scanner.Buffer(make([]byte, 64*1024), maxFromExtensionBytes)
	// The daemon answers every hello: hello-ok, or hello-rejected with a reason
	// (bad token, protocol mismatch, another Chrome profile already connected).
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	if !scanner.Scan() {
		_ = writer.write(hostFrame{Type: "error", Code: "DAEMON_UNAVAILABLE", Message: "AO closed the connection"})
		return fmt.Errorf("no hello reply from AO: %w", scanner.Err())
	}
	_ = conn.SetReadDeadline(time.Time{})
	var reply struct {
		Type  string                       `json:"type"`
		Error *browserruntime.CommandError `json:"error"`
	}
	if json.Unmarshal(scanner.Bytes(), &reply) != nil || reply.Type != "hello-ok" {
		code, message := "DAEMON_REJECTED", "AO refused the connection"
		if reply.Error != nil {
			code, message = reply.Error.Code, reply.Error.Message
		}
		if code == "BROWSER_RUNTIME_BUSY" {
			message = "Another Chrome profile is already connected to AO"
		}
		_ = writer.write(hostFrame{Type: "error", Code: code, Message: message})
		return fmt.Errorf("AO rejected the host: %s", code)
	}
	if err := writer.write(hostFrame{Type: "ready", Protocol: HostProtocol}); err != nil {
		return err
	}

	done := make(chan error, 2)
	// Daemon → extension.
	go func() {
		for scanner.Scan() {
			line := bytes.TrimSpace(scanner.Bytes())
			if len(line) == 0 {
				continue
			}
			var probe struct {
				Type      string                       `json:"type"`
				RequestID string                       `json:"requestId"`
				Error     *browserruntime.CommandError `json:"error"`
			}
			if json.Unmarshal(line, &probe) != nil {
				continue
			}
			if probe.Type == "rejected" {
				code, message := "BROWSER_RUNTIME_BUSY", "Another Chrome profile is already connected to AO"
				if probe.Error != nil {
					code = probe.Error.Code
				}
				_ = writer.write(hostFrame{Type: "error", Code: code, Message: message})
				done <- errors.New("daemon rejected this runtime")
				return
			}
			if len(line) > maxToExtensionBytes {
				if probe.Type == "command" && probe.RequestID != "" {
					writeDaemonError(conn, probe.RequestID, "BROWSER_COMMAND_TOO_LARGE", "Browser command is too large for Chrome")
				}
				continue
			}
			if err := writer.writeRaw(line); err != nil {
				done <- err
				return
			}
		}
		done <- scanner.Err()
	}()
	// Extension → daemon.
	go func() {
		for {
			raw, err := readNative(in, maxFromExtensionBytes)
			if err != nil {
				done <- err
				return
			}
			var compact bytes.Buffer
			if json.Compact(&compact, raw) != nil {
				continue
			}
			compact.WriteByte('\n')
			if _, err := conn.Write(compact.Bytes()); err != nil {
				done <- err
				return
			}
		}
	}()
	select {
	case err := <-done:
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func writeDaemonError(conn net.Conn, requestID, code, message string) {
	frame, err := json.Marshal(map[string]interface{}{
		"type":      "result",
		"requestId": requestID,
		"ok":        false,
		"error":     map[string]string{"code": code, "message": message},
	})
	if err == nil {
		_, _ = conn.Write(append(frame, '\n'))
	}
}

// readNative reads one Chrome native-messaging frame: a 4-byte length in
// native (little-endian on every Chrome platform) order, then UTF-8 JSON.
func readNative(in io.Reader, limit int) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(in, header[:]); err != nil {
		return nil, err
	}
	size := binary.LittleEndian.Uint32(header[:])
	if int64(size) > int64(limit) {
		return nil, fmt.Errorf("native message of %d bytes exceeds %d", size, limit)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(in, body); err != nil {
		return nil, err
	}
	return body, nil
}

type nativeWriter struct {
	mu  sync.Mutex
	out io.Writer
}

func (w *nativeWriter) write(frame hostFrame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	return w.writeRaw(data)
}

func (w *nativeWriter) writeRaw(data []byte) error {
	if len(data) > maxToExtensionBytes {
		return fmt.Errorf("native message of %d bytes exceeds %d", len(data), maxToExtensionBytes)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data))) //nolint:gosec // bounded above
	if _, err := w.out.Write(header[:]); err != nil {
		return err
	}
	_, err := w.out.Write(data)
	return err
}
