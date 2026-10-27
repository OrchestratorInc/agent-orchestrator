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
	"strconv"
	"strings"
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
	// Created and RecordCreated are set when Ensure made the tunnel and the
	// DNS record, rather than finding them.
	Created       bool
	RecordCreated bool
}

// Hostname is the fixed address userID's account gives hostID. Derived from
// the random IDs rather than stored, so the label never carries a machine or
// user name, and a recreated tunnel keeps the address. Each account that lists
// a host gets its own: an account that lists another account's host ID gets a
// tunnel at an address no device of that account uses. It sits directly under
// the zone apex: Universal SSL covers one level only.
func (c *Cloudflare) Hostname(userID, hostID string) string {
	return "h-" + tunnelLabel(userID, hostID) + "." + c.Zone
}

func tunnelLabel(userID, hostID string) string {
	sum := sha256.Sum256([]byte("ao-named-tunnel:" + userID + ":" + hostID))
	return hex.EncodeToString(sum[:10])
}

// tunnelPrefix starts every host tunnel's name.
const tunnelPrefix = "ao-"

func tunnelName(userID, hostID string) string { return tunnelPrefix + tunnelLabel(userID, hostID) }

func (c *Cloudflare) tunnelsPath() string {
	return "/accounts/" + url.PathEscape(c.AccountID) + "/cfd_tunnel"
}

func (c *Cloudflare) recordsPath() string {
	return "/zones/" + url.PathEscape(c.ZoneID) + "/dns_records"
}

// Ensure makes the tunnel userID's account gives hostID, and its proxied DNS
// record, exist and returns them with the run token. Idempotent, so it also
// repairs a deleted tunnel or a record someone edited.
//
// The tunnel is locally configured: the connector's --url names the machine's
// port, so a port change needs no API call. A call that fails partway undoes
// what it made: no row names it, so the sweep would never find it.
func (c *Cloudflare) Ensure(ctx context.Context, userID, hostID string) (Tunnel, error) {
	out, err := c.ensure(ctx, userID, hostID)
	if err != nil {
		return Tunnel{}, errors.Join(err, c.Discard(ctx, out))
	}
	return out, nil
}

// Discard deletes what the Ensure call that returned t made, for a caller that
// could not record it. Found parts are left alone. An error names what is
// left over, for the operator: no row names it.
func (c *Cloudflare) Discard(ctx context.Context, t Tunnel) error {
	// Undone even after the caller gave up waiting.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	var errs []error
	if t.RecordCreated {
		record, err := c.findRecord(ctx, t.Hostname)
		if err == nil && record.ID != "" && record.Content == t.ID+".cfargotunnel.com" {
			err = c.do(ctx, http.MethodDelete, c.recordsPath()+"/"+url.PathEscape(record.ID), nil, nil)
		}
		if err != nil && !notFound(err) {
			errs = append(errs, fmt.Errorf("left dns record %s: %w", t.Hostname, err))
		}
	}
	if t.Created {
		if err := c.do(ctx, http.MethodDelete, c.tunnelsPath()+"/"+url.PathEscape(t.ID), nil, nil); err != nil && !notFound(err) {
			errs = append(errs, fmt.Errorf("left tunnel %s: %w", t.ID, err))
		}
	}
	return errors.Join(errs...)
}

