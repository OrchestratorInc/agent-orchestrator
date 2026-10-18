package review

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// The launcher must merge the reviewer's resolved agent-config env into the
// reviewer process env: that map is the transport for a per-role provider pin
// (issue #6156), and explicit env wins over settings files.
func TestLauncherSpawnMergesAgentConfigEnv(t *testing.T) {
	rt := &fakeRuntime{}
	l := NewLauncher(fakeReviewerResolver{reviewer: &fakeReviewer{}, ok: true}, rt, t.TempDir())
	spec := launchSpec()
	spec.AgentConfig = domain.AgentConfig{Env: map[string]string{
		"ANTHROPIC_BASE_URL":   "https://pinned.example",
		"ANTHROPIC_AUTH_TOKEN": "tok",
	}}

	if _, err := l.Spawn(context.Background(), spec); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if rt.createCfg.Env["ANTHROPIC_BASE_URL"] != "https://pinned.example" || rt.createCfg.Env["ANTHROPIC_AUTH_TOKEN"] != "tok" {
		t.Fatalf("agent config env did not reach the reviewer process: %v", rt.createCfg.Env)
	}
}
