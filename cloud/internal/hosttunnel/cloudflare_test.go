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
	limitAt   int                   // calls after this many answer 429; zero never
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
	if f.limitAt > 0 && len(f.calls) > f.limitAt {
		fail(http.StatusTooManyRequests, 10000, "rate limited")
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
	if err := c.Verify(context.Background()); !errors.Is(err, ErrBadSettings) {
		t.Fatalf("wrong zone name = %v, want ErrBadSettings", err)
	}
	// A zone ID Cloudflare does not have cannot work either.
	c.Zone, c.ZoneID = "example.dev", "missing"
	if err := c.Verify(context.Background()); !errors.Is(err, ErrBadSettings) {
		t.Fatalf("unknown zone ID = %v, want ErrBadSettings", err)
	}
	// Cloudflare not answering is not wrong settings.
	c.BaseURL = "http://127.0.0.1:1"
	if err := c.Verify(context.Background()); err == nil || errors.Is(err, ErrBadSettings) {
		t.Fatalf("unreachable Cloudflare = %v", err)
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
	// A kept record it moved onto its new tunnel goes back to the tunnel the
	// row names, so deleting that row later still finds it.
	other := c.Hostname(userID, "h_other")
	cf.records[other] = fakeRecord{id: "rec-" + other, content: "tun-old.cfargotunnel.com", proxied: true}
	if _, err := c.Ensure(ctx, userID, "h_other"); err == nil {
		t.Fatal("ensure without a run token succeeded")
	}
	if cf.records[other].content != "tun-old.cfargotunnel.com" || len(cf.tunnels) != 1 {
		t.Fatalf("record %+v, tunnels %v", cf.records[other], cf.tunnels)
	}
}

type fakeSweepStore struct {
	candidates []domain.RemoteHostTunnelCandidate
	rows       map[string]domain.RemoteHostTunnel // by tunnel
	touched    []string
	seen       map[string]time.Time
	released   []string
	deleted    []string
	relisted   map[string]bool // by host, listed again since the candidates were read
	unlisted   map[string]bool // by host, removed since the candidates were read
}

func (s *fakeSweepStore) ReleaseRemoteHostTunnel(_ context.Context, tunnelID string) error {
	if row, ok := s.rows[tunnelID]; ok && !row.Released {
		row.Released = true
		s.rows[tunnelID] = row
		s.released = append(s.released, tunnelID)
	}
	return nil
}

func (s *fakeSweepStore) RemoteHostListed(_ context.Context, _, hostID string) (bool, error) {
	if s.relisted[hostID] {
		return true, nil
	}
	return !s.unlisted[hostID] && slices.ContainsFunc(s.candidates, func(c domain.RemoteHostTunnelCandidate) bool {
		return c.HostID == hostID && c.Registered
	}), nil
}

func (s *fakeSweepStore) FindRemoteHostTunnel(_ context.Context, tunnelID string) (domain.RemoteHostTunnel, bool, error) {
	if row, ok := s.rows[tunnelID]; ok {
		return row, true, nil
	}
	// A candidate's row, as the listing read it: last seen long ago.
	for _, c := range s.candidates {
		if c.TunnelID == tunnelID && !slices.Contains(s.deleted, tunnelID) {
			return domain.RemoteHostTunnel{UserID: c.UserID, HostID: c.HostID, TunnelID: c.TunnelID, Hostname: c.Hostname, Released: c.Released}, true, nil
		}
	}
	return domain.RemoteHostTunnel{}, false, nil
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
		{HostID: "relisted", TunnelID: "relisted", Hostname: "h-relisted.example.dev"},
		{HostID: "back", TunnelID: "back", Hostname: "h-back.example.dev", Registered: true},
		{HostID: "released", TunnelID: "released", Hostname: "h-released.example.dev", Registered: true, Released: true},
		{HostID: "removed", TunnelID: "removed", Hostname: "h-removed.example.dev", Registered: true},
	}, relisted: map[string]bool{"relisted": true}, unlisted: map[string]bool{"removed": true},
		rows: map[string]domain.RemoteHostTunnel{
			// Seen after the candidates were read: back after 90 days away.
			"back": {HostID: "back", TunnelID: "back", Hostname: "h-back.example.dev", LastSeenAt: now},
			// Removed from the account after the candidates were read: the
			// record keeps its grace.
			"removed": {HostID: "removed", TunnelID: "removed", Hostname: "h-removed.example.dev", LastSeenAt: long, UpdatedAt: now, Released: true},
		}}
	cf.records["h-removed.example.dev"] = fakeRecord{id: "rec-removed", content: "removed.cfargotunnel.com", proxied: true}
	cf.tunnels["back"] = fakeTunnel{name: "ao-back", status: "down", inactive: &long}
	cf.tunnels["relisted"] = fakeTunnel{name: "ao-relisted", status: "healthy"}
	done, err := Sweep(context.Background(), store, c, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cf.tunnels["relisted"]; !ok {
		t.Fatal("a host listed again during the sweep lost its tunnel")
	}
	if _, ok := cf.tunnels["back"]; !ok {
		t.Fatal("a machine seen again during the sweep lost its tunnel")
	}
	if _, ok := cf.records["h-removed.example.dev"]; !ok {
		t.Fatal("a host removed during the sweep lost its record before its grace")
	}
	if done != 4 || strings.Join(store.deleted, ",") != "stale,missing,orphan,released" || strings.Join(store.touched, ",") != "online,relisted" {
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
	// Down for 10 minutes, but its machine fetched its token a minute ago:
	// it is about to connect.
	cf.tunnels["waking"] = fakeTunnel{name: "ao-h_waking", status: "down", inactive: &gone}
	rows["waking"] = domain.RemoteHostTunnel{HostID: "h_waking", TunnelID: "waking", Hostname: "h-waking.example.dev", LastSeenAt: now.Add(-time.Minute)}
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
	for _, kept := range []string{"brief", "back", "foreign", "replaced", "waking"} {
		if _, ok := cf.tunnels[kept]; !ok {
			t.Errorf("tunnel %s was released", kept)
		}
	}
}

// A machine linked and never run loses its tunnel after an hour and keeps its
// address: its account may still list it, and a device that looked the name
// Every account shares the API token's rate limit: once Cloudflare answers
// 429, the sweep makes no more calls.
func TestSweepStopsOnceCloudflareRateLimits(t *testing.T) {
	cf, c := newFakeCloudflare(t)
	now := time.Now()
	old := now.Add(-2 * time.Hour)
	rows := map[string]domain.RemoteHostTunnel{}
	for i := range 5 {
		id := fmt.Sprintf("idle-%d", i)
		cf.tunnels[id] = fakeTunnel{name: "ao-h_" + id, status: "inactive", created: old}
		rows[id] = domain.RemoteHostTunnel{HostID: "h_" + id, TunnelID: id, Hostname: "h-" + id + ".example.dev"}
	}
	cf.limitAt = 3 // the two listings and one release
	if _, err := Sweep(context.Background(), &fakeSweepStore{rows: rows}, c, now); err == nil {
		t.Fatal("a rate-limited sweep reported no error")
	}
	cf.mu.Lock()
	defer cf.mu.Unlock()
	if len(cf.calls) != 4 {
		t.Fatalf("made %d calls, want 4 (the refused one last): %v", len(cf.calls), cf.calls)
	}
}

// up without a record would cache the miss.
func TestSweepReleasesTunnelsNoConnectorEverServed(t *testing.T) {
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
	store := &fakeSweepStore{rows: rows}

	// A sweep releases at most sweepDeletions; the next one starts further
	// along and releases the rest.
	done, next, err := sweep(context.Background(), store, c, now, 0)
	if err != nil {
		t.Fatal(err)
	}
	if done != sweepDeletions {
		t.Fatalf("first sweep released %d", done)
	}
	more, _, err := sweep(context.Background(), store, c, now, next)
	if err != nil {
		t.Fatal(err)
	}
	if done+more != 106 || len(store.released) != 106 || len(store.deleted) != 0 {
		t.Fatalf("done %d, released %d, deleted %v", done+more, len(store.released), store.deleted)
	}
	for _, kept := range []string{"young", "foreign", "replaced", "ran", "other"} {
		if _, ok := cf.tunnels[kept]; !ok {
			t.Errorf("tunnel %s was released", kept)
		}
	}
	if _, ok := cf.tunnels["unused"]; ok || cf.records["h-unused.example.dev"].id == "" {
		t.Fatal("the unused tunnel survived, or its address did not")
	}
}

// One host's tunnel work waits for the last; another host's does not.
func TestLockHoldsOnlyItsHost(t *testing.T) {
	c := &Cloudflare{}
	unlock := c.Lock(userID, hostID)
	other := make(chan struct{})
	go func() { c.Lock(userID, "h_other")(); close(other) }()
	select {
	case <-other:
	case <-time.After(time.Second):
		t.Fatal("another host waited")
	}
	same := make(chan struct{})
	go func() { c.Lock(userID, hostID)(); close(same) }()
	select {
	case <-same:
		t.Fatal("the same host did not wait")
	case <-time.After(50 * time.Millisecond):
	}
	unlock()
	select {
	case <-same:
	case <-time.After(time.Second):
		t.Fatal("the same host never got the lock")
	}
	if len(c.locks.m) != 0 {
		t.Fatalf("%d locks left after every holder let go", len(c.locks.m))
	}
}
