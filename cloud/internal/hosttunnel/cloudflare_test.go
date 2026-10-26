package hosttunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// fakeCloudflare keeps just enough state for tunnels and their DNS records.
type fakeCloudflare struct {
	mu        sync.Mutex
	calls     []string
	created   int
	tunnels   map[string]fakeTunnel // by id
	configSrc string
	records   map[string]fakeRecord // by name
	live      bool                  // tunnels refuse deletion until connections are dropped
}

type fakeTunnel struct {
	name     string
	remote   bool
	status   string
	inactive *time.Time
}

type fakeRecord struct {
	id, content string
	proxied     bool
}

func newFakeCloudflare(t *testing.T) (*fakeCloudflare, *Cloudflare) {
	t.Helper()
	cf := &fakeCloudflare{tunnels: map[string]fakeTunnel{}, records: map[string]fakeRecord{}}
	srv := httptest.NewServer(cf)
	t.Cleanup(srv.Close)
	return cf, &Cloudflare{AccountID: "acct", ZoneID: "zone-1", Zone: "example.dev", APIToken: "api-token", BaseURL: srv.URL}
}

func (f *fakeCloudflare) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	fail := func(status, code int, message string) {
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"success":false,"errors":[{"code":%d,"message":%q}]}`, code, message)
	}
	if r.Header.Get("Authorization") != "Bearer api-token" {
		fail(http.StatusForbidden, 10000, "Authentication error")
		return
	}
	const tunnels = "/accounts/acct/cfd_tunnel"
	const records = "/zones/zone-1/dns_records"
	path := r.URL.Path
	var result any
	switch {
	case r.Method == http.MethodGet && path == "/zones/zone-1":
		result = map[string]any{"name": "example.dev", "account": map[string]string{"id": "acct"}}
	case r.Method == http.MethodGet && path == tunnels:
		list := []map[string]any{}
		for id, t := range f.tunnels {
			if t.name == r.URL.Query().Get("name") {
				list = append(list, map[string]any{"id": id, "name": t.name, "remote_config": t.remote})
			}
		}
		result = list
	case r.Method == http.MethodPost && path == tunnels:
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		f.configSrc, _ = in["config_src"].(string)
		f.created++
		id := fmt.Sprintf("tun-%d", f.created)
		f.tunnels[id] = fakeTunnel{name: in["name"].(string), status: "inactive"}
		result = map[string]string{"id": id}
	case r.Method == http.MethodGet && path == records:
		list := []map[string]any{}
		if rec, ok := f.records[r.URL.Query().Get("name")]; ok {
			list = append(list, map[string]any{"id": rec.id, "content": rec.content, "proxied": rec.proxied})
		}
		result = list
	case (r.Method == http.MethodPost && path == records) || (r.Method == http.MethodPut && strings.HasPrefix(path, records+"/")):
		var rec map[string]any
		_ = json.Unmarshal(body, &rec)
		name := rec["name"].(string)
		proxied, _ := rec["proxied"].(bool)
		f.records[name] = fakeRecord{id: "rec-" + name, content: rec["content"].(string), proxied: proxied}
	case r.Method == http.MethodDelete && strings.HasPrefix(path, records+"/"):
		for name, rec := range f.records {
			if records+"/"+rec.id == path {
				delete(f.records, name)
			}
		}
	case strings.HasPrefix(path, tunnels+"/"):
		rest := strings.TrimPrefix(path, tunnels+"/")
		id, sub, _ := strings.Cut(rest, "/")
		t, ok := f.tunnels[id]
		if !ok {
			fail(http.StatusNotFound, 1003, "tunnel not found")
			return
		}
		switch {
		case r.Method == http.MethodGet && sub == "token":
			result = "run-token-" + id
		case r.Method == http.MethodGet && sub == "":
			result = map[string]any{"id": id, "status": t.status, "created_at": "2026-01-01T00:00:00Z", "conns_inactive_at": t.inactive}
		case r.Method == http.MethodDelete && sub == "connections":
			f.live = false
		case r.Method == http.MethodDelete && sub == "":
			if f.live {
				fail(http.StatusBadRequest, 1022, "tunnel has active connections")
				return
			}
			delete(f.tunnels, id)
		default:
			fail(http.StatusNotFound, 7003, "no route")
			return
		}
	default:
		fail(http.StatusNotFound, 7003, "no route")
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

const hostID = "h_00000000-0000-0000-0000-000000000001"

func TestEnsureMakesOneLocallyConfiguredTunnelAndRepairsIt(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	ctx := context.Background()
	host := c.Hostname(hostID)
	if !strings.HasPrefix(host, "h-") || !strings.HasSuffix(host, ".example.dev") || strings.Count(host, ".") != 2 {
		t.Fatalf("hostname %q", host)
	}

	first, err := c.Ensure(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "tun-1" || first.Hostname != host || first.RunToken != "run-token-tun-1" || cf.configSrc != "local" ||
		cf.records[host] != (fakeRecord{id: "rec-" + host, content: "tun-1.cfargotunnel.com", proxied: true}) {
		t.Fatalf("first = %+v, config_src=%q, records=%v", first, cf.configSrc, cf.records)
	}

	// Idempotent: the same tunnel, and no writes.
	cf.takeCalls()
	again, err := c.Ensure(ctx, hostID)
	if err != nil || again != first {
		t.Fatalf("again = %+v, %v", again, err)
	}
	for _, call := range cf.takeCalls() {
		if !strings.HasPrefix(call, "GET") {
			t.Fatalf("second ensure wrote: %s", call)
		}
	}

	// A deleted tunnel is recreated under the same address, and a record with
	// proxying switched off is repaired.
	delete(cf.tunnels, "tun-1")
	cf.records[host] = fakeRecord{id: "rec-" + host, content: "tun-1.cfargotunnel.com"}
	recreated, err := c.Ensure(ctx, hostID)
	if err != nil || recreated.ID != "tun-2" || recreated.Hostname != host ||
		cf.records[host].content != "tun-2.cfargotunnel.com" || !cf.records[host].proxied {
		t.Fatalf("recreated = %+v, %v, record=%+v", recreated, err, cf.records[host])
	}
}

func TestEnsureRefusesARemotelyManagedTunnel(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	cf.tunnels["tun-9"] = fakeTunnel{name: tunnelName(hostID), remote: true}
	if _, err := c.Ensure(context.Background(), hostID); err == nil || !strings.Contains(err.Error(), "managed remotely") {
		t.Fatalf("error = %v", err)
	}
}

func TestDeleteDropsConnectionsAndKeepsARecordForAnotherTunnel(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	ctx := context.Background()
	first, err := c.Ensure(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}
	cf.live = true
	if err := c.Delete(ctx, first.ID, first.Hostname); err != nil {
		t.Fatal(err)
	}
	if len(cf.tunnels) != 0 || len(cf.records) != 0 {
		t.Fatalf("left tunnels=%v records=%v", cf.tunnels, cf.records)
	}
	// Deleting again is a no-op, and a record now pointing at a newer tunnel
	// survives the old tunnel's delete.
	second, err := c.Ensure(ctx, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Delete(ctx, first.ID, first.Hostname); err != nil {
		t.Fatal(err)
	}
	if cf.records[second.Hostname].content != second.ID+".cfargotunnel.com" {
		t.Fatalf("record for the new tunnel was deleted: %v", cf.records)
	}
}

func TestVerifyRejectsAnotherZone(t *testing.T) {
	_, c := newFakeCloudflare(t)
	if err := c.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Zone = "other.dev"
	if err := c.Verify(context.Background()); err == nil {
		t.Fatal("wrong zone name passed")
	}
}

type fakeSweepStore struct {
	candidates []domain.RemoteHostTunnelCandidate
	touched    []string
	seen       map[string]time.Time
	deleted    []string
}

func (s *fakeSweepStore) ListRemoteHostTunnelSweepCandidates(context.Context, time.Time) ([]domain.RemoteHostTunnelCandidate, error) {
	return s.candidates, nil
}
func (s *fakeSweepStore) TouchRemoteHostTunnel(_ context.Context, hostID string, seenAt time.Time) error {
	s.touched = append(s.touched, hostID)
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	s.seen[hostID] = seenAt
	return nil
}
func (s *fakeSweepStore) DeleteRemoteHostTunnel(_ context.Context, hostID, _ string) error {
	s.deleted = append(s.deleted, hostID)
	return nil
}

func TestSweepDeletesOnlyWhatIsGone(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	long, recent := now.Add(-100*24*time.Hour), now.Add(-10*24*time.Hour)
	cf.tunnels["online"] = fakeTunnel{name: "ao-online", status: "healthy"}
	cf.tunnels["recent"] = fakeTunnel{name: "ao-recent", status: "down", inactive: &recent}
	cf.tunnels["stale"] = fakeTunnel{name: "ao-stale", status: "down", inactive: &long}
	cf.tunnels["orphan"] = fakeTunnel{name: "ao-orphan", status: "healthy"}
	store := &fakeSweepStore{candidates: []domain.RemoteHostTunnelCandidate{
		{HostID: "online", TunnelID: "online", Hostname: "h-online.example.dev", Registered: true},
		{HostID: "recent", TunnelID: "recent", Hostname: "h-recent.example.dev", Registered: true},
		{HostID: "stale", TunnelID: "stale", Hostname: "h-stale.example.dev", Registered: true},
		{HostID: "missing", TunnelID: "missing", Hostname: "h-missing.example.dev", Registered: true},
		{HostID: "orphan", TunnelID: "orphan", Hostname: "h-orphan.example.dev"},
	}}
	deleted, err := Sweep(context.Background(), store, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 || strings.Join(store.deleted, ",") != "stale,missing,orphan" || strings.Join(store.touched, ",") != "online,recent" {
		t.Fatalf("deleted %d %v, touched %v", deleted, store.deleted, store.touched)
	}
	// The kept offline tunnel is due again 90 days after Cloudflare last saw it,
	// not 90 days after this sweep, and leaves the batch until then.
	if !store.seen["online"].Equal(now) || !store.seen["recent"].Equal(recent) {
		t.Fatalf("seen %v", store.seen)
	}
	if _, ok := cf.tunnels["recent"]; !ok {
		t.Fatal("a tunnel offline for 10 days was deleted")
	}
}
