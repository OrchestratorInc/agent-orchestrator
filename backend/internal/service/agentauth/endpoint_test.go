package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

type invalidatingResolver struct {
	managedExecutableResolver
	invalidated []string
}

func (r *invalidatingResolver) InvalidateAgentAuthentication(agentID string) {
	r.invalidated = append(r.invalidated, agentID)
}

func newEndpointService(t *testing.T) (*Service, *invalidatingResolver) {
	t.Helper()
	resolver := &invalidatingResolver{managedExecutableResolver: managedExecutableResolver{agentID: "claude-code", path: "/test/bin/claude"}}
	svc := NewWithAgentResolver(nil, resolver, &recordingTerminalOpener{}, t.TempDir())
	// Tests never reach a real endpoint: an unreachable one keeps the default style.
	svc.endpointProbeClient = &http.Client{Transport: offlineTransport{}}
	return svc, resolver
}

type offlineTransport struct{}

func (offlineTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("offline")
}

func claudePlan(t *testing.T, svc *Service) Plan {
	t.Helper()
	plan, err := svc.Plan(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("Plan(claude-code): %v", err)
	}
	return plan
}

func TestEndpointSetAndClear(t *testing.T) {
	t.Parallel()
	svc, resolver := newEndpointService(t)

	plan := claudePlan(t, svc)
	if plan.CustomEndpoint == nil || plan.CustomEndpoint.Configured {
		t.Fatalf("initial CustomEndpoint = %+v, want supported and unconfigured", plan.CustomEndpoint)
	}

	status, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-secret-value"})
	if err != nil {
		t.Fatalf("SetEndpoint: %v", err)
	}
	if !status.Configured || !status.HasAPIKey || status.BaseURL != "https://gateway.example.com" {
		t.Fatalf("status = %+v", status)
	}

	plan = claudePlan(t, svc)
	if plan.CustomEndpoint == nil || !plan.CustomEndpoint.Configured {
		t.Fatalf("plan after set = %+v", plan.CustomEndpoint)
	}
	encoded, err := json.Marshal(svc.Plans(context.Background()))
	if err != nil {
		t.Fatalf("marshal plans: %v", err)
	}
	if strings.Contains(string(encoded), "sk-secret-value") {
		t.Fatal("plans must never expose the stored API key")
	}

	status, err = svc.ClearEndpoint(context.Background(), "claude-code")
	if err != nil {
		t.Fatalf("ClearEndpoint: %v", err)
	}
	if status.Configured || status.HasAPIKey {
		t.Fatalf("status after clear = %+v", status)
	}
	if got := strings.Join(resolver.invalidated, ","); got != "claude-code,claude-code" {
		t.Fatalf("invalidated = %q, want both writes to refresh readiness", got)
	}
}

func TestEndpointErrors(t *testing.T) {
	t.Parallel()
	svc, resolver := newEndpointService(t)
	withoutStore := NewWithAgentResolver(nil, resolver, &recordingTerminalOpener{}, "")

	cases := []struct {
		name    string
		svc     *Service
		agentID string
		in      harnessendpoint.Input
		kind    apierr.Kind
		code    string
	}{
		{name: "unknown target", svc: svc, agentID: "not-a-harness", in: harnessendpoint.Input{BaseURL: "https://a.example.com", APIKey: "k"}, kind: apierr.KindInvalid, code: "AGENT_AUTH_TARGET_UNKNOWN"},
		{name: "unsupported harness", svc: svc, agentID: "codex", in: harnessendpoint.Input{BaseURL: "https://a.example.com", APIKey: "k"}, kind: apierr.KindInvalid, code: "AGENT_ENDPOINT_UNSUPPORTED"},
		{name: "invalid url", svc: svc, agentID: "claude-code", in: harnessendpoint.Input{BaseURL: "not a url", APIKey: "k"}, kind: apierr.KindInvalid, code: "AGENT_ENDPOINT_INVALID"},
		{name: "missing key", svc: svc, agentID: "claude-code", in: harnessendpoint.Input{BaseURL: "https://a.example.com"}, kind: apierr.KindInvalid, code: "AGENT_ENDPOINT_INVALID"},
		{name: "no store", svc: withoutStore, agentID: "claude-code", in: harnessendpoint.Input{BaseURL: "https://a.example.com", APIKey: "k"}, kind: apierr.KindInternal, code: "AGENT_ENDPOINT_UNAVAILABLE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.svc.SetEndpoint(context.Background(), tc.agentID, tc.in)
			var apiErr *apierr.Error
			if !errors.As(err, &apiErr) || apiErr.Kind != tc.kind || apiErr.Code != tc.code {
				t.Fatalf("SetEndpoint error = %#v, want %v %s", err, tc.kind, tc.code)
			}
		})
	}
	if len(resolver.invalidated) != 0 {
		t.Fatalf("failed writes must not invalidate readiness: %v", resolver.invalidated)
	}
	if plan := claudePlan(t, withoutStore); plan.CustomEndpoint != nil {
		t.Fatalf("plans without a data directory must omit endpoint status: %+v", plan.CustomEndpoint)
	}
}

