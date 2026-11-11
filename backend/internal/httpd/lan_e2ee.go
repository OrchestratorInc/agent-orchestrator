package httpd

import (
	"context"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
)

// e2eePath carries the same requests as lanTunnelPath, encrypted between the
// client and this daemon, so whatever relays the WebSocket (Cloudflare, a LAN)
// sees only ciphertext. The upgrade carries no credential: every request inside
// is authorized by the LAN chain.
//
// The handshake is two WebSocket binary messages:
//
//	client: 0x01 | client ephemeral X25519 key (32)
//	host:   0x01 | host ephemeral X25519 key (32) | signature (64)
//
// The signature is the host key's Ed25519 signature over the transcript,
// e2eeLabel | client key | host key, so only the holder of the key the client
// pinned can answer. HKDF-SHA256 over the X25519 secret, salted with
// SHA-256(transcript), gives 64 bytes: the client-to-host key, then the
// host-to-client key. Every later message is one ChaCha20-Poly1305 record of at
// most e2eeMaxPlain bytes, its nonce the count of earlier records in its
// direction.
const (
	e2eePath     = "/api/v1/e2ee"
	e2eeLabel    = "ao-e2ee-v1\n"
	e2eeKeysInfo = "ao-e2ee-v1 keys"
	e2eeVersion  = 1
	e2eeMaxPlain = 16 << 10
)

var errE2EEHandshake = errors.New("e2ee handshake refused")

func (m *LANManager) serveE2EE(w http.ResponseWriter, r *http.Request) {
	var key ed25519.PrivateKey
	if m.hostKey != nil {
		key, _ = m.hostKey()
	}
	if key == nil {
		envelope.WriteAPIError(w, r, http.StatusServiceUnavailable, "unavailable", "HOST_KEY_UNAVAILABLE", "This machine's host key cannot be read.", nil)
		return
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return
	}
	defer func() { _ = ws.CloseNow() }()
	ws.SetReadLimit(e2eeMaxPlain + chacha20poly1305.Overhead)
	ctx := context.WithoutCancel(r.Context())
	handshake, cancel := context.WithTimeout(ctx, 10*time.Second)
	in, out, err := e2eeAccept(handshake, ws, key)
	cancel()
	if err != nil {
		return
	}

	// The router reads and writes plaintext on one end of a pipe; the pumps
	// move records between the other end and the WebSocket.
	inner, outer := net.Pipe()
	done := func() {
		_ = outer.Close()
		_ = ws.CloseNow()
	}
	go func() {
		defer done()
		var sent uint64
		buf := make([]byte, e2eeMaxPlain)
		for {
			n, err := outer.Read(buf)
			if n > 0 {
				if ws.Write(ctx, websocket.MessageBinary, out.Seal(nil, e2eeNonce(sent), buf[:n], nil)) != nil {
					return
				}
				sent++
			}
			if err != nil {
				return
			}
		}
	}()
	go func() {
		defer done()
		var received uint64
		for {
			typ, record, err := ws.Read(ctx)
			if err != nil || typ != websocket.MessageBinary {
				return
			}
			plain, err := in.Open(record[:0], e2eeNonce(received), record, nil)
			if err != nil {
				return
			}
			received++
			if _, err := outer.Write(plain); err != nil {
				return
			}
		}
	}()
	_, port, _ := net.SplitHostPort(r.RemoteAddr)
	m.serveTunnelConn(r, &tunnelConn{
		Conn:   inner,
		remote: tunnelAddr(net.JoinHostPort(clientSource(r), port)),
		closed: make(chan struct{}),
	})
}

// e2eeAccept answers the client's hello and returns the record keys: in for
// client-to-host, out for host-to-client.
func e2eeAccept(ctx context.Context, ws *websocket.Conn, hostKey ed25519.PrivateKey) (in, out cipher.AEAD, err error) {
	typ, hello, err := ws.Read(ctx)
	if err != nil {
		return nil, nil, err
	}
	if typ != websocket.MessageBinary || len(hello) != 33 || hello[0] != e2eeVersion {
		return nil, nil, errE2EEHandshake
	}
	clientKey, err := ecdh.X25519().NewPublicKey(hello[1:])
	if err != nil {
		return nil, nil, errE2EEHandshake
	}
	ephemeral, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	// ECDH refuses a low-order client key, whose secret would be all zeros.
	secret, err := ephemeral.ECDH(clientKey)
	if err != nil {
		return nil, nil, errE2EEHandshake
	}
	transcript := e2eeTranscript(hello[1:], ephemeral.PublicKey().Bytes())
	reply := append([]byte{e2eeVersion}, ephemeral.PublicKey().Bytes()...)
	reply = append(reply, ed25519.Sign(hostKey, transcript)...)
	if err := ws.Write(ctx, websocket.MessageBinary, reply); err != nil {
		return nil, nil, err
	}
	return e2eeRecordKeys(secret, transcript)
}

func e2eeTranscript(clientKey, hostKey []byte) []byte {
	transcript := append([]byte(e2eeLabel), clientKey...)
	return append(transcript, hostKey...)
}

// e2eeRecordKeys returns the client-to-host and host-to-client ciphers.
func e2eeRecordKeys(secret, transcript []byte) (cipher.AEAD, cipher.AEAD, error) {
	salt := sha256.Sum256(transcript)
	keys, err := hkdf.Key(sha256.New, secret, salt[:], e2eeKeysInfo, 2*chacha20poly1305.KeySize)
	if err != nil {
		return nil, nil, err
	}
	toHost, err := chacha20poly1305.New(keys[:chacha20poly1305.KeySize])
	if err != nil {
		return nil, nil, err
	}
	toClient, err := chacha20poly1305.New(keys[chacha20poly1305.KeySize:])
	return toHost, toClient, err
}

func e2eeNonce(count uint64) []byte {
	nonce := make([]byte, chacha20poly1305.NonceSize)
	binary.BigEndian.PutUint64(nonce[4:], count)
	return nonce
}
