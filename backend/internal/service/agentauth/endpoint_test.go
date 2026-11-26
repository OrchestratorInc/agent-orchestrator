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
		{name: "unsupported harness", svc: svc, agentID: "aider", in: harnessendpoint.Input{BaseURL: "https://a.example.com", APIKey: "k"}, kind: apierr.KindInvalid, code: "AGENT_ENDPOINT_UNSUPPORTED"},
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
// x-api-key header, and the style that worked is saved.
func TestSetEndpointDetectsTheAuthStyleTheEndpointAccepts(t *testing.T) {
	t.Parallel()
	var probes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probes.Add(1)
		if r.URL.Path == "/v1/models" && r.Header.Get("x-api-key") == "sk-header" {
			w.WriteHeader(http.StatusOK)
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
	if status.AuthStyle != harnessendpoint.AuthAPIKey || probes.Load() != 2 {
		t.Fatalf("status = %+v after %d probes, want api-key after trying Bearer first", status, probes.Load())
	}

	// Changing only the model keeps the style that works, without asking again.
	if status, err = svc.SetEndpoint(context.Background(), "claude-code", harnessendpoint.Input{BaseURL: server.URL, Model: "gateway-model"}); err != nil {
		t.Fatalf("SetEndpoint model only: %v", err)
	}
	if status.AuthStyle != harnessendpoint.AuthAPIKey || probes.Load() != 2 {
		t.Fatalf("status = %+v after %d probes, want the saved style kept without a probe", status, probes.Load())
	}
}

// A key the endpoint refuses in every style is not saved.
func TestSetEndpointRejectsAKeyTheEndpointRefuses(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
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
