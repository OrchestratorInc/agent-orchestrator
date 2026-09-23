package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

type preferenceFakeStore struct {
	Store
	provider   string
	configured bool
	writes     int
}

func (s *preferenceFakeStore) GetUserSandboxProvider(_ context.Context, _ string) (string, bool, error) {
	return s.provider, s.configured, nil
}

func (s *preferenceFakeStore) PutUserSandboxProvider(_ context.Context, _ string, provider string, initializeOnly bool) (string, error) {
	if initializeOnly && s.configured {
		return "", postgres.ErrConflict
	}
	s.provider, s.configured = provider, true
	s.writes++
	return provider, nil
}

func preferenceRequest(method, body string) *http.Request {
	req := httptest.NewRequest(method, "/api/cloud/v1/me/preferences", strings.NewReader(body))
	return req.WithContext(context.WithValue(req.Context(), principalKey, domain.Principal{UserID: "00000000-0000-0000-0000-000000000001"}))
}

func TestUserSandboxPreferenceGetAndPut(t *testing.T) {
	store := &preferenceFakeStore{}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
	get := httptest.NewRecorder()
	srv.getUserPreferences(get, preferenceRequest(http.MethodGet, ""))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"sandboxProvider":null`) {
		t.Fatalf("get: %d %s", get.Code, get.Body.String())
	}
	put := httptest.NewRecorder()
	srv.putUserPreferences(put, preferenceRequest(http.MethodPut, `{"sandboxProvider":"coder"}`))
	if put.Code != 200 || !strings.Contains(put.Body.String(), `"sandboxProvider":"coder"`) || store.writes != 1 {
		t.Fatalf("put: %d %s", put.Code, put.Body.String())
	}
	reset := httptest.NewRecorder()
	srv.putUserPreferences(reset, preferenceRequest(http.MethodPut, `{"sandboxProvider":null}`))
	if reset.Code != 200 || !strings.Contains(reset.Body.String(), `"sandboxProvider":null`) {
		t.Fatalf("reset: %d %s", reset.Code, reset.Body.String())
	}
}

func TestUserSandboxPreferenceRequiresAuthentication(t *testing.T) {
	store := &preferenceFakeStore{}
	srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/cloud/v1/me/preferences", strings.NewReader(`{"sandboxProvider":"coder"}`))
		srv.Handler().ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized || store.writes != 0 {
			t.Fatalf("%s status=%d writes=%d body=%s", method, rec.Code, store.writes, rec.Body.String())
		}
	}
}

func TestUserSandboxPreferenceRejectsInvalidAndConflictingWrites(t *testing.T) {
	cases := []struct {
		body   string
		status int
		code   string
	}{
		{`{}`, 400, "invalid_request"},
		{`{"sandboxProvider":""}`, 422, "validation_error"},
		{`{"sandboxProvider":"unknown"}`, 422, "provider_unavailable"},
		{`{"sandboxProvider":null,"extra":true}`, 400, "invalid_request"},
		{`{"sandboxProvider":"coder","initializeOnly":true}`, 409, "preference_conflict"},
	}
	for _, tc := range cases {
		store := &preferenceFakeStore{configured: true, provider: sandbox.ProviderNodeOps}
		srv := newChildServer(store, bothProviderProvisioning(sandbox.ProviderNodeOps), sandbox.ProviderNodeOps)
		rec := httptest.NewRecorder()
		srv.putUserPreferences(rec, preferenceRequest(http.MethodPut, tc.body))
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) || store.writes != 0 {
			t.Errorf("body=%s status=%d response=%s writes=%d", tc.body, rec.Code, rec.Body.String(), store.writes)
		}
	}
}
