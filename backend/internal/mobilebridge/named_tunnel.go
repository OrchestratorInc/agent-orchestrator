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
	// Since is when AO Cloud made Hostname's DNS record. Until it settles,
	// resolvers that asked for the name earlier may still hold an NXDOMAIN.
	Since time.Time `json:"since,omitzero"`
	// Repaired is set when AO Cloud remade the tunnel for a repair request.
	Repaired bool `json:"repaired,omitempty"`
}

type cachedNamedTunnel struct {
	Key    string      `json:"key"`
	Tunnel NamedTunnel `json:"tunnel"`
}

var (
	// errNotLinked means no signed-in device has linked this machine yet.
	errNotLinked = errors.New("this machine is not linked to an AO account")
	// errHostRefused means AO Cloud does not list this machine under the
	// digest any more: its account removed it. Only AO Cloud's own answer
	// says so, never a proxy's.
	errHostRefused = errors.New("AO Cloud no longer lists this machine")
	// errNoFixedAddresses means this AO Cloud hands this machine no fixed
	// address: it has no tunnel settings or predates them, or the account
	// already uses all of its fixed addresses.
	errNoFixedAddresses = errors.New("AO Cloud hands out no fixed addresses")
)

// unlinked reports an answer that leaves the machine on a quick tunnel: no
// link, no listing, or no fixed addresses at this AO Cloud.
func unlinked(err error) bool {
	return errors.Is(err, errNotLinked) || errors.Is(err, errHostRefused) || errors.Is(err, errNoFixedAddresses)
}

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
// because its tunnel is deleted with its registration. A tunnel with an
// empty RunToken is unusable.
func (s *FixedTunnelSource) TunnelFor(ctx context.Context, hostID string, repair bool) (NamedTunnel, error) {
	state, err := Load(s.StatePath)
	if err != nil {
		return NamedTunnel{}, err
	}
	if state.AccountTokenHash == "" {
		// Unlinked, or the user's new password revoked the link: its run
		// token is not kept on disk.
		_ = os.Remove(s.CachePath)
		return NamedTunnel{}, errNotLinked
	}
	key := s.cacheKey(hostID, state.AccountTokenHash)
	cached, stale := loadNamedTunnelCache(s.CachePath, key)
	if stale {
		_ = os.Remove(s.CachePath) // another link's tunnel
	}
	t, err := s.request(ctx, hostID, state.AccountTokenHash, repair)
	if errors.Is(err, errHostRefused) || errors.Is(err, errNoFixedAddresses) {
		_ = os.Remove(s.CachePath)
		return NamedTunnel{}, err
	}
	if err != nil {
		return cached, err // the zero value when nothing is cached
	}
	// AO Cloud knows when the record was made, even one it deleted and made
	// again under the same name; the hostname alone cannot tell.
	if t.Since.IsZero() {
		t.Since = time.Now()
	}
	// A lost cache only costs the fallback until the next good answer.
	if b, err := json.Marshal(cachedNamedTunnel{Key: key, Tunnel: t}); err == nil && s.CachePath != "" {
		_ = writeFileAtomic(s.CachePath, b)
	}
	return t, nil
}

// cacheKey names the AO Cloud, host and account link a cached tunnel came
// from: a machine linked to another account since has another tunnel.
func (s *FixedTunnelSource) cacheKey(hostID, digest string) string {
	return strings.TrimRight(s.ControlPlaneURL, "/") + "/" + hostID + "#" + digest
}

// loadNamedTunnelCache reads the cached tunnel for key. stale is set when the
// file holds a tunnel for another key.
func loadNamedTunnelCache(path, key string) (tunnel NamedTunnel, stale bool) {
	var cached cachedNamedTunnel
	b, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(b, &cached) != nil {
		return NamedTunnel{}, false
	}
	if cached.Key != key {
		return NamedTunnel{}, true
	}
	if cached.Tunnel.Hostname == "" || cached.Tunnel.RunToken == "" {
		return NamedTunnel{}, false
	}
	return cached.Tunnel, false
}

// fixedTunnelClient follows no redirect, which would carry the host digest
// elsewhere, as the address publisher's client does.
var fixedTunnelClient = &http.Client{
	Timeout:       namedProvisionTimeout,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

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
	resp, err := DoHostRequest(fixedTunnelClient, req, body, digest, s.StatePath)
	if err != nil {
		return NamedTunnel{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		code := errorCode(resp)
		// A bare 404 is an AO Cloud that predates the route, or a proxy in
		// front of one, as during a deploy. Like an unreachable AO Cloud, it
		// keeps a cached tunnel: its tunnel and record are still there.
		switch {
		case resp.StatusCode == http.StatusUnauthorized && code == "unauthorized":
			return NamedTunnel{}, errHostRefused
		case code == "fixed_addresses_unavailable" && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusServiceUnavailable),
			resp.StatusCode == http.StatusConflict && code == "fixed_address_limit_reached":
			return NamedTunnel{}, errNoFixedAddresses
		}
		return NamedTunnel{}, fmt.Errorf("AO Cloud returned %d for the fixed address", resp.StatusCode)
	}
	var t NamedTunnel
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&t); err != nil {
		return NamedTunnel{}, fmt.Errorf("decode fixed address: %w", err)
	}
	if t.Hostname == "" || t.RunToken == "" {
		return NamedTunnel{}, errors.New("AO Cloud returned a fixed address without a hostname or run token")
	}
	// since is on AO Cloud's clock; moved onto this machine's, so a clock
	// running fast cannot skip the settle delay of a record made just now.
	// Date has a one-second resolution, so a smaller difference is noise.
	if served, err := http.ParseTime(resp.Header.Get("Date")); err == nil && !t.Since.IsZero() {
		if skew := time.Since(served); skew > 2*time.Second || skew < -2*time.Second {
			t.Since = t.Since.Add(skew)
		}
	}
	return t, nil
}

// IsHostDigestRefusal reports AO Cloud's own refusal of a host digest: no
// account lists the machine under it. A proxy's or portal's 401 is not one.
// It reads the body.
func IsHostDigestRefusal(resp *http.Response) bool {
	return resp.StatusCode == http.StatusUnauthorized && errorCode(resp) == "unauthorized"
}

// errorCode is the code in AO Cloud's error body, or "". It reads the body.
func errorCode(resp *http.Response) string {
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
	return body.Code
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
