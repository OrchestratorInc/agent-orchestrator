package mobilebridge

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"
)

// NamedTunnelConfig switches remote access from a quick tunnel to a named
// Cloudflare tunnel with a stable https://h-<id>.<zone apex> address. It is
// read from ~/.ao/mobile/cloudflare.json.
//
// ponytail: proof of concept. The account API token lives on this machine, so
// only use it on machines the token owner controls. In production AO Cloud
// holds the token, provisions the tunnel, reaps offline ones, and hands the
// daemon only the tunnel's run token.
type NamedTunnelConfig struct {
	AccountID string `json:"accountId"`
	ZoneID    string `json:"zoneId"`
	APIToken  string `json:"apiToken"`

	cachePath string // last provisioned tunnel; see NamedTunnelFor
	apiBase   string // tests point this at a fake API
}

// NamedTunnelConfigPath is ~/.ao/mobile/cloudflare.json.
func NamedTunnelConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "mobile", "cloudflare.json")
}

// LoadNamedTunnelConfig returns nil without an error when the file is absent,
// which keeps the quick tunnel as the default. The file is read once, when the
// tunnel controller is built, so editing it needs a daemon restart.
func LoadNamedTunnelConfig(dataDir string) (*NamedTunnelConfig, error) {
	b, err := os.ReadFile(NamedTunnelConfigPath(dataDir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil //nolint:nilnil // absent file means "use a quick tunnel"
	}
	if err != nil {
		return nil, err
	}
	var c NamedTunnelConfig
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse cloudflare.json: %w", err)
	}
	if c.AccountID == "" || c.ZoneID == "" || c.APIToken == "" {
		return nil, errors.New("cloudflare.json needs accountId, zoneId and apiToken")
	}
	c.cachePath = filepath.Join(dataDir, "mobile", "named-tunnel.json")
	return &c, nil
}

// NamedTunnelHostname is this host's address. Derived from the random HostID
// rather than stored, so the label never carries a machine or user name. It
// sits directly under the zone apex: Universal SSL covers one level only.
func NamedTunnelHostname(hostID, zone string) string {
	sum := sha256.Sum256([]byte("ao-named-tunnel:" + hostID))
	return "h-" + hex.EncodeToString(sum[:10]) + "." + zone
}

// NamedTunnel is what a connector needs to serve this host's address.
type NamedTunnel struct {
	Hostname string `json:"hostname"`
	RunToken string `json:"runToken"`
	// NewRecord is set when this provision published the DNS record, so
	// resolvers that asked for the name earlier may still hold an NXDOMAIN.
	NewRecord bool `json:"-"`
}

type cachedNamedTunnel struct {
	Key    string      `json:"key"`
	Tunnel NamedTunnel `json:"tunnel"`
}

// NamedTunnelFor returns this host's tunnel. With useCache it reuses the last
// provisioned one without calling the Cloudflare API: the run token is all a
// connector needs, so remote access survives the API being unreachable and a
// restart costs nothing. The cache is keyed by account, zone and host, so
// pointing cloudflare.json elsewhere provisions afresh.
func NamedTunnelFor(ctx context.Context, c NamedTunnelConfig, hostID string, useCache bool) (NamedTunnel, error) {
	key := c.AccountID + "/" + c.ZoneID + "/" + hostID
	if useCache {
		var cached cachedNamedTunnel
		if b, err := os.ReadFile(c.cachePath); err == nil && json.Unmarshal(b, &cached) == nil &&
			cached.Key == key && cached.Tunnel.Hostname != "" && cached.Tunnel.RunToken != "" {
			return cached.Tunnel, nil
		}
	}
	t, err := ProvisionNamedTunnel(ctx, c, hostID)
	if err != nil {
		return NamedTunnel{}, err
	}
	// A lost cache only costs one more provision on the next start.
	if b, err := json.Marshal(cachedNamedTunnel{Key: key, Tunnel: t}); err == nil && c.cachePath != "" {
		_ = os.WriteFile(c.cachePath, b, 0o600)
	}
	return t, nil
}

