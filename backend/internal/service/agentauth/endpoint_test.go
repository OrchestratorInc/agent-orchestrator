package agentauth

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
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
	return svc, resolver
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
