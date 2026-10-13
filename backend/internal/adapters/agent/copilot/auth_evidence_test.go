package copilot

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/authprobe"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestCopilotAuthRecordsItsEvidence(t *testing.T) {
	t.Run("token environment variable", func(t *testing.T) {
		// No stored Copilot or GitHub CLI login: the environment is the fallback.
		clearCopilotAuthProbeEnv(t)
		t.Setenv("COPILOT_HOME", t.TempDir())
		previous := authprobe.CmdRunner
		t.Cleanup(func() { authprobe.CmdRunner = previous })
		authprobe.CmdRunner = func(context.Context, string, ...string) ([]byte, error) {
			return nil, errors.New("gh: not logged in")
		}
		t.Setenv("GH_TOKEN", "gho_test")
		ctx, evidence := ports.WithAuthEvidence(context.Background())
		if status, ok, err := copilotLocalAuthStatus(ctx); err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
			t.Fatalf("status = (%q, %v, %v)", status, ok, err)
		}
		want := domain.EnvironmentSource("GH_TOKEN").WithMethod(domain.AgentAccountMethodToken)
		if got := evidence.Identity(); got != want {
			t.Fatalf("evidence = %+v, want %+v", got, want)
		}
	})
	t.Run("stored login", func(t *testing.T) {
		clearCopilotAuthProbeEnv(t)
		dir := t.TempDir()
		t.Setenv("COPILOT_HOME", dir)
		path := filepath.Join(dir, "config.json")
		if err := os.WriteFile(path, []byte(`{"authToken":"oauth-token"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		ctx, evidence := ports.WithAuthEvidence(context.Background())
		if status, ok, err := copilotLocalAuthStatus(ctx); err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
			t.Fatalf("status = (%q, %v, %v)", status, ok, err)
		}
		want := domain.FileSource(path).WithMethod(domain.AgentAccountMethodAccount)
		if got := evidence.Identity(); got != want {
			t.Fatalf("evidence = %+v, want %+v", got, want)
		}
	})
}
