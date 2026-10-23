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
// Cloudflare tunnel with a stable https://h-<id>.<zone> address. It is read
// from ~/.ao/mobile/cloudflare.json.
//
// ponytail: proof of concept. The account API token lives on this machine, so
// only use it on machines the token owner controls. In production AO Cloud
// holds the token, provisions the tunnel, reaps offline ones, and hands the
// daemon only the tunnel's run token.
type NamedTunnelConfig struct {
	AccountID string `json:"accountId"`
	ZoneID    string `json:"zoneId"`
	Zone      string `json:"zone"`
	APIToken  string `json:"apiToken"`

	apiBase string // tests point this at a fake API
}

// NamedTunnelConfigPath is ~/.ao/mobile/cloudflare.json.
func NamedTunnelConfigPath(dataDir string) string {
	return filepath.Join(dataDir, "mobile", "cloudflare.json")
}

// LoadNamedTunnelConfig returns nil without an error when the file is absent,
// which keeps the quick tunnel as the default.
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
	if c.AccountID == "" || c.ZoneID == "" || c.Zone == "" || c.APIToken == "" {
		return nil, errors.New("cloudflare.json needs accountId, zoneId, zone and apiToken")
	}
	return &c, nil
}

// NamedTunnelHostname is this host's address. Derived from the random HostID
// rather than stored, so Cloudflare is the only state and the label never
// carries a machine or user name.
func NamedTunnelHostname(hostID, zone string) string {
	sum := sha256.Sum256([]byte("ao-named-tunnel:" + hostID))
	return "h-" + hex.EncodeToString(sum[:10]) + "." + zone
}

// ProvisionNamedTunnel makes this host's tunnel, ingress and DNS record exist
// and point at localPort, then returns the hostname and the connector's run
// token. Idempotent: every connector start calls it, and on a port change only
// the ingress is rewritten.
func ProvisionNamedTunnel(ctx context.Context, c NamedTunnelConfig, hostID string, localPort int) (hostname, runToken string, err error) {
	api := cloudflareAPI{base: c.apiBase, token: c.APIToken}
	tunnels := "/accounts/" + url.PathEscape(c.AccountID) + "/cfd_tunnel"
	name := "ao-" + hostID

	var found []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := api.do(ctx, http.MethodGet, tunnels+"?is_deleted=false&name="+url.QueryEscape(name), nil, &found); err != nil {
		return "", "", fmt.Errorf("list tunnels: %w", err)
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
		if err := api.do(ctx, http.MethodPost, tunnels, map[string]any{"name": name, "config_src": "cloudflare"}, &created); err != nil {
			return "", "", fmt.Errorf("create tunnel: %w", err)
		}
		id = created.ID
	}
	if id == "" {
		return "", "", errors.New("cloudflare returned a tunnel without an id")
	}

	hostname = NamedTunnelHostname(hostID, c.Zone)
	ingress := map[string]any{"config": map[string]any{"ingress": []map[string]any{
		{"hostname": hostname, "service": fmt.Sprintf("http://127.0.0.1:%d", localPort)},
		{"service": "http_status:404"},
	}}}
	if err := api.do(ctx, http.MethodPut, tunnels+"/"+id+"/configurations", ingress, nil); err != nil {
		return "", "", fmt.Errorf("configure tunnel: %w", err)
	}

	records := "/zones/" + url.PathEscape(c.ZoneID) + "/dns_records"
	record := map[string]any{"type": "CNAME", "name": hostname, "content": id + ".cfargotunnel.com", "proxied": true, "ttl": 1}
	var existing []struct {
		ID      string `json:"id"`
		Content string `json:"content"`
	}
	if err := api.do(ctx, http.MethodGet, records+"?type=CNAME&name="+url.QueryEscape(hostname), nil, &existing); err != nil {
		return "", "", fmt.Errorf("find dns record: %w", err)
	}
	switch {
	case len(existing) == 0:
		err = api.do(ctx, http.MethodPost, records, record, nil)
	case existing[0].Content != record["content"]:
		// The tunnel was deleted and recreated: keep the address, repoint it.
		err = api.do(ctx, http.MethodPut, records+"/"+existing[0].ID, record, nil)
	}
	if err != nil {
		return "", "", fmt.Errorf("write dns record: %w", err)
	}

	if err := api.do(ctx, http.MethodGet, tunnels+"/"+id+"/token", nil, &runToken); err != nil {
		return "", "", fmt.Errorf("get run token: %w", err)
	}
	return hostname, runToken, nil
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
