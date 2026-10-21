package daemon

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeScmProxySource serves a canned ScmProxyURL value.
type fakeScmProxySource struct{ url string }

func (f fakeScmProxySource) ScmProxyURL(context.Context) string { return f.url }

// clearProxyEnv removes proxy variables so ProxyFromEnvironment resolves to
// direct dialing regardless of the machine the suite runs on.
func clearProxyEnv(t *testing.T) {
	t.Helper()
	t.Setenv("HTTP_PROXY", "")
	t.Setenv("HTTPS_PROXY", "")
	t.Setenv("http_proxy", "")
	t.Setenv("https_proxy", "")
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
}

// An explicit SCM proxy setting routes the request through that proxy: the
// proxy host sees the request, the target never does directly.
func TestSCMHTTPClient_ExplicitSettingDialsThroughProxy(t *testing.T) {
	clearProxyEnv(t)

	var proxyGot string
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyGot = r.Host
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "via-proxy")
	}))
	defer proxy.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("target must not be contacted directly when a proxy is set")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer target.Close()

	client := newSCMHTTPClient(fakeScmProxySource{url: proxy.URL}, slog.Default())
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || string(body) != "via-proxy" {
		t.Fatalf("status=%d body=%q, want 200 via-proxy", resp.StatusCode, body)
	}
	if proxyGot == "" || !strings.HasPrefix(proxyGot, "127.0.0.1") {
		t.Fatalf("proxy handler saw Host=%q, want the target host routed through the proxy", proxyGot)
	}
}

// An empty setting keeps the standard behavior: direct dialing (with a clean
// environment) and no proxy round-trip.
func TestSCMHTTPClient_EmptySettingDialsDirect(t *testing.T) {
	clearProxyEnv(t)

	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("proxy must not be consulted when the setting is empty")
		w.WriteHeader(http.StatusTeapot)
	}))
	defer proxy.Close()

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()

	client := newSCMHTTPClient(fakeScmProxySource{}, slog.Default())
	resp, err := client.Get(target.URL)
	if err != nil {
		t.Fatalf("direct request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", resp.StatusCode)
	}
}

// The client built for the SCM providers always carries the 30s timeout the
// providers' default branches would have applied: an injected client is taken
// verbatim, so the wiring owns the deadline.
func TestSCMHTTPClient_CarriesProviderTimeout(t *testing.T) {
	clearProxyEnv(t)
	client := newSCMHTTPClient(fakeScmProxySource{}, slog.Default())
	if client.Timeout != scmHTTPTimeout {
		t.Fatalf("timeout=%v, want %v", client.Timeout, scmHTTPTimeout)
	}
}
