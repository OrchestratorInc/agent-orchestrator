package claudeacp

import (
	"context"
	"strings"
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSharedAdapterRefusesSocketPathOverLimit(t *testing.T) {
	cfg := acpdriver.LaunchConfig{SessionID: "long-1", DataDir: "/" + strings.Repeat("d", maxSocketPath)}
	_, ok, err := sharedAdapterLaunch(context.Background(), cfg, runtimeLaunch{command: "node", server: "acp-server.mjs"}, nil)
	if ok || err == nil {
		t.Fatalf("ok=%v err=%v, want a fallback with an error", ok, err)
	}
}

func TestSessionMetaCarriesSharedChatEnv(t *testing.T) {
	id := domain.SessionID("shared-env-1")
	sessionEnv.Store(id, map[string]string{"AO_SESSION_ID": "shared-env-1"})
	t.Cleanup(func() { sessionEnv.Delete(id) })
	meta := claudeSessionMeta(acpdriver.LaunchConfig{SessionID: id, Env: map[string]string{claudeAutoCompactWindowEnv: "200000"}})
	options, _ := meta["claudeCode"].(map[string]any)["options"].(map[string]any)
	env, _ := options["env"].(map[string]string)
	if env["AO_SESSION_ID"] != "shared-env-1" || env[claudeAutoCompactWindowEnv] != "200000" {
		t.Fatalf("session env = %v", env)
	}
	if stored := sharedSessionEnv(id); len(stored) != 1 {
		t.Fatalf("meta changed the stored env: %v", stored)
	}
}
