package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/go-chi/chi/v5"
)

const (
	offeringTeamOrgID     = "00000000-0000-0000-0000-00000000aa01"
	offeringPersonalOrgID = "00000000-0000-0000-0000-00000000aa02"
)

// A server with coder capability-gated (AO_CLOUD_CAPABILITY_GATED_PROVIDERS=coder).
func gatedServer() *Server {
	return &Server{capabilityGatedProviders: map[string]bool{"coder": true}}
}

// teamPrincipal is a principal whose WorkOS organization maps to
// offeringTeamOrgID and carries the given capabilities.
func teamPrincipal(capabilities ...string) domain.Principal {
	return domain.Principal{TeamOrgID: offeringTeamOrgID, ExternalOrgID: "org_workos", OrgCapabilities: capabilities}
}

// Every provider is open to every organization; the gate decides only who
// holds the coder capability (Coder by default, bring-your-own Coder).
func TestOrgAllowsProvider(t *testing.T) {
	s := gatedServer()
	for _, caps := range [][]string{nil, {"coder"}, {"feature-x"}, {"feature-x", "coder"}} {
		for _, provider := range []string{"coder", "nodeops", "freestyle"} {
			if !s.orgAllowsProvider(teamPrincipal(caps...), offeringTeamOrgID, provider) {
				t.Errorf("orgAllowsProvider(caps=%v, %q) = false, want every provider open", caps, provider)
			}
		}
	}
}

func TestOrgHasCoderCapability(t *testing.T) {
	gated := gatedServer()
	cases := []struct {
		name string
		caps []string
		want bool
	}{
		{"metadata capabilities=coder", []string{"coder"}, true},
		{"coder among others", []string{"feature-x", "coder"}, true},
		{"no capabilities", nil, false},
		{"unrelated capability", []string{"feature-x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gated.orgHasCoderCapability(teamPrincipal(tc.caps...), offeringTeamOrgID); got != tc.want {
				t.Fatalf("orgHasCoderCapability(%v) = %v, want %v", tc.caps, got, tc.want)
			}
		})
	}
	if !(&Server{}).orgHasCoderCapability(domain.Principal{}, offeringPersonalOrgID) {
		t.Fatal("a deployment that does not gate coder gives everyone the coder capability, as before")
	}
}

// The rollout-safe default: with no providers configured as gated (the empty /
// nil set), every provider is allowed regardless of org capabilities, so
// shipping the code does not disturb existing coder users.
func TestUngatedServerAllowsEverything(t *testing.T) {
	s := &Server{} // capabilityGatedProviders is nil
	if !s.orgAllowsProvider(domain.Principal{}, offeringPersonalOrgID, "coder") {
		t.Fatal("with no gated providers, coder must be allowed (rollout-safe default)")
	}
}

