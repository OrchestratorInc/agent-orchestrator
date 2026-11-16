package opencode

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// opencode's own stored credentials decide first, then `opencode auth list`,
// and a global key in the environment only answers when neither has one.
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
	listOutput := "2 credentials\n"
	previous := authprobe.CmdRunner
	t.Cleanup(func() { authprobe.CmdRunner = previous })
	authprobe.CmdRunner = func(context.Context, string, ...string) ([]byte, error) {
		return []byte(listOutput), nil
	}
	plugin := &Plugin{resolvedBinary: "opencode"}

	ctx, evidence := ports.WithAuthEvidence(context.Background())
	if status, err := plugin.AuthStatus(ctx); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("status = (%q, %v)", status, err)
	}
	got := evidence.Identity()
	if got.SourceKind != domain.AgentCredentialSourceFile || got.Source != authprobe.FileSource(authPath).Source || got.Provider != "zai-coding-plan" {
		t.Fatalf("evidence = %+v, want the stored auth.json", got)
	}

	if err := os.Remove(authPath); err != nil {
		t.Fatal(err)
	}
	ctx, evidence = ports.WithAuthEvidence(context.Background())
	if status, err := plugin.AuthStatus(ctx); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("auth list status = (%q, %v)", status, err)
	}
	if got := evidence.Identity(); got.SourceKind != domain.AgentCredentialSourceCommand || got.Source != "opencode auth list" {
		t.Fatalf("auth list evidence = %+v", got)
	}

	listOutput = "0 credentials\n"
	ctx, evidence = ports.WithAuthEvidence(context.Background())
	if status, err := plugin.AuthStatus(ctx); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("env fallback status = (%q, %v)", status, err)
	}
	if got := evidence.Identity(); got.SourceKind != domain.AgentCredentialSourceEnvironment || got.Source != "OPENAI_API_KEY" {
		t.Fatalf("env fallback evidence = %+v", got)
	}
}
