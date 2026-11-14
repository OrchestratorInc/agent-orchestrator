package authprobe

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type connectionTransport func(*http.Request) (*http.Response, error)

func (f connectionTransport) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestVerifyConnectionRequiresProviderAcceptance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		want   ports.AgentAuthStatus
		code   string
	}{
		{"accepted", 200, `{"choices":[{"message":{"role":"assistant","content":"OK"}}]}`, ports.AgentAuthStatusAuthorized, "provider_accepted"},
		{"bad key", 401, `{"error":{"message":"secret-key"}}`, ports.AgentAuthStatusUnauthorized, "provider_rejected"},
		{"denied", 403, `{}`, ports.AgentAuthStatusUnauthorized, "provider_rejected"},
		{"rate limited", 429, `{}`, ports.AgentAuthStatusConfigured, "provider_inconclusive"},
		{"offline provider", 503, `{}`, ports.AgentAuthStatusConfigured, "provider_inconclusive"},
		{"error envelope with HTTP200", 200, `{"error":{"code":"bad_key"}}`, ports.AgentAuthStatusConfigured, "provider_inconclusive"},
		{"model inventory", 200, `{"data":[{"id":"glm-5.2"}]}`, ports.AgentAuthStatusConfigured, "provider_inconclusive"},
		{"invalid JSON", 200, `not-json`, ports.AgentAuthStatusConfigured, "provider_inconclusive"},
		{"empty choices", 200, `{"choices":[]}`, ports.AgentAuthStatusConfigured, "provider_inconclusive"},
		{"not a completion", 200, `{"choices":[{}]}`, ports.AgentAuthStatusConfigured, "provider_inconclusive"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: connectionTransport(func(req *http.Request) (*http.Response, error) {
				if req.URL.String() != "https://api.z.ai/api/coding/paas/v4/chat/completions" || req.Method != http.MethodPost || req.Header.Get("Authorization") != "Bearer secret-key" {
					t.Fatalf("wrong provider request: %s %s", req.Method, req.URL.Redacted())
				}
				var body map[string]any
				if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["model"] != "glm-5.2" || body["max_tokens"] != float64(1) || body["tools"] != nil {
					t.Fatalf("unexpected probe body: %v", body)
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}}, nil
			})}
			status, step := VerifyConnection(t.Context(), Connection{Provider: "zai", BaseURL: "https://api.z.ai/api/coding/paas/v4", Model: "glm-5.2", APIKey: "secret-key"}, client)
			if status != tc.want || step.Code != tc.code || step.HTTPStatus != tc.status {
				t.Fatalf("status=%s step=%+v", status, step)
			}
			encoded, _ := json.Marshal(step)
			if strings.Contains(string(encoded), "secret-key") {
				t.Fatal("credential entered diagnostics")
			}
		})
	}
}

func TestVerifyConnectionDoesNotFollowRedirects(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: connectionTransport(func(*http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": []string{"https://other.example/"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	status, _ := VerifyConnection(t.Context(), Connection{BaseURL: "https://provider.example/v1", Model: "m", APIKey: "secret"}, client)
	if calls != 1 || status != ports.AgentAuthStatusConfigured {
		t.Fatalf("calls=%d status=%s", calls, status)
	}
}

func TestVerifyConnectionCancellationAndUnreachable(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		client := &http.Client{Transport: connectionTransport(func(*http.Request) (*http.Response, error) { return nil, cause })}
		status, step := VerifyConnection(t.Context(), Connection{BaseURL: "https://provider.example/v1", Model: "m", APIKey: "secret"}, client)
		if status != ports.AgentAuthStatusConfigured || step.Code != "provider_unreachable" {
			t.Fatalf("status=%s step=%+v", status, step)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	status, step := VerifyConnection(ctx, Connection{BaseURL: "https://provider.example/v1", Model: "m", APIKey: "secret"}, &http.Client{Transport: connectionTransport(func(*http.Request) (*http.Response, error) { return nil, context.Canceled })})
	if status != ports.AgentAuthStatusConfigured || step.Code != "probe_cancelled" {
		t.Fatalf("status=%s step=%+v", status, step)
	}
}