// The token's WorkOS organization capabilities describe that organization only.
// A request against the user's personal organization, or a WorkOS organization
// whose metadata has no capabilities key, uses the deployment defaults.
func TestOrgCapabilitiesDefaultsAndScoping(t *testing.T) {
	s := &Server{
		capabilityGatedProviders: map[string]bool{"coder": true, "freestyle": true},
		defaultOrgCapabilities:   []string{"freestyle"},
	}
	cases := []struct {
		name      string
		principal domain.Principal
		orgID     string
		want      []string
	}{
		{"team org with its own capabilities", teamPrincipal("coder"), offeringTeamOrgID, []string{"coder"}},
		{"team org capabilities never bleed into the personal org", teamPrincipal("coder"), offeringPersonalOrgID, []string{"freestyle"}},
		{"team org without a capabilities key gets the defaults", teamPrincipal(), offeringTeamOrgID, []string{"freestyle"}},
		{"no WorkOS organization gets the defaults", domain.Principal{}, offeringPersonalOrgID, []string{"freestyle"}},
		{"no target organization gets the defaults", teamPrincipal("coder"), "", []string{"freestyle"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := s.orgCapabilities(tc.principal, tc.orgID); !slices.Equal(got, tc.want) {
				t.Fatalf("orgCapabilities = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestOrgBillingArrangementDefaultsAndScoping(t *testing.T) {
	s := &Server{defaultOrgBilling: domain.OrgBillingSelfServe}
	contract := teamPrincipal("coder")
	contract.OrgBilling = domain.OrgBillingContract
	if got := s.orgBillingArrangement(contract, offeringTeamOrgID); got != domain.OrgBillingContract {
		t.Fatalf("team org = %q, want contract", got)
	}
	if got := s.orgBillingArrangement(contract, offeringPersonalOrgID); got != domain.OrgBillingSelfServe {
		t.Fatalf("personal org = %q, want the self_serve default", got)
	}
	s.defaultOrgBilling = domain.OrgBillingContract
	if got := s.orgBillingArrangement(teamPrincipal(), offeringTeamOrgID); got != domain.OrgBillingContract {
		t.Fatalf("team org without billing metadata = %q, want the contract default", got)
	}
}

func TestProvidersAndDefaultProviderForOrg(t *testing.T) {
	cases := []struct {
		name        string
		caps        []string
		wantAllowed []string
		wantDefault string
	}{
		{"coder capability starts on coder", []string{"coder"}, []string{"coder", "freestyle"}, "coder"},
		{"no coder capability starts on freestyle", []string{"freestyle"}, []string{"coder", "freestyle"}, "freestyle"},
		{"coder capability with freestyle starts on coder", []string{"freestyle", "coder"}, []string{"coder", "freestyle"}, "coder"},
		{"a new organization starts on freestyle", nil, []string{"coder", "freestyle"}, "freestyle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New(Options{
				SandboxProvider:           sandbox.ProviderCoder,
				AvailableSandboxProviders: []string{sandbox.ProviderCoder, sandbox.ProviderFreestyle},
				CapabilityGatedProviders:  []string{"coder", "freestyle"},
				Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
			})
			principal := teamPrincipal(tc.caps...)
			if got := s.providersForOrg(principal, offeringTeamOrgID); !slices.Equal(got, tc.wantAllowed) {
				t.Fatalf("providersForOrg = %v, want %v", got, tc.wantAllowed)
			}
			if got := s.defaultProviderForOrg(context.Background(), principal, offeringTeamOrgID); got != tc.wantDefault {
				t.Fatalf("defaultProviderForOrg = %q, want %q", got, tc.wantDefault)
			}
		})
	}
}

// offeringStore answers the store calls /me and the session paths make.
type offeringStore struct {
	Store
	memberships   []domain.Membership
	plan          string
	grandfathered bool
	projectConfig json.RawMessage
	created       bool
	captured      domain.CreateSession
}

func (s *offeringStore) ListMemberships(context.Context, domain.Principal) ([]domain.Membership, error) {
	return s.memberships, nil
}

func (s *offeringStore) OrgBillingStanding(context.Context, domain.Principal, string) (string, bool, error) {
	return s.plan, s.grandfathered, nil
}

func (s *offeringStore) UserAgentCredentialAvailable(context.Context, string, string) (bool, error) {
	return true, nil
}

func (s *offeringStore) GetProject(_ context.Context, _ domain.Principal, _, projectID string) (domain.Project, error) {
	return domain.Project{ID: projectID, Config: s.projectConfig}, nil
}

func (s *offeringStore) ListProviderConnections(context.Context, domain.Principal, string) ([]domain.ProviderConnection, error) {
	return nil, nil
}

func (s *offeringStore) CreateSession(
	_ context.Context, _ domain.Principal, _, _ string, _ int, input domain.CreateSession,
) (domain.Session, error) {
	s.created = true
	s.captured = input
	return domain.Session{ID: "00000000-0000-0000-0000-0000000000e5", Kind: input.Kind}, nil
}

// newOfferingServer offers coder (the deployment default) and nodeops, both
// capability-gated, so an organization's capabilities alone decide its kind.
func newOfferingServer(store Store, defaultCapabilities ...string) *Server {
	return New(Options{
		Store:                     store,
		SandboxProvider:           sandbox.ProviderCoder,
		AvailableSandboxProviders: []string{sandbox.ProviderCoder, sandbox.ProviderNodeOps},
		CapabilityGatedProviders:  []string{sandbox.ProviderCoder, sandbox.ProviderNodeOps},
		DefaultOrgCapabilities:    defaultCapabilities,
		Provisioning:              bothProviderProvisioning(sandbox.ProviderCoder),
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

type meOfferingBody struct {
	ActiveOrganizationID string           `json:"activeOrganizationId"`
	Offering             offeringResponse `json:"offering"`
	SandboxProviders     struct {
		Available []string `json:"available"`
		Default   string   `json:"default"`
	} `json:"sandboxProviders"`
	Organizations []organizationResponse `json:"organizations"`
}

func getMe(t *testing.T, s *Server, principal domain.Principal) meOfferingBody {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/cloud/v1/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), principalKey, principal))
	rec := httptest.NewRecorder()
	s.me(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body meOfferingBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /me: %v", err)
	}
	return body
}

// Every organization is offered every provider; the coder capability decides
// its default and whether it may bring its own Coder.
func TestMeOfferingKinds(t *testing.T) {
	cases := []struct {
		name            string
		caps            []string
		wantDefault     string
		wantCustomCoder bool
	}{
		{"coder capability", []string{"coder"}, "coder", true},
		{"no coder capability falls back to the deployment default", []string{"nodeops"}, "coder", false},
		{"no capabilities", []string{"feature-x"}, "coder", false},
	}
	all := []string{"coder", "nodeops"}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &offeringStore{memberships: []domain.Membership{{OrgID: offeringTeamOrgID, Role: "member"}}}
			body := getMe(t, newOfferingServer(store), teamPrincipal(tc.caps...))
			if body.Offering.Kind != domain.OfferingKindBoth || body.Offering.DefaultProvider != tc.wantDefault ||
				!slices.Equal(body.Offering.Providers, all) || body.Offering.CustomCoder != tc.wantCustomCoder {
				t.Fatalf("offering = %+v, want both providers, default %q, customCoder %v",
					body.Offering, tc.wantDefault, tc.wantCustomCoder)
			}
			if body.Offering.BillingMode != domain.BillingModeOff || body.Offering.Features == nil {
				t.Fatalf("offering = %+v, want billingMode off and an empty features array", body.Offering)
			}
			if !slices.Equal(body.SandboxProviders.Available, all) || body.SandboxProviders.Default != tc.wantDefault {
				t.Fatalf("sandboxProviders = %+v, want the offering's providers and default", body.SandboxProviders)
			}
			if len(body.Organizations) != 1 || body.Organizations[0].Offering == nil ||
				body.Organizations[0].Offering.CustomCoder != tc.wantCustomCoder {
				t.Fatalf("organizations = %+v, want a per-organization offering", body.Organizations)
			}
		})
	}
}

