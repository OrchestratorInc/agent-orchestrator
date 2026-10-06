package daemon

import (
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/testingevidence"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	testingsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testing"
)

// testingProviders is the single composition point for slices A, D and E.
// Empty providers deliberately keep creation/dispatch unavailable. Management
// cancellation and saved evidence use durable state independently of providers.
type testingProviders struct {
	Target  ports.TestingTargetEnvironment
	Desktop ports.TestingDesktopControl
	Workers testingsvc.WorkerLauncher
	Recipes map[string]testingsvc.Recipe
}

func newTestingService(cfg config.Config, store testingsvc.Store, providers testingProviders) *testingsvc.Service {
	home, _ := os.UserHomeDir()
	return testingsvc.New(testingsvc.Deps{Store: store, Target: providers.Target, Desktop: providers.Desktop, Workers: providers.Workers, Recipes: providers.Recipes, Evidence: testingevidence.New(cfg.DataDir, store), TargetStateRoot: filepath.Join(home, ".ao", "dev", "agentic-target")})
}
