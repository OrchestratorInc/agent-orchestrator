package agent

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/harnessendpoint"
)

func TestAuthenticationOverrideReportsCustomEndpointConfigured(t *testing.T) {
	svc := newService(nil, nil, nil)
	svc.dataDir = t.TempDir()

	before, _ := svc.authenticationOverride(context.Background(), "claude-code", domain.AgentReadinessPurposeLaunch)
	if before.State == domain.AgentAuthenticationConfigured && before.ReasonCode == domain.AgentReadinessReasonAuthConfigured {
		t.Fatalf("no endpoint saved yet, got %+v", before)
	}

	if _, err := harnessendpoint.ForDataDir(svc.dataDir).Save("claude-code", harnessendpoint.Input{BaseURL: "https://gateway.example.com", APIKey: "sk-test"}, time.Now()); err != nil {
		t.Fatalf("Save: %v", err)
	}
	observation, handled := svc.authenticationOverride(context.Background(), "claude-code", domain.AgentReadinessPurposeLaunch)
	if !handled || observation.State != domain.AgentAuthenticationConfigured || observation.ReasonCode != domain.AgentReadinessReasonAuthConfigured {
		t.Fatalf("observation = %+v, handled = %v; want configured custom endpoint", observation, handled)
	}
}
