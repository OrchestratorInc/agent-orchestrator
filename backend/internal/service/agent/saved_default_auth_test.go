package agent

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// When new Codex sessions start on a saved account, Codex is ready exactly when
// that account is signed in, whatever the computer's own login says. When they
// start on the computer's login, the existing check decides.
func TestCodexReadinessFollowsTheSavedDefaultAccount(t *testing.T) {
	svc := newService(nil, nil, nil, nil)
	for _, tc := range []struct {
		name              string
		signedIn, handled bool
		want              domain.AgentAuthenticationState
		decided           bool
	}{
		{"saved default signed in", true, true, domain.AgentAuthenticationAuthorized, true},
		{"saved default signed out", false, true, domain.AgentAuthenticationUnauthorized, true},
		{"default is this computer", false, false, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc.SetSavedDefaultSignIn(func(context.Context, domain.AgentHarness) (bool, bool) { return tc.signedIn, tc.handled })
			got, decided := svc.savedDefaultCodexAuthentication(context.Background(), string(domain.HarnessCodex))
			if decided != tc.decided || (decided && (got.State != tc.want || got.Freshness != domain.AgentReadinessFresh)) {
				t.Fatalf("readiness = %+v decided %v, want %s decided %v", got, decided, tc.want, tc.decided)
			}
		})
	}
	if _, decided := svc.savedDefaultCodexAuthentication(context.Background(), string(domain.HarnessClaudeCode)); decided {
		t.Fatal("the hook decided readiness for a harness other than Codex")
	}
}
