package hosttunnel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
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
	noToken   bool                  // run token reads fail
}

type fakeTunnel struct {
	name      string
	remote    bool
	status    string
	inactive  *time.Time
	created   time.Time
	connected bool // a connector serves it, so it cannot be deleted
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
		q := r.URL.Query()
		existedAt, _ := time.Parse(time.RFC3339, q.Get("existed_at"))
		list := []map[string]any{}
		for _, id := range slices.Sorted(maps.Keys(f.tunnels)) {
			t := f.tunnels[id]
			if (q.Has("name") && t.name != q.Get("name")) || (q.Has("status") && t.status != q.Get("status")) ||
				!strings.HasPrefix(t.name, q.Get("include_prefix")) || (q.Has("existed_at") && !t.created.Before(existedAt)) {
				continue
			}
			list = append(list, map[string]any{"id": id, "name": t.name, "remote_config": t.remote, "conns_inactive_at": t.inactive})
		}
		// Cloudflare pages listings; per_page defaults to 20.
		perPage, page := 20, 1
		if n, err := strconv.Atoi(q.Get("per_page")); err == nil {
			perPage = n
		}
		if n, err := strconv.Atoi(q.Get("page")); err == nil {
			page = n
		}
		from, to := min((page-1)*perPage, len(list)), min(page*perPage, len(list))
		result = list[from:to]
	case r.Method == http.MethodPost && path == tunnels:
		var in map[string]any
		_ = json.Unmarshal(body, &in)
		f.configSrc, _ = in["config_src"].(string)
		f.created++
		id := fmt.Sprintf("tun-%d", f.created)
		f.tunnels[id] = fakeTunnel{name: in["name"].(string), status: "inactive", created: time.Now()}
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
			if f.noToken {
				fail(http.StatusInternalServerError, 1000, "internal error")
				return
			}
			result = "run-token-" + id
		case r.Method == http.MethodGet && sub == "":
			result = map[string]any{"id": id, "status": t.status, "created_at": "2026-01-01T00:00:00Z", "conns_inactive_at": t.inactive}
		case r.Method == http.MethodDelete && sub == "connections":
			f.live = false
		case r.Method == http.MethodDelete && sub == "":
			if f.live || t.connected {
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

const (
	userID = "00000000-0000-0000-0000-000000000002"
	hostID = "h_00000000-0000-0000-0000-000000000001"
)

func TestEnsureMakesOneLocallyConfiguredTunnelAndRepairsIt(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	ctx := context.Background()
	host := c.Hostname(userID, hostID)
	if !strings.HasPrefix(host, "h-") || !strings.HasSuffix(host, ".example.dev") || strings.Count(host, ".") != 2 {
		t.Fatalf("hostname %q", host)
	}
	// Another account listing the same host ID gets another address.
	if other := c.Hostname("00000000-0000-0000-0000-000000000003", hostID); other == host {
		t.Fatalf("two accounts share %q", host)
	}

	first, err := c.Ensure(ctx, userID, hostID)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != "tun-1" || first.Hostname != host || first.RunToken != "run-token-tun-1" || !first.RecordCreated || cf.configSrc != "local" ||
		cf.records[host] != (fakeRecord{id: "rec-" + host, content: "tun-1.cfargotunnel.com", proxied: true}) {
		t.Fatalf("first = %+v, config_src=%q, records=%v", first, cf.configSrc, cf.records)
	}

	// Idempotent: the same tunnel, and no writes.
	cf.takeCalls()
	again, err := c.Ensure(ctx, userID, hostID)
	if want := (Tunnel{ID: first.ID, Hostname: first.Hostname, RunToken: first.RunToken}); err != nil || again != want {
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
	recreated, err := c.Ensure(ctx, userID, hostID)
	if err != nil || recreated.ID != "tun-2" || recreated.Hostname != host || recreated.RecordCreated ||
		cf.records[host].content != "tun-2.cfargotunnel.com" || !cf.records[host].proxied {
		t.Fatalf("recreated = %+v, %v, record=%+v", recreated, err, cf.records[host])
	}
}

func TestEnsureRefusesARemotelyManagedTunnel(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	cf.tunnels["tun-9"] = fakeTunnel{name: tunnelName(userID, hostID), remote: true}
	if _, err := c.Ensure(context.Background(), userID, hostID); err == nil || !strings.Contains(err.Error(), "managed remotely") {
		t.Fatalf("error = %v", err)
	}
}

func TestDeleteDropsConnectionsAndKeepsARecordForAnotherTunnel(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	ctx := context.Background()
	first, err := c.Ensure(ctx, userID, hostID)
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
	second, err := c.Ensure(ctx, userID, hostID)
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
	if err := c.Verify(context.Background()); !errors.Is(err, ErrWrongZone) {
		t.Fatalf("wrong zone name = %v, want ErrWrongZone", err)
	}
	// Cloudflare not answering is not a wrong zone.
	c.Zone, c.ZoneID = "example.dev", "missing"
	if err := c.Verify(context.Background()); err == nil || errors.Is(err, ErrWrongZone) {
		t.Fatalf("unreadable zone = %v", err)
	}
}

// A call that fails partway leaves nothing behind: no row would name it, so
// the sweep would never delete it.
func TestEnsureUndoesWhatAFailedCallMade(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	ctx := context.Background()
	cf.noToken = true
	if _, err := c.Ensure(ctx, userID, hostID); err == nil {
		t.Fatal("ensure without a run token succeeded")
	}
	if len(cf.tunnels) != 0 || len(cf.records) != 0 {
		t.Fatalf("left tunnels=%v records=%v", cf.tunnels, cf.records)
	}
	// What it found is left alone.
	cf.noToken = false
	kept, err := c.Ensure(ctx, userID, hostID)
	if err != nil {
		t.Fatal(err)
	}
	cf.noToken = true
	if _, err := c.Ensure(ctx, userID, hostID); err == nil {
		t.Fatal("ensure without a run token succeeded")
	}
	if _, ok := cf.tunnels[kept.ID]; !ok || cf.records[kept.Hostname].content != kept.ID+".cfargotunnel.com" {
		t.Fatalf("a failed call removed a found tunnel or record: %v %v", cf.tunnels, cf.records)
	}
}

type fakeSweepStore struct {
	candidates []domain.RemoteHostTunnelCandidate
	rows       map[string]domain.RemoteHostTunnel // by tunnel
	touched    []string
	seen       map[string]time.Time
	released   []string
	deleted    []string
}

func (s *fakeSweepStore) ReleaseRemoteHostTunnel(_ context.Context, tunnelID string) error {
	if row, ok := s.rows[tunnelID]; ok && !row.Released {
		row.Released = true
		s.rows[tunnelID] = row
		s.released = append(s.released, tunnelID)
	}
	return nil
}

func (s *fakeSweepStore) FindRemoteHostTunnel(_ context.Context, tunnelID string) (domain.RemoteHostTunnel, bool, error) {
	row, ok := s.rows[tunnelID]
	return row, ok, nil
}

func (s *fakeSweepStore) ListRemoteHostTunnelSweepCandidates(context.Context, time.Time, time.Time) ([]domain.RemoteHostTunnelCandidate, error) {
	return s.candidates, nil
}
func (s *fakeSweepStore) TouchRemoteHostTunnel(_ context.Context, tunnelID string, seenAt time.Time) error {
	s.touched = append(s.touched, tunnelID)
	if s.seen == nil {
		s.seen = map[string]time.Time{}
	}
	s.seen[tunnelID] = seenAt
	return nil
}
func (s *fakeSweepStore) DeleteRemoteHostTunnel(_ context.Context, tunnelID string) error {
	s.deleted = append(s.deleted, tunnelID)
	delete(s.rows, tunnelID)
	return nil
}

func TestSweepDeletesOnlyWhatIsGone(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	long := now.Add(-100 * 24 * time.Hour)
	cf.tunnels["online"] = fakeTunnel{name: "ao-online", status: "healthy"}
	cf.tunnels["stale"] = fakeTunnel{name: "ao-stale", status: "down", inactive: &long}
	cf.tunnels["orphan"] = fakeTunnel{name: "ao-orphan", status: "healthy"}
	// Released long ago: the tunnel is gone and only its DNS record is left.
	cf.records["h-released.example.dev"] = fakeRecord{id: "rec-released", content: "released.cfargotunnel.com", proxied: true}
	store := &fakeSweepStore{candidates: []domain.RemoteHostTunnelCandidate{
		{HostID: "online", TunnelID: "online", Hostname: "h-online.example.dev", Registered: true},
		{HostID: "stale", TunnelID: "stale", Hostname: "h-stale.example.dev", Registered: true},
		{HostID: "missing", TunnelID: "missing", Hostname: "h-missing.example.dev", Registered: true},
		{HostID: "orphan", TunnelID: "orphan", Hostname: "h-orphan.example.dev"},
		{HostID: "released", TunnelID: "released", Hostname: "h-released.example.dev", Registered: true, Released: true},
	}}
	done, err := Sweep(context.Background(), store, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if done != 4 || strings.Join(store.deleted, ",") != "stale,missing,orphan,released" || strings.Join(store.touched, ",") != "online" {
		t.Fatalf("done %d, deleted %v, touched %v", done, store.deleted, store.touched)
	}
	// A machine serving through a daemon that cannot reach AO Cloud is seen now.
	if !store.seen["online"].Equal(now) {
		t.Fatalf("seen %v", store.seen)
	}
	if _, ok := cf.records["h-released.example.dev"]; ok {
		t.Fatal("an address unused for 90 days kept its DNS record")
	}
}

// A machine that goes offline loses its tunnel after five minutes and keeps
// its address: the DNS record stays for the tunnel it gets when it is back.
func TestSweepReleasesTheTunnelsOfMachinesThatLeft(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	now := time.Now()
	gone, brief := now.Add(-10*time.Minute), now.Add(-2*time.Minute)
	rows := map[string]domain.RemoteHostTunnel{}
	for _, host := range []string{"asleep", "brief", "back"} {
		rows[host] = domain.RemoteHostTunnel{HostID: "h_" + host, TunnelID: host, Hostname: "h-" + host + ".example.dev"}
	}
	rows["newer"] = domain.RemoteHostTunnel{HostID: "h_replaced", TunnelID: "newer", Hostname: "h-replaced.example.dev"}
	cf.tunnels["asleep"] = fakeTunnel{name: "ao-h_asleep", status: "down", inactive: &gone}
	cf.tunnels["brief"] = fakeTunnel{name: "ao-h_brief", status: "down", inactive: &brief}
	// Listed as down, but its machine reconnected before the release.
	cf.tunnels["back"] = fakeTunnel{name: "ao-h_back", status: "down", inactive: &gone, connected: true}
	cf.tunnels["foreign"] = fakeTunnel{name: "ao-h_foreign", status: "down", inactive: &gone}
	cf.tunnels["replaced"] = fakeTunnel{name: "ao-h_replaced", status: "down", inactive: &gone}
	cf.records["h-asleep.example.dev"] = fakeRecord{id: "rec-asleep", content: "asleep.cfargotunnel.com", proxied: true}
	store := &fakeSweepStore{rows: rows}

	done, err := Sweep(context.Background(), store, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if done != 1 || strings.Join(store.released, ",") != "asleep" || len(store.deleted) != 0 || !store.rows["asleep"].Released {
		t.Fatalf("done %d, released %v, deleted %v", done, store.released, store.deleted)
	}
	if _, ok := cf.tunnels["asleep"]; ok {
		t.Fatal("the tunnel of a machine offline for 10 minutes was kept")
	}
	if cf.records["h-asleep.example.dev"].content != "asleep.cfargotunnel.com" {
		t.Fatal("releasing a tunnel removed its address")
	}
	for _, kept := range []string{"brief", "back", "foreign", "replaced"} {
		if _, ok := cf.tunnels[kept]; !ok {
			t.Errorf("tunnel %s was released", kept)
		}
	}
}

// A machine linked and never run, or a registration made only to hold a
// tunnel, loses it after an hour; nothing else does.
func TestSweepDeletesTunnelsNoConnectorEverServed(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	now := time.Now()
	old, young := now.Add(-2*time.Hour), now.Add(-10*time.Minute)
	went := now.Add(-24 * time.Hour)
	cf.tunnels["unused"] = fakeTunnel{name: "ao-h_unused", status: "inactive", created: old}
	cf.tunnels["young"] = fakeTunnel{name: "ao-h_young", status: "inactive", created: young}
	cf.tunnels["foreign"] = fakeTunnel{name: "ao-h_foreign", status: "inactive", created: old}
	cf.tunnels["replaced"] = fakeTunnel{name: "ao-h_replaced", status: "inactive", created: old}
	cf.tunnels["ran"] = fakeTunnel{name: "ao-h_ran", status: "healthy", created: old, inactive: &went}
	cf.tunnels["other"] = fakeTunnel{name: "coder-poc", status: "inactive", created: old}
	// Enough unused tunnels for a second page.
	rows := map[string]domain.RemoteHostTunnel{}
	for i := range 105 {
		id := fmt.Sprintf("bulk-%03d", i)
		cf.tunnels[id] = fakeTunnel{name: "ao-h_" + id, status: "inactive", created: old}
		rows[id] = domain.RemoteHostTunnel{HostID: "h_" + id, TunnelID: id, Hostname: "h-" + id + ".example.dev"}
	}
	for _, host := range []string{"unused", "young", "ran"} {
		rows[host] = domain.RemoteHostTunnel{HostID: "h_" + host, TunnelID: host, Hostname: "h-" + host + ".example.dev"}
	}
	rows["newer"] = domain.RemoteHostTunnel{HostID: "h_replaced", TunnelID: "newer", Hostname: "h-replaced.example.dev"}
	cf.records["h-unused.example.dev"] = fakeRecord{id: "rec-unused", content: "unused.cfargotunnel.com", proxied: true}
	// Made under a record kept from the machine's earlier tunnel: the machine
	// ran before, so only the tunnel goes.
	cf.tunnels["remade"] = fakeTunnel{name: "ao-h_remade", status: "inactive", created: old}
	rows["remade"] = domain.RemoteHostTunnel{HostID: "h_remade", TunnelID: "remade", Hostname: "h-remade.example.dev",
		CreatedAt: now.Add(-30 * 24 * time.Hour), UpdatedAt: old}
	cf.records["h-remade.example.dev"] = fakeRecord{id: "rec-remade", content: "remade.cfargotunnel.com", proxied: true}
	store := &fakeSweepStore{rows: rows}

	deleted, err := Sweep(context.Background(), store, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 107 || len(store.deleted) != 106 || !slices.Equal(store.released, []string{"remade"}) {
		t.Fatalf("done %d, deleted %v, released %v", deleted, store.deleted, store.released)
	}
	if _, ok := cf.tunnels["remade"]; ok || cf.records["h-remade.example.dev"].id == "" {
		t.Fatal("a remade tunnel was kept, or its address was not")
	}
	for _, kept := range []string{"young", "foreign", "replaced", "ran", "other"} {
		if _, ok := cf.tunnels[kept]; !ok {
			t.Errorf("tunnel %s was deleted", kept)
		}
	}
	if _, ok := cf.tunnels["unused"]; ok || cf.records["h-unused.example.dev"].id != "" {
		t.Fatal("the unused tunnel or its DNS record survived")
	}
}
