package github

import (
	"encoding/json"
	"net/http"
	"testing"
)

// A /user response without a login identifies no one, so it is not cached:
// the next caller asks again rather than keep it for the provider's lifetime.
func TestAuthenticatedIdentityEmptyLoginNotCached(t *testing.T) {
	f := newFakeGH(t)
	f.on(http.MethodGet, "/user", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "User"})
	})
	p := newProviderForTest(t, f)
	for i := 0; i < 2; i++ {
		id, err := p.AuthenticatedIdentity(ctx())
		if err != nil {
			t.Fatal(err)
		}
		if id.Login != "" {
			t.Fatalf("identity = %#v, want no login", id)
		}
	}
	if got := f.callsTo(http.MethodGet, "/user"); got != 2 {
		t.Fatalf("GET /user calls = %d, want 2 (empty login not cached)", got)
	}
}
