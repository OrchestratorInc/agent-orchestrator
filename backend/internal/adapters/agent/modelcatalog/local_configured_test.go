package modelcatalog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLocalConfiguredUsesOnlyNativeSelectionsWithoutExecutingBinary(t *testing.T) {
	home := isolateHome(t)
	env := providerTestEnv(t)
	env["HOME"] = home
	piDir := t.TempDir()
	env["PI_CODING_AGENT_DIR"] = piDir
	writeConfig(t, filepath.Join(home, ".pi", "agent", "settings.json"), `{"defaultProvider":"wrong-home","defaultModel":"not-selected"}`)
	writeConfig(t, filepath.Join(piDir, "settings.json"), `{"defaultProvider":"custom","defaultModel":"selected"}`)
	writeConfig(t, filepath.Join(piDir, "models-store.json"), `{"public-provider":{"models":{"public-model":{}}}}`)
	result := LocalConfigured(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "pi", Binary: "/never/execute/provider", Env: env})
	if len(result.Models) != 1 || result.Models[0].ID != "custom/selected" || !result.Models[0].IsDefault {
		t.Fatalf("native configured models = %#v", result.Models)
	}
	result = LocalConfigured(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "muse", Binary: "/never/execute/provider", Env: env})
	if len(result.Models) != 0 {
		t.Fatalf("public Muse catalog was offered without configuration: %#v", result.Models)
	}
}

func TestLocalConfiguredReadsDeclaredModelListsAndNativeDefault(t *testing.T) {
	home := isolateHome(t)
	env := providerTestEnv(t)
	env["HOME"] = home
	env["CONTINUE_GLOBAL_DIR"] = filepath.Join(home, ".continue")
	writeConfig(t, filepath.Join(home, ".continue", "config.yaml"), "models:\n  - name: Local choice\n    provider: openai\n    model: selected-model\ndefaults:\n  chat: Local choice\n")
	result := LocalConfigured(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "continue", Env: env})
	if len(result.Models) != 1 || result.Models[0].ID != "selected-model" || !result.Models[0].IsDefault {
		t.Fatalf("declared configured models = %#v", result.Models)
	}
	env["AMP_API_KEY"] = "fixture-private-key"
	result = LocalConfigured(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "amp", Env: env})
	if len(result.Models) != 1 || result.Models[0].ID != "" || !result.Models[0].IsDefault {
		t.Fatalf("native configured default = %#v", result.Models)
	}
	env["FX_MODEL"], env["FX_PROVIDER"] = "selected-native-model", "selected-provider"
	result = LocalConfigured(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "fx", Env: env})
	if len(result.Models) != 1 || result.Models[0].ID != "fx:selected-provider:selected-native-model" || !result.Models[0].IsDefault {
		t.Fatalf("explicit FX selection = %#v", result.Models)
	}
	env["DEEPSEEK_API_KEY"] = "fixture-private-key"
	result = LocalConfigured(context.Background(), ports.AgentModelDiscoveryRequest{AgentID: "deepseek-harness", Env: env})
	if len(result.Models) != 1 || result.Models[0].ID != "" || !result.Models[0].IsDefault {
		t.Fatalf("DeepSeek native default = %#v", result.Models)
	}
}
