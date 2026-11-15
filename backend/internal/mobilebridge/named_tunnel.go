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

type FixedTunnelSource struct {
	ControlPlaneURL string
	StatePath       string
	CachePath       string
}

func NewFixedTunnelSource(controlPlaneURL, dataDir string) *FixedTunnelSource {
	return &FixedTunnelSource{
		ControlPlaneURL: controlPlaneURL,
		StatePath:       Path(dataDir),
		CachePath:       filepath.Join(dataDir, "mobile", "named-tunnel.json"),
	}
}

type NamedTunnel struct {
	Hostname string    `json:"hostname"`
	RunToken string    `json:"runToken"`
	Since    time.Time `json:"since,omitzero"`
	Repaired bool      `json:"repaired,omitempty"`
}

type cachedNamedTunnel struct {
	Key    string      `json:"key"`
	Tunnel NamedTunnel `json:"tunnel"`
}

var (
	errNotLinked        = errors.New("this machine is not linked to an Orchestrator account")
	errHostRefused      = errors.New("this machine is no longer listed in Orchestrator Cloud")
	errNoFixedAddresses = errors.New("no fixed addresses are available from Orchestrator Cloud")
)

func unlinked(err error) bool {
	return errors.Is(err, errNotLinked) || errors.Is(err, errHostRefused) || errors.Is(err, errNoFixedAddresses)
}

func (s *FixedTunnelSource) digest() string {
	state, err := Load(s.StatePath)
	if err != nil {
		return ""
	}
	return state.AccountTokenHash
}

func (s *FixedTunnelSource) TunnelFor(ctx context.Context, hostID string, repair bool) (NamedTunnel, error) {
	state, err := Load(s.StatePath)
	if err != nil {
		return NamedTunnel{}, err
	}
	if state.AccountTokenHash == "" {
		_ = os.Remove(s.CachePath)
		return NamedTunnel{}, errNotLinked
	}
	key := s.cacheKey(hostID, state.AccountTokenHash)
	cached, stale := loadNamedTunnelCache(s.CachePath, key)
	if stale {
		_ = os.Remove(s.CachePath)
	}
	t, err := s.request(ctx, hostID, state.AccountTokenHash, repair)
	if errors.Is(err, errHostRefused) || errors.Is(err, errNoFixedAddresses) {
		_ = os.Remove(s.CachePath)
		return NamedTunnel{}, err
	}
	if err != nil {
		return cached, err
	}
	if t.Since.IsZero() {
		t.Since = time.Now()
	}
	if b, err := json.Marshal(cachedNamedTunnel{Key: key, Tunnel: t}); err == nil && s.CachePath != "" {
		_ = writeFileAtomic(s.CachePath, b)
	}
	return t, nil
}

func (s *FixedTunnelSource) cacheKey(hostID, digest string) string {
	return strings.TrimRight(s.ControlPlaneURL, "/") + "/" + hostID + "#" + digest
}

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
		switch {
		case resp.StatusCode == http.StatusUnauthorized && code == "unauthorized":
			return NamedTunnel{}, errHostRefused
		case code == "fixed_addresses_unavailable" && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusServiceUnavailable),
			resp.StatusCode == http.StatusConflict && code == "fixed_address_limit_reached":
			return NamedTunnel{}, errNoFixedAddresses
		}
		return NamedTunnel{}, fmt.Errorf("the fixed address request to Orchestrator Cloud returned %d", resp.StatusCode)
	}
	var t NamedTunnel
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&t); err != nil {
		return NamedTunnel{}, fmt.Errorf("decode fixed address: %w", err)
	}
	if t.Hostname == "" || t.RunToken == "" {
		return NamedTunnel{}, errors.New("the fixed address from Orchestrator Cloud has no hostname or run token")
	}
	if served, err := http.ParseTime(resp.Header.Get("Date")); err == nil && !t.Since.IsZero() {
		if skew := time.Since(served); skew > 2*time.Second || skew < -2*time.Second {
			t.Since = t.Since.Add(skew)
		}
	}
	return t, nil
}

func IsHostDigestRefusal(resp *http.Response) bool {
	return resp.StatusCode == http.StatusUnauthorized && errorCode(resp) == "unauthorized"
}

func errorCode(resp *http.Response) string {
	var body struct {
		Code string `json:"code"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&body)
	return body.Code
}

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
