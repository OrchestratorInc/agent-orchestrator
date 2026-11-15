package dockeragent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestVerifyDefaultProvider(t *testing.T) {
	cases := []struct {
		name   string
		status int
		want   ports.AgentAuthStatus
	}{
		{"accepted", http.StatusOK, ports.AgentAuthStatusAuthorized},
		{"rejected", http.StatusUnauthorized, ports.AgentAuthStatusUnauthorized},
		{"forbidden", http.StatusForbidden, ports.AgentAuthStatusUnauthorized},
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
