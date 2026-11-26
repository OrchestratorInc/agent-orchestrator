package harnessendpoint

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestDetectAuthStyle(t *testing.T) {
	cases := []struct {
		name   string
		handle func(w http.ResponseWriter, r *http.Request)
		want   AuthStyle
		err    error
		probes int32
	}{
		{name: "bearer accepted", want: AuthBearer, probes: 1, handle: func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer sk-test" || r.Header.Get("x-api-key") != "" {
				w.WriteHeader(http.StatusUnauthorized)
			}
		}},
		{name: "x-api-key accepted after bearer refused", want: AuthAPIKey, probes: 2, handle: func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") != "sk-test" || r.Header.Get("Authorization") != "" {
				w.WriteHeader(http.StatusUnauthorized)
			}
		}},
		{name: "no model listing keeps the default", want: AuthBearer, probes: 1, handle: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}},
		{name: "refused in every style", err: ErrKeyRejected, probes: 2, handle: func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var probes atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				probes.Add(1)
				if r.URL.Path != "/v1/models" || r.Header.Get("anthropic-version") == "" {
					t.Errorf("probe = %s %s, version %q", r.Method, r.URL.Path, r.Header.Get("anthropic-version"))
				}
				tc.handle(w, r)
			}))
			defer server.Close()
			got, err := DetectAuthStyle(context.Background(), server.Client(), "claude-code", server.URL+"/", "sk-test")
			if !errors.Is(err, tc.err) || got != tc.want || probes.Load() != tc.probes {
				t.Fatalf("DetectAuthStyle = %q, %v after %d probes; want %q, %v after %d", got, err, probes.Load(), tc.want, tc.err, tc.probes)
			}
		})
	}
}

// A redirect could carry the key to another host, so it is never followed.
func TestDetectAuthStyleDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Store(true) }))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v1/models", http.StatusFound)
	}))
	defer server.Close()
	got, err := DetectAuthStyle(context.Background(), nil, "claude-code", server.URL, "sk-test")
	if err != nil || got != AuthBearer || leaked.Load() {
		t.Fatalf("DetectAuthStyle = %q, %v, leaked = %v; want the default and no request elsewhere", got, err, leaked.Load())
	}
}

// An endpoint that does not answer keeps the default rather than blocking the save.
func TestDetectAuthStyleKeepsTheDefaultWhenUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	got, err := DetectAuthStyle(context.Background(), &http.Client{Timeout: time.Second}, "claude-code", url, "sk-test")
	if err != nil || got != AuthBearer {
		t.Fatalf("DetectAuthStyle = %q, %v; want the default", got, err)
	}
}

func TestProbeTargetKeepsTheSavedStyleForTheSameEndpoint(t *testing.T) {
	store := ForDataDir(t.TempDir())
	if _, err := store.Save("claude-code", Input{BaseURL: "https://gateway.example.com", APIKey: "sk-saved", AuthStyle: AuthAPIKey}, time.Now()); err != nil {
		t.Fatal(err)
	}
	target, ok, err := store.ProbeTarget("claude-code", Input{BaseURL: "https://gateway.example.com/", Model: "m"})
	if err != nil || !ok || target.Key != "sk-saved" || target.SavedStyle != AuthAPIKey {
		t.Fatalf("same endpoint: target = %+v, ok = %v, err = %v; want the saved key and style", target.SavedStyle, ok, err)
	}
	target, ok, err = store.ProbeTarget("claude-code", Input{BaseURL: "https://other.example.com"})
	if err != nil || !ok || target.Key != "sk-saved" || target.SavedStyle != "" {
		t.Fatalf("new URL: style = %q, ok = %v, err = %v; want the saved key probed again", target.SavedStyle, ok, err)
	}
	target, ok, err = store.ProbeTarget("claude-code", Input{BaseURL: "https://gateway.example.com", APIKey: "sk-new"})
	if err != nil || !ok || target.Key != "sk-new" || target.SavedStyle != "" {
		t.Fatalf("new key: style = %q, ok = %v, err = %v; want the new key probed", target.SavedStyle, ok, err)
	}
	if _, _, err := store.ProbeTarget("claude-code", Input{BaseURL: "http://remote.example.com", APIKey: "k"}); err == nil {
		t.Fatal("a cleartext remote URL must be rejected before any probe")
	}
}

// Codex sends its key one way only, so its endpoint is never asked.
func TestDetectAuthStyleSkipsSingleStyleHarnesses(t *testing.T) {
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { probes.Add(1) }))
	defer server.Close()
	got, err := DetectAuthStyle(context.Background(), server.Client(), "codex", server.URL+"/v1", "sk-test")
	if err != nil || got != AuthBearer || probes.Load() != 0 {
		t.Fatalf("DetectAuthStyle = %q, %v after %d probes; want bearer without a probe", got, err, probes.Load())
	}
}
