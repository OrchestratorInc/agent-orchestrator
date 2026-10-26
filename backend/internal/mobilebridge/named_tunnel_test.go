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
)

// fakeAOCloud answers the fixed-tunnel route for host-uuid.
type fakeAOCloud struct {
	mu      sync.Mutex
	repairs []bool
	digests []string
	status  int // 0 answers 200
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
	if f.status != 0 {
		w.WriteHeader(f.status)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"hostname": "h-test.example.dev", "runToken": "run-token-tun-1"})
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

	first, err := src.TunnelFor(ctx, "host-uuid", false)
	if err != nil || first.Hostname != "h-test.example.dev" || first.RunToken != "run-token-tun-1" || !first.NewRecord {
		t.Fatalf("first = %+v, %v", first, err)
	}
	again, err := src.TunnelFor(ctx, "host-uuid", true)
	if err != nil || again.NewRecord || cloud.repairs[1] != true || cloud.digests[1] != "account-digest" {
		t.Fatalf("again = %+v, %v, repairs %v, digests %v", again, err, cloud.repairs, cloud.digests)
	}

	// AO Cloud down: the cached tunnel still starts.
	cloud.set(http.StatusBadGateway)
	cached, err := src.TunnelFor(ctx, "host-uuid", false)
	if err == nil || cached.RunToken != "run-token-tun-1" {
		t.Fatalf("down = %+v, %v", cached, err)
	}
	// Another host never borrows the cache.
	if other, _ := src.TunnelFor(ctx, "other-host", false); other.RunToken != "" {
		t.Fatalf("other host = %+v", other)
	}

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
