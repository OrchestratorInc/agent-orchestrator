// Package hosttunnel gives each linked machine a Cloudflare tunnel with a fixed
// address. AO Cloud holds the Cloudflare API token; a machine only ever
// receives its own tunnel's run token.
package hosttunnel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// Cloudflare provisions tunnels in one account and their DNS records in one
// zone of that account.
type Cloudflare struct {
	AccountID string
	ZoneID    string
	// Zone is the zone apex, such as useao.dev.
	Zone     string
	APIToken string
	// BaseURL and HTTP are for tests; empty uses the real API.
	BaseURL string
	HTTP    *http.Client
}

// Tunnel is what a machine needs to serve its fixed address.
type Tunnel struct {
	ID       string
	Hostname string
	RunToken string
}

// Hostname is hostID's fixed address. Derived from the random host ID rather
// than stored, so the label never carries a machine or user name, and a
// recreated tunnel keeps the address. It sits directly under the zone apex:
// Universal SSL covers one level only.
func (c *Cloudflare) Hostname(hostID string) string {
	sum := sha256.Sum256([]byte("ao-named-tunnel:" + hostID))
	return "h-" + hex.EncodeToString(sum[:10]) + "." + c.Zone
}

func tunnelName(hostID string) string { return "ao-" + hostID }

func (c *Cloudflare) tunnelsPath() string {
	return "/accounts/" + url.PathEscape(c.AccountID) + "/cfd_tunnel"
}

func (c *Cloudflare) recordsPath() string {
	return "/zones/" + url.PathEscape(c.ZoneID) + "/dns_records"
}

// Ensure makes hostID's tunnel and its proxied DNS record exist and returns
// them with the run token. Idempotent, so it also repairs a deleted tunnel or
// a record someone edited.
//
// The tunnel is locally configured: the connector's --url names the machine's
// port, so a port change needs no API call.
func (c *Cloudflare) Ensure(ctx context.Context, hostID string) (Tunnel, error) {
	name := tunnelName(hostID)
	var found []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		RemoteConfig bool   `json:"remote_config"`
	}
	if err := c.do(ctx, http.MethodGet, c.tunnelsPath()+"?is_deleted=false&name="+url.QueryEscape(name), nil, &found); err != nil {
		return Tunnel{}, fmt.Errorf("list tunnels: %w", err)
	}
	out := Tunnel{Hostname: c.Hostname(hostID)}
	for _, t := range found {
		if t.Name != name {
			continue
		}
		// A remotely managed tunnel's ingress overrides --url, and Cloudflare
		// cannot switch it back to local management.
		if t.RemoteConfig {
			return Tunnel{}, fmt.Errorf("tunnel %s is managed remotely, so it ignores the connector's port", name)
		}
		out.ID = t.ID
		break
	}
	if out.ID == "" {
		var created struct {
			ID string `json:"id"`
		}
		if err := c.do(ctx, http.MethodPost, c.tunnelsPath(), map[string]any{"name": name, "config_src": "local"}, &created); err != nil {
			return Tunnel{}, fmt.Errorf("create tunnel: %w", err)
		}
		out.ID = created.ID
	}
	if out.ID == "" {
		return Tunnel{}, errors.New("cloudflare returned a tunnel without an id")
	}

	target := out.ID + ".cfargotunnel.com"
	record := map[string]any{"type": "CNAME", "name": out.Hostname, "content": target, "proxied": true, "ttl": 1}
	existing, err := c.findRecord(ctx, out.Hostname)
	if err != nil {
		return Tunnel{}, err
	}
	switch {
	case existing.ID == "":
		err = c.do(ctx, http.MethodPost, c.recordsPath(), record, nil)
	case existing.Content != target || !existing.Proxied:
		// Recreated tunnel, or proxying switched off by hand: an unproxied
		// cfargotunnel.com CNAME serves nothing. Keep the address, repair it.
		err = c.do(ctx, http.MethodPut, c.recordsPath()+"/"+url.PathEscape(existing.ID), record, nil)
	}
	if err != nil {
		return Tunnel{}, fmt.Errorf("write dns record: %w", err)
	}

	if err := c.do(ctx, http.MethodGet, c.tunnelsPath()+"/"+url.PathEscape(out.ID)+"/token", nil, &out.RunToken); err != nil {
		return Tunnel{}, fmt.Errorf("get run token: %w", err)
	}
	if out.RunToken == "" {
		return Tunnel{}, errors.New("cloudflare returned an empty run token")
	}
	return out, nil
}

type dnsRecord struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Proxied bool   `json:"proxied"`
}

func (c *Cloudflare) findRecord(ctx context.Context, hostname string) (dnsRecord, error) {
	var records []dnsRecord
	if err := c.do(ctx, http.MethodGet, c.recordsPath()+"?type=CNAME&name="+url.QueryEscape(hostname), nil, &records); err != nil {
		return dnsRecord{}, fmt.Errorf("find dns record: %w", err)
	}
	if len(records) == 0 {
		return dnsRecord{}, nil
	}
	return records[0], nil
}

