package controllers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
)

type fakeAccountsManagerStatus struct {
	status   accountsmanager.Status
	endpoint accountsmanager.Endpoint
}

type fakeAccountsManagerCatalog struct{}

type emptyAccountsManagerCatalog struct{ fakeAccountsManagerCatalog }

func (emptyAccountsManagerCatalog) ListCredentials(context.Context) ([]accountsmanager.CredentialSummary, error) {
	return nil, nil
}

type fakeAccountsManagerRoutingStore struct {
	mu       sync.Mutex
	policies map[domain.AccountsManagerProvider]domain.AccountsManagerRoutingPolicy
}

func (f *fakeAccountsManagerRoutingStore) GetAccountsManagerRoutingPolicy(_ context.Context, provider domain.AccountsManagerProvider) (domain.AccountsManagerRoutingPolicy, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	policy := f.policies[provider]
	policy.Provider = provider
	policy.AccountIDs = append([]string(nil), policy.AccountIDs...)
	return policy, nil
}
func (f *fakeAccountsManagerRoutingStore) PutAccountsManagerRoutingPolicy(_ context.Context, policy domain.AccountsManagerRoutingPolicy) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	policy.AccountIDs = append([]string(nil), policy.AccountIDs...)
	f.policies[policy.Provider] = policy
	return nil
}
func (*fakeAccountsManagerRoutingStore) GetAccountsManagerSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error) {
	return domain.AccountsManagerSessionRoute{}, false, nil
}
func (*fakeAccountsManagerRoutingStore) GetOrCreateAccountsManagerSessionRoute(_ context.Context, route domain.AccountsManagerSessionRoute) (domain.AccountsManagerSessionRoute, bool, error) {
	return route, true, nil
}

func (fakeAccountsManagerCatalog) ListCredentials(context.Context) ([]accountsmanager.CredentialSummary, error) {
	return []accountsmanager.CredentialSummary{{Ref: "raw-auth-index", Provider: accountsmanager.ProviderCodex, Kind: accountsmanager.CredentialOAuth, Email: "safe@example.com", Status: accountsmanager.CredentialActive}}, nil
}
func (fakeAccountsManagerCatalog) CredentialPublicID(string) (string, error) { return "amc_safe", nil }
func (fakeAccountsManagerCatalog) OAuthPublicID(string) (string, error)      { return "amo_safe", nil }
func (fakeAccountsManagerCatalog) StreamOAuthEvents(ctx context.Context, _ func(accountsmanager.OAuthEvent) error) error {
	<-ctx.Done()
	return ctx.Err()
}

func (f fakeAccountsManagerStatus) Status() accountsmanager.Status { return f.status }

func TestAccountsManagerStatusResponseIsRedacted(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		status accountsmanager.Status
		state  string
		reason any
	}{
		{
			name:   "ready",
			status: accountsmanager.Status{State: accountsmanager.StateReady, EngineVersion: "v7.3.8"},
			state:  "ready",
			reason: nil,
		},
		{
			name:   "degraded",
			status: accountsmanager.Status{State: accountsmanager.StateDegraded, Reason: accountsmanager.ReasonBinaryMissing},
			state:  "degraded",
			reason: "binary_missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := chi.NewRouter()
			privateEndpoint := accountsmanager.Endpoint{
				BaseURL:         "http://127.0.0.1:54321",
				ClientToken:     "recognizable-client-token",
				ManagementToken: "recognizable-management-token",
			}
			controller := AccountsManagerController{Status: fakeAccountsManagerStatus{status: tt.status, endpoint: privateEndpoint}}
			controller.Register(router)
			request := httptest.NewRequest(http.MethodGet, "/accounts-manager/status", nil)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["state"] != tt.state || body["reason"] != tt.reason {
				t.Fatalf("body = %#v", body)
			}
			for _, forbidden := range []string{"url", "port", "pid", "token", "auth", "path", "error"} {
				if strings.Contains(strings.ToLower(response.Body.String()), forbidden) {
					t.Fatalf("response exposed forbidden word %q: %s", forbidden, response.Body.String())
				}
			}
			for _, secret := range []string{privateEndpoint.BaseURL, privateEndpoint.ClientToken, privateEndpoint.ManagementToken} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatalf("response exposed private endpoint material")
				}
			}
		})
	}
}

func TestAccountsManagerAccountsResponseNeverExposesPrivateReference(t *testing.T) {
	t.Parallel()
	router := chi.NewRouter()
	controller := AccountsManagerController{Service: accountsvc.New(fakeAccountsManagerCatalog{})}
	controller.Register(router)
	request := httptest.NewRequest(http.MethodGet, "/accounts-manager/accounts", nil)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, "raw-auth-index") || !strings.Contains(body, "amc_safe") || !strings.Contains(body, "safe@example.com") {
		t.Fatalf("unsafe response: %s", body)
	}
}

func TestAccountsManagerRoutingUpdatePreservesOrderedSafeIDs(t *testing.T) {
	store := &fakeAccountsManagerRoutingStore{policies: make(map[domain.AccountsManagerProvider]domain.AccountsManagerRoutingPolicy)}
	router := chi.NewRouter()
	controller := AccountsManagerController{Service: accountsvc.New(fakeAccountsManagerCatalog{}, store)}
	controller.Register(router)
	request := httptest.NewRequest(http.MethodPut, "/accounts-manager/routing/codex", strings.NewReader(`{"enabled":true,"accountIds":["amc_safe"]}`))
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"routing":[{"provider":"codex","enabled":true,"accountIds":["amc_safe"]}`) {
		t.Fatalf("routing response = %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "raw-auth-index") {
		t.Fatalf("routing response exposed private ref: %s", response.Body.String())
	}
}

func TestAccountsManagerFirstLaunchReturnsEmptyArrays(t *testing.T) {
	t.Parallel()
	store := &fakeAccountsManagerRoutingStore{}
	router := chi.NewRouter()
	controller := AccountsManagerController{Service: accountsvc.New(emptyAccountsManagerCatalog{}, store)}
	controller.Register(router)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/accounts-manager/accounts", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body AccountsManagerAccountsResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Accounts == nil || len(body.Accounts) != 0 || body.OAuthSessions == nil || len(body.OAuthSessions) != 0 {
		t.Fatalf("first-launch collections must be empty arrays: %s", response.Body.String())
	}
	if len(body.Routing) != 2 {
		t.Fatalf("routing policies = %d, want 2", len(body.Routing))
	}
	for _, policy := range body.Routing {
		if policy.Enabled || policy.AccountIDs == nil || len(policy.AccountIDs) != 0 {
			t.Errorf("unset policy must be disabled with an empty account array: %#v", policy)
		}
	}
}
