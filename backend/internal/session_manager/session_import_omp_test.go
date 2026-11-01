package sessionmanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/omp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type ompHomeAgents struct{}

func (ompHomeAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return omp.New(), true }

func TestImportedOMPConfigEnvKeepsDefaultAndSelectsCustomHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", "")
	m := &Manager{agents: ompHomeAgents{}}
	rec := domain.SessionRecord{Harness: domain.HarnessOMP}
	rec.Metadata.ProviderConversationID = "native"
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "native", ConfigDir: filepath.Join(home, ".omp", "agent")}
	env := map[string]string{"HOME": home}
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if value, set := env["PI_CODING_AGENT_DIR"]; set {
		t.Fatalf("default OMP home forced an override: %q", value)
	}
	custom := t.TempDir()
	rec.Metadata.ImportSource.ConfigDir = custom
	env = map[string]string{"HOME": home}
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if env["PI_CODING_AGENT_DIR"] != custom {
		t.Fatalf("PI_CODING_AGENT_DIR = %q, want %q", env["PI_CODING_AGENT_DIR"], custom)
	}
}
