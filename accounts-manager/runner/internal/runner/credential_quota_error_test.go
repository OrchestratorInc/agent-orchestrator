package runner

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

func TestCredentialQuotaFailureCategories(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   int
		code   string
	}{
		{"expired", 401, `{"secret":"private-provider-body"}`, 401, "quota_authentication_required"},
		{"denied", 403, `{"secret":"private-provider-body"}`, 403, "quota_access_denied"},
		{"limited", 429, `{"secret":"private-provider-body"}`, 429, "quota_rate_limited"},
		{"outage", 503, `{"secret":"private-provider-body"}`, 503, "quota_unavailable"},
		{"redirect", 302, `{"secret":"private-provider-body"}`, 503, "quota_unavailable"},
		{"transport", 0, "", 503, "quota_unavailable"},
		{"unreadable observation", 200, `{"secret":"private-provider-body"}`, 502, "quota_response_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			vault := newTestVault(t)
			runtime := &credentialRuntime{vault: vault, manager: coreauth.NewManager(vault, nil, nil)}
			runtime.checkTransport = runnerRoundTripFunc(func(request *http.Request) (*http.Response, error) {
				if test.status == 0 {
					return nil, errors.New("private-provider-body https://private.invalid")
				}
				return &http.Response{StatusCode: test.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(test.body)), Request: request}, nil
			})
			if err := vault.Begin(t.Context(), "quota-category", "codex", time.Now().Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			auth, err := runtime.completeObserved(t.Context(), "quota-category", vaultFixture(), false, true)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, credentialPath+"/quota?ref="+auth.Index, nil)
			request.Header.Set("Authorization", "Bearer management")
			response := httptest.NewRecorder()
			(&credentialHTTP{key: "management", runtime: runtime}).ServeHTTP(response, request)
			if response.Code != test.want || !strings.Contains(response.Body.String(), test.code) {
				t.Fatalf("quota failure = %d %s, want %d %s", response.Code, response.Body.String(), test.want, test.code)
			}
			for _, secret := range []string{"private-provider-body", "private.invalid", "vault-secret"} {
				if strings.Contains(response.Body.String(), secret) {
					t.Fatal("quota error exposed private material")
				}
			}
			if !vault.admitVerified(t.Context(), auth) || len(runtime.manager.List()) != 1 {
				t.Fatal("quota failure changed account authorization")
			}
		})
	}
}