// ensure is Ensure without the undo. On failure it still returns what it made.
func (c *Cloudflare) ensure(ctx context.Context, userID, hostID string) (Tunnel, error) {
	name := tunnelName(userID, hostID)
	var found []struct {
		ID           string `json:"id"`
		Name         string `json:"name"`
		RemoteConfig bool   `json:"remote_config"`
	}
	if err := c.do(ctx, http.MethodGet, c.tunnelsPath()+"?is_deleted=false&name="+url.QueryEscape(name), nil, &found); err != nil {
		return Tunnel{}, fmt.Errorf("list tunnels: %w", err)
	}
	out := Tunnel{Hostname: c.Hostname(userID, hostID)}
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
		out.ID, out.Created = created.ID, created.ID != ""
	}
	if out.ID == "" {
		return Tunnel{}, errors.New("cloudflare returned a tunnel without an id")
	}

	target := out.ID + ".cfargotunnel.com"
	record := map[string]any{"type": "CNAME", "name": out.Hostname, "content": target, "proxied": true, "ttl": 1}
	existing, err := c.findRecord(ctx, out.Hostname)
	if err != nil {
		return out, err
	}
	switch {
	case existing.ID == "":
		err = c.do(ctx, http.MethodPost, c.recordsPath(), record, nil)
		out.RecordCreated = err == nil
	case existing.Content != target || !existing.Proxied:
		// Recreated tunnel, or proxying switched off by hand: an unproxied
		// cfargotunnel.com CNAME serves nothing. Keep the address, repair it.
		err = c.do(ctx, http.MethodPut, c.recordsPath()+"/"+url.PathEscape(existing.ID), record, nil)
	}
	if err != nil {
		return out, fmt.Errorf("write dns record: %w", err)
	}

	if err := c.do(ctx, http.MethodGet, c.tunnelsPath()+"/"+url.PathEscape(out.ID)+"/token", nil, &out.RunToken); err != nil {
		return out, fmt.Errorf("get run token: %w", err)
	}
	if out.RunToken == "" {
		return out, errors.New("cloudflare returned an empty run token")
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
// under the same address. Already-deleted parts count as deleted: measured,
// Cloudflare answers both deletes of a deleted tunnel with success.
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

// Online reports whether a connector serves the tunnel now: Cloudflare says
// "healthy" or "degraded", rather than "down" after one disconnected or
// "inactive" before one ever connected. A deleted tunnel is not online.
func (c *Cloudflare) Online(ctx context.Context, tunnelID string) (bool, error) {
	var t struct {
		Status    string     `json:"status"`
		DeletedAt *time.Time `json:"deleted_at"`
	}
	if err := c.do(ctx, http.MethodGet, c.tunnelsPath()+"/"+url.PathEscape(tunnelID), nil, &t); err != nil {
		if notFound(err) {
			return false, nil
		}
		return false, err
	}
	return t.DeletedAt == nil && (t.Status == "healthy" || t.Status == "degraded"), nil
}

// NeverConnected lists the host tunnels made before createdBefore that no
// connector has ever served, Cloudflare's "inactive". The account may hold
// other tunnels, and other AO Clouds' host tunnels: the caller checks each
// against its own records before deleting it.
func (c *Cloudflare) NeverConnected(ctx context.Context, createdBefore time.Time) ([]string, error) {
	return c.listHostTunnels(ctx, "inactive", url.Values{"existed_at": {createdBefore.UTC().Format(time.RFC3339)}}, nil)
}

// Disconnected lists the host tunnels whose last connector left before
// downBefore, Cloudflare's "down". The caller checks them like NeverConnected's.
func (c *Cloudflare) Disconnected(ctx context.Context, downBefore time.Time) ([]string, error) {
	return c.listHostTunnels(ctx, "down", nil, func(inactiveAt *time.Time) bool {
		return inactiveAt != nil && !inactiveAt.After(downBefore)
	})
}

// listHostTunnels pages through the IDs of the account's host tunnels in one
// state. keep, when set, filters on when each tunnel's connections went.
func (c *Cloudflare) listHostTunnels(ctx context.Context, status string, filter url.Values, keep func(*time.Time) bool) ([]string, error) {
	const perPage = 100
	var out []string
	// The account holds at most 1,000 tunnels, so ten pages cover it.
	for page := 1; page <= 10; page++ {
		q := url.Values{
			"is_deleted": {"false"}, "status": {status}, "include_prefix": {tunnelPrefix},
			"per_page": {strconv.Itoa(perPage)}, "page": {strconv.Itoa(page)},
		}
		for k, v := range filter {
			q[k] = v
		}
		var found []struct {
			ID              string     `json:"id"`
			Name            string     `json:"name"`
			ConnsInactiveAt *time.Time `json:"conns_inactive_at"`
		}
		if err := c.do(ctx, http.MethodGet, c.tunnelsPath()+"?"+q.Encode(), nil, &found); err != nil {
			return out, fmt.Errorf("list %s tunnels: %w", status, err)
		}
		for _, t := range found {
			if strings.HasPrefix(t.Name, tunnelPrefix) && t.ID != "" && (keep == nil || keep(t.ConnsInactiveAt)) {
				out = append(out, t.ID)
			}
		}
		if len(found) < perPage {
			break
		}
	}
	return out, nil
}

// Release deletes an idle tunnel and leaves its DNS record, so the address
// survives until the machine gets a new tunnel under it. It reports false,
// and deletes nothing, when a connector is serving the tunnel again: Cloudflare
// refuses to delete a tunnel with live connections (code 1022).
func (c *Cloudflare) Release(ctx context.Context, tunnelID string) (bool, error) {
	err := c.do(ctx, http.MethodDelete, c.tunnelsPath()+"/"+url.PathEscape(tunnelID), nil, nil)
	var apiErr *apiError
	switch {
	case err == nil || notFound(err):
		return true, nil
	case errors.As(err, &apiErr) && apiErr.Code == 1022:
		return false, nil
	}
	return false, fmt.Errorf("release tunnel: %w", err)
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

// ErrWrongZone means the configured zone is not the named zone of the tunnel
// account. Unlike an unreachable Cloudflare, it does not pass.
var ErrWrongZone = errors.New("host tunnel zone does not match")

// Verify reads the zone once, so a zone outside the tunnel account fails at
// boot with ErrWrongZone: the edge refuses a CNAME to another account's
// tunnel (error 1014) while the connector still registers, so it would look
// healthy.
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
		return fmt.Errorf("%w: zone %s is %q, not %q", ErrWrongZone, c.ZoneID, z.Name, c.Zone)
	}
	if z.Account.ID != c.AccountID {
		return fmt.Errorf("%w: zone %s belongs to account %s, not %s", ErrWrongZone, z.Name, z.Account.ID, c.AccountID)
	}
	return nil
}
