package mobilebridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// FixedTunnelSource gets this machine's fixed-address tunnel from AO Cloud.
// AO Cloud owns the tunnel and the Cloudflare credentials; the machine only
// receives its own run token. It proves the host with the digest of its
// account token, the same proof it publishes addresses with, so a machine no
// signed-in device has linked has no digest and stays on a quick tunnel.
type FixedTunnelSource struct {
	ControlPlaneURL string
	// StatePath is the Connect Mobile state (~/.ao/mobile/config.json), read
	// for the account token digest on every request: linking and unlinking
	// change it while the daemon runs.
	StatePath string
	// CachePath keeps the last tunnel (~/.ao/mobile/named-tunnel.json), so the
	// address survives AO Cloud being unreachable.
	CachePath string

	client *http.Client // tests
}

// NewFixedTunnelSource reads and caches under the data dir.
func NewFixedTunnelSource(controlPlaneURL, dataDir string) *FixedTunnelSource {
	return &FixedTunnelSource{
		ControlPlaneURL: controlPlaneURL,
		StatePath:       Path(dataDir),
		CachePath:       filepath.Join(dataDir, "mobile", "named-tunnel.json"),
	}
}

// NamedTunnel is what a connector needs to serve this host's fixed address.
type NamedTunnel struct {
	Hostname string `json:"hostname"`
	RunToken string `json:"runToken"`
	// Since is when this machine first got Hostname from AO Cloud. Its DNS
	// record is no older, so until it settles, resolvers that asked for the
	// name earlier may still hold an NXDOMAIN. Zero means long ago.
	Since time.Time `json:"since,omitzero"`
}

type cachedNamedTunnel struct {
	Key    string      `json:"key"`
	Tunnel NamedTunnel `json:"tunnel"`
}

var (
	// errNotLinked means no signed-in device has linked this machine yet.
	errNotLinked = errors.New("this machine is not linked to an AO account")
	// errHostRefused means AO Cloud does not list this machine under the
	// digest any more: it was removed from every account.
	errHostRefused = errors.New("AO Cloud no longer lists this machine")
)

// digest is the account token digest a signed-in device linked this machine
// with, or empty while it is not linked.
func (s *FixedTunnelSource) digest() string {
	state, err := Load(s.StatePath)
	if err != nil {
		return ""
	}
	return state.AccountTokenHash
}

// TunnelFor asks AO Cloud for hostID's tunnel and caches the answer. repair
// asks AO Cloud to make the tunnel exist again, for an address that stopped
// being served.
//
// When AO Cloud cannot answer it returns the cached tunnel together with the
// error, so the caller can log it and still start the connector: the run
// token is all a connector needs. A machine AO Cloud refuses loses the cache,
// because its tunnel is deleted with its last registration. A tunnel with an
// empty RunToken is unusable.
func (s *FixedTunnelSource) TunnelFor(ctx context.Context, hostID string, repair bool) (NamedTunnel, error) {
	state, err := Load(s.StatePath)
	if err != nil {
		return NamedTunnel{}, err
	}
	if state.AccountTokenHash == "" {
		return NamedTunnel{}, errNotLinked
	}
	key := s.cacheKey(hostID)
	cached := loadNamedTunnelCache(s.CachePath, key)
	t, err := s.request(ctx, hostID, state.AccountTokenHash, repair)
	if errors.Is(err, errHostRefused) {
		_ = os.Remove(s.CachePath)
		return NamedTunnel{}, err
	}
	if err != nil {
		return cached, err // the zero value when nothing is cached
	}
	// Kept across answers: the quick-to-fixed check and the rechecks all
	// write this cache, so "cached" never means "old".
	t.Since = cached.Since
	if cached.Hostname != t.Hostname {
		t.Since = time.Now()
	}
	// A lost cache only costs the fallback until the next good answer.
	if b, err := json.Marshal(cachedNamedTunnel{Key: key, Tunnel: t}); err == nil && s.CachePath != "" {
		_ = os.WriteFile(s.CachePath, b, 0o600)
	}
	return t, nil
}

func (s *FixedTunnelSource) cacheKey(hostID string) string {
	return strings.TrimRight(s.ControlPlaneURL, "/") + "/" + hostID
}

func loadNamedTunnelCache(path, key string) NamedTunnel {
	var cached cachedNamedTunnel
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &cached) != nil || cached.Key != key ||
		cached.Tunnel.Hostname == "" || cached.Tunnel.RunToken == "" {
		return NamedTunnel{}
	}
	return cached.Tunnel
}

var fixedTunnelClient = &http.Client{Timeout: 15 * time.Second}

func (s *FixedTunnelSource) request(ctx context.Context, hostID, digest string, repair bool) (NamedTunnel, error) {
	target, err := ControlPlaneTarget(s.ControlPlaneURL, "/api/cloud/v1/remote-hosts/"+url.PathEscape(hostID)+"/tunnel")
	if err != nil {
		return NamedTunnel{}, err
	}
	body, _ := json.Marshal(map[string]bool{"repair": repair})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, bytes.NewReader(body))
	if err != nil {
		return NamedTunnel{}, err
	}
	req.Header.Set("Authorization", "Bearer "+digest)
	req.Header.Set("Content-Type", "application/json")
	client := s.client
	if client == nil {
		client = fixedTunnelClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return NamedTunnel{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return NamedTunnel{}, errHostRefused
	case resp.StatusCode != http.StatusOK:
		return NamedTunnel{}, fmt.Errorf("AO Cloud returned %d for the fixed address", resp.StatusCode)
	}
	var t NamedTunnel
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&t); err != nil {
		return NamedTunnel{}, fmt.Errorf("decode fixed address: %w", err)
	}
	if t.Hostname == "" || t.RunToken == "" {
		return NamedTunnel{}, errors.New("AO Cloud returned a fixed address without a hostname or run token")
	}
	return t, nil
}

// ControlPlaneTarget joins path onto AO Cloud's base URL. Only https, or plain
// http to localhost for development, so a host credential never crosses the
// network in clear.
func ControlPlaneTarget(controlPlaneURL, path string) (string, error) {
	base, err := url.Parse(controlPlaneURL)
	if err != nil {
		return "", errors.New("invalid control-plane URL")
	}
	localHTTP := base.Scheme == "http" && (base.Hostname() == "localhost" || base.Hostname() == "127.0.0.1")
	if base.Scheme != "https" && !localHTTP {
		return "", errors.New("invalid control-plane URL")
	}
	return strings.TrimRight(controlPlaneURL, "/") + path, nil
}
