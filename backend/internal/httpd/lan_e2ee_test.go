package httpd

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"

	"github.com/coder/websocket"

	"github.com/aoagents/agent-orchestrator/backend/internal/mobilebridge"
)

// e2eeClient is the client half of the handshake, as the phone runs it.
type e2eeClient struct {
	conn net.Conn
	ws   *websocket.Conn
	mu   sync.Mutex
	sent [][]byte // every record sent, as it crossed the wire
}

func dialE2EE(t *testing.T, url string, hostKey ed25519.PublicKey) (*e2eeClient, error) {
	t.Helper()
	ws, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	ephemeral, _ := ecdh.X25519().GenerateKey(rand.Reader)
	if err := ws.Write(ctx, websocket.MessageBinary, append([]byte{e2eeVersion}, ephemeral.PublicKey().Bytes()...)); err != nil {
		return nil, err
	}
	_, reply, err := ws.Read(ctx)
	if err != nil {
		return nil, err
	}
	if len(reply) != 97 || reply[0] != e2eeVersion {
		return nil, fmt.Errorf("reply of %d bytes", len(reply))
	}
	transcript := e2eeTranscript(ephemeral.PublicKey().Bytes(), reply[1:33])
	if !ed25519.Verify(hostKey, transcript, reply[33:]) {
		return nil, fmt.Errorf("the host key did not sign the handshake")
	}
	hostEphemeral, _ := ecdh.X25519().NewPublicKey(reply[1:33])
	secret, err := ephemeral.ECDH(hostEphemeral)
	if err != nil {
		return nil, err
	}
	out, in, err := e2eeRecordKeys(secret, transcript)
	if err != nil {
		return nil, err
	}
	c := &e2eeClient{ws: ws}
	inner, outer := net.Pipe()
	c.conn = inner
	go func() {
		defer outer.Close()
		var n uint64
		buf := make([]byte, e2eeMaxPlain)
		for {
			k, err := outer.Read(buf)
			if k > 0 {
				record := out.Seal(nil, e2eeNonce(n), buf[:k], nil)
				n++
				c.mu.Lock()
				c.sent = append(c.sent, record)
				c.mu.Unlock()
				if ws.Write(ctx, websocket.MessageBinary, record) != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer outer.Close()
		var n uint64
		for {
			_, record, err := ws.Read(ctx)
			if err != nil {
				return
			}
			plain, err := in.Open(nil, e2eeNonce(n), record, nil)
			if err != nil {
				return
			}
			n++
			if _, err := outer.Write(plain); err != nil {
				return
			}
		}
	}()
	return c, nil
}

func (c *e2eeClient) get(t *testing.T, auth string) (int, string) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, "http://e2ee/anything", nil)
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	if err := req.Write(c.conn); err != nil {
		t.Fatalf("write request: %v", err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c.conn), req)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func startE2EELAN(t *testing.T) (string, ed25519.PublicKey) {
	t.Helper()
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, r.Proto+" "+r.URL.Path)
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(inner, st, 0, slog.Default(), nil)
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	m.UseHostKey(func() (ed25519.PrivateKey, error) { return private, nil })
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	t.Cleanup(func() { m.Stop(context.Background()) })
	return fmt.Sprintf("ws://127.0.0.1:%d%s", port, e2eePath), public
}

