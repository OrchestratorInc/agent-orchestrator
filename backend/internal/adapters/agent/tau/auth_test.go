package tau

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestProviderCredentialsAreNotAuthorization(t *testing.T) {
	for _, tt := range []struct {
		credential string
		want       ports.AgentAuthStatus
	}{
		{"stored:openai", ports.AgentAuthStatusConfigured},
		{"env:OPENAI_API_KEY", ports.AgentAuthStatusConfigured},
		{"missing", ports.AgentAuthStatusUnknown},
	} {
		p := fixturePlugin()
		p.run = func(context.Context, string, map[string]string, ...string) ([]byte, error) {
			return []byte("*\topenai\topenai\tgpt-test\tgpt-test,gpt-other\tOPENAI_API_KEY\t" + tt.credential + "\thttps://example.invalid\t60s\tretries=2\tretry_delay=1s\n"), nil
		}
		got, err := p.AuthStatus(context.Background())
		if err != nil || got != tt.want {
			t.Fatalf("credential %q: %v, %v", tt.credential, got, err)
		}
	}
	if got := ParseProviders([]byte("malformed\n")); len(got) != 0 {
		t.Fatal("malformed rows became providers")
	}
}
