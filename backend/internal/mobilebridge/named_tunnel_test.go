package mobilebridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeCloudflare keeps just enough state for one tunnel and its DNS record.
type fakeCloudflare struct {
	mu        sync.Mutex
	calls     []string
	tunnel    string // id, once created
	configSrc string // config_src of the created tunnel
	record    string // CNAME content, once created
	proxied   bool
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	call := r.Method + " " + r.URL.Path
	f.calls = append(f.calls, call)
	if r.Header.Get("Authorization") != "Bearer api-token" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
		return
	}
	var result any
	switch call {
	case "GET /zones/zone-1":
		result = map[string]string{"name": "example.dev"}
	case "GET /accounts/acct/cfd_tunnel":
		list := []map[string]string{}
		if f.tunnel != "" {
			list = append(list, map[string]string{"id": f.tunnel, "name": r.URL.Query().Get("name")})
		}
		result = list
	case "POST /accounts/acct/cfd_tunnel":
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		f.configSrc, _ = in["config_src"].(string)
		f.tunnel = "tun-1"
		result = map[string]string{"id": f.tunnel}
	case "GET /zones/zone-1/dns_records":
		list := []map[string]any{}
		if f.record != "" {
			list = append(list, map[string]any{"id": "rec-1", "content": f.record, "proxied": f.proxied})
		}
		result = list
	case "POST /zones/zone-1/dns_records", "PUT /zones/zone-1/dns_records/rec-1":
		var rec map[string]any
		_ = json.Unmarshal(body, &rec)
		f.record, _ = rec["content"].(string)
		f.proxied, _ = rec["proxied"].(bool)
	case "GET /accounts/acct/cfd_tunnel/tun-1/token":
		result = "run-token"
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":7003,"message":"no route"}]}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
}

func (f *fakeCloudflare) takeCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	calls := f.calls
	f.calls = nil
	return calls
}

func newFakeCloudflare(t *testing.T) (*fakeCloudflare, NamedTunnelConfig) {
	t.Helper()
	cf := &fakeCloudflare{}
	srv := httptest.NewServer(cf)
	t.Cleanup(srv.Close)
	return cf, NamedTunnelConfig{
		AccountID: "acct", ZoneID: "zone-1", APIToken: "api-token",
		cachePath: filepath.Join(t.TempDir(), "named-tunnel.json"), apiBase: srv.URL,
	}
}

func TestNamedTunnelForProvisionsOnceThenRunsFromCache(t *testing.T) {
	cf, cfg := newFakeCloudflare(t)
	ctx := context.Background()

	first, err := NamedTunnelFor(ctx, cfg, "host-uuid", true)
	if err != nil {
		t.Fatal(err)
	}
	if want := NamedTunnelHostname("host-uuid", "example.dev"); first.Hostname != want {
		t.Fatalf("hostname = %q, want %q (one level under the zone apex)", first.Hostname, want)
	}
	if first.RunToken != "run-token" || !first.NewRecord || cf.record != "tun-1.cfargotunnel.com" || !cf.proxied || cf.configSrc != "local" {
		t.Fatalf("first provision = %+v, record=%q proxied=%v config_src=%q", first, cf.record, cf.proxied, cf.configSrc)
	}

	// A restart, on any port, runs from the cache without touching the API.
	cf.takeCalls()
	cached, err := NamedTunnelFor(ctx, cfg, "host-uuid", true)
	if err != nil {
		t.Fatal(err)
	}
	if calls := cf.takeCalls(); len(calls) != 0 {
		t.Fatalf("cached start called the API: %v", calls)
	}
	if cached.Hostname != first.Hostname || cached.RunToken != "run-token" || cached.NewRecord {
		t.Fatalf("cached = %+v", cached)
	}

	// After a run that never reached an edge, it asks again but finds what exists.
	again, err := NamedTunnelFor(ctx, cfg, "host-uuid", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range cf.takeCalls() {
		if !strings.HasPrefix(call, "GET") {
			t.Fatalf("re-provision wrote %s", call)
		}
	}
	if again.Hostname != first.Hostname || again.NewRecord {
		t.Fatalf("re-provision = %+v", again)
	}

	// A cache written for another host is not reused.
	if _, err := NamedTunnelFor(ctx, cfg, "other-host", true); err != nil {
		t.Fatal(err)
	}
	if len(cf.takeCalls()) == 0 {
		t.Fatal("another host's cache entry was reused")
	}
}

func TestProvisionNamedTunnelRepairsItsDNSRecord(t *testing.T) {
	for name, preset := range map[string]struct {
		content string
		proxied bool
	}{
		"tunnel recreated":  {"old-tunnel.cfargotunnel.com", true},
		"proxying disabled": {"tun-1.cfargotunnel.com", false},
	} {
		t.Run(name, func(t *testing.T) {
			cf, cfg := newFakeCloudflare(t)
			cf.tunnel, cf.record, cf.proxied = "tun-1", preset.content, preset.proxied

			got, err := ProvisionNamedTunnel(context.Background(), cfg, "host-uuid")
			if err != nil {
				t.Fatal(err)
			}
			if got.NewRecord || cf.record != "tun-1.cfargotunnel.com" || !cf.proxied {
				t.Fatalf("got %+v, record=%q proxied=%v; calls %v", got, cf.record, cf.proxied, cf.calls)
			}
		})
	}
}

func TestProvisionNamedTunnelReportsCloudflareErrors(t *testing.T) {
	_, cfg := newFakeCloudflare(t)
	cfg.APIToken = "wrong"
	if _, err := ProvisionNamedTunnel(context.Background(), cfg, "host-uuid"); err == nil || !strings.Contains(err.Error(), "Authentication error") {
		t.Fatalf("bad token error = %v", err)
	}
}

func TestLoadNamedTunnelConfig(t *testing.T) {
	dir := t.TempDir()
	if c, err := LoadNamedTunnelConfig(dir); c != nil || err != nil {
		t.Fatalf("absent file = %v, %v; want the quick tunnel", c, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "mobile"), 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(body string) {
		if err := os.WriteFile(NamedTunnelConfigPath(dir), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"accountId":"acct","zoneId":"zone-1"}`)
	if _, err := LoadNamedTunnelConfig(dir); err == nil || !strings.Contains(err.Error(), "apiToken") {
		t.Fatalf("missing apiToken error = %v", err)
	}
	write(`{"accountId":"acct","zoneId":"zone-1","zone":"ignored.example","apiToken":"t"}`)
	c, err := LoadNamedTunnelConfig(dir)
	if err != nil || c.cachePath != filepath.Join(dir, "mobile", "named-tunnel.json") {
		t.Fatalf("config = %+v, %v", c, err)
	}
}
