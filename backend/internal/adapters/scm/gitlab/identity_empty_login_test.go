package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"
)

// A /user response without a username identifies no one, so it is not cached.
func TestAuthenticatedIdentityEmptyUsernameNotCached(t *testing.T) {
	var calls atomic.Int32
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/user" {
			calls.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{})
			return
		}
		http.NotFound(w, r)
	})
	_, p := testServer(t, handler)
	for i := 0; i < 2; i++ {
		id, err := p.AuthenticatedIdentity(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if id.Login != "" {
			t.Fatalf("identity = %#v, want no login", id)
		}
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("GET /user calls = %d, want 2 (empty username not cached)", got)
	}
}
