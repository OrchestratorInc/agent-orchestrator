package githubapp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// tokenBackend serves GitHub's installation access_tokens endpoint, counting
// mints and recording each request's permissions.
type tokenBackend struct {
	mints       atomic.Int32
	expiresIn   time.Duration
	fail        atomic.Bool
	mu          sync.Mutex
	permissions []map[string]string
}

func (b *tokenBackend) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/app/installations/1234/access_tokens" {
			http.NotFound(w, r)
			return
		}
		var body struct {
			Permissions map[string]string `json:"permissions"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		b.mu.Lock()
		b.permissions = append(b.permissions, body.Permissions)
		b.mu.Unlock()
		if b.fail.Load() {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		n := b.mints.Add(1)
		// Simulate GitHub's round trip so concurrent callers overlap.
		time.Sleep(20 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":      "tok-" + strconv.Itoa(int(n)),
			"expires_at": time.Now().UTC().Add(b.expiresIn).Format(time.RFC3339),
		})
	})
}

func newTokenTestClient(t *testing.T, backend *tokenBackend) *Client {
	t.Helper()
	server := httptest.NewServer(backend.handler(t))
	t.Cleanup(server.Close)
	return newAppTestClient(t, server.URL, server.Client())
}

func TestReadTokenIsReusedForTheSameScope(t *testing.T) {
	backend := &tokenBackend{expiresIn: time.Hour}
	client := newTokenTestClient(t, backend)
	first, err := client.repositoryToken(context.Background(), 1234, 7)
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	second, err := client.repositoryReadTokenForRepos(context.Background(), 1234, []int64{7})
	if err != nil {
		t.Fatalf("second token: %v", err)
	}
	if backend.mints.Load() != 1 || first.Token != second.Token {
		t.Fatalf("mints = %d, tokens %q/%q; want one shared mint", backend.mints.Load(), first.Token, second.Token)
	}
	// The repository set is a set: order and duplicates do not change scope.
	if _, err := client.repositoryReadTokenForRepos(context.Background(), 1234, []int64{9, 7, 9}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.repositoryReadTokenForRepos(context.Background(), 1234, []int64{7, 9}); err != nil {
		t.Fatal(err)
	}
	if backend.mints.Load() != 2 {
		t.Fatalf("mints = %d, want 2 (one per distinct scope)", backend.mints.Load())
	}
}

func TestReadTokenNearExpiryIsReminted(t *testing.T) {
	// GitHub tokens that expire within the reuse margin are never handed out.
	backend := &tokenBackend{expiresIn: readTokenReuseMargin - time.Minute}
	client := newTokenTestClient(t, backend)
	for range 2 {
		if _, err := client.repositoryToken(context.Background(), 1234, 7); err != nil {
			t.Fatal(err)
		}
	}
	if backend.mints.Load() != 2 {
		t.Fatalf("mints = %d, want a fresh mint each time for a short-lived token", backend.mints.Load())
	}
}

func TestReadTokenConcurrentMissesShareOneMint(t *testing.T) {
	backend := &tokenBackend{expiresIn: time.Hour}
	client := newTokenTestClient(t, backend)
	var wg sync.WaitGroup
	tokens := make([]string, 16)
	for i := range tokens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := client.repositoryToken(context.Background(), 1234, 7)
			if err != nil {
				t.Errorf("token: %v", err)
				return
			}
			tokens[i] = token.Token
		}()
	}
	wg.Wait()
	if backend.mints.Load() != 1 {
		t.Fatalf("mints = %d for 16 concurrent callers, want 1", backend.mints.Load())
	}
	for _, token := range tokens {
		if token != tokens[0] {
			t.Fatalf("callers received different tokens: %v", tokens)
		}
	}
}

func TestReadTokenFailureIsNotCached(t *testing.T) {
	backend := &tokenBackend{expiresIn: time.Hour}
	client := newTokenTestClient(t, backend)
	backend.fail.Store(true)
	if _, err := client.repositoryToken(context.Background(), 1234, 7); err == nil {
		t.Fatal("expected the failed mint to surface")
	}
	backend.fail.Store(false)
	if _, err := client.repositoryToken(context.Background(), 1234, 7); err != nil {
		t.Fatalf("a failure must not be cached: %v", err)
	}
}

func TestWriteTokensAreNeverCached(t *testing.T) {
	backend := &tokenBackend{expiresIn: time.Hour}
	client := newTokenTestClient(t, backend)
	if _, err := client.repositoryToken(context.Background(), 1234, 7); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := client.repositoryWriteToken(context.Background(), 1234, 7); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.repositoryWriteTokenForRepos(context.Background(), 1234, []int64{7}); err != nil {
		t.Fatal(err)
	}
	if backend.mints.Load() != 4 {
		t.Fatalf("mints = %d, want 1 read + 3 uncached writes", backend.mints.Load())
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if got := backend.permissions[1]["contents"]; got != "write" {
		t.Fatalf("a write request was answered from the read cache: %v", backend.permissions)
	}
}

// The grantability probe for extra repositories is a live permission check and
// must reach GitHub even when a read token for that repository is cached.
func TestMintReadTokenBypassesTheCache(t *testing.T) {
	backend := &tokenBackend{expiresIn: time.Hour}
	client := newTokenTestClient(t, backend)
	if _, err := client.repositoryToken(context.Background(), 1234, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := client.mintReadToken(context.Background(), 1234, []int64{7}); err != nil {
		t.Fatal(err)
	}
	if backend.mints.Load() != 2 {
		t.Fatalf("mints = %d, want the probe to mint fresh", backend.mints.Load())
	}
}