// Delete removes a tunnel and the DNS record that points at it. A record that
// points at another tunnel is left alone: the machine recreated its tunnel
// under the same address. Already-deleted parts count as deleted.
func (c *Cloudflare) Delete(ctx context.Context, tunnelID, hostname string) error {
	tunnel := c.tunnelsPath() + "/" + url.PathEscape(tunnelID)
	// Cloudflare refuses to delete a tunnel with live connections (code 1022).
	if err := c.do(ctx, http.MethodDelete, tunnel+"/connections", nil, nil); err != nil && !notFound(err) {
		return fmt.Errorf("drop tunnel connections: %w", err)
	}
	if err := c.do(ctx, http.MethodDelete, tunnel, nil, nil); err != nil && !notFound(err) {
		return fmt.Errorf("delete tunnel: %w", err)
	}
	record, err := c.findRecord(ctx, hostname)
	if err != nil || record.ID == "" || record.Content != tunnelID+".cfargotunnel.com" {
		return err
	}
	if err := c.do(ctx, http.MethodDelete, c.recordsPath()+"/"+url.PathEscape(record.ID), nil, nil); err != nil && !notFound(err) {
		return fmt.Errorf("delete dns record: %w", err)
	}
	return nil
}

// Status is whether a tunnel has a connector now, and since when it has had
// none. Missing is set when Cloudflare no longer has the tunnel.
type Status struct {
	Online       bool
	OfflineSince time.Time
	Missing      bool
}

// Status reads a tunnel's connection state. Cloudflare reports "healthy" or
// "degraded" while a connector serves it, "down" after one disconnects and
// "inactive" before one ever connected.
func (c *Cloudflare) Status(ctx context.Context, tunnelID string) (Status, error) {
	var t struct {
		Status          string     `json:"status"`
		CreatedAt       time.Time  `json:"created_at"`
		DeletedAt       *time.Time `json:"deleted_at"`
		ConnsInactiveAt *time.Time `json:"conns_inactive_at"`
	}
	if err := c.do(ctx, http.MethodGet, c.tunnelsPath()+"/"+url.PathEscape(tunnelID), nil, &t); err != nil {
		if notFound(err) {
			return Status{Missing: true}, nil
		}
		return Status{}, err
	}
	if t.DeletedAt != nil {
		return Status{Missing: true}, nil
	}
	if t.Status == "healthy" || t.Status == "degraded" {
		return Status{Online: true}, nil
	}
	since := t.CreatedAt
	if t.ConnsInactiveAt != nil {
		since = *t.ConnsInactiveAt
	}
	return Status{OfflineSince: since}, nil
}

// apiError is a failed Cloudflare call.
type apiError struct {
	HTTPStatus int
	Code       int
	Message    string
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("cloudflare %d: %s", e.Code, e.Message)
	}
	return fmt.Sprintf("cloudflare HTTP %d", e.HTTPStatus)
}

func notFound(err error) bool {
	var apiErr *apiError
	return errors.As(err, &apiErr) && apiErr.HTTPStatus == http.StatusNotFound
}

var defaultClient = &http.Client{Timeout: 30 * time.Second}

// do sends one Cloudflare v4 API call and decodes the envelope's result.
func (c *Cloudflare) do(ctx context.Context, method, path string, body, result any) error {
	base := c.BaseURL
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	client := c.HTTP
	if client == nil {
		client = defaultClient
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
	req.Header.Set("Authorization", "Bearer "+c.APIToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
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
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env); err != nil || !env.Success {
		apiErr := &apiError{HTTPStatus: resp.StatusCode}
		if len(env.Errors) > 0 {
			apiErr.Code, apiErr.Message = env.Errors[0].Code, env.Errors[0].Message
		}
		return apiErr
	}
	if result == nil {
		return nil
	}
	return json.Unmarshal(env.Result, result)
}

// Verify reads the zone once, so a zone outside the tunnel account fails at
// boot: the edge refuses a CNAME to another account's tunnel (error 1014)
// while the connector still registers, so it would look healthy.
func (c *Cloudflare) Verify(ctx context.Context) error {
	var z struct {
		Name    string `json:"name"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := c.do(ctx, http.MethodGet, "/zones/"+url.PathEscape(c.ZoneID), nil, &z); err != nil {
		return fmt.Errorf("read zone: %w", err)
	}
	if z.Name != c.Zone {
		return fmt.Errorf("zone %s is %q, not %q", c.ZoneID, z.Name, c.Zone)
	}
	if z.Account.ID != c.AccountID {
		return fmt.Errorf("zone %s belongs to account %s, not %s", z.Name, z.Account.ID, c.AccountID)
	}
	return nil
}
