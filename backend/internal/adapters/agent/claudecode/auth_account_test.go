package claudecode

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestClaudeAuthReportAccount(t *testing.T) {
	out := []byte(`{"loggedIn":true,"authMethod":"claude.ai","apiProvider":"firstParty","email":"dev@example.com","orgId":"org-1","orgName":"Acme","subscriptionType":"pro"}`)
	report, ok := claudeAuthReportFromOutput(out)
	if !ok {
		t.Fatal("report did not parse")
	}
	want := domain.AgentAccountIdentity{Email: "dev@example.com", Organization: "Acme", Plan: "pro", Method: domain.AgentAccountMethodAccount}
	if got := report.account(); got != want {
		t.Fatalf("account = %+v, want %+v", got, want)
	}
	signedOut, _ := claudeAuthReportFromOutput([]byte(`{"loggedIn":false,"email":"dev@example.com"}`))
	if got := signedOut.account(); got != (domain.AgentAccountIdentity{}) {
		t.Fatalf("signed-out account = %+v, want none", got)
	}
	apiKey, _ := claudeAuthReportFromOutput([]byte(`{"loggedIn":true,"authMethod":"api_key"}`))
	if got := apiKey.account(); got.Method != domain.AgentAccountMethodAPIKey {
		t.Fatalf("api key account = %+v", got)
	}
}
