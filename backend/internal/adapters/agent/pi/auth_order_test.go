package pi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestPiStoredCredentialWinsOverEnvironmentKey(t *testing.T) {
	configDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", configDir)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-from-zshrc")
	path := filepath.Join(configDir, "auth.json")
	if err := os.WriteFile(path, []byte(`{"zai-glm":{"type":"api_key","key":"zai-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, ok, err := piLocalAuthStatus(ctx)
	if err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v, %v)", status, ok, err)
	}
	if got := evidence.Identity(); got.SourceKind != domain.AgentCredentialSourceFile || got.Provider != "zai-glm" {
		t.Fatalf("evidence = %+v, want the stored auth.json", got)
	}
}