// The personal organization listed first does not inherit the team org's coder
// capability: it is offered the deployment defaults, while the team org entry
// keeps coder. The token's team org is the active one, which the top-level
// offering describes.
func TestMeOfferingIsPerOrganization(t *testing.T) {
	store := &offeringStore{memberships: []domain.Membership{
		{OrgID: offeringPersonalOrgID, Role: "owner"},
		{OrgID: offeringTeamOrgID, Role: "member"},
	}}
	body := getMe(t, newOfferingServer(store, "nodeops"), teamPrincipal("coder"))
	if body.Organizations[0].Offering.CustomCoder {
		t.Fatalf("personal org offering = %+v, want no custom Coder", body.Organizations[0].Offering)
	}
	if body.ActiveOrganizationID != offeringTeamOrgID || !body.Offering.CustomCoder || body.Offering.DefaultProvider != "coder" {
		t.Fatalf("active org = %q offering = %+v, want the team org's coder offering", body.ActiveOrganizationID, body.Offering)
	}
	if !body.Organizations[1].Offering.CustomCoder {
		t.Fatalf("team org offering = %+v, want custom Coder", body.Organizations[1].Offering)
	}
}

func TestMeOfferingBillingMode(t *testing.T) {
	cases := []struct {
		name          string
		orgBilling    string
		plan          string
		grandfathered bool
		want          string
	}{
		// Freestyle is paid by everyone: contract, internal and grandfathered
		// organizations are self-serve too.
		{"self-serve", "", "free", false, domain.BillingModeSelfServe},
		{"contract metadata", domain.OrgBillingContract, "free", false, domain.BillingModeSelfServe},
		{"internal plan", "", domain.PlanInternal, false, domain.BillingModeSelfServe},
		{"grandfathered", "", "free", true, domain.BillingModeSelfServe},
		{"coder only with the coder capability has nothing to buy", "", "free", false, domain.BillingModeOff},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &offeringStore{
				memberships:   []domain.Membership{{OrgID: offeringTeamOrgID, Role: "member"}},
				plan:          tc.plan,
				grandfathered: tc.grandfathered,
			}
			s := newOfferingServer(store)
			s.billingOptions = &BillingOptions{Stripe: &fakeStripe{}}
			s.billingStore = newFakeBillingStore()
			principal := teamPrincipal("coder")
			if tc.want != domain.BillingModeOff {
				// Plans sell Freestyle capacity, so only an organization
				// offered Freestyle has a self-serve or contract mode.
				s.availableSandboxProviders = append(s.availableSandboxProviders, sandbox.ProviderFreestyle)
				principal = teamPrincipal("coder", "freestyle")
			}
			principal.OrgBilling = tc.orgBilling
			if got := getMe(t, s, principal).Offering.BillingMode; got != tc.want {
				t.Fatalf("billingMode = %q, want %q", got, tc.want)
			}
		})
	}
}

