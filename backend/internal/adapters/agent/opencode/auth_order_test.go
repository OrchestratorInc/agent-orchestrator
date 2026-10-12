package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// opencode's own stored credentials decide before a global key in the
// environment; the environment only answers when they hold none.
func TestOpencodeStoredCredentialWinsOverEnvironmentKey(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv("OPENCODE_DATA_DIR", dataDir)
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("OPENCODE_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "sk-from-zshrc")
	authPath := filepath.Join(dataDir, "auth.json")
	if err := os.WriteFile(authPath, []byte(`{"zai-coding-plan":{"type":"api","key":"zai-key"}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, evidence := ports.WithAuthEvidence(context.Background())
	status, ok, err := opencodeLocalAuthStatus(ctx)
	if err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v, %v)", status, ok, err)
	}
	got := evidence.Identity()
	if got.SourceKind != domain.AgentCredentialSourceFile || got.Source != domain.FileSource(authPath).Source || got.Provider != "zai-coding-plan" {
		t.Fatalf("evidence = %+v, want the stored auth.json", got)
	}

	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	ctx, evidence = ports.WithAuthEvidence(context.Background())
	if status, ok, err := opencodeLocalAuthStatus(ctx); err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("env fallback status = (%q, %v, %v)", status, ok, err)
	}
	if got := evidence.Identity(); got.SourceKind != domain.AgentCredentialSourceEnvironment || got.Source != "OPENAI_API_KEY" {
		t.Fatalf("env fallback evidence = %+v", got)
	}
}
