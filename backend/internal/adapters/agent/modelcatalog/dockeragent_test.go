package modelcatalog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/dockeragent"
)

// Docker Agent's catalog follows the provider keys AO's default config can use
// or the user's selected config, so either changing must refresh it.
func TestDockerAgentDiscoveryFingerprintFollowsKeysAndConfig(t *testing.T) {
	for _, key := range []string{"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "GOOGLE_API_KEY", "GEMINI_API_KEY", dockeragent.ConfigEnv} {
		t.Setenv(key, "")
	}
	openai := dockerAgentDiscoveryFingerprint(map[string]string{"OPENAI_API_KEY": "secret-openai"})
	anthropic := dockerAgentDiscoveryFingerprint(map[string]string{"ANTHROPIC_API_KEY": "secret-anthropic"})
	if openai == anthropic {
		t.Fatal("provider key change did not change the fingerprint")
	}
	if strings.Contains(openai+anthropic, "secret") {
		t.Fatal("fingerprint must not contain key material")
	}
	config := filepath.Join(t.TempDir(), "agent.yaml")
	if err := os.WriteFile(config, []byte("models: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	first := dockerAgentDiscoveryFingerprint(map[string]string{dockeragent.ConfigEnv: config})
	if err := os.WriteFile(config, []byte("models: {a: {provider: openai, model: x}}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if first == dockerAgentDiscoveryFingerprint(map[string]string{dockeragent.ConfigEnv: config}) {
		t.Fatal("config edit did not change the fingerprint")
	}
}
