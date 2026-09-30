package harnessupdate

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeStore struct {
	mu      sync.Mutex
	records map[string]Record
}

func newFakeStore() *fakeStore { return &fakeStore{records: make(map[string]Record)} }

func (f *fakeStore) key(registry, name string) string { return registry + "\x00" + name }

func (f *fakeStore) GetHarnessLatestVersion(_ context.Context, registry, name string) (Record, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.records[f.key(registry, name)]
	return record, ok, nil
}

func (f *fakeStore) UpsertHarnessLatestVersion(_ context.Context, record Record) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[f.key(record.Registry, record.Name)] = record
	return nil
}

// waitFor polls until cond returns true or the deadline passes, avoiding a
// fixed sleep for the background refresh goroutine's completion.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}

func TestLatestReturnsFalseWithoutRegistryOrName(t *testing.T) {
	t.Parallel()
	svc := New(&http.Client{}, newFakeStore())
	if _, ok := svc.Latest(context.Background(), "", "pkg"); ok {
		t.Fatal("ok = true, want false for empty registry")
	}
	if _, ok := svc.Latest(context.Background(), "npm", ""); ok {
		t.Fatal("ok = true, want false for empty name")
	}
}

func TestLatestFetchesAndCachesInBackground(t *testing.T) {
	t.Parallel()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "1.2.3"})
	}))
	defer server.Close()

	store := newFakeStore()
	svc := New(&http.Client{}, store)
	svc.registryEndpoint = func(string, string) (string, error) { return server.URL, nil }

	// First call: nothing cached yet, kicks a background fetch, returns not-ok
	// immediately rather than blocking on the network.
	if _, ok := svc.Latest(context.Background(), "npm", "@anthropic-ai/claude-code"); ok {
		t.Fatal("ok = true on first call, want false before the background fetch lands")
	}
	waitFor(t, func() bool {
		record, ok, _ := store.GetHarnessLatestVersion(context.Background(), "npm", "@anthropic-ai/claude-code")
		return ok && record.Version == "1.2.3"
	})

	latest, ok := svc.Latest(context.Background(), "npm", "@anthropic-ai/claude-code")
	if !ok || latest.Version != "1.2.3" {
		t.Fatalf("Latest = %+v, ok=%v", latest, ok)
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1 (second Latest should serve from cache, not refetch within TTL)", got)
	}
}

func TestLatestRefreshesAgainOnceStale(t *testing.T) {
	t.Parallel()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "1.2.3"})
	}))
	defer server.Close()

	store := newFakeStore()
	svc := New(&http.Client{}, store)
	svc.registryEndpoint = func(string, string) (string, error) { return server.URL, nil }
	svc.ttl = 10 * time.Millisecond

	svc.Latest(context.Background(), "npm", "pkg")
	waitFor(t, func() bool {
		_, ok, _ := store.GetHarnessLatestVersion(context.Background(), "npm", "pkg")
		return ok
	})

	time.Sleep(20 * time.Millisecond) // outlive the TTL
	svc.Latest(context.Background(), "npm", "pkg")
	waitFor(t, func() bool { return atomic.LoadInt32(&calls) >= 2 })
}

func TestLatestDedupsConcurrentBackgroundRefreshes(t *testing.T) {
	t.Parallel()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(20 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(map[string]string{"version": "9.9.9"})
	}))
	defer server.Close()

	store := newFakeStore()
	svc := New(&http.Client{}, store)
	svc.registryEndpoint = func(string, string) (string, error) { return server.URL, nil }

	for i := 0; i < 5; i++ {
		svc.Latest(context.Background(), "npm", "pkg")
	}
	waitFor(t, func() bool {
		_, ok, _ := store.GetHarnessLatestVersion(context.Background(), "npm", "pkg")
		return ok
	})
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("calls = %d, want 1 request deduped across concurrent lookups", got)
	}
}

func TestLatestUnrecognizedRegistryNeverCaches(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	svc := New(&http.Client{}, store)
	if _, ok := svc.Latest(context.Background(), "carrier-pigeon", "pkg"); ok {
		t.Fatal("ok = true for unrecognized registry")
	}
	time.Sleep(50 * time.Millisecond)
	if _, ok, _ := store.GetHarnessLatestVersion(context.Background(), "carrier-pigeon", "pkg"); ok {
		t.Fatal("store has a record for an unrecognized registry")
	}
}

func TestLatestFailedFetchLeavesCacheEmpty(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	store := newFakeStore()
	svc := New(&http.Client{}, store)
	svc.registryEndpoint = func(string, string) (string, error) { return server.URL, nil }

	svc.Latest(context.Background(), "npm", "pkg")
	time.Sleep(50 * time.Millisecond)
	if _, ok, _ := store.GetHarnessLatestVersion(context.Background(), "npm", "pkg"); ok {
		t.Fatal("store has a record despite a failed fetch")
	}
}

func TestRegistryEndpointEscapesScopedPackageNames(t *testing.T) {
	t.Parallel()
	endpoint, err := registryEndpoint("npm", "@anthropic-ai/claude-code")
	if err != nil {
		t.Fatalf("registryEndpoint: %v", err)
	}
	if want := "https://registry.npmjs.org/@anthropic-ai%2Fclaude-code/latest"; endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
}

func TestRegistryEndpointGitHubRelease(t *testing.T) {
	t.Parallel()
	endpoint, err := registryEndpoint("github-release", "aaif-goose/goose")
	if err != nil {
		t.Fatalf("registryEndpoint: %v", err)
	}
	if want := "https://api.github.com/repos/aaif-goose/goose/releases/latest"; endpoint != want {
		t.Fatalf("endpoint = %q, want %q", endpoint, want)
	}
	for _, bad := range []string{"goose", "/goose", "aaif-goose/", "a/b/c"} {
		if _, err := registryEndpoint("github-release", bad); err == nil {
			t.Fatalf("registryEndpoint(%q) succeeded, want error", bad)
		}
	}
}

func TestRegistryEndpointUnrecognized(t *testing.T) {
	t.Parallel()
	if _, err := registryEndpoint("carrier-pigeon", "pkg"); err == nil {
		t.Fatal("expected an error for an unrecognized registry")
	}
}

func TestParseVersionPerRegistry(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		registry string
		body     string
		want     string
	}{
		{registry: "npm", body: `{"version":"1.0.0"}`, want: "1.0.0"},
		{registry: "pypi", body: `{"info":{"version":"2.0.0"}}`, want: "2.0.0"},
		{registry: "homebrew-formula", body: `{"versions":{"stable":"3.0.0"}}`, want: "3.0.0"},
		{registry: "homebrew-cask", body: `{"version":"4.0.0"}`, want: "4.0.0"},
		{registry: "github-release", body: `{"tag_name":"v5.0.0"}`, want: "5.0.0"},
	} {
		t.Run(tt.registry, func(t *testing.T) {
			t.Parallel()
			got, err := parseVersion(tt.registry, []byte(tt.body))
			if err != nil {
				t.Fatalf("parseVersion: %v", err)
			}
			if got != tt.want {
				t.Fatalf("version = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseVersionRejectsEmptyOrUnrecognized(t *testing.T) {
	t.Parallel()
	if _, err := parseVersion("npm", []byte(`{"version":""}`)); err != nil {
		// Empty version is caught by the caller (fetch), not parseVersion
		// itself, which only reports malformed JSON or an unknown registry.
		t.Fatalf("parseVersion on empty version field: %v", err)
	}
	if _, err := parseVersion("carrier-pigeon", []byte(`{}`)); err == nil {
		t.Fatal("expected an error for an unrecognized registry")
	}
}