func offeringSessionRequest(t *testing.T, principal domain.Principal, provider string) *http.Request {
	t.Helper()
	body := `{"projectId":"` + byocProjectID + `","kind":"orchestrator","harness":"claude-code","displayName":"s","prompt":"go","mode":"trusted"`
	if provider != "" {
		body += `,"provider":"` + provider + `"`
	}
	body += `}`
	req := httptest.NewRequest(http.MethodPost, "/api/cloud/v1/orgs/"+offeringTeamOrgID+"/sessions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "33333333-3333-3333-3333-333333333333")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgId", offeringTeamOrgID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, principalKey, principal)
	return req.WithContext(ctx)
}

// newGenericOfferingServer offers coder (the deployment default) and freestyle,
// both capability-gated, as staging and production do.
func newGenericOfferingServer(store Store) *Server {
	return New(Options{
		Store:                     store,
		SandboxProvider:           sandbox.ProviderCoder,
		AvailableSandboxProviders: []string{sandbox.ProviderCoder, sandbox.ProviderFreestyle},
		CapabilityGatedProviders:  []string{sandbox.ProviderCoder, sandbox.ProviderFreestyle},
		DefaultOrgCapabilities:    []string{sandbox.ProviderFreestyle},
		Provisioning:              genericProvisioning(),
		Logger:                    slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

// genericProvisioning provisions Coder and Freestyle sessions.
func genericProvisioning() sandbox.ProvisioningDefaults {
	provisioning := bothProviderProvisioning(sandbox.ProviderCoder)
	provisioning.Freestyle = sandbox.FreestyleConfig{
		BaseURL: "https://api.freestyle.test", APIKey: "test-key",
		DefaultSnapshot: "sc-default", WorkerTokenTTL: 15 * time.Minute,
	}
	return provisioning
}

// A session created without a provider gets the organization's default:
// Freestyle for an organization without the coder capability, Coder (unbilled)
// for one with it.
func TestCreateSessionWithoutProviderUsesOrgDefault(t *testing.T) {
	for _, tc := range []struct {
		name           string
		caps           []string
		wantProvider   string
		wantCapability bool
	}{
		{"no coder capability", nil, sandbox.ProviderFreestyle, false},
		{"coder capability", []string{"coder"}, sandbox.ProviderCoder, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := &offeringStore{}
			rec := httptest.NewRecorder()
			newGenericOfferingServer(store).createSession(rec, offeringSessionRequest(t, teamPrincipal(tc.caps...), ""))
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			if store.captured.Provider != tc.wantProvider || store.captured.CoderCapability != tc.wantCapability {
				t.Fatalf("provider %q coder capability %v, want %q %v",
					store.captured.Provider, store.captured.CoderCapability, tc.wantProvider, tc.wantCapability)
			}
		})
	}
}

// Any organization may pick Coder; its sessions are billed unless the
// organization holds the coder capability.
func TestCreateSessionOnCoderWithoutCapability(t *testing.T) {
	store := &offeringStore{}
	rec := httptest.NewRecorder()
	newGenericOfferingServer(store).createSession(rec, offeringSessionRequest(t, teamPrincipal(), sandbox.ProviderCoder))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if store.captured.Provider != sandbox.ProviderCoder || store.captured.CoderCapability {
		t.Fatalf("captured = %+v, want a billed coder session", store.captured)
	}
}

// A provider the deployment does not offer is still refused.
func TestCreateSessionRejectsProviderTheDeploymentDoesNotOffer(t *testing.T) {
	store := &offeringStore{}
	rec := httptest.NewRecorder()
	newOfferingServer(store).createSession(rec, offeringSessionRequest(t, teamPrincipal("coder"), sandbox.ProviderFreestyle))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "provider_unavailable") {
		t.Fatalf("status = %d, body = %s; want 422 provider_unavailable", rec.Code, rec.Body.String())
	}
	if store.created {
		t.Fatal("no session may be created on a provider the deployment does not offer")
	}
}

// A project saved for a provider the deployment no longer offers is refused.
func TestCreateSessionRejectsProjectProviderNoLongerOffered(t *testing.T) {
	store := &offeringStore{projectConfig: json.RawMessage(`{"sandboxProvider":"freestyle"}`)}
	rec := httptest.NewRecorder()
	newOfferingServer(store).createSession(rec, offeringSessionRequest(t, teamPrincipal("nodeops"), ""))
	if rec.Code < 400 || !strings.Contains(rec.Body.String(), "provider_") {
		t.Fatalf("status = %d, body = %s; want a provider refusal", rec.Code, rec.Body.String())
	}
	if store.created {
		t.Fatal("no session may be created on a provider the deployment no longer offers")
	}
}

func TestCreateProjectRejectsDisallowedSandboxProvider(t *testing.T) {
	s := newOfferingServer(&offeringStore{})
	body := `{"displayName":"p","repositoryUrl":"https://github.com/acme/app","defaultBranch":"main","config":{"sandboxProvider":"freestyle"}}`
	req := httptest.NewRequest(http.MethodPost, "/api/cloud/v1/orgs/"+offeringTeamOrgID+"/projects", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "44444444-4444-4444-4444-444444444444")
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("orgId", offeringTeamOrgID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, principalKey, teamPrincipal("nodeops"))
	rec := httptest.NewRecorder()
	s.createProject(rec, req.WithContext(ctx))
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "provider_unavailable") {
		t.Fatalf("status = %d, body = %s; want 422 provider_unavailable", rec.Code, rec.Body.String())
	}
}

