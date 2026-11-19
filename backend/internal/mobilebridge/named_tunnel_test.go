package mobilebridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeAOCloud answers the fixed-tunnel route for host-uuid.
type fakeAOCloud struct {
	mu      sync.Mutex
	repairs []bool
	digests []string
	status  int // 0 answers 200
	// repairStatus answers repair requests instead, when set.
	repairStatus int
	// tokens are handed out in turn, the last one from then on; empty means
	// run-token-tun-1 every time.
	tokens []string
	// since is when the DNS record was made; zero means just now.
	since time.Time
	// skew sets AO Cloud's clock this far behind the machine's.
	skew time.Duration
}

func (f *fakeAOCloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Method != http.MethodPost || r.URL.Path != "/api/cloud/v1/remote-hosts/host-uuid/tunnel" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var in struct{ Repair bool }
	_ = json.NewDecoder(r.Body).Decode(&in)
	f.repairs = append(f.repairs, in.Repair)
	f.digests = append(f.digests, strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
	status := f.status
	if in.Repair && f.repairStatus != 0 {
		status = f.repairStatus
	}
	if status != 0 {
		w.WriteHeader(status)
		if status == http.StatusUnauthorized { // as AO Cloud words a refusal
			_, _ = w.Write([]byte(`{"error":"Unauthorized","code":"unauthorized","message":"Invalid host credential."}`))
		}
		return
	}
	token := "run-token-tun-1"
	if len(f.tokens) > 0 {
		token = f.tokens[min(len(f.repairs), len(f.tokens))-1]
	}
	since := f.since
	if since.IsZero() {
		since = time.Now().Add(-f.skew)
	}
	if f.skew != 0 {
		w.Header().Set("Date", time.Now().Add(-f.skew).UTC().Format(http.TimeFormat))
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"hostname": "h-test.example.dev", "runToken": token, "since": since, "repaired": in.Repair})
}

func (f *fakeAOCloud) set(status int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status = status
}

func (f *fakeAOCloud) requests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.repairs)
}

// newFixedSource returns a linked machine's tunnel source backed by a fake AO
// Cloud. Plain http is accepted because the fake listens on 127.0.0.1.
func newFixedSource(t *testing.T) (*fakeAOCloud, *FixedTunnelSource) {
	t.Helper()
	cloud := &fakeAOCloud{}
	srv := httptest.NewServer(cloud)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	if err := os.MkdirAll(dir+"/mobile", 0o700); err != nil {
		t.Fatal(err)
	}
	src := NewFixedTunnelSource(srv.URL, dir)
	link(t, src, "account-digest")
	return cloud, src
}

func link(t *testing.T, src *FixedTunnelSource, digest string) {
	t.Helper()
	if err := Save(src.StatePath, State{Enabled: true, Password: "pw", AccountTokenHash: digest}); err != nil {
		t.Fatal(err)
	}
}

// knownTunnel makes the fake's record a day old and caches its tunnel, as on
// every start after the first.
func knownTunnel(t *testing.T, cloud *fakeAOCloud, src *FixedTunnelSource) {
	t.Helper()
	cloud.mu.Lock()
	cloud.since = time.Now().Add(-24 * time.Hour)
	cloud.mu.Unlock()
	b, err := json.Marshal(cachedNamedTunnel{Key: src.cacheKey("host-uuid", "account-digest"), Tunnel: NamedTunnel{
		Hostname: "h-test.example.dev", RunToken: "run-token-tun-1", Since: time.Now().Add(-24 * time.Hour),
	}})
	if err == nil {
		err = os.WriteFile(src.CachePath, b, 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
}

// AO Cloud's answers that leave the machine on a quick tunnel: no fixed
// addresses there, or an account that already uses all of its fixed addresses.
func TestFixedTunnelReadsAOCloudsNoFixedAddressAnswers(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusNotFound, `{"code":"fixed_addresses_unavailable"}`},
		{http.StatusServiceUnavailable, `{"code":"fixed_addresses_unavailable"}`}, // an older AO Cloud
		{http.StatusConflict, `{"code":"fixed_address_limit_reached"}`},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(tc.body))
		}))
		dir := t.TempDir()
		if err := os.MkdirAll(dir+"/mobile", 0o700); err != nil {
			t.Fatal(err)
		}
		src := NewFixedTunnelSource(srv.URL, dir)
		link(t, src, "account-digest")
		if _, err := src.TunnelFor(context.Background(), "host-uuid", false); !errors.Is(err, errNoFixedAddresses) {
			t.Errorf("%d %s: err = %v", tc.status, tc.body, err)
		}
		srv.Close()
	}
}

// A bare 404, from an AO Cloud that predates the route or a proxy in front of
// one, keeps the cached tunnel, as an unreachable AO Cloud does.
func TestFixedTunnelKeepsTheCacheOnABare404(t *testing.T) {
	cloud, src := newFixedSource(t)
	knownTunnel(t, cloud, src)
	cloud.set(http.StatusNotFound)
	got, err := src.TunnelFor(context.Background(), "host-uuid", false)
	if err == nil || unlinked(err) || got.RunToken != "run-token-tun-1" {
		t.Fatalf("bare 404 = %+v, %v", got, err)
	}
	if _, err := os.Stat(src.CachePath); err != nil {
		t.Fatalf("cache: %v", err)
	}
}

