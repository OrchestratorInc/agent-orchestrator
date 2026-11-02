package agent

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type runtimeCredentialAgent struct {
	fakeAgent
	dataDir string
}

func (a runtimeCredentialAgent) RuntimeCredentialEnv(dataDir string) map[string]string {
	if dataDir != a.dataDir {
		return nil
	}
	return map[string]string{"CLAUDE_CODE_OAUTH_TOKEN": "sk-ant-oat01-saved"}
}

// A setup token saved through AO's login menu is the only credential on a
// computer with no native Claude login, so local discovery must carry it or the
// catalog finds no credential and goes stale.
func TestModelDiscoveryRequestCarriesRuntimeCredential(t *testing.T) {
	dataDir := t.TempDir()
	svc := newService([]agentregistry.HarnessAgent{{
		Harness:  domain.AgentHarness("claude-code"),
		Manifest: adapters.Manifest{ID: "claude-code", Name: "Claude Code"},
		Agent:    runtimeCredentialAgent{dataDir: dataDir},
	}}, nil, nil)
	svc.dataDir = dataDir

	request, err := svc.modelDiscoveryRequest("claude-code", "", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if request.Env["CLAUDE_CODE_OAUTH_TOKEN"] != "sk-ant-oat01-saved" {
		t.Fatalf("discovery env = %v, want the saved setup token", request.Env)
	}

	// A cloud credential scope reflects the pushed credential, not this computer's.
	request, err = svc.modelDiscoveryRequest("claude-code", "@cred:anthropic_api_key", "/bin/claude")
	if err != nil {
		t.Fatal(err)
	}
	if len(request.Env) != 0 {
		t.Fatalf("credential-scope discovery env = %v, want none", request.Env)
	}
}
