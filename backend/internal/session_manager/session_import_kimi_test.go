package sessionmanager

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"testing"
)

func TestKimiImportsUseCurrentConfigHomeHook(t *testing.T) {
	if importedHarnesses[domain.HarnessKimi].configEnvKey != "KIMI_CODE_HOME" {
		t.Fatal("prepared Kimi home is not restored through the native import hook")
	}
}