func TestFixedTunnelNeedsALinkedMachine(t *testing.T) {
	cloud, src := newFixedSource(t)
	link(t, src, "")
	if got, err := src.TunnelFor(context.Background(), "host-uuid", false); !errors.Is(err, errNotLinked) || got.RunToken != "" {
		t.Fatalf("unlinked = %+v, %v", got, err)
	}
	if cloud.requests() != 0 {
		t.Fatal("an unlinked machine asked AO Cloud")
	}
}

func TestFixedTunnelFallsBackToTheCacheOnlyWhileAOCloudIsDown(t *testing.T) {
	cloud, src := newFixedSource(t)
	ctx := context.Background()

	knownTunnel(t, cloud, src)
	first, err := src.TunnelFor(ctx, "host-uuid", false)
	if err != nil || first.Hostname != "h-test.example.dev" || first.RunToken != "run-token-tun-1" || !first.Since.Equal(cloud.since) {
		t.Fatalf("first = %+v, %v", first, err)
	}
	// AO Cloud deleted the record and made it again under the same name: the
	// address is new again, though the machine has known the name for a day.
	cloud.mu.Lock()
	cloud.since = time.Now()
	cloud.mu.Unlock()
	again, err := src.TunnelFor(ctx, "host-uuid", true)
	if err != nil || !again.Since.Equal(cloud.since) || cloud.repairs[1] != true || cloud.digests[1] != "account-digest" {
		t.Fatalf("again = %+v, %v, repairs %v, digests %v", again, err, cloud.repairs, cloud.digests)
	}

	// AO Cloud down: the cached tunnel still starts.
	cloud.set(http.StatusBadGateway)
	cached, err := src.TunnelFor(ctx, "host-uuid", false)
	if err == nil || cached.RunToken != "run-token-tun-1" {
		t.Fatalf("down = %+v, %v", cached, err)
	}
	// A proxy's refusal is not AO Cloud's: the cache stays.
	cloud.set(http.StatusForbidden)
	if got, err := src.TunnelFor(ctx, "host-uuid", false); errors.Is(err, errHostRefused) || got.RunToken == "" {
		t.Fatalf("403 = %+v, %v", got, err)
	}
	cloud.set(http.StatusBadGateway)
	// Another host never borrows the cache, nor does another account's link,
	// which also drops the run token from disk.
	if other, _ := src.TunnelFor(ctx, "other-host", false); other.RunToken != "" {
		t.Fatalf("other host = %+v", other)
	}
	link(t, src, "other-account-digest")
	if other, _ := src.TunnelFor(ctx, "host-uuid", false); other.RunToken != "" {
		t.Fatalf("another account's link started %+v", other)
	}
	if _, err := os.Stat(src.CachePath); !os.IsNotExist(err) {
		t.Fatalf("another link's run token stayed on disk: %v", err)
	}
	link(t, src, "account-digest")

	// Removed from every account: the tunnel is gone, and so is the cache.
	cloud.set(http.StatusUnauthorized)
	if got, err := src.TunnelFor(ctx, "host-uuid", false); !errors.Is(err, errHostRefused) || got.RunToken != "" {
		t.Fatalf("refused = %+v, %v", got, err)
	}
	cloud.set(http.StatusBadGateway)
	if got, _ := src.TunnelFor(ctx, "host-uuid", false); got.RunToken != "" {
		t.Fatalf("cache survived a refusal: %+v", got)
	}
}

// A machine whose clock runs fast must still wait out a record AO Cloud made
// just now: since is moved onto the machine's clock.
func TestFixedTunnelSinceFollowsTheMachinesClock(t *testing.T) {
	cloud, src := newFixedSource(t)
	cloud.skew = 30 * time.Second
	got, err := src.TunnelFor(context.Background(), "host-uuid", false)
	if err != nil {
		t.Fatal(err)
	}
	if age := time.Since(got.Since); age < -2*time.Second || age > 2*time.Second {
		t.Fatalf("a record made just now reads %v old", age)
	}
}

func TestControlPlaneTargetKeepsCredentialsOffPlainHTTP(t *testing.T) {
	for raw, ok := range map[string]bool{
		"https://api.aoagents.dev/": true,
		"http://127.0.0.1:8080":     true,
		"http://localhost:8080":     true,
		"http://api.aoagents.dev":   false,
		"ftp://api.aoagents.dev":    false,
	} {
		got, err := ControlPlaneTarget(raw, "/x")
		if (err == nil) != ok || (ok && !strings.HasSuffix(got, "/x")) || strings.Contains(got, "//x") {
			t.Errorf("%s = %q, %v", raw, got, err)
		}
	}
}
