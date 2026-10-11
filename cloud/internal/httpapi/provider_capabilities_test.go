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

func TestOrgAllowsProvider(t *testing.T) {
	s := gatedServer()
	cases := []struct {
		name         string
		provider     string
		capabilities []string
		want         bool
	}{
		{"ungated provider without caps", "nodeops", nil, true},
		{"ungated provider ignores caps", "nodeops", []string{"coder"}, true},
		{"gated coder with capability", "coder", []string{"coder"}, true},
		{"gated coder with capability among others", "coder", []string{"feature-x", "coder"}, true},
		{"gated coder without any caps", "coder", nil, false},
		{"gated coder with unrelated cap only", "coder", []string{"feature-x"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := s.orgAllowsProvider(teamPrincipal(tc.capabilities...), offeringTeamOrgID, tc.provider)
			if got != tc.want {
				t.Fatalf("orgAllowsProvider(caps=%v, %q) = %v, want %v", tc.capabilities, tc.provider, got, tc.want)
			}
		})
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
		{"b2b org", []string{"coder"}, []string{"coder"}, "coder"},
		{"b2c org falls back to its first allowed provider", []string{"freestyle"}, []string{"freestyle"}, "freestyle"},
		{"both keeps the deployment default", []string{"freestyle", "coder"}, []string{"coder", "freestyle"}, "coder"},
		{"entitled to nothing", []string{"feature-x"}, []string{}, ""},
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
			if got := s.defaultProviderForOrg(principal, offeringTeamOrgID); got != tc.wantDefault {
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

func TestMeOfferingKinds(t *testing.T) {
	cases := []struct {
		name        string
		caps        []string
		wantKind    string
		wantDefault string
		wantList    []string
	}{
		{"b2b", []string{"coder"}, domain.OfferingKindB2B, "coder", []string{"coder"}},
		{"b2c", []string{"nodeops"}, domain.OfferingKindB2C, "nodeops", []string{"nodeops"}},
		{"both", []string{"coder", "nodeops"}, domain.OfferingKindBoth, "coder", []string{"coder", "nodeops"}},
		{"none", []string{"feature-x"}, domain.OfferingKindNone, "", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &offeringStore{memberships: []domain.Membership{{OrgID: offeringTeamOrgID, Role: "member"}}}
			body := getMe(t, newOfferingServer(store), teamPrincipal(tc.caps...))
			if body.Offering.Kind != tc.wantKind || body.Offering.DefaultProvider != tc.wantDefault ||
				!slices.Equal(body.Offering.Providers, tc.wantList) {
				t.Fatalf("offering = %+v, want kind %q default %q providers %v",
					body.Offering, tc.wantKind, tc.wantDefault, tc.wantList)
			}
			if body.Offering.BillingMode != domain.BillingModeOff || body.Offering.Features == nil {
				t.Fatalf("offering = %+v, want billingMode off and an empty features array", body.Offering)
			}
			if !slices.Equal(body.SandboxProviders.Available, tc.wantList) || body.SandboxProviders.Default != tc.wantDefault {
				t.Fatalf("sandboxProviders = %+v, want the offering's providers and default", body.SandboxProviders)
			}
			if len(body.Organizations) != 1 || body.Organizations[0].Offering == nil ||
				body.Organizations[0].Offering.Kind != tc.wantKind {
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
	if body.Organizations[0].Offering.Kind != domain.OfferingKindB2C || body.Organizations[0].Offering.DefaultProvider != "nodeops" {
		t.Fatalf("personal org offering = %+v, want the b2c defaults", body.Organizations[0].Offering)
	}
	if body.ActiveOrganizationID != offeringTeamOrgID || body.Offering.Kind != domain.OfferingKindB2B {
		t.Fatalf("active org = %q offering = %+v, want the team org's b2b offering", body.ActiveOrganizationID, body.Offering)
	}
	if body.Organizations[1].Offering.Kind != domain.OfferingKindB2B {
		t.Fatalf("team org offering = %+v, want b2b", body.Organizations[1].Offering)
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
		{"self-serve", "", "free", false, domain.BillingModeSelfServe},
		{"contract metadata", domain.OrgBillingContract, "free", false, domain.BillingModeContract},
		{"internal plan", "", domain.PlanInternal, false, domain.BillingModeContract},
		{"grandfathered", "", "free", true, domain.BillingModeContract},
		{"coder only has nothing to buy", "", "free", false, domain.BillingModeOff},
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

// A session created without a provider gets the organization's default, which
// for a b2c org is not the deployment default.
func TestCreateSessionWithoutProviderUsesOrgDefault(t *testing.T) {
	store := &offeringStore{}
	rec := httptest.NewRecorder()
	newOfferingServer(store).createSession(rec, offeringSessionRequest(t, teamPrincipal("nodeops"), ""))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if store.captured.Provider != sandbox.ProviderNodeOps {
		t.Fatalf("provider = %q, want the org default nodeops", store.captured.Provider)
	}
}

// With no provider sent, the gate still applies: an organization entitled to no
// provider cannot fall through to the deployment default.
func TestCreateSessionWithoutProviderGatedWhenOrgHasNone(t *testing.T) {
	store := &offeringStore{}
	rec := httptest.NewRecorder()
	newOfferingServer(store).createSession(rec, offeringSessionRequest(t, teamPrincipal("feature-x"), ""))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "provider_forbidden") {
		t.Fatalf("status = %d, body = %s; want 403 provider_forbidden", rec.Code, rec.Body.String())
	}
	if store.created {
		t.Fatal("no session may be created for an organization without providers")
	}
}

// A project saved for a provider the organization has since lost is refused with
// a message that names the project's provider.
func TestCreateSessionRejectsProjectProviderNoLongerAllowed(t *testing.T) {
	store := &offeringStore{projectConfig: json.RawMessage(`{"sandboxProvider":"coder"}`)}
	rec := httptest.NewRecorder()
	newOfferingServer(store).createSession(rec, offeringSessionRequest(t, teamPrincipal("nodeops"), "nodeops"))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "This project is set to run on the coder") {
		t.Fatalf("status = %d, body = %s; want 403 naming the project's provider", rec.Code, rec.Body.String())
	}
	if store.created {
		t.Fatal("no session may be created on a provider the org lost")
	}
}

func TestCreateProjectRejectsDisallowedSandboxProvider(t *testing.T) {
	s := newOfferingServer(&offeringStore{})
	body := `{"displayName":"p","repositoryUrl":"https://github.com/acme/app","defaultBranch":"main","config":{"sandboxProvider":"coder"}}`
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

func TestScratchSessionPlanFollowsOrgDefaultForB2C(t *testing.T) {
	s := newOfferingServer(&offeringStore{})
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
		{"org with no provider", []string{"feature-x"}, `{"source":"scratch"}`, http.StatusForbidden, "provider_forbidden"},
		{"config names a disallowed provider", []string{"nodeops"}, `{"sandboxProvider":"coder"}`, http.StatusUnprocessableEntity, "provider_unavailable"},
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
