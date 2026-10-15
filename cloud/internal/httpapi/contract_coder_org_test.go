package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
)

// coderCustomerPrincipals are members of a customer set up on Coder alone
// (WorkOS metadata capabilities=coder): as 11x is tagged (no billing key), and
// with billing=contract.
func coderCustomerPrincipals() map[string]domain.Principal {
	contract := teamPrincipal("coder")
	contract.OrgBilling = domain.OrgBillingContract
	return map[string]domain.Principal{"capabilities=coder": teamPrincipal("coder"), "and billing=contract": contract}
}

// A Coder customer keeps what it had before every organization was offered
// Coder and Freestyle, on a deployment that offers both, gated, with billing
// on (production): Coder alone, its default, no plans to buy, its own Coder
// settings page, and sessions that land on Coder unbilled. Freestyle stays
// closed to it, and nothing pins its projects.
func TestCoderCustomerKeepsCoderAlone(t *testing.T) {
	for name, principal := range coderCustomerPrincipals() {
		t.Run(name, func(t *testing.T) {
			t.Run("offering", func(t *testing.T) {
				store := &offeringStore{memberships: []domain.Membership{{OrgID: offeringTeamOrgID, Role: "admin"}}}
				server := newGenericOfferingServer(store)
				server.billingOptions = &BillingOptions{Stripe: &fakeStripe{}}
				server.billingStore = newFakeBillingStore()
				offering := getMe(t, server, principal).Offering
				if !slices.Equal(offering.Providers, []string{sandbox.ProviderCoder}) || offering.DefaultProvider != sandbox.ProviderCoder ||
					offering.BillingMode != domain.BillingModeOff || offering.Kind != domain.OfferingKindB2B || !offering.CustomCoder {
					t.Fatalf("offering = %+v, want Coder alone, its default, billing off, b2b, and the custom Coder page", offering)
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

			t.Run("Freestyle stays closed", func(t *testing.T) {
				store := &offeringStore{}
				rec := httptest.NewRecorder()
				newGenericOfferingServer(store).createSession(rec, offeringSessionRequest(t, principal, sandbox.ProviderFreestyle))
				if rec.Code != http.StatusForbidden || store.created {
					t.Fatalf("status = %d, body = %s; want Freestyle refused", rec.Code, rec.Body.String())
				}
			})

			t.Run("projects are not pinned", func(t *testing.T) {
				pinned, err := newGenericOfferingServer(&offeringStore{}).pinProjectProvider(
					context.Background(), principal, offeringTeamOrgID, json.RawMessage(`{"source":"scratch"}`))
				if err != nil || string(pinned) != `{"source":"scratch"}` {
					t.Fatalf("pinned = %s, %v; want the config unchanged", pinned, err)
				}
			})

			t.Run("own Coder connection", func(t *testing.T) {
				store := &coderConfigFakeStore{connections: []domain.ProviderConnection{{
					Provider: sandbox.ProviderCoder, Label: defaultAgentConnectionLabel,
					Config: json.RawMessage(`{"baseUrl":"https://coder.11x.example","templateId":"ao-11x-replica"}`),
				}}}
				server, _ := newCoderConfigServer(t, store, true)
				rec := httptest.NewRecorder()
				server.getOrgCoderConfig(rec, coderConfigRequest(t, http.MethodGet, "", principal.OrgCapabilities))
				var body orgCoderConfigResponse
				if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &body) != nil || !body.Configured {
					t.Fatalf("status = %d, body = %s; want its stored connection", rec.Code, rec.Body.String())
				}
			})

			t.Run("no default to choose", func(t *testing.T) {
				store := &sandboxPreferenceStore{chosen: map[string]string{}}
				if rec := putSandboxProvider(sandboxPreferenceServer(store), principal, `{"defaultProvider":"freestyle"}`); rec.Code != http.StatusConflict ||
					store.chosen[offeringTeamOrgID] != "" {
					t.Fatalf("choose freestyle: %d %s, want 409 with nothing stored", rec.Code, rec.Body.String())
				}
			})
		})
	}
}