// An endpoint launches reject stays on the card as a problem the user can
// edit or remove, not as "Not set"; saving a new key repairs it.
func TestEndpointStatusReportsUnusableEndpoint(t *testing.T) {
	t.Parallel()
	svc, _ := newEndpointService(t)
	path := filepath.Join(svc.dataDir, "harness-endpoints", "claude-code", "endpoint.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	status := claudePlan(t, svc).CustomEndpoint
	if status == nil || status.Error != harnessendpoint.UnusableMessage || status.HasAPIKey {
		t.Fatalf("CustomEndpoint = %+v, want the unusable endpoint reported without a saved key", status)
	}

	if _, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-new"}); err != nil {
		t.Fatalf("SetEndpoint with a new key: %v", err)
	}
	if status := claudePlan(t, svc).CustomEndpoint; status == nil || status.Error != "" || !status.Configured {
		t.Fatalf("CustomEndpoint after repair = %+v, want a configured endpoint without an error", status)
	}
}

// The key is sent the way the endpoint accepts: Bearer first, then the
// x-api-key header, and the style that worked is saved. The model is not asked
// on the form: the first model the endpoint lists becomes the default, and a
// model the user picks later is kept when the key changes.
func TestSetEndpointDetectsTheAuthStyleTheEndpointAccepts(t *testing.T) {
	t.Parallel()
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		if r.URL.Path == "/v1/models" && strings.HasPrefix(r.Header.Get("x-api-key"), "sk-header") {
			_, _ = w.Write([]byte(`{"data":[{"id":"gateway-large"},{"id":"gateway-small"}]}`))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	svc, _ := newEndpointService(t)
	svc.endpointProbeClient = server.Client()

	status, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, APIKey: "sk-header"})
	if err != nil {
		t.Fatalf("SetEndpoint: %v", err)
	}
	if status.AuthStyle != harnessendpoint.AuthAPIKey || status.Model != "gateway-large" || probes.Load() != 2 {
		t.Fatalf("status = %+v after %d probes, want api-key after trying Bearer first, and the first listed model", status, probes.Load())
	}

	// Picking another default keeps the style that works, without asking again.
	if status, err = svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, Model: "gateway-small"}); err != nil {
		t.Fatalf("SetEndpoint model only: %v", err)
	}
	if status.AuthStyle != harnessendpoint.AuthAPIKey || status.Model != "gateway-small" || probes.Load() != 2 {
		t.Fatalf("status = %+v after %d probes, want the saved style kept without a probe", status, probes.Load())
	}

	// A new key is checked again, and the chosen model stays.
	if status, err = svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, APIKey: "sk-header-2"}); err != nil {
		t.Fatalf("SetEndpoint new key: %v", err)
	}
	if status.AuthStyle != harnessendpoint.AuthAPIKey || status.Model != "gateway-small" || probes.Load() != 4 {
		t.Fatalf("status = %+v after %d probes, want the new key checked and the chosen model kept", status, probes.Load())
	}
}

// An endpoint that does not list models leaves the harness on its own default.
func TestSetEndpointWithoutAModelListingSavesNoModel(t *testing.T) {
	t.Parallel()
	svc, _ := newEndpointService(t)
	status, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-test"})
	if err != nil {
		t.Fatalf("SetEndpoint: %v", err)
	}
	if !status.Configured || status.Model != "" || status.AuthStyle != harnessendpoint.AuthBearer {
		t.Fatalf("status = %+v, want a configured endpoint with the default style and no model", status)
	}
}

