package controllers_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentauth"
)

type fakeProviderKeyService struct {
	fakeAgentAuthService
	name, secret string
	device       bool
	removed      string
	options      agentauth.LoginOptions
}

func (f *fakeProviderKeyService) ProviderKeys(context.Context) ([]agentauth.ProviderKey, error) {
	return []agentauth.ProviderKey{{Name: "OPENAI_API_KEY", Label: "OpenAI API key", Configured: true, Hint: "abcd", Agents: []string{"goose"}}}, nil
}

func (f *fakeProviderKeyService) SaveProviderKey(_ context.Context, name, secret string, device bool) (agentauth.ProviderKey, error) {
	if name == "PATH" {
		return agentauth.ProviderKey{}, apierr.Invalid("PROVIDER_KEY_UNKNOWN", "Choose a supported provider key.", nil)
	}
	f.name, f.secret, f.device = name, secret, device
	return agentauth.ProviderKey{Name: name, Configured: true, Device: device}, nil
}

func (f *fakeProviderKeyService) RemoveProviderKey(_ context.Context, name string) (agentauth.ProviderKey, error) {
	f.removed = name
	return agentauth.ProviderKey{Name: name}, nil
}

func (f *fakeProviderKeyService) CheckProviderKey(_ context.Context, name, secret string) (agentauth.ProviderKeyCheck, error) {
	f.name, f.secret = name, secret
	return agentauth.ProviderKeyCheck{Status: "valid", Models: []string{"gpt-5"}}, nil
}

func (f *fakeProviderKeyService) StartLoginWithOptions(ctx context.Context, agentID, method, secret string, options agentauth.LoginOptions) (agentauth.LoginSnapshot, error) {
	f.options = options
	return f.StartLogin(ctx, agentID, method, secret)
}

func TestProviderKeyRoutesNeverReturnSecrets(t *testing.T) {
	svc := &fakeProviderKeyService{}
	server := newAgentAuthTestServer(t, svc)
	body, status, headers := doRequest(t, server, http.MethodGet, "/api/v1/agents/provider-keys", "")
	if status != http.StatusOK || !strings.Contains(string(body), `"hint":"abcd"`) || headers.Get("Cache-Control") != "no-store" {
		t.Fatalf("list status=%d body=%s", status, body)
	}
	body, status, _ = doRequest(t, server, http.MethodPut, "/api/v1/agents/provider-keys/OPENAI_API_KEY", `{"secret":"test-secret","device":true}`)
	if status != http.StatusOK || svc.secret != "test-secret" || !svc.device || strings.Contains(string(body), "test-secret") {
		t.Fatalf("save status=%d body=%s", status, body)
	}
	body, status, _ = doRequest(t, server, http.MethodPost, "/api/v1/agents/provider-keys/OPENAI_API_KEY/check", `{"secret":"check-secret"}`)
	if status != http.StatusOK || svc.secret != "check-secret" || !strings.Contains(string(body), "gpt-5") || strings.Contains(string(body), "check-secret") {
		t.Fatalf("check status=%d body=%s", status, body)
	}
	if _, status, _ = doRequest(t, server, http.MethodDelete, "/api/v1/agents/provider-keys/OPENAI_API_KEY", ""); status != http.StatusOK || svc.removed != "OPENAI_API_KEY" {
		t.Fatalf("remove status=%d", status)
	}
	if _, status, _ = doRequest(t, server, http.MethodPut, "/api/v1/agents/provider-keys/PATH", `{"secret":"x","device":false}`); status != http.StatusBadRequest {
		t.Fatalf("unknown key status=%d", status)
	}
	if _, status, _ = doRequest(t, server, http.MethodPut, "/api/v1/agents/provider-keys/OPENAI_API_KEY", `{"secret":"x","value":"y"}`); status != http.StatusBadRequest {
		t.Fatalf("unknown field status=%d", status)
	}
}

func TestAgentUILoginPassesDeviceChoice(t *testing.T) {
	svc := &fakeProviderKeyService{}
	svc.uiResult = agentauth.LoginSnapshot{ID: "login-1", AgentID: "goose", Method: "env_OPENAI_API_KEY", Status: "succeeded"}
	_, status, _ := doRequest(t, newAgentAuthTestServer(t, svc), http.MethodPost, "/api/v1/agents/goose/auth/operations", `{"method":"env_OPENAI_API_KEY","secret":"test-secret","device":true}`)
	if status != http.StatusAccepted || !svc.options.Device || svc.uiSecret != "test-secret" {
		t.Fatalf("status=%d options=%+v", status, svc.options)
	}
}
