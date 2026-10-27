package browserchrome

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/browserruntime"
)

type extensionSide struct {
	t   *testing.T
	in  *io.PipeWriter // extension → host
	out *io.PipeReader // host → extension
}

func (e extensionSide) send(frame interface{}) {
	e.t.Helper()
	data, err := json.Marshal(frame)
	if err != nil {
		e.t.Fatal(err)
	}
	var header [4]byte
	binary.LittleEndian.PutUint32(header[:], uint32(len(data)))
	if _, err := e.in.Write(append(header[:], data...)); err != nil {
		e.t.Fatal(err)
	}
}

func (e extensionSide) receive() map[string]interface{} {
	e.t.Helper()
	raw, err := readNative(e.out, maxToExtensionBytes)
	if err != nil {
		e.t.Fatal(err)
	}
	var frame map[string]interface{}
	if err := json.Unmarshal(raw, &frame); err != nil {
		e.t.Fatal(err)
	}
	return frame
}

func startHost(t *testing.T, endpointPath string) (extensionSide, chan error) {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	errCh := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() {
		errCh <- RunHost(ctx, inR, outW, HostOptions{EndpointPath: endpointPath, Dial: func(ctx context.Context, address string) (net.Conn, error) {
			var dialer net.Dialer
			return dialer.DialContext(ctx, "tcp", address)
		}})
		_ = outW.Close()
	}()
	return extensionSide{t: t, in: inW, out: outR}, errCh
}

func startBroker(t *testing.T, token string) (*browserruntime.Broker, string) {
	t.Helper()
	broker := browserruntime.New(nil, token)
	broker.SetTransport(Transport)
	broker.SetExclusive(true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go func() { _ = broker.Serve(ctx, ln) }()
	endpointPath := filepath.Join(t.TempDir(), "endpoint.json")
	if err := WriteEndpoint(endpointPath, Endpoint{Address: ln.Addr().String(), Token: token, PID: os.Getpid(), Protocol: 2}); err != nil {
		t.Fatal(err)
	}
	return broker, endpointPath
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met")
		}
		time.Sleep(time.Millisecond)
	}
}

func TestHostRelaysCommandsAndResults(t *testing.T) {
	broker, endpointPath := startBroker(t, "secret")
	ext, _ := startHost(t, endpointPath)
	ext.send(map[string]interface{}{"type": "hello", "protocol": HostProtocol, "extensionVersion": "0.9.1"})
	if ready := ext.receive(); ready["type"] != "ready" {
		t.Fatalf("ready = %#v", ready)
	}
	waitFor(t, func() bool { return broker.Status().Connected })
	if status := broker.Status(); status.Client != Client || status.ClientVersion != "0.9.1" || status.Transport != Transport {
		t.Fatalf("status = %#v", status)
	}

	resultCh := make(chan browserruntime.Result, 1)
	errCh := make(chan error, 1)
	go func() {
		result, err := broker.Execute(context.Background(), "s1", "snapshot", map[string]interface{}{"interactive": true})
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- result
	}()
	command := ext.receive()
	if command["type"] != "command" || command["sessionId"] != "s1" || command["action"] != "snapshot" {
		t.Fatalf("command = %#v", command)
	}
	ext.send(map[string]interface{}{"type": "result", "requestId": command["requestId"], "ok": true, "result": map[string]interface{}{"text": "- button \"Save\" [ref=e1]"}})
	select {
	case err := <-errCh:
		t.Fatal(err)
	case result := <-resultCh:
		if result.Value.(map[string]interface{})["text"] != "- button \"Save\" [ref=e1]" {
			t.Fatalf("result = %#v", result.Value)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no result")
	}

	failCh := make(chan error, 1)
	go func() {
		_, err := broker.Execute(context.Background(), "s1", "click", nil)
		failCh <- err
	}()
	command = ext.receive()
	ext.send(map[string]interface{}{"type": "result", "requestId": command["requestId"], "ok": false, "error": map[string]string{"code": "BROWSER_CHROME_CONSENT_DENIED", "message": "denied"}})
	var commandErr browserruntime.CommandError
	if err := <-failCh; err == nil || !asCommandError(err, &commandErr) || commandErr.Code != "BROWSER_CHROME_CONSENT_DENIED" {
		t.Fatalf("error = %v", err)
	}
}

func asCommandError(err error, target *browserruntime.CommandError) bool {
	return errors.As(err, target)
}

func TestHostRejectsProtocolMismatch(t *testing.T) {
	_, endpointPath := startBroker(t, "secret")
	ext, errCh := startHost(t, endpointPath)
	ext.send(map[string]interface{}{"type": "hello", "protocol": 99})
	if frame := ext.receive(); frame["type"] != "error" || frame["code"] != "PROTOCOL_MISMATCH" {
		t.Fatalf("frame = %#v", frame)
	}
	if err := <-errCh; err == nil {
		t.Fatal("host accepted a mismatched protocol")
	}
}

func TestHostReportsMissingDaemon(t *testing.T) {
	ext, errCh := startHost(t, filepath.Join(t.TempDir(), "missing.json"))
	ext.send(map[string]interface{}{"type": "hello", "protocol": HostProtocol})
	if frame := ext.receive(); frame["type"] != "error" || frame["code"] != "DAEMON_UNAVAILABLE" {
		t.Fatalf("frame = %#v", frame)
	}
	if err := <-errCh; err == nil {
		t.Fatal("host ran without a daemon")
	}
}

func TestHostWrongTokenIsRejectedByDaemon(t *testing.T) {
	broker, endpointPath := startBroker(t, "secret")
	endpoint, err := ReadEndpoint(endpointPath)
	if err != nil {
		t.Fatal(err)
	}
	endpoint.Token = "wrong"
	if err := WriteEndpoint(endpointPath, endpoint); err != nil {
		t.Fatal(err)
	}
	ext, errCh := startHost(t, endpointPath)
	ext.send(map[string]interface{}{"type": "hello", "protocol": HostProtocol})
	if frame := ext.receive(); frame["type"] != "error" || frame["code"] != "BROWSER_RUNTIME_TOKEN_REJECTED" {
		t.Fatalf("frame = %#v", frame)
	}
	select {
	case <-errCh:
	case <-time.After(5 * time.Second):
		t.Fatal("host stayed up after the daemon rejected a bad token")
	}
	if broker.Status().Connected {
		t.Fatal("daemon accepted a wrong token")
	}
}

func TestHostForwardsBusyRejection(t *testing.T) {
	broker, endpointPath := startBroker(t, "secret")
	first, _ := startHost(t, endpointPath)
	first.send(map[string]interface{}{"type": "hello", "protocol": HostProtocol, "extensionVersion": "first"})
	first.receive()
	waitFor(t, func() bool { return broker.Status().Connected })

	second, errCh := startHost(t, endpointPath)
	second.send(map[string]interface{}{"type": "hello", "protocol": HostProtocol, "extensionVersion": "second"})
	if frame := second.receive(); frame["type"] != "error" || frame["code"] != "BROWSER_RUNTIME_BUSY" {
		t.Fatalf("frame = %#v", frame)
	}
	<-errCh
	if broker.Status().ClientVersion != "first" {
		t.Fatal("second profile displaced the first")
	}
}
