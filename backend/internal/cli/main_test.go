//go:build !e2e

package cli

import (
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/testenv"
)

func TestMain(m *testing.M) {
	testenv.ClearSession()
	os.Exit(m.Run())
}
