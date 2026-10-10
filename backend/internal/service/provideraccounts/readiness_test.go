package provideraccounts

import (
	"reflect"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAuthenticationReadinessUsesManagedAccountState(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		for _, purpose := range []domain.AgentReadinessPurpose{domain.AgentReadinessPurposeDisplay, domain.AgentReadinessPurposeLaunch} {
			t.Run(string(harness)+"/"+string(purpose), func(t *testing.T) {
				h := setupAccounts(t)
				assertAuth := func(want domain.AgentAuthenticationState) {
					t.Helper()
					got, handled := h.svc.AuthenticationReadiness(h.ctx, harness, purpose)
					if !handled || got.State != want || got.Freshness != domain.AgentReadinessFresh {
						t.Fatalf("handled=%t observation=%+v, want %s", handled, got, want)
					}
					if got.CheckedAt == nil || got.AttemptedAt == nil || got.Reason == "" {
						t.Fatalf("missing observation metadata: %+v", got)
					}
				}
				assertAuth(domain.AgentAuthenticationUnauthorized)
				otherProvider := "claude"
				if Provider(harness) == "claude" {
					otherProvider = "codex"
				}
				h.login(t, otherProvider, "other@example.test")
				assertAuth(domain.AgentAuthenticationUnauthorized)
				id := h.login(t, Provider(harness), "managed@example.test")
				assertAuth(domain.AgentAuthenticationAuthorized)
				if err := h.svc.Remove(h.ctx, id, "", true); err != nil {
					t.Fatal(err)
				}
				assertAuth(domain.AgentAuthenticationUnauthorized)
				h.login(t, Provider(harness), "managed-two@example.test")
				assertAuth(domain.AgentAuthenticationAuthorized)
			})
		}
	}
}

func TestAuthenticationReadinessUnknownOnStorageFailure(t *testing.T) {
	h := setupAccounts(t)
	h.login(t, "claude", "managed@example.test")
	h.store.fail = "load"
	got, handled := h.svc.AuthenticationReadiness(h.ctx, domain.HarnessClaudeCode, domain.AgentReadinessPurposeDisplay)
	if !handled || got.State != domain.AgentAuthenticationUnknown || got.ReasonCode != domain.AgentReadinessReasonAuthCheckFailed {
		t.Fatalf("load failure = handled=%t observation=%+v", handled, got)
	}
	if got.CheckedAt != nil || got.AttemptedAt == nil || got.Freshness != domain.AgentReadinessStale {
		t.Fatalf("failed check must not claim fresh validation: %+v", got)
	}
	// An unsupported provider must not read managed storage or inherit its error.
	if got, handled := h.svc.AuthenticationReadiness(h.ctx, domain.AgentHarness("cursor"), domain.AgentReadinessPurposeDisplay); handled || !reflect.DeepEqual(got, domain.AgentAuthenticationObservation{}) {
		t.Fatalf("unmanaged provider = handled=%t observation=%+v", handled, got)
	}
}

func TestAuthenticationReadinessRequiresCompleteCredentialIdentity(t *testing.T) {
	for _, tc := range []struct {
		name string
		ref  string
		auth string
	}{
		{name: "signed out"},
		{name: "missing auth identity", ref: "saved.json"},
		{name: "missing credential file", auth: "auth-id"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := setupAccounts(t)
			h.store.state.Accounts = []domain.ProviderAccount{{ID: "a", Provider: "codex", CredentialRef: tc.ref, AuthID: tc.auth}}
			got, handled := h.svc.AuthenticationReadiness(h.ctx, domain.HarnessCodex, domain.AgentReadinessPurposeLaunch)
			if !handled || got.State != domain.AgentAuthenticationUnauthorized {
				t.Fatalf("incomplete account accepted: %+v handled=%t", got, handled)
			}
		})
	}
}

