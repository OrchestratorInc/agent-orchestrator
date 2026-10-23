package mobilebridge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeCloudflare keeps just enough state for one tunnel and its DNS record.
type fakeCloudflare struct {
	mu      sync.Mutex
	calls   []string
	tunnel  string // id, once created
	ingress string // last PUT configuration body
	record  string // CNAME content, once created
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
	case "GET /accounts/acct/cfd_tunnel":
		list := []map[string]string{}
		if f.tunnel != "" {
			list = append(list, map[string]string{"id": f.tunnel, "name": r.URL.Query().Get("name")})
		}
		result = list
	case "POST /accounts/acct/cfd_tunnel":
		f.tunnel = "tun-1"
		result = map[string]string{"id": f.tunnel}
	case "PUT /accounts/acct/cfd_tunnel/tun-1/configurations":
		f.ingress = string(body)
	case "GET /zones/zone-1/dns_records":
		list := []map[string]string{}
		if f.record != "" {
			list = append(list, map[string]string{"id": "rec-1", "content": f.record})
		}
		result = list
	case "POST /zones/zone-1/dns_records":
		var rec map[string]any
		_ = json.Unmarshal(body, &rec)
		f.record, _ = rec["content"].(string)
	case "GET /accounts/acct/cfd_tunnel/tun-1/token":
		result = "run-token"
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":7003,"message":"no route"}]}`))
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
}

func TestProvisionNamedTunnelCreatesOnceThenReuses(t *testing.T) {
	cf := &fakeCloudflare{}
	srv := httptest.NewServer(cf)
	defer srv.Close()
	cfg := NamedTunnelConfig{AccountID: "acct", ZoneID: "zone-1", Zone: "example.dev", APIToken: "api-token", apiBase: srv.URL}

	host, token, err := ProvisionNamedTunnel(context.Background(), cfg, "host-uuid", 3011)
	if err != nil {
		t.Fatal(err)
	}
	if want := NamedTunnelHostname("host-uuid", "example.dev"); host != want || !strings.HasPrefix(host, "h-") {
		t.Fatalf("hostname = %q, want %q", host, want)
	}
	if token != "run-token" || cf.record != "tun-1.cfargotunnel.com" || !strings.Contains(cf.ingress, "127.0.0.1:3011") {
		t.Fatalf("token=%q record=%q ingress=%s", token, cf.record, cf.ingress)
	}

	// A restart on a new port keeps the tunnel and address, rewriting only the ingress.
	cf.calls = nil
	again, _, err := ProvisionNamedTunnel(context.Background(), cfg, "host-uuid", 4022)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range cf.calls {
		if strings.HasPrefix(call, "POST") {
			t.Fatalf("second provision created something: %v", cf.calls)
		}
	}
	if again != host || !strings.Contains(cf.ingress, "127.0.0.1:4022") {
		t.Fatalf("hostname %q -> %q, ingress=%s", host, again, cf.ingress)
	}

	cfg.APIToken = "wrong"
	if _, _, err := ProvisionNamedTunnel(context.Background(), cfg, "host-uuid", 3011); err == nil || !strings.Contains(err.Error(), "Authentication error") {
		t.Fatalf("bad token error = %v", err)
	}
}