func TestE2EECarriesAuthorizedRequestsEncrypted(t *testing.T) {
	url, hostKey := startE2EELAN(t)
	c, err := dialE2EE(t, url, hostKey)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer c.ws.CloseNow()
	if status, _ := c.get(t, ""); status != http.StatusUnauthorized {
		t.Fatalf("request without a password: got %d, want 401", status)
	}
	if status, body := c.get(t, "Bearer secret12"); status != http.StatusOK || body != "HTTP/1.1 /anything" {
		t.Fatalf("authorized request: got %d %q", status, body)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, record := range c.sent {
		if bytes.Contains(record, []byte("secret12")) || bytes.Contains(record, []byte("/anything")) {
			t.Fatalf("a record carries plaintext: %q", record)
		}
	}
}

func TestE2EEHandshakeFailsForAnotherHostKey(t *testing.T) {
	url, _ := startE2EELAN(t)
	other, _, _ := ed25519.GenerateKey(rand.Reader)
	if _, err := dialE2EE(t, url, other); err == nil {
		t.Fatal("handshake verified against a key the host does not hold")
	}
}

func TestE2EEClosesOnATamperedRecord(t *testing.T) {
	url, hostKey := startE2EELAN(t)
	c, err := dialE2EE(t, url, hostKey)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	if err := c.ws.Write(context.Background(), websocket.MessageBinary, make([]byte, 40)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := c.ws.Read(context.Background()); err == nil {
		t.Fatal("the host answered after a record that does not decrypt")
	}
}

func TestE2EERefusesALowOrderKey(t *testing.T) {
	url, _ := startE2EELAN(t)
	ws, _, err := websocket.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer ws.CloseNow()
	if err := ws.Write(context.Background(), websocket.MessageBinary, append([]byte{e2eeVersion}, make([]byte, 32)...)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := ws.Read(context.Background()); err == nil {
		t.Fatal("the host answered a low-order key")
	}
}

// The phone's implementation (packages/mobile/lib/e2ee/handshake.test.ts)
// checks the same values.
func TestE2EETestVector(t *testing.T) {
	seq := func(start byte) []byte {
		b := make([]byte, 32)
		for i := range b {
			b[i] = start + byte(i)
		}
		return b
	}
	hostKey := ed25519.NewKeyFromSeed(seq(0x40))
	client, _ := ecdh.X25519().NewPrivateKey(seq(0x00))
	host, _ := ecdh.X25519().NewPrivateKey(seq(0x20))
	transcript := e2eeTranscript(client.PublicKey().Bytes(), host.PublicKey().Bytes())
	secret, _ := host.ECDH(client.PublicKey())
	toHost, toClient, err := e2eeRecordKeys(secret, transcript)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{
		"hostKey":      hex.EncodeToString(hostKey.Public().(ed25519.PublicKey)),
		"signature":    hex.EncodeToString(ed25519.Sign(hostKey, transcript)),
		"firstToHost":  hex.EncodeToString(toHost.Seal(nil, e2eeNonce(0), []byte("hello"), nil)),
		"secondToHost": hex.EncodeToString(toHost.Seal(nil, e2eeNonce(1), []byte("hello"), nil)),
		"firstToPhone": hex.EncodeToString(toClient.Seal(nil, e2eeNonce(0), []byte("hello"), nil)),
	}
	want := map[string]string{
		"hostKey":      "2543b92ff1095511476adc8369db6ddc933665a11978dda1404ee1066ca9559d",
		"signature":    "81ed928ebe753e3473a964e208f2672176e9589e3ca93b6b336bb63a0d39e0e6b9c9521010e2a48574a44d22fd652328418ec0f6f51d9aa4169c8516c2822605",
		"firstToHost":  "1c81cef78476ac94861f2e435da04c39209f4090f9",
		"secondToHost": "193168b762511db53e75f5e7c5ede3d01c759f22a2",
		"firstToPhone": "e5a4f93bdf8d52be21ddaa2d8a42aacef7fbd0c87a",
	}
	for name, value := range got {
		if want[name] != value {
			t.Errorf("%s = %s", name, value)
		}
	}
}

func TestE2EECarriesAWebSocket(t *testing.T) {
	echo := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		typ, msg, err := ws.Read(r.Context())
		if err == nil {
			_ = ws.Write(r.Context(), typ, append([]byte("echo "), msg...))
		}
	})
	st := &authState{}
	st.setHash(mobilebridge.HashPassword("secret12"))
	m := NewLANManager(echo, st, 0, slog.Default(), nil)
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	m.UseHostKey(func() (ed25519.PrivateKey, error) { return private, nil })
	port, err := m.Start(0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	defer m.Stop(context.Background())
	c, err := dialE2EE(t, fmt.Sprintf("ws://127.0.0.1:%d%s", port, e2eePath), public)
	if err != nil {
		t.Fatalf("handshake: %v", err)
	}
	defer c.ws.CloseNow()
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(context.Context, string, string) (net.Conn, error) { return c.conn, nil },
	}}
	inner, _, err := websocket.Dial(context.Background(), "ws://e2ee/mux", &websocket.DialOptions{
		HTTPClient: client,
		HTTPHeader: http.Header{"Authorization": {"Bearer secret12"}},
	})
	if err != nil {
		t.Fatalf("WebSocket inside the tunnel: %v", err)
	}
	defer inner.CloseNow()
	if err := inner.Write(context.Background(), websocket.MessageText, []byte("hi")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, msg, err := inner.Read(context.Background()); err != nil || string(msg) != "echo hi" {
		t.Fatalf("read: %q %v", msg, err)
	}
}
