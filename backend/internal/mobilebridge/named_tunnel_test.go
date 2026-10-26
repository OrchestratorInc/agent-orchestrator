package mobilebridge

import (
	"context"
	"encoding/json"
	"fmt"
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
	mu          sync.Mutex
	calls       []string
	created     int
	tunnel      string // live tunnel id, "" when there is none
	remote      bool   // the live tunnel is remotely managed
	configSrc   string // config_src of the last created tunnel
	record      string // CNAME content, once created
	proxied     bool
	zoneAccount string // account owning zone-1; "" means acct
	emptyToken  bool
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
		account := f.zoneAccount
		if account == "" {
			account = "acct"
		}
		result = map[string]any{"name": "example.dev", "account": map[string]string{"id": account}}
	case "GET /accounts/acct/cfd_tunnel":
		list := []map[string]any{}
		if f.tunnel != "" {
			list = append(list, map[string]any{"id": f.tunnel, "name": r.URL.Query().Get("name"), "remote_config": f.remote})
		}
		result = list
	case "POST /accounts/acct/cfd_tunnel":
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		f.configSrc, _ = in["config_src"].(string)
		f.created++
		f.tunnel = fmt.Sprintf("tun-%d", f.created)
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
	case "GET /accounts/acct/cfd_tunnel/" + f.tunnel + "/token":
		result = "run-token-" + f.tunnel
		if f.emptyToken {
			result = ""
		}
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

func TestNamedTunnelForProvisionsEveryStartAndFallsBackToTheCache(t *testing.T) {
	cf, cfg := newFakeCloudflare(t)
	ctx := context.Background()
	host := NamedTunnelHostname("host-uuid", "example.dev")

	first, err := NamedTunnelFor(ctx, cfg, "host-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if first.Hostname != host || first.RunToken != "run-token-tun-1" || !first.NewRecord ||
		cf.record != "tun-1.cfargotunnel.com" || !cf.proxied || cf.configSrc != "local" {
		t.Fatalf("first = %+v, record=%q proxied=%v config_src=%q", first, cf.record, cf.proxied, cf.configSrc)
	}

	// A restart provisions again, without the zone read and without writing.
	cf.takeCalls()
	again, err := NamedTunnelFor(ctx, cfg, "host-uuid")
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range cf.takeCalls() {
		if call == "GET /zones/zone-1" || !strings.HasPrefix(call, "GET") {
			t.Fatalf("restart called %s", call)
		}
	}
	if again.Hostname != host || again.RunToken != first.RunToken || again.NewRecord {
		t.Fatalf("again = %+v", again)
	}

	// A deleted tunnel is recreated under the same address.
	cf.tunnel = ""
	recreated, err := NamedTunnelFor(ctx, cfg, "host-uuid")
	if err != nil {
		t.Fatal(err)
	}
	if recreated.Hostname != host || recreated.RunToken != "run-token-tun-2" || recreated.NewRecord || cf.record != "tun-2.cfargotunnel.com" {
		t.Fatalf("recreated = %+v, record=%q", recreated, cf.record)
	}

	// With the API refusing, the cached tunnel still starts.
	cfg.APIToken = "wrong"
	cached, err := NamedTunnelFor(ctx, cfg, "host-uuid")
	if err == nil || !strings.Contains(err.Error(), "Authentication error") || cached.RunToken != "run-token-tun-2" || cached.Hostname != host {
		t.Fatalf("fallback = %+v, %v", cached, err)
	}
	// Another host's cache is never borrowed.
	if other, err := NamedTunnelFor(ctx, cfg, "other-host"); err == nil || other.RunToken != "" {
		t.Fatalf("other host = %+v, %v", other, err)
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

			got, err := ProvisionNamedTunnel(context.Background(), cfg, "host-uuid", "")
			if err != nil {
				t.Fatal(err)
			}
			if got.NewRecord || cf.record != "tun-1.cfargotunnel.com" || !cf.proxied {
				t.Fatalf("got %+v, record=%q proxied=%v; calls %v", got, cf.record, cf.proxied, cf.calls)
			}
		})
	}
}

func TestProvisionNamedTunnelRefusesWhatCannotServe(t *testing.T) {
	for name, tc := range map[string]struct {
		setup func(*fakeCloudflare, *NamedTunnelConfig)
		want  string
	}{
		"zone in another account": {func(f *fakeCloudflare, _ *NamedTunnelConfig) { f.zoneAccount = "other" }, "belongs to account other"},
		"remotely managed tunnel": {func(f *fakeCloudflare, _ *NamedTunnelConfig) { f.tunnel, f.remote = "tun-1", true }, "managed remotely"},
		"empty run token":         {func(f *fakeCloudflare, _ *NamedTunnelConfig) { f.emptyToken = true }, "empty run token"},
		"bad API token":           {func(_ *fakeCloudflare, c *NamedTunnelConfig) { c.APIToken = "wrong" }, "Authentication error"},
	} {
		t.Run(name, func(t *testing.T) {
			cf, cfg := newFakeCloudflare(t)
			tc.setup(cf, &cfg)
			if _, err := ProvisionNamedTunnel(context.Background(), cfg, "host-uuid", ""); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
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
