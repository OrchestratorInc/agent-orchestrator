package codex

import (
	"context"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The reviewer builds its endpoint flags from the endpoint env its process
// receives, for both a fresh launch and a restore.
func TestReviewCommandsPassEndpointEnvToTheAgent(t *testing.T) {
	endpointEnv := map[string]string{"AO_CODEX_ENDPOINT_BASE_URL": "https://project.example.com"}
	agent := &captureAgent{}
	r := &Reviewer{agent: agent}
	inv := ports.ReviewInvocation{
		ReviewerID:     "review-w1",
		AgentSessionID: "native-1",
		WorkspacePath:  "/ws/w1",
		DataDir:        t.TempDir(),
		EndpointEnv:    endpointEnv,
		Prompt:         "review it",
	}
	if _, err := r.ReviewCommand(context.Background(), inv); err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if agent.got.Env["AO_CODEX_ENDPOINT_BASE_URL"] != "https://project.example.com" || agent.got.DataDir != inv.DataDir {
		t.Fatalf("launch config Env=%v DataDir=%q", agent.got.Env, agent.got.DataDir)
	}
	if _, _, err := r.ReviewRestoreCommand(context.Background(), inv); err != nil {
		t.Fatalf("ReviewRestoreCommand: %v", err)
	}
	if agent.gotRestore.Env["AO_CODEX_ENDPOINT_BASE_URL"] != "https://project.example.com" {
		t.Fatalf("restore config Env=%v", agent.gotRestore.Env)
	}
}