// ProvisionNamedTunnel makes this host's tunnel and its proxied DNS record
// exist, then returns them with the connector's run token. Idempotent. The
// tunnel is locally configured: the connector's --url names the port, so a port
// change needs no API call, and a connector left running by a crashed daemon
// keeps its old target instead of following a shared remote ingress.
func ProvisionNamedTunnel(ctx context.Context, c NamedTunnelConfig, hostID string) (NamedTunnel, error) {
	api := cloudflareAPI{base: c.apiBase, token: c.APIToken}

	var zone struct {
		Name string `json:"name"`
	}
	if err := api.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(c.ZoneID), nil, &zone); err != nil {
		return NamedTunnel{}, fmt.Errorf("read zone: %w", err)
	}
	if zone.Name == "" {
		return NamedTunnel{}, errors.New("cloudflare returned a zone without a name")
	}

	tunnels := "/accounts/" + url.PathEscape(c.AccountID) + "/cfd_tunnel"
	name := "ao-" + hostID
	var found []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := api.do(ctx, http.MethodGet, tunnels+"?is_deleted=false&name="+url.QueryEscape(name), nil, &found); err != nil {
		return NamedTunnel{}, fmt.Errorf("list tunnels: %w", err)
	}
	var id string
	for _, t := range found {
		if t.Name == name {
			id = t.ID
			break
		}
	}
	if id == "" {
		var created struct {
			ID string `json:"id"`
		}
		if err := api.do(ctx, http.MethodPost, tunnels, map[string]any{"name": name, "config_src": "local"}, &created); err != nil {
			return NamedTunnel{}, fmt.Errorf("create tunnel: %w", err)
		}
		id = created.ID
	}
	if id == "" {
		return NamedTunnel{}, errors.New("cloudflare returned a tunnel without an id")
	}

	out := NamedTunnel{Hostname: NamedTunnelHostname(hostID, zone.Name)}
	target := id + ".cfargotunnel.com"
	records := "/zones/" + url.PathEscape(c.ZoneID) + "/dns_records"
	record := map[string]any{"type": "CNAME", "name": out.Hostname, "content": target, "proxied": true, "ttl": 1}
	var existing []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		Proxied bool   `json:"proxied"`
	}
	if err := api.do(ctx, http.MethodGet, records+"?type=CNAME&name="+url.QueryEscape(out.Hostname), nil, &existing); err != nil {
		return NamedTunnel{}, fmt.Errorf("find dns record: %w", err)
	}
	var err error
	switch {
	case len(existing) == 0:
		err = api.do(ctx, http.MethodPost, records, record, nil)
		out.NewRecord = true
	case existing[0].Content != target || !existing[0].Proxied:
		// Recreated tunnel, or proxying switched off by hand: an unproxied
		// cfargotunnel.com CNAME serves nothing. Keep the address, repair it.
		err = api.do(ctx, http.MethodPut, records+"/"+existing[0].ID, record, nil)
	}
	if err != nil {
		return NamedTunnel{}, fmt.Errorf("write dns record: %w", err)
	}

	if err := api.do(ctx, http.MethodGet, tunnels+"/"+id+"/token", nil, &out.RunToken); err != nil {
		return NamedTunnel{}, fmt.Errorf("get run token: %w", err)
	}
	return out, nil
}

var cloudflareClient = &http.Client{Timeout: 30 * time.Second}

type cloudflareAPI struct{ base, token string }

// do sends one Cloudflare v4 API call and decodes the envelope's result.
func (a cloudflareAPI) do(ctx context.Context, method, path string, body, result any) error {
	base := a.base
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+a.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := cloudflareClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	var env struct {
		Success bool `json:"success"`
		Errors  []struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"errors"`
		Result json.RawMessage `json:"result"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil {
		return fmt.Errorf("cloudflare HTTP %d", resp.StatusCode)
	}
	if !env.Success {
		if len(env.Errors) > 0 {
			return fmt.Errorf("cloudflare %d: %s", env.Errors[0].Code, env.Errors[0].Message)
		}
		return fmt.Errorf("cloudflare HTTP %d", resp.StatusCode)
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(env.Result, result)
}
