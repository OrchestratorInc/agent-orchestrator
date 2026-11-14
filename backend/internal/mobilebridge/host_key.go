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

var hostKeyMu sync.Mutex

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

func HostPublicKey(statePath string) (string, error) {
	key, err := HostKey(statePath)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(key[ed25519.SeedSize:]), nil
}

var aoCloudSkew atomic.Int64

func NoteAOCloudDate(date string) {
	if served, err := http.ParseTime(date); err == nil {
		aoCloudSkew.Store(int64(time.Since(served)))
	}
}

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

func AuthorizeHostRequest(req *http.Request, body []byte, digest, statePath string) {
	req.Header.Set("Authorization", "Bearer "+digest)
	req.Header.Set("Content-Type", "application/json")
	if key, err := HostKey(statePath); err == nil {
		signHostRequest(req, body, key, time.Now().Add(-time.Duration(aoCloudSkew.Load())))
	}
}

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
