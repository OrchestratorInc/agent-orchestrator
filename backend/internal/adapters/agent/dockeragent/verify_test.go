package dockeragent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

func TestVerifyDefaultProvider(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   ports.AgentAuthStatus
	}{
		{"accepted", http.StatusOK, ports.AgentAuthStatusAuthorized},
		{"rejected", http.StatusUnauthorized, ports.AgentAuthStatusUnauthorized},
		{"forbidden to list models stays unverified", http.StatusForbidden, ports.AgentAuthStatusConfigured},
		{"provider error stays unverified", http.StatusServiceUnavailable, ports.AgentAuthStatusConfigured},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			isolateProviderKeys(t)
			var gotAuth string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(tc.status)
			}))
			defer server.Close()
			previous := providerVerifyURLs["openai"]
			providerVerifyURLs["openai"] = server.URL
			defer func() { providerVerifyURLs["openai"] = previous }()

			got := verifyDefaultProvider(context.Background(), map[string]string{"OPENAI_API_KEY": "sk-test"})
			if got != tc.want {
				t.Fatalf("verifyDefaultProvider = %q, want %q", got, tc.want)
			}
			if gotAuth != "Bearer sk-test" {
				t.Fatalf("Authorization = %q", gotAuth)
			}
		})
	}
}

func TestVerifyDefaultProviderOfflineStaysUnverified(t *testing.T) {
	isolateProviderKeys(t)
	server := httptest.NewServer(http.NotFoundHandler())
	url := server.URL
	server.Close()
	previous := providerVerifyURLs["openai"]
	providerVerifyURLs["openai"] = url
	defer func() { providerVerifyURLs["openai"] = previous }()

	if got := verifyDefaultProvider(context.Background(), map[string]string{"OPENAI_API_KEY": "sk-test"}); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("offline verify = %q, want configured", got)
	}
}

func TestVerifyDefaultProviderUsesOpenAIBaseURL(t *testing.T) {
	isolateProviderKeys(t)
	var gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	previous := providerVerifyURLs["openai"]
	providerVerifyURLs["openai"] = "http://127.0.0.1:1/unused"
	defer func() { providerVerifyURLs["openai"] = previous }()

	env := map[string]string{"OPENAI_API_KEY": "gateway-key", "OPENAI_BASE_URL": server.URL + "/api/coding/paas/v4/"}
	if got := verifyDefaultProvider(context.Background(), env); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("verifyDefaultProvider = %q, want authorized", got)
	}
	if gotPath != "/api/coding/paas/v4/models" {
		t.Fatalf("checked %q, want the gateway's /models", gotPath)
	}
}

// A redirect must not carry the key: Go keeps x-api-key across origins.
func TestVerifyDefaultProviderDoesNotFollowRedirects(t *testing.T) {
	isolateProviderKeys(t)
	var leaked string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/models", http.StatusFound)
	}))
	defer redirect.Close()
	previous := providerVerifyURLs["anthropic"]
	providerVerifyURLs["anthropic"] = redirect.URL
	defer func() { providerVerifyURLs["anthropic"] = previous }()

	if got := verifyDefaultProvider(context.Background(), map[string]string{"ANTHROPIC_API_KEY": "sk-ant-test"}); got != ports.AgentAuthStatusConfigured {
		t.Fatalf("redirected verify = %q, want configured", got)
	}
	if leaked != "" {
		t.Fatalf("redirect target received the key %q", leaked)
	}
}

// OPENAI_BASE_URL is user-controlled: a key goes only to HTTPS or loopback HTTP.
func TestVerifyDefaultProviderRefusesPlainHTTPGateway(t *testing.T) {
	isolateProviderKeys(t)
	for _, base := range []string{"http://gateway.example.test/v1", "ftp://gateway.example.test", "https://user:pass@gateway.example.test/v1", "not a url"} {
		env := map[string]string{"OPENAI_API_KEY": "gateway-key", "OPENAI_BASE_URL": base}
		if got := verifyDefaultProvider(context.Background(), env); got != ports.AgentAuthStatusConfigured {
			t.Fatalf("%q: verify = %q, want configured without sending the key", base, got)
		}
	}
	for endpoint, allowed := range map[string]bool{
		"https://api.openai.com/v1/models": true, "http://127.0.0.1:8080/v1": true, "http://localhost/v1": true, "http://[::1]:9/v1": true,
		"http://gateway.example.test/v1": false, "https://user@gateway.example.test": false, "": false,
	} {
		if got := credentialEndpointAllowed(endpoint); got != allowed {
			t.Fatalf("credentialEndpointAllowed(%q) = %v, want %v", endpoint, got, allowed)
		}
	}
}

func TestVerifyDefaultProviderRejectsInvalidGeminiKey(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want ports.AgentAuthStatus
	}{
		{"invalid key", `{"error":{"code":400,"status":"INVALID_ARGUMENT","details":[{"reason":"API_KEY_INVALID"}]}}`, ports.AgentAuthStatusUnauthorized},
		{"other bad request stays unverified", `{"error":{"code":400,"status":"FAILED_PRECONDITION"}}`, ports.AgentAuthStatusConfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateProviderKeys(t)
			var gotKey string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotKey = r.Header.Get("x-goog-api-key")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()
			env := map[string]string{"GEMINI_API_KEY": "bad-key", "GOOGLE_GEMINI_BASE_URL": server.URL}
			if got := verifyDefaultProvider(context.Background(), env); got != tc.want {
				t.Fatalf("verifyDefaultProvider = %q, want %q", got, tc.want)
			}
			if gotKey != "bad-key" {
				t.Fatalf("x-goog-api-key = %q", gotKey)
			}
		})
	}
}

func TestVerifyDefaultProviderUsesAnthropicBaseURL(t *testing.T) {
	isolateProviderKeys(t)
	var gotPath, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	previous := providerVerifyURLs["anthropic"]
	providerVerifyURLs["anthropic"] = "http://127.0.0.1:1/unused"
	defer func() { providerVerifyURLs["anthropic"] = previous }()

	env := map[string]string{"ANTHROPIC_API_KEY": "gateway-token", "ANTHROPIC_BASE_URL": server.URL + "/gateway/"}
	if got := verifyDefaultProvider(context.Background(), env); got != ports.AgentAuthStatusAuthorized {
		t.Fatalf("verifyDefaultProvider = %q, want authorized", got)
	}
	if gotPath != "/gateway/v1/models" || gotKey != "gateway-token" {
		t.Fatalf("checked %q with key %q, want the gateway's /v1/models", gotPath, gotKey)
	}
}
