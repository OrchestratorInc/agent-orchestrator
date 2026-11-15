package claudecode

import (
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/pkg/agentcreds"
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

func TestClaudeCredentialEvidenceNamesSourceNeverValue(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	for _, tt := range []struct {
		name       string
		credential agentcreds.Credential
		want       domain.AgentAccountIdentity
	}{
		{"oauth token env", agentcreds.Credential{Kind: agentcreds.KindOAuthToken, Source: "CLAUDE_CODE_OAUTH_TOKEN", Secret: "sk-ant-oat-secret"},
			domain.AgentAccountIdentity{Method: domain.AgentAccountMethodToken, SourceKind: domain.AgentCredentialSourceEnvironment, Source: "CLAUDE_CODE_OAUTH_TOKEN"}},
		{"api key env", agentcreds.Credential{Kind: agentcreds.KindAPIKey, Source: "ANTHROPIC_API_KEY", Secret: "sk-ant-api-secret"},
			domain.AgentAccountIdentity{Method: domain.AgentAccountMethodAPIKey, SourceKind: domain.AgentCredentialSourceEnvironment, Source: "ANTHROPIC_API_KEY"}},
		{"keychain login", agentcreds.Credential{Kind: agentcreds.KindOAuthToken, Source: "keychain", Secret: "secret"},
			domain.AgentAccountIdentity{Method: domain.AgentAccountMethodAccount, SourceKind: domain.AgentCredentialSourceKeychain}},
		{"credentials file login", agentcreds.Credential{Kind: agentcreds.KindOAuthToken, Source: "credentials-file", Secret: "secret"},
			domain.AgentAccountIdentity{Method: domain.AgentAccountMethodAccount, SourceKind: domain.AgentCredentialSourceFile, Source: filepath.Join("~", ".claude", ".credentials.json")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := credentialEvidence(tt.credential); got != tt.want {
				t.Fatalf("evidence = %+v, want %+v", got, tt.want)
			}
		})
	}
}
