package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestReadinessPreservesFailedNativeEvidenceAndPreviousLogin(t *testing.T) {
	a := &readinessTestAgent{auth: func(ctx context.Context) (ports.AgentAuthStatus, error) {
		step := domain.NewAgentAuthDiagnostic("credential_store", "credential_store_unreadable")
		step.Source = "~/.pi/agent/auth.json"
		ports.RecordAuthDiagnostic(ctx, step)
		return ports.AgentAuthStatusUnknown, errors.New("secret-token from failed read")
	}}
	c := newReadinessCoordinator(readinessCoordinatorConfig{})
	observation, failed := c.checkAuthentication(readinessHarness("pi", "Pi", a), domain.AgentReadinessPurposeDisplay)
	if !failed || observation.State != domain.AgentAuthenticationUnknown || len(observation.Diagnostics) != 1 {
		t.Fatalf("observation=%+v failed=%v", observation, failed)
	}
	step := observation.Diagnostics[0]
	if step.Code != "credential_store_unreadable" || step.Source != "~/.pi/agent/auth.json" || step.NextStep == "" {
		t.Fatalf("lost evidence: %+v", step)
	}
	payload, _ := json.Marshal(observation)
	if strings.Contains(string(payload), "secret-token") {
		t.Fatal("raw error escaped into diagnostics")
	}
	checked := time.Now().Add(-time.Hour)
	previous := domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized, CheckedAt: &checked}
	preserveAuthenticationFailure(&previous, observation)
	if previous.State != domain.AgentAuthenticationAuthorized || previous.CheckedAt != &checked || previous.Freshness != domain.AgentReadinessStale || len(previous.Diagnostics) != 1 {
		t.Fatalf("previous=%+v", previous)
	}
}

func TestReadinessCollectsAccountFallbackEvidence(t *testing.T) {
	a := &readinessTestAgent{auth: func(ctx context.Context) (ports.AgentAuthStatus, error) {
		ports.RecordAuthDiagnostic(ctx, domain.NewAgentAuthDiagnostic("codex login status", "cli_signed_in"))
		ports.RecordAuthDiagnostic(ctx, domain.NewAgentAuthDiagnostic("provider_validation", "provider_rejected"))
		return ports.AgentAuthStatusUnauthorized, nil
	}}
	c := newReadinessCoordinator(readinessCoordinatorConfig{AuthenticationCheck: func(ctx context.Context, _ string, _ domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
		ports.RecordAuthDiagnostic(ctx, domain.NewAgentAuthDiagnostic("codex_account", "account_store_unavailable"))
		return domain.AgentAuthenticationObservation{}, false
	}})
	observation, failed := c.checkAuthentication(readinessHarness("codex", "Codex", a), domain.AgentReadinessPurposeDisplay)
	if failed || observation.State != domain.AgentAuthenticationUnauthorized {
		t.Fatalf("verdict changed: %+v", observation)
	}
	codes := map[string]bool{}
	for _, step := range observation.Diagnostics {
		codes[step.Code] = true
	}
	for _, code := range []string{"account_store_unavailable", "cli_signed_in", "provider_rejected", "native_login_rejected"} {
		if !codes[code] {
			t.Fatalf("missing %s in %+v", code, observation.Diagnostics)
		}
	}
}

func TestReadinessInconclusiveCheckHasActionableFallback(t *testing.T) {
	a := &readinessTestAgent{auth: func(context.Context) (ports.AgentAuthStatus, error) { return ports.AgentAuthStatusUnknown, nil }}
	c := newReadinessCoordinator(readinessCoordinatorConfig{})
	observation, failed := c.checkAuthentication(readinessHarness("test", "Test", a), domain.AgentReadinessPurposeDisplay)
	if !failed || observation.State != domain.AgentAuthenticationUnknown || len(observation.Diagnostics) != 1 || observation.Diagnostics[0].Code != "check_inconclusive" || observation.Diagnostics[0].NextStep == "" {
		t.Fatalf("observation=%+v", observation)
	}
}

func TestCodexDisplayExplainsPendingLocalAccountReconciliation(t *testing.T) {
	fixture := newCodexLaunchReadinessFixture(t)
	fixture.manager.reconciliation = domain.CodexDeviceReconciliation{Status: domain.CodexDeviceReconciliationChecking}
	fixture.manager.deviceAccountID = ""
	observation, handled := fixture.service.codexAuthenticationWithAccount(context.Background(), "codex", domain.AgentReadinessPurposeDisplay)
	if !handled || observation.State == domain.AgentAuthenticationUnauthorized || len(observation.Diagnostics) != 1 || observation.Diagnostics[0].Code != "account_sync_pending" {
		t.Fatalf("observation=%+v handled=%v", observation, handled)
	}
}
