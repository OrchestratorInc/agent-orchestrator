package sessionmanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/agent/droid"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

type droidImportAgents struct{}

func (droidImportAgents) Agent(domain.AgentHarness) (ports.Agent, bool) { return droid.New(), true }

func TestDroidImportedConfigKeepsExistingLoginAndTranslatesHomeOverride(t *testing.T) {
	t.Setenv("FACTORY_HOME_OVERRIDE", "")
	home := t.TempDir()
	m := &Manager{agents: droidImportAgents{}}
	config := filepath.Join(home, ".factory")
	rec := domain.SessionRecord{Harness: domain.HarnessDroid, Metadata: domain.SessionMetadata{ProviderConversationID: "copy", ImportSource: &domain.SessionImportSource{NativeID: "source", ResumeNativeID: "copy", ConfigDir: config}}}
	env := map[string]string{"HOME": home}
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if _, set := env["FACTORY_HOME_OVERRIDE"]; set {
		t.Fatal("default profile login unnecessarily overridden")
	}
	other := t.TempDir()
	env = map[string]string{"HOME": other}
	m.applyImportedConfigEnv(context.Background(), rec, env)
	if env["FACTORY_HOME_OVERRIDE"] != home {
		t.Fatalf("override must be home, not .factory dir: %q", env["FACTORY_HOME_OVERRIDE"])
	}
}
