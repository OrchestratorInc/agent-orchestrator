package agent

import (
	"context"
	"path/filepath"
	"testing"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// #8603: on Windows the account-file sync was blocked (account_storage_unsafe)
// on every start, so display readiness stayed "Login status unavailable" while
// Codex's own login was valid. Only a manual check, which uses launch
// readiness and its native fallback, showed the login.
func newBlockedSyncCodexService(t *testing.T) (*Service, *readinessTestAgent) {
	t.Helper()
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	manager := newCodexAccountManager(ctx, filepath.Join(root, "accounts"), filepath.Join(root, "pending"),
		filepath.Join(root, "staging"), filepath.Join(root, "global"), &fakeCodexAccountFactory{capabilities: supportedCodexAccountCapabilities()}, nil)
	manager.accountStoreReady = true
	manager.reconciliation = domain.CodexDeviceReconciliation{Status: domain.CodexDeviceReconciliationBlocked, ReasonCode: "account_storage_unsafe"}
	native := &readinessTestAgent{
		resolve: func(context.Context) (string, error) { return "codex.exe", nil },
		auth:    func(context.Context) (ports.AgentAuthStatus, error) { return ports.AgentAuthStatusAuthorized, nil },
	}
	item := readinessHarness(string(domain.HarnessCodex), "Codex", native)
	svc := &Service{codexAccounts: manager}
	svc.readiness = newReadinessCoordinator(readinessCoordinatorConfig{
		Context:             ctx,
		Agents:              []agentregistry.HarnessAgent{item},
		Factory:             func() []agentregistry.HarnessAgent { return []agentregistry.HarnessAgent{item} },
		AuthenticationCheck: svc.authenticationWithAccount,
	})
	return svc, native
}

func ensureCodexDisplayAuthentication(t *testing.T, svc *Service) domain.AgentAuthenticationObservation {
	t.Helper()
	items, err := svc.readiness.Ensure(context.Background(), []string{string(domain.HarnessCodex)}, domain.AgentReadinessPurposeDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("readiness = %#v, want one Codex snapshot", items)
	}
	return items[0].Authentication
}

func TestCodexDisplayReadinessUsesNativeLoginWhenWindowsSyncIsBlocked(t *testing.T) {
	previous := codexDisplayUsesNativeLoginWhenSyncBlocked
	codexDisplayUsesNativeLoginWhenSyncBlocked = true
	t.Cleanup(func() { codexDisplayUsesNativeLoginWhenSyncBlocked = previous })
	svc, native := newBlockedSyncCodexService(t)

	auth := ensureCodexDisplayAuthentication(t, svc)
	if auth.State != domain.AgentAuthenticationAuthorized {
		t.Fatalf("display authentication = %#v, want the native login to be authorized", auth)
	}
	if native.authCalls.Load() != 1 {
		t.Fatalf("native login checks = %d, want 1", native.authCalls.Load())
	}
}

func TestCodexDisplayReadinessKeepsPendingSyncOffWindows(t *testing.T) {
	previous := codexDisplayUsesNativeLoginWhenSyncBlocked
	codexDisplayUsesNativeLoginWhenSyncBlocked = false
	t.Cleanup(func() { codexDisplayUsesNativeLoginWhenSyncBlocked = previous })
	svc, native := newBlockedSyncCodexService(t)

	auth := ensureCodexDisplayAuthentication(t, svc)
	if auth.State != domain.AgentAuthenticationUnknown || !hasAuthDiagnostic(auth, "account_sync_pending") {
		t.Fatalf("display authentication = %#v, want the existing account_sync_pending verdict", auth)
	}
	if native.authCalls.Load() != 0 {
		t.Fatalf("native login checks = %d, want 0", native.authCalls.Load())
	}
}

func hasAuthDiagnostic(auth domain.AgentAuthenticationObservation, code string) bool {
	for _, diagnostic := range auth.Diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
