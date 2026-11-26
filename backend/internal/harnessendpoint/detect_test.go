package harnessendpoint

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const listing = `{"data":[{"id":"glm-5"},{"id":" "},{"id":"glm-5"},{"id":"glm-4.6"},{"id":"bad id"}]}`

func TestDetect(t *testing.T) {
	cases := []struct {
		name   string
		handle func(w http.ResponseWriter, r *http.Request)
		want   AuthStyle
		models []string
		err    error
		probes int32
	}{
		{name: "bearer accepted", want: AuthBearer, models: []string{"glm-5", "glm-4.6"}, probes: 1, handle: func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer sk-test" || r.Header.Get("x-api-key") != "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, listing)
		}},
		{name: "x-api-key accepted after bearer refused", want: AuthAPIKey, models: []string{"glm-5", "glm-4.6"}, probes: 2, handle: func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("x-api-key") != "sk-test" || r.Header.Get("Authorization") != "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, listing)
		}},
		{name: "a listing in another shape keeps the style without models", want: AuthBearer, probes: 1, handle: func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, `{"models":["x"]}`)
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
			got, err := Detect(context.Background(), server.Client(), "claude-code", server.URL+"/", "sk-test")
			if !errors.Is(err, tc.err) || got.Style != tc.want || !slices.Equal(got.Models, tc.models) || probes.Load() != tc.probes {
				t.Fatalf("Detect = %+v, %v after %d probes; want %q %v, %v after %d", got, err, probes.Load(), tc.want, tc.models, tc.err, tc.probes)
			}
		})
	}
}

// A redirect could carry the key to another host, so it is never followed.
func TestDetectDoesNotFollowRedirects(t *testing.T) {
	var leaked atomic.Bool
	elsewhere := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { leaked.Store(true) }))
	defer elsewhere.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL+"/v1/models", http.StatusFound)
	}))
	defer server.Close()
	got, err := Detect(context.Background(), nil, "claude-code", server.URL, "sk-test")
	if err != nil || got.Style != AuthBearer || leaked.Load() {
		t.Fatalf("Detect = %+v, %v, leaked = %v; want the default and no request elsewhere", got, err, leaked.Load())
	}
}

// An endpoint that does not answer keeps the default rather than blocking the save.
func TestDetectKeepsTheDefaultWhenUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()
	got, err := Detect(context.Background(), &http.Client{Timeout: time.Second}, "claude-code", url, "sk-test")
	if err != nil || got.Style != AuthBearer || len(got.Models) != 0 {
		t.Fatalf("Detect = %+v, %v; want the default", got, err)
	}
}

func TestListModels(t *testing.T) {
	status := http.StatusOK
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("x-api-key") != "sk-test" {
			t.Errorf("request = %s, x-api-key set = %v", r.URL.Path, r.Header.Get("x-api-key") != "")
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, listing)
	}))
	defer server.Close()
	got, err := ListModels(context.Background(), server.Client(), "claude-code", server.URL, AuthAPIKey, "sk-test")
	if err != nil || !slices.Equal(got, []string{"glm-5", "glm-4.6"}) {
		t.Fatalf("ListModels = %v, %v", got, err)
	}
	status = http.StatusUnauthorized
	if _, err := ListModels(context.Background(), server.Client(), "claude-code", server.URL, AuthAPIKey, "sk-test"); !errors.Is(err, ErrKeyRejected) {
		t.Fatalf("401: err = %v, want ErrKeyRejected", err)
	}
	status = http.StatusNotFound
	_, err = ListModels(context.Background(), server.Client(), "claude-code", server.URL, AuthAPIKey, "sk-test")
	if !errors.Is(err, ErrNoModelListing) || strings.Contains(err.Error(), "sk-test") {
		t.Fatalf("404: err = %v, want ErrNoModelListing without the key", err)
	}
	if _, err := ListModels(context.Background(), nil, "claude-code", "http://remote.example.com", AuthAPIKey, "sk-test"); err == nil {
		t.Fatal("a cleartext remote URL must not receive the key")
	}
}

func TestProbeTargetKeepsTheSavedStyleForTheSameEndpoint(t *testing.T) {
	store := ForDataDir(t.TempDir())
	if _, err := store.Save("claude-code", Input{BaseURL: "https://gateway.example.com", APIKey: "sk-saved", AuthStyle: AuthAPIKey, Model: "glm-5"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	target, ok, err := store.ProbeTarget("claude-code", Input{BaseURL: "https://gateway.example.com/"})
	if err != nil || !ok || target.Key != "sk-saved" || target.SavedStyle != AuthAPIKey || target.SavedModel != "glm-5" {
		t.Fatalf("same endpoint: target = %+v, ok = %v, err = %v; want the saved key and style", target.SavedStyle, ok, err)
	}
	target, ok, err = store.ProbeTarget("claude-code", Input{BaseURL: "https://other.example.com"})
	if err != nil || !ok || target.Key != "sk-saved" || target.SavedStyle != "" || target.SavedModel != "" {
		t.Fatalf("new URL: style = %q, ok = %v, err = %v; want the saved key probed again", target.SavedStyle, ok, err)
	}
	target, ok, err = store.ProbeTarget("claude-code", Input{BaseURL: "https://gateway.example.com", APIKey: "sk-new"})
	if err != nil || !ok || target.Key != "sk-new" || target.SavedStyle != "" || target.SavedModel != "glm-5" {
		t.Fatalf("new key: target = %+v, ok = %v, err = %v; want the new key probed and the model kept", target.SavedStyle, ok, err)
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