func scratchPlanRequest(principal domain.Principal) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	return req.WithContext(context.WithValue(req.Context(), principalKey, principal))
}

// A scratch orchestrator runs on the organization's default provider and, for a
// bring-your-own Coder organization, binds to its connection.
func TestScratchSessionPlanUsesOrgDefaultAndBYOConnection(t *testing.T) {
	store := &stubCoderBYOCStore{}
	s := newBYOCServer(store)
	principal := domain.Principal{UserID: byocUserID}
	rec := httptest.NewRecorder()
	plan, connectionID, ok := s.scratchSessionPlan(
		rec, scratchPlanRequest(principal), principal, byocOrgID, "claude-code", json.RawMessage(`{"source":"scratch"}`),
	)
	if !ok {
		t.Fatalf("scratch plan failed: %d %s", rec.Code, rec.Body.String())
	}
	if plan.Provider != sandbox.ProviderCoder || connectionID != byocConnID {
		t.Fatalf("plan provider %q connection %q, want coder on %q", plan.Provider, connectionID, byocConnID)
	}
	profile, err := sandbox.DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.BaseURL != byocBaseURL {
		t.Fatalf("scratch plan base URL = %q, want the org's own Coder", profile.BaseURL)
	}
}

// A scratch project cannot pick a template, so when the organization's own
// Coder connection has no default template its orchestrator runs on the
// deployment's Coder instead of failing coder_template_required.
func TestScratchSessionPlanFallsBackWhenBYOHasNoTemplate(t *testing.T) {
	store := &stubCoderBYOCStore{omitOrgTemplate: true}
	s := newBYOCServer(store)
	principal := domain.Principal{UserID: byocUserID}
	rec := httptest.NewRecorder()
	plan, connectionID, ok := s.scratchSessionPlan(
		rec, scratchPlanRequest(principal), principal, byocOrgID, "claude-code", json.RawMessage(`{"source":"scratch"}`),
	)
	if !ok {
		t.Fatalf("scratch plan failed: %d %s", rec.Code, rec.Body.String())
	}
	if plan.Provider != sandbox.ProviderCoder || connectionID != "" {
		t.Fatalf("plan provider %q connection %q, want the deployment's coder", plan.Provider, connectionID)
	}
	profile, err := sandbox.DecodeCoderSessionProfile(plan.ResourceProfile)
	if err != nil {
		t.Fatalf("decode profile: %v", err)
	}
	if profile.BaseURL == byocBaseURL {
		t.Fatal("scratch plan still targets the org's own Coder")
	}
}

