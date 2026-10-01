package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func serveDesktopAuthCallback(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	(&Server{}).desktopAuthCallback(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestDesktopAuthCallbackForwardsOnlyCallbackParamsToDeepLink(t *testing.T) {
	rec := serveDesktopAuthCallback(t,
		"/api/cloud/v1/auth/desktop/callback?code=abc&state=xyz&next=https://evil.test")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	want := `ao-app://callback?code=abc&amp;state=xyz`
	if !strings.Contains(body, `content="0;url=`+want+`"`) {
		t.Fatalf("page does not refresh to %q:\n%s", want, body)
	}
	if !strings.Contains(body, `href="`+want+`"`) {
		t.Fatalf("page has no fallback link to %q:\n%s", want, body)
	}
	if strings.Contains(body, "evil.test") {
		t.Fatalf("page forwarded an unexpected parameter:\n%s", body)
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
}

func TestDesktopAuthCallbackForwardsProviderErrors(t *testing.T) {
	rec := serveDesktopAuthCallback(t,
		"/api/cloud/v1/auth/desktop/callback?error=access_denied&error_description=%3Cb%3Eno%3C%2Fb%3E")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "ao-app://callback?error=access_denied&amp;error_description=%3Cb%3Eno%3C%2Fb%3E") {
		t.Fatalf("page does not forward the provider error:\n%s", body)
	}
	if strings.Contains(body, "<b>") {
		t.Fatalf("page rendered unescaped provider input:\n%s", body)
	}
	if !strings.Contains(body, "Sign-in did not complete") {
		t.Fatalf("page does not explain the failure:\n%s", body)
	}
}

func TestDesktopAuthCallbackRejectsIncompleteCallback(t *testing.T) {
	for _, target := range []string{
		"/api/cloud/v1/auth/desktop/callback",
		"/api/cloud/v1/auth/desktop/callback?code=abc",
		"/api/cloud/v1/auth/desktop/callback?state=xyz",
	} {
		rec := serveDesktopAuthCallback(t, target)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status = %d, want 400", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "ao-app://") {
			t.Fatalf("%s: incomplete callback must not forward to the app", target)
		}
	}
}
