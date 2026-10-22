package agent

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// When new Claude Code or Codex sessions start on a saved account, the harness
// is ready exactly when that account is signed in, whatever the computer's own
// login says, and readiness names that account. When they start on the
// computer's login, the existing check decides.
func TestReadinessFollowsTheSavedDefaultAccount(t *testing.T) {
	svc := newService(nil, nil, nil, nil)
	for _, harness := range []domain.AgentHarness{domain.HarnessClaudeCode, domain.HarnessCodex} {
		for _, tc := range []struct {
			name     string
			identity domain.AgentAccountIdentityCheck
			handled  bool
			want     domain.AgentAuthenticationState
		}{
			{"saved default signed in", domain.AgentAccountIdentityCheck{SignedIn: true, Email: "b@example.com", Plan: "team"}, true, domain.AgentAuthenticationAuthorized},
			{"saved default signed out", domain.AgentAccountIdentityCheck{Email: "b@example.com"}, true, domain.AgentAuthenticationUnauthorized},
			{"default is this computer", domain.AgentAccountIdentityCheck{}, false, ""},
		} {
			t.Run(string(harness)+"/"+tc.name, func(t *testing.T) {
				var asked domain.AgentHarness
				svc.SetSavedDefaultSignIn(func(_ context.Context, h domain.AgentHarness) (domain.AgentAccountIdentityCheck, bool) {
					asked = h
					return tc.identity, tc.handled
				})
				got, decided := svc.savedDefaultAuthentication(context.Background(), string(harness))
				if asked != harness || decided != tc.handled {
					t.Fatalf("asked %q decided %v, want %q decided %v", asked, decided, harness, tc.handled)
				}
				if !decided {
					return
				}
				if got.State != tc.want || got.Freshness != domain.AgentReadinessFresh {
					t.Fatalf("readiness = %+v, want %s", got, tc.want)
				}
				if len(got.Diagnostics) != 1 || got.Diagnostics[0].Code != "saved_default_account" {
					t.Fatalf("diagnostics = %+v, want the saved-default marker", got.Diagnostics)
				}
				if tc.identity.SignedIn && (got.Account == nil || got.Account.Email != "b@example.com" || got.Account.Plan != "team" || got.Account.Method != domain.AgentAccountMethodAccount) {
					t.Fatalf("account = %+v, want the saved account", got.Account)
				}
				if !tc.identity.SignedIn && got.Account != nil {
					t.Fatalf("a signed-out account was named: %+v", got.Account)
				}
			})
		}
	}
	svc.SetSavedDefaultSignIn(func(context.Context, domain.AgentHarness) (domain.AgentAccountIdentityCheck, bool) {
		t.Fatal("asked about a harness without accounts")
		return domain.AgentAccountIdentityCheck{}, false
	})
	if _, decided := svc.savedDefaultAuthentication(context.Background(), "aider"); decided {
		t.Fatal("the hook decided readiness for a harness without accounts")
	}
}
