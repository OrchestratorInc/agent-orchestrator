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
		// No stored Copilot or GitHub CLI login.
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
	t.Run("GitHub CLI token from the environment", func(t *testing.T) {
		// gh auth token prints GH_TOKEN when it is set, whoever hosts.yml names.
		clearCopilotAuthProbeEnv(t)
		t.Setenv("COPILOT_HOME", t.TempDir())
		ghConfig := t.TempDir()
		t.Setenv("GH_CONFIG_DIR", ghConfig)
		if err := os.WriteFile(filepath.Join(ghConfig, "hosts.yml"), []byte("github.com:\n    user: stored-user\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		previous := authprobe.CmdRunner
		t.Cleanup(func() { authprobe.CmdRunner = previous })
		authprobe.CmdRunner = func(context.Context, string, ...string) ([]byte, error) {
			return []byte("gho_from_env\n"), nil
		}
		t.Setenv("GH_TOKEN", "gho_from_env")
		ctx, evidence := ports.WithAuthEvidence(context.Background())
		if status, ok, err := copilotLocalAuthStatus(ctx); err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
			t.Fatalf("status = (%q, %v, %v)", status, ok, err)
		}
		want := domain.EnvironmentSource("GH_TOKEN").WithMethod(domain.AgentAccountMethodToken)
		if got := evidence.Identity(); got != want {
			t.Fatalf("evidence = %+v, want %+v", got, want)
		}
	})
	// Copilot CLI checks COPILOT_GITHUB_TOKEN, GH_TOKEN, GITHUB_TOKEN, its stored
	// login, then gh auth token; a variable silently overrides a stored login.
	t.Run("Copilot's own token variable precedes GH_TOKEN", func(t *testing.T) {
		clearCopilotAuthProbeEnv(t)
		t.Setenv("COPILOT_HOME", t.TempDir())
		t.Setenv("COPILOT_GITHUB_TOKEN", "github_pat_copilot")
		t.Setenv("GH_TOKEN", "gho_gh")
		ctx, evidence := ports.WithAuthEvidence(context.Background())
		if status, ok, err := copilotLocalAuthStatus(ctx); err != nil || !ok || status != ports.AgentAuthStatusAuthorized {
			t.Fatalf("status = (%q, %v, %v)", status, ok, err)
		}
		want := domain.EnvironmentSource("COPILOT_GITHUB_TOKEN").WithMethod(domain.AgentAccountMethodToken)
		if got := evidence.Identity(); got != want {
			t.Fatalf("evidence = %+v, want %+v", got, want)
		}
	})
	t.Run("token variable overrides a stored login", func(t *testing.T) {
		clearCopilotAuthProbeEnv(t)
		dir := t.TempDir()
		t.Setenv("COPILOT_HOME", dir)
		if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"authToken":"oauth-token"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("GH_TOKEN", "gho_from_env")
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
		want := authprobe.FileSource(path).WithMethod(domain.AgentAccountMethodAccount)
		if got := evidence.Identity(); got != want {
			t.Fatalf("evidence = %+v, want %+v", got, want)
		}
	})
}

func TestGitHubCLIUserReadsTheActiveHostLogin(t *testing.T) {
	for name, hosts := range map[string]string{
		"four-space indent": "github.com:\n    git_protocol: https\n    users:\n        octocat:\n    user: octocat\n",
		"two-space indent":  "github.com:\n  users:\n    other:\n  user: octocat\n",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("GH_CONFIG_DIR", dir)
			if err := os.WriteFile(filepath.Join(dir, "hosts.yml"), []byte(hosts), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := gitHubCLIUser(context.Background()); got != "octocat" {
				t.Fatalf("gitHubCLIUser = %q, want octocat", got)
			}
		})
	}
}
