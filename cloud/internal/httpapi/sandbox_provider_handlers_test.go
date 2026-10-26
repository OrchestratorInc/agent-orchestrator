package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// sandboxPreferenceStore keeps one organization's default sandbox provider.
type sandboxPreferenceStore struct {
	Store
	chosen   map[string]string
	notAdmin bool
}

func (f *sandboxPreferenceStore) OrgDefaultSandboxProvider(_ context.Context, orgID string) (string, error) {
	return f.chosen[orgID], nil
}

func (f *sandboxPreferenceStore) SetOrgDefaultSandboxProvider(
	_ context.Context, _ domain.Principal, orgID, provider string,
) error {
	if f.notAdmin {
		return postgres.ErrForbidden
	}
	f.chosen[orgID] = provider
	return nil
}

func (f *sandboxPreferenceStore) RequireOrgMember(context.Context, domain.Principal, string) error {
	return nil
}

func sandboxPreferenceServer(store *sandboxPreferenceStore) *Server {
	return New(Options{
		Store:                     store,
		SandboxProvider:           sandbox.ProviderCoder,
		AvailableSandboxProviders: []string{sandbox.ProviderCoder, sandbox.ProviderFreestyle},
		CapabilityGatedProviders:  []string{"coder", "freestyle"},
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func putSandboxProvider(server *Server, principal domain.Principal, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(body))
	route := chi.NewRouteContext()
	route.URLParams.Add("orgId", offeringTeamOrgID)
	ctx := context.WithValue(request.Context(), chi.RouteCtxKey, route)
	ctx = context.WithValue(ctx, principalKey, principal)
	response := httptest.NewRecorder()
	server.putOrgSandboxProvider(response, request.WithContext(ctx))
	return response
}

// An organization's chosen provider is its default for sessions nothing else
// places, as long as it may still use it.
func TestDefaultProviderFollowsTheOrganizationsChoice(t *testing.T) {
	store := &sandboxPreferenceStore{chosen: map[string]string{offeringTeamOrgID: sandbox.ProviderFreestyle}}
	server := sandboxPreferenceServer(store)
	ctx := context.Background()
	if got := server.defaultProviderForOrg(ctx, teamPrincipal("coder", "freestyle"), offeringTeamOrgID); got != sandbox.ProviderFreestyle {
		t.Fatalf("org allowed both chose freestyle: default = %q", got)
	}
	// A choice the deployment no longer offers is ignored.
	store.chosen[offeringTeamOrgID] = sandbox.ProviderNodeOps
	if got := server.defaultProviderForOrg(ctx, teamPrincipal("coder", "freestyle"), offeringTeamOrgID); got != sandbox.ProviderCoder {
		t.Fatalf("choice no longer offered: default = %q, want coder", got)
	}
}

func TestPutOrgSandboxProvider(t *testing.T) {
	store := &sandboxPreferenceStore{chosen: map[string]string{}}
	server := sandboxPreferenceServer(store)
	both := teamPrincipal("coder", "freestyle")

	response := putSandboxProvider(server, both, `{"defaultProvider":"Freestyle"}`)
	var offering offeringResponse
	_ = json.Unmarshal(response.Body.Bytes(), &offering)
	if response.Code != http.StatusOK || store.chosen[offeringTeamOrgID] != "freestyle" || offering.DefaultProvider != "freestyle" {
		t.Fatalf("choose freestyle: %d %s, stored %q", response.Code, response.Body, store.chosen[offeringTeamOrgID])
	}

	response = putSandboxProvider(server, both, `{"defaultProvider":"coder"}`)
	_ = json.Unmarshal(response.Body.Bytes(), &offering)
	if response.Code != http.StatusOK || offering.DefaultProvider != "coder" {
		t.Fatalf("choose coder: %d %s", response.Code, response.Body)
	}

	// Cleared, an organization without the coder capability is back on
	// Freestyle.
	response = putSandboxProvider(server, teamPrincipal(), `{"defaultProvider":""}`)
	_ = json.Unmarshal(response.Body.Bytes(), &offering)
	if response.Code != http.StatusOK || store.chosen[offeringTeamOrgID] != "" || offering.DefaultProvider != "freestyle" {
		t.Fatalf("clear: %d %s", response.Code, response.Body)
	}

	single := New(Options{Store: store, SandboxProvider: sandbox.ProviderFreestyle,
		AvailableSandboxProviders: []string{sandbox.ProviderFreestyle},
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil))})
	if response = putSandboxProvider(single, teamPrincipal(), `{"defaultProvider":"freestyle"}`); response.Code != http.StatusConflict {
		t.Fatalf("single-provider deployment: %d, want 409", response.Code)
	}
	if response = putSandboxProvider(server, both, `{"defaultProvider":"nodeops"}`); response.Code != http.StatusForbidden {
		t.Fatalf("provider the org may not use: %d, want 403", response.Code)
	}
	if response = putSandboxProvider(server, both, `{"defaultProvider":"coder","extra":1}`); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d, want 400", response.Code)
	}
	store.notAdmin = true
	if response = putSandboxProvider(server, both, `{"defaultProvider":"coder"}`); response.Code != http.StatusForbidden {
		t.Fatalf("non-admin: %d, want 403", response.Code)
	}
}
