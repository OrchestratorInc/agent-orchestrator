package agent

import (
	"context"
	"os"
	"path/filepath"
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

// A saved endpoint every launch rejects must not read as ready through the
// native login: readiness reports the failed check instead.
func TestAuthenticationOverrideFailsOnUnusableEndpoint(t *testing.T) {
	svc := newService(nil, nil, nil)
	svc.dataDir = t.TempDir()
	path := filepath.Join(svc.dataDir, "harness-endpoints", "claude-code", "endpoint.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}

	observation, handled := svc.authenticationOverride(context.Background(), "claude-code", domain.AgentReadinessPurposeLaunch)
	if !handled || observation.State == domain.AgentAuthenticationConfigured || observation.State == domain.AgentAuthenticationAuthorized ||
		observation.ReasonCode != domain.AgentReadinessReasonAuthCheckFailed || observation.Reason != harnessendpoint.UnusableMessage {
		t.Fatalf("observation = %+v, handled = %v; want a failed check naming the unusable endpoint", observation, handled)
	}
}
