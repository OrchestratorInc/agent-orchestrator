package agent

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCodexStartupReadinessUsesNativeLoginAfterAccountSyncFails(t *testing.T) {
	for _, status := range []ports.AgentAuthStatus{ports.AgentAuthStatusAuthorized, ports.AgentAuthStatusUnauthorized, ports.AgentAuthStatusUnknown} {
		t.Run(string(status), func(t *testing.T) {
			t.Setenv("CODEX_HOME", t.TempDir())
			root := t.TempDir()
			// Reproduce account_storage_unsafe without changing any real ACLs or
			// credentials: AO cannot reconcile through a non-directory home.
			global := filepath.Join(root, "global")
			if err := os.WriteFile(global, []byte("not a directory"), 0o600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			factory := &fakeCodexAccountFactory{capabilities: supportedCodexAccountCapabilities()}
			svc := NewWithDeps(Deps{Context: ctx, CodexAccountRoot: filepath.Join(root, "accounts"), CodexPendingRoot: filepath.Join(root, "pending"), CodexSwitchStagingRoot: filepath.Join(root, "staging"), CodexGlobalHome: global, CodexAccounts: factory})
			native := &readinessTestAgent{
				resolve: func(context.Context) (string, error) { return "codex.exe", nil },
				auth:    func(context.Context) (ports.AgentAuthStatus, error) { return status, nil },
			}
			item := readinessHarness("codex", "Codex", native)
			svc.readiness = newReadinessCoordinator(readinessCoordinatorConfig{
				Context: ctx, Agents: []agentregistry.HarnessAgent{item},
				Factory:             func() []agentregistry.HarnessAgent { return []agentregistry.HarnessAgent{item} },
				AuthenticationCheck: svc.authenticationWithAccount,
			})
			<-svc.WarmReadiness()
			if native.authCalls.Load() != 0 {
				t.Fatal("pending reconciliation must not be treated as a native rejection")
			}
			svc.WarmCodexAccounts()
			deadline := time.Now().Add(2 * time.Second)
			for {
				cached, err := svc.CachedReadiness(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if native.authCalls.Load() > 0 && cached.Agents[0].Authentication.State == domain.AgentAuthenticationState(status) && cached.Agents[0].Authentication.ReasonCode != domain.AgentReadinessReasonNotChecked {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("startup never used native login: %#v", cached.Agents[0])
				}
				time.Sleep(time.Millisecond)
			}
			accounts, err := svc.CachedCodexAccounts(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if accounts.DeviceReconciliation.Status != domain.CodexDeviceReconciliationBlocked || accounts.DeviceReconciliation.ReasonCode != "account_storage_unsafe" || accounts.DeviceReconciliation.ActiveAccountVerified {
				t.Fatalf("native fallback must not bypass account-file safety: %#v", accounts.DeviceReconciliation)
			}
			before := native.authCalls.Load()
			for range 10 {
				_, _ = svc.CachedReadiness(ctx)
			}
			if native.authCalls.Load() != before {
				t.Fatal("cached reads repeated native work")
			}
		})
	}
}

func TestCodexStartupReadinessRecoversAfterPendingDeviceSync(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	root := t.TempDir()
	global := filepath.Join(root, "global")
	if err := ensurePrivateDirectory(global); err != nil {
		t.Fatal(err)
	}
	if err := writeGlobalCredentialAtomic(filepath.Join(global, codexCredentialFilename), testAPIKeyCredential("startup-test-key")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory := &fakeCodexAccountFactory{capabilities: supportedCodexAccountCapabilities(), open: func(ports.CodexAccountContext) (ports.CodexAccountClient, error) {
		return &fakeCodexAccountClient{read: ports.CodexAccountObservation{Authentication: domain.AgentAuthenticationAuthorized, Method: domain.CodexAuthMethodAPIKey}}, nil
	}}
	svc := NewWithDeps(Deps{Context: ctx, CodexAccountRoot: filepath.Join(root, "accounts"), CodexPendingRoot: filepath.Join(root, "pending"), CodexSwitchStagingRoot: filepath.Join(root, "staging"), CodexGlobalHome: global, CodexAccounts: factory})
	agent := &readinessTestAgent{resolve: func(context.Context) (string, error) { return "codex.exe", nil }}
	item := readinessHarness("codex", "Codex", agent)
	svc.readiness = newReadinessCoordinator(readinessCoordinatorConfig{
		Context: ctx, Agents: []agentregistry.HarnessAgent{item},
		Factory:             func() []agentregistry.HarnessAgent { return []agentregistry.HarnessAgent{item} },
		AuthenticationCheck: svc.authenticationWithAccount,
	})
	initial, err := svc.EnsureReadiness(ctx, []string{"codex"}, domain.AgentReadinessPurposeDisplay)
	if err != nil {
		t.Fatal(err)
	}
	if auth := initial.Agents[0].Authentication; auth.State != domain.AgentAuthenticationUnknown || len(auth.Diagnostics) == 0 || auth.Diagnostics[0].Code != "account_sync_pending" {
		t.Fatalf("initial authentication = %#v, want pending sync", auth)
	}
	if err := svc.EnsureCodexDeviceAccountReconciled(ctx); err != nil {
		t.Fatal(err)
	}
	// Read only the cached endpoint: neither Settings nor another ensure should
	// be needed to turn a completed startup reconciliation into ready state.
	waitReady := func() {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for {
			cached, err := svc.CachedReadiness(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if cached.Agents[0].Authentication.State == domain.AgentAuthenticationAuthorized && cached.Agents[0].Authentication.Freshness == domain.AgentReadinessFresh {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("completed reconciliation left cached readiness pending: %#v", cached.Agents[0])
			}
			time.Sleep(time.Millisecond)
		}
	}
	waitReady()
	// A later reconciliation of the same account also temporarily removes its
	// verified association. A check in that window must recover on completion.
	<-svc.codexAccounts.mutations
	released := false
	defer func() {
		if !released {
			svc.codexAccounts.mutations <- struct{}{}
		}
	}()
	settled := make(chan error, 1)
	go func() { settled <- svc.EnsureCodexDeviceAccountReconciled(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		svc.codexAccounts.mu.Lock()
		checking := svc.codexAccounts.reconciliation.Status == domain.CodexDeviceReconciliationChecking
		svc.codexAccounts.mu.Unlock()
		if checking {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("reconciliation did not start")
		}
		time.Sleep(time.Millisecond)
	}
	pending, err := svc.readiness.Force(ctx, []string{"codex"}, domain.AgentReadinessPurposeDisplay)
	if err != nil || pending[0].Authentication.State != domain.AgentAuthenticationUnknown {
		t.Fatalf("readiness while reconciling = %#v, %v", pending, err)
	}
	svc.codexAccounts.mutations <- struct{}{}
	released = true
	if err := <-settled; err != nil {
		t.Fatal(err)
	}
	waitReady()
	factory.mu.Lock()
	reads := factory.opens
	factory.mu.Unlock()
	if reads != 1 {
		t.Fatalf("opened %d account clients, want one coalesced authentication check", reads)
	}
}
