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

type fakeAOCloud struct {
	mu           sync.Mutex
	repairs      []bool
	digests      []string
	status       int
	repairStatus int
	tokens       []string
	since        time.Time
	skew         time.Duration
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
		if status == http.StatusUnauthorized {
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

func TestFixedTunnelReadsAOCloudsNoFixedAddressAnswers(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{http.StatusNotFound, `{"code":"fixed_addresses_unavailable"}`},
		{http.StatusServiceUnavailable, `{"code":"fixed_addresses_unavailable"}`},
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
	cloud.mu.Lock()
	cloud.since = time.Now()
	cloud.mu.Unlock()
	again, err := src.TunnelFor(ctx, "host-uuid", true)
	if err != nil || !again.Since.Equal(cloud.since) || cloud.repairs[1] != true || cloud.digests[1] != "account-digest" {
		t.Fatalf("again = %+v, %v, repairs %v, digests %v", again, err, cloud.repairs, cloud.digests)
	}

	cloud.set(http.StatusBadGateway)
	cached, err := src.TunnelFor(ctx, "host-uuid", false)
	if err == nil || cached.RunToken != "run-token-tun-1" {
		t.Fatalf("down = %+v, %v", cached, err)
	}
	cloud.set(http.StatusForbidden)
	if got, err := src.TunnelFor(ctx, "host-uuid", false); errors.Is(err, errHostRefused) || got.RunToken == "" {
		t.Fatalf("403 = %+v, %v", got, err)
	}
	cloud.set(http.StatusBadGateway)
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

	cloud.set(http.StatusUnauthorized)
	if got, err := src.TunnelFor(ctx, "host-uuid", false); !errors.Is(err, errHostRefused) || got.RunToken != "" {
		t.Fatalf("refused = %+v, %v", got, err)
	}
	cloud.set(http.StatusBadGateway)
	if got, _ := src.TunnelFor(ctx, "host-uuid", false); got.RunToken != "" {
		t.Fatalf("cache survived a refusal: %+v", got)
	}
}

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
