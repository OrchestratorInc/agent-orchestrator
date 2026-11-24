package sessionmanager

import (
	"os"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearSession()
	os.Exit(m.Run())
}
