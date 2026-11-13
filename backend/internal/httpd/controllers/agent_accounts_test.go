package controllers

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/agentaccounts"
)

// The app words a failed check from its kind; the error text rides along as
// detail. The identity check's problem comes first, and a limits problem is
// shown only for a signed-in account.
func TestAgentAccountResponseCarriesTheProblemKind(t *testing.T) {
	account := domain.AgentAccount{ID: "acct", Provider: domain.AgentAccountProviderClaude, Home: "/tmp/acct"}
	for _, tc := range []struct {
		name    string
		view    agentaccounts.AccountView
		problem string
		kind    domain.AgentAccountProblemKind
	}{
		{"slow limits", agentaccounts.AccountView{Account: account, SignedIn: true, LimitsErr: "context deadline exceeded", LimitsProblem: domain.AgentAccountProblemSlow},
			"context deadline exceeded", domain.AgentAccountProblemSlow},
		{"identity first", agentaccounts.AccountView{Account: account, SignedIn: true, IdentityErr: "the folder is now signed in as c@example.com, not b@example.com", IdentityProblem: domain.AgentAccountProblemOtherLogin, LimitsErr: "x", LimitsProblem: domain.AgentAccountProblemUnavailable},
			"the folder is now signed in as c@example.com, not b@example.com", domain.AgentAccountProblemOtherLogin},
		{"signed out hides limits", agentaccounts.AccountView{Account: account, LimitsErr: "signed out"}, "", ""},
	} {
		got := agentAccountResponse(tc.view)
		if got.Problem != tc.problem || got.ProblemKind != tc.kind {
			t.Errorf("%s: problem %q kind %q, want %q %q", tc.name, got.Problem, got.ProblemKind, tc.problem, tc.kind)
		}
	}
}
