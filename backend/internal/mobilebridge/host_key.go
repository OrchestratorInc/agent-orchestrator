package mobilebridge

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// The host key is an Ed25519 key only this machine holds. A desktop that links
// the machine sends its public half to AO Cloud, which then needs the
// machine's signature on the machine's own requests: every signed-in device of
// the account can read the account token, but not this key.

var hostKeyMu sync.Mutex

// HostKey returns the machine's host key, kept in host.key beside the Connect
// Mobile config at statePath, and makes it on first use.
func HostKey(statePath string) (ed25519.PrivateKey, error) {
	if statePath == "" {
		return nil, errors.New("no Connect Mobile config path for the host key")
	}
	hostKeyMu.Lock()
	defer hostKeyMu.Unlock()
	path := filepath.Join(filepath.Dir(statePath), "host.key")
	seed, err := os.ReadFile(path)
	switch {
	case err == nil && len(seed) == ed25519.SeedSize:
		return ed25519.NewKeyFromSeed(seed), nil
	case err == nil:
		// A new key, as for a missing file: a link sends it to AO Cloud. Until
		// then a listing that holds the old one refuses this machine. The bad
		// file is kept aside for whoever looks into why.
		slog.Warn("the host key file is damaged; making a new host key", "path", path, "bytes", len(seed))
		_ = os.Rename(path, path+".bad")
	case !errors.Is(err, fs.ErrNotExist):
		slog.Warn("could not read the host key; AO Cloud requests go unsigned", "path", path, "error", err)
		return nil, err
	}
	_, key, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	if err := writeFileAtomic(path, key.Seed()); err != nil {
		slog.Warn("could not save a new host key; AO Cloud requests go unsigned", "path", path, "error", err)
		return nil, err
	}
	return key, nil
}

// HostPublicKey is the host key's public half as AO Cloud takes it: unpadded
// base64url.
func HostPublicKey(statePath string) (string, error) {
	key, err := HostKey(statePath)
	if err != nil {
		return "", err
	}
	// A private key is its seed followed by its public key.
	return base64.RawURLEncoding.EncodeToString(key[ed25519.SeedSize:]), nil
}

// aoCloudSkew is how far this machine's clock runs ahead of AO Cloud's, from
// the Date header of AO Cloud's last answer: a signature carries AO Cloud's
// time, and AO Cloud refuses one more than 5 minutes off.
var aoCloudSkew atomic.Int64

// NoteAOCloudDate records AO Cloud's clock from an answer's Date header.
func NoteAOCloudDate(date string) {
	if served, err := http.ParseTime(date); err == nil {
		aoCloudSkew.Store(int64(time.Since(served)))
	}
}

// DoHostRequest sends a machine's own request to AO Cloud, authorized by
// AuthorizeHostRequest. A 401 to a request signed on a clock AO Cloud's answer
// then corrected by more than a minute, as on the first request after a start
// on a machine whose clock is off, is signed again and sent once more: a
// wrong clock costs a request, not the fixed address.
func DoHostRequest(client *http.Client, req *http.Request, body []byte, digest, statePath string) (*http.Response, error) {
	for retried := false; ; retried = true {
		before := aoCloudSkew.Load()
		AuthorizeHostRequest(req, body, digest, statePath)
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		NoteAOCloudDate(resp.Header.Get("Date"))
		moved := time.Duration(aoCloudSkew.Load() - before)
		if retried || resp.StatusCode != http.StatusUnauthorized || req.Header.Get("X-AO-Host-Signature") == "" ||
			(moved < time.Minute && moved > -time.Minute) || req.GetBody == nil {
			return resp, nil
		}
		_ = resp.Body.Close()
		if req.Body, err = req.GetBody(); err != nil {
			return nil, err
		}
	}
}

// AuthorizeHostRequest sets what a machine's own request to AO Cloud carries:
// the account token digest and, when the host key can be read, its signature.
// Unsigned, it still works on a listing without a key and against an AO
// Cloud that takes none.
func AuthorizeHostRequest(req *http.Request, body []byte, digest, statePath string) {
	req.Header.Set("Authorization", "Bearer "+digest)
	req.Header.Set("Content-Type", "application/json")
	if key, err := HostKey(statePath); err == nil {
		signHostRequest(req, body, key, time.Now().Add(-time.Duration(aoCloudSkew.Load())))
	}
}

// signHostRequest sets X-AO-Host-Signature: the host key's signature over
// "ao-host-v1", the method, the path from /api/cloud/ on, the time and the
// body's SHA-256, each followed by a newline. The path starts at /api/cloud/
// because that is what AO Cloud receives behind a proxy that strips a prefix.
func signHostRequest(req *http.Request, body []byte, key ed25519.PrivateKey, at time.Time) {
	path := req.URL.Path
	if i := strings.LastIndex(path, "/api/cloud/"); i > 0 {
		path = path[i:]
	}
	sum := sha256.Sum256(body)
	unix := strconv.FormatInt(at.Unix(), 10)
	message := "ao-host-v1\n" + req.Method + "\n" + path + "\n" + unix + "\n" + hex.EncodeToString(sum[:]) + "\n"
	req.Header.Set("X-AO-Host-Signature", unix+"."+base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, []byte(message))))
}
