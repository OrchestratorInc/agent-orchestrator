package claudeacp

import (
	"context"
	"os"
	"path/filepath"
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

func TestSharedAdapterFollowsInheritedEnv(t *testing.T) {
	rt := runtimeLaunch{command: "node", server: "acp-server.mjs"}
	overlay := map[string]string{"AO_SESSION_ID": "s-1"}
	t.Setenv("MAX_THINKING_TOKENS", "1000")
	t.Setenv("AO_TEST_INHERITED", "old")
	server, session := sharedAdapterEnvs(overlay, "/workspace")
	first, err := sharedAdapterSocket("/data", rt, server)
	if err != nil {
		t.Fatal(err)
	}
	if server["MAX_THINKING_TOKENS"] != "1000" || server["AO_TEST_INHERITED"] != "" {
		t.Fatalf("adapter env = %v", server)
	}
	if session["AO_TEST_INHERITED"] != "old" || session["AO_SESSION_ID"] != "s-1" {
		t.Fatalf("session env lacks inherited or launch values")
	}

	// The daemon restarts with a changed setting and without one variable.
	t.Setenv("MAX_THINKING_TOKENS", "2000")
	if err := os.Unsetenv("AO_TEST_INHERITED"); err != nil {
		t.Fatal(err)
	}
	server, session = sharedAdapterEnvs(overlay, "/workspace")
	second, err := sharedAdapterSocket("/data", rt, server)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("a changed inherited adapter setting reused the old adapter process")
	}
	if _, ok := session["AO_TEST_INHERITED"]; ok {
		t.Fatal("a variable removed from the daemon env still reaches the session")
	}
}

func TestSharedAdapterResolvesRelativeConfigDirPerWorkspace(t *testing.T) {
	rt := runtimeLaunch{command: "node", server: "acp-server.mjs"}
	overlay := map[string]string{"CLAUDE_CONFIG_DIR": ".claude-custom"}
	serverA, sessionA := sharedAdapterEnvs(overlay, "/work/a")
	serverB, _ := sharedAdapterEnvs(overlay, "/work/b")
	want := filepath.Join("/work/a", ".claude-custom")
	if serverA["CLAUDE_CONFIG_DIR"] != want || sessionA["CLAUDE_CONFIG_DIR"] != want {
		t.Fatalf("config dir = %q / %q", serverA["CLAUDE_CONFIG_DIR"], sessionA["CLAUDE_CONFIG_DIR"])
	}
	socketA, _ := sharedAdapterSocket("/data", rt, serverA)
	socketB, _ := sharedAdapterSocket("/data", rt, serverB)
	if socketA == socketB {
		t.Fatal("two workspaces with different config dirs share one adapter")
	}
}
