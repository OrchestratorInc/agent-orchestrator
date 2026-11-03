package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublishRemoteHostAddressUsesScopedDigestWithoutRedirects(t *testing.T) {
	hash := strings.Repeat("a", 64)
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.URL.Path != "/api/cloud/v1/remote-hosts/h_test/address" || r.Header.Get("Authorization") != "Bearer "+hash {
			t.Errorf("unexpected request: %s %s", r.URL.Path, r.Header.Get("Authorization"))
		}
		if !strings.Contains(r.Header.Get("X-AO-Host-Signature"), ".") {
			t.Errorf("unsigned publish: %q", r.Header.Get("X-AO-Host-Signature"))
		}
		var input struct {
			URL string `json:"url"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.URL != "https://new.example.com:443" {
			t.Errorf("unexpected address: %+v (%v)", input, err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if err := publishRemoteHostAddress(context.Background(), srv.Client(), srv.URL, "h_test", "https://new.example.com:443", hash, filepath.Join(t.TempDir(), "mobile", "config.json")); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("address was not published")
	}
}