// A scratch orchestrator runs on the organization's chosen default.
func TestScratchSessionPlanFollowsOrgDefaultForB2C(t *testing.T) {
	s := newOfferingServer(&chosenProviderStore{offeringStore: offeringStore{}, chosen: sandbox.ProviderNodeOps})
	principal := teamPrincipal("nodeops")
	rec := httptest.NewRecorder()
	plan, connectionID, ok := s.scratchSessionPlan(
		rec, scratchPlanRequest(principal), principal, offeringTeamOrgID, "claude-code", json.RawMessage(`{"source":"scratch"}`),
	)
	if !ok || plan.Provider != sandbox.ProviderNodeOps || connectionID != "" {
		t.Fatalf("ok %v provider %q connection %q (%s), want nodeops", ok, plan.Provider, connectionID, rec.Body.String())
	}
}

func TestScratchSessionPlanRejects(t *testing.T) {
	s := newOfferingServer(&offeringStore{})
	cases := []struct {
		name   string
		caps   []string
		config string
		status int
		code   string
	}{
		{"config names a provider the deployment does not offer", []string{"nodeops"}, `{"sandboxProvider":"freestyle"}`, http.StatusUnprocessableEntity, "provider_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			principal := teamPrincipal(tc.caps...)
			rec := httptest.NewRecorder()
			if _, _, ok := s.scratchSessionPlan(
				rec, scratchPlanRequest(principal), principal, offeringTeamOrgID, "claude-code", json.RawMessage(tc.config),
			); ok {
				t.Fatal("scratch plan succeeded, want a rejection")
			}
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) {
				t.Fatalf("status = %d, body = %s; want %d %s", rec.Code, rec.Body.String(), tc.status, tc.code)
			}
		})
	}
}

// chosenProviderStore is an offeringStore whose organization chose a default
// provider in settings.
type chosenProviderStore struct {
	offeringStore
	chosen string
}

func (s *chosenProviderStore) OrgDefaultSandboxProvider(context.Context, string) (string, error) {
	return s.chosen, nil
}

func (s *chosenProviderStore) SetOrgDefaultSandboxProvider(context.Context, domain.Principal, string, string) error {
	return nil
}