func TestAuthenticationReadinessUsesCommittedStateDuringRecovery(t *testing.T) {
	h := setupAccounts(t)
	h.login(t, "codex", "current@example.test")
	next := clone(h.store.state)
	next.Revision++
	next.Accounts[0].CredentialRef = ""
	next.Accounts[0].AuthID = ""
	h.store.pending = &domain.ProviderAccountIntent{Next: next}
	before, _ := h.svc.AuthenticationReadiness(h.ctx, domain.HarnessCodex, domain.AgentReadinessPurposeDisplay)
	if before.State != domain.AgentAuthenticationAuthorized {
		t.Fatalf("uncommitted sign-out changed authentication: %+v", before)
	}
	if err := h.svc.Recover(h.ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := h.svc.AuthenticationReadiness(h.ctx, domain.HarnessCodex, domain.AgentReadinessPurposeDisplay)
	if after.State != domain.AgentAuthenticationUnauthorized {
		t.Fatalf("committed recovery did not change authentication: %+v", after)
	}
}

func TestManagedReadinessInvalidatedAfterLoginLogoutAndRecovery(t *testing.T) {
	for _, failure := range []string{"", "commit", "finish"} {
		t.Run("failure="+failure, func(t *testing.T) {
			h := setupAccounts(t)
			notified := map[string]int{}
			h.svc.SetReadinessInvalidator(func(provider string) { notified[provider]++ })
			id := h.login(t, "codex", "one@example.test")
			if notified["codex"] != 1 {
				t.Fatalf("login did not invalidate readiness: %v", notified)
			}
			h.store.fail = failure
			err := h.svc.Remove(h.ctx, id, "", true)
			if (err != nil) != (failure != "") {
				t.Fatalf("sign-out error=%v, injected=%q", err, failure)
			}
			if notified["codex"] != 2 {
				t.Fatalf("sign-out did not invalidate readiness: %v", notified)
			}
			if failure != "" {
				h.store.fail = ""
				if err := h.svc.Recover(h.ctx); err != nil {
					t.Fatal(err)
				}
				if notified["codex"] != 3 {
					t.Fatalf("background recovery left stale readiness: %v", notified)
				}
			}
			got, _ := h.svc.AuthenticationReadiness(h.ctx, domain.HarnessCodex, domain.AgentReadinessPurposeLaunch)
			if got.State != domain.AgentAuthenticationUnauthorized {
				t.Fatalf("signed-out account remains ready: %+v", got)
			}
		})
	}
}

func TestNativeAdoptionUpdatesManagedReadiness(t *testing.T) {
	h := setupAccounts(t)
	notifications := 0
	h.svc.SetReadinessInvalidator(func(provider string) {
		if provider == "claude" {
			notifications++
		}
	})
	h.svc.SetNativeAccountSource(&nativeSourceFake{
		inputs: map[string]ports.NativeProviderCredential{"claude": {Fingerprint: "native"}},
		verified: map[string]ports.VerifiedProviderLogin{"claude": {
			Provider: "claude", Email: "native@example.test", Kind: "oauth", CredentialRef: "native.json", AuthID: "native-auth",
		}},
	})
	before, _ := h.svc.AuthenticationReadiness(h.ctx, domain.HarnessClaudeCode, domain.AgentReadinessPurposeDisplay)
	if before.State != domain.AgentAuthenticationUnauthorized {
		t.Fatalf("unexpected initial state: %+v", before)
	}
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 {
		t.Fatalf("native adoption did not invalidate readiness: %d", notifications)
	}
	after, _ := h.svc.AuthenticationReadiness(h.ctx, domain.HarnessClaudeCode, domain.AgentReadinessPurposeDisplay)
	if after.State != domain.AgentAuthenticationAuthorized {
		t.Fatalf("native import not reflected centrally: %+v", after)
	}
	if err := h.svc.RefreshNativeAccounts(h.ctx); err != nil {
		t.Fatal(err)
	}
	if notifications != 1 {
		t.Fatalf("unchanged native login needlessly invalidated readiness: %d", notifications)
	}
}
