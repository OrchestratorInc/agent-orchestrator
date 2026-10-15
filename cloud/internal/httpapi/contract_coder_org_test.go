package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// contractCoderPrincipal is a member of a contract customer on Coder: WorkOS
// metadata capabilities=coder and billing=contract, like 11x.
func contractCoderPrincipal() domain.Principal {
	principal := teamPrincipal("coder")
	principal.OrgBilling = domain.OrgBillingContract
	return principal
}

// A contract customer on Coder keeps what it had on a deployment that offers
// Coder and Freestyle with both gated (production): it starts on Coder, sees
// its own Coder settings page, its sessions land on Coder unbilled, and an
// admin can still pick the default.
func TestContractCoderOrganizationKeepsCoder(t *testing.T) {
	principal := contractCoderPrincipal()

	t.Run("offering", func(t *testing.T) {
		store := &offeringStore{memberships: []domain.Membership{{OrgID: offeringTeamOrgID, Role: "admin"}}}
		offering := getMe(t, newGenericOfferingServer(store), principal).Offering
		if !slices.Contains(offering.Providers, sandbox.ProviderCoder) || offering.DefaultProvider != sandbox.ProviderCoder ||
			!offering.CustomCoder {
			t.Fatalf("offering = %+v, want Coder offered, the default, and the custom Coder page", offering)
		}
	})

	for _, tc := range []struct{ name, config string }{
		{"project pinned to Coder", `{"sandboxProvider":"coder","coder":{"templateId":"ao-11x-replica"}}`},
		{"project with Coder config from before pinning", `{"coder":{"templateId":"ao-11x-replica"}}`},
		{"project following the organization default", `{}`},
	} {
		t.Run("create session/"+tc.name, func(t *testing.T) {
			store := &offeringStore{projectConfig: json.RawMessage(tc.config)}
			rec := httptest.NewRecorder()
			newGenericOfferingServer(store).createSession(rec, offeringSessionRequest(t, principal, ""))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if store.captured.Provider != sandbox.ProviderCoder || !store.captured.CoderCapability {
				t.Fatalf("captured provider %q coder capability %v, want an unbilled Coder session",
					store.captured.Provider, store.captured.CoderCapability)
			}
		})
	}

	t.Run("own Coder connection", func(t *testing.T) {
		store := &coderConfigFakeStore{connections: []domain.ProviderConnection{{
			Provider: sandbox.ProviderCoder, Label: defaultAgentConnectionLabel,
			Config: json.RawMessage(`{"baseUrl":"https://coder.11x.example","templateId":"ao-11x-replica"}`),
		}}}
		server, _ := newCoderConfigServer(t, store, true)
		rec := httptest.NewRecorder()
		server.getOrgCoderConfig(rec, coderConfigRequest(t, http.MethodGet, "", []string{sandbox.ProviderCoder}))
		var body orgCoderConfigResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || !body.Configured {
			t.Fatalf("status = %d, body = %s; want its stored connection", rec.Code, rec.Body.String())
		}
	})

	t.Run("default provider", func(t *testing.T) {
		store := &sandboxPreferenceStore{chosen: map[string]string{}}
		if rec := putSandboxProvider(sandboxPreferenceServer(store), principal, `{"defaultProvider":"coder"}`); rec.Code != http.StatusOK ||
			store.chosen[offeringTeamOrgID] != sandbox.ProviderCoder {
			t.Fatalf("choose coder: %d %s", rec.Code, rec.Body.String())
		}
	})
}