// A key the endpoint refuses in every style is not saved.
func TestSetEndpointRejectsAKeyTheEndpointRefuses(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	t.Cleanup(server.Close)
	svc, resolver := newEndpointService(t)
	svc.endpointProbeClient = server.Client()

	_, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, APIKey: "sk-wrong"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_ENDPOINT_KEY_REJECTED" || strings.Contains(apiErr.Error(), "sk-wrong") {
		t.Fatalf("SetEndpoint error = %#v, want AGENT_ENDPOINT_KEY_REJECTED without the key", err)
	}
	if status := claudePlan(t, svc).CustomEndpoint; status == nil || status.Configured {
		t.Fatalf("CustomEndpoint = %+v, want nothing saved", status)
	}
	if len(resolver.invalidated) != 0 {
		t.Fatalf("a refused key must not invalidate readiness: %v", resolver.invalidated)
	}
}

// Moving a saved endpoint to another host without entering the key again is
// refused before any request, so the saved key never reaches the new host.
func TestSetEndpointNeverSendsTheSavedKeyToANewHost(t *testing.T) {
	t.Parallel()
	saved := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"m"}]}`))
	}))
	t.Cleanup(saved.Close)
	var reached atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(other.Close)
	svc, _ := newEndpointService(t)
	svc.endpointProbeClient = saved.Client()
	if _, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: saved.URL, APIKey: "sk-saved"}); err != nil {
		t.Fatalf("SetEndpoint: %v", err)
	}

	_, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: other.URL})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_ENDPOINT_INVALID" || reached.Load() != 0 {
		t.Fatalf("SetEndpoint to another host = %v after %d requests there, want AGENT_ENDPOINT_INVALID and none", err, reached.Load())
	}
	if status := claudePlan(t, svc).CustomEndpoint; status == nil || status.BaseURL != saved.URL {
		t.Fatalf("CustomEndpoint = %+v, want the saved endpoint unchanged", status)
	}
}

// A key may run requests without being allowed to list models. An endpoint
// that keeps its listing from the key (403) still saves, with no model picked,
// and a client that names the auth style is never blocked by the listing.
func TestSetEndpointSavesAKeyWithoutModelListAccess(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(server.Close)
	svc, _ := newEndpointService(t)
	svc.endpointProbeClient = server.Client()

	status, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, APIKey: "sk-inference-only"})
	if err != nil || !status.Configured || status.AuthStyle != harnessendpoint.AuthBearer || status.Model != "" {
		t.Fatalf("detected save = %+v, %v; want it saved with the default style and no model", status, err)
	}
	status, err = svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, APIKey: "sk-inference-only", AuthStyle: harnessendpoint.AuthAPIKey})
	if err != nil || status.AuthStyle != harnessendpoint.AuthAPIKey || status.Model != "" {
		t.Fatalf("explicit style save = %+v, %v; want it saved without asking the listing to agree", status, err)
	}
}

// An endpoint without a model listing (404) still takes a default model the
// user types from the provider's docs. It is saved as given, and later saves
// that keep the base URL, with or without a new key, keep it.
func TestSetEndpointKeepsATypedModelWhenTheEndpointListsNone(t *testing.T) {
	t.Parallel()
	var listings atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			listings.Add(1)
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	svc, _ := newEndpointService(t)
	svc.endpointProbeClient = server.Client()

	status, err := svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, APIKey: "sk-test"})
	if err != nil || !status.Configured || status.Model != "" {
		t.Fatalf("first save = %+v, %v; want it saved with no model", status, err)
	}

	if status, err = svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, Model: "provider-model"}); err != nil {
		t.Fatalf("SetEndpoint typed model: %v", err)
	}
	if !status.Configured || status.Model != "provider-model" || status.AuthStyle != harnessendpoint.AuthBearer {
		t.Fatalf("typed model save = %+v, want the typed model saved with the kept style", status)
	}
	if saved := claudePlan(t, svc).CustomEndpoint; saved == nil || saved.Model != "provider-model" {
		t.Fatalf("CustomEndpoint = %+v, want the typed model reported", saved)
	}

	before := listings.Load()
	if status, err = svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, APIKey: "sk-test-2"}); err != nil {
		t.Fatalf("SetEndpoint new key: %v", err)
	}
	if status.Model != "provider-model" {
		t.Fatalf("new key save = %+v, want the typed model kept", status)
	}
	if listings.Load() == before {
		t.Fatalf("a new key must be checked against the endpoint again")
	}
}
