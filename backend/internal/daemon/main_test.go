package daemon

import (
	"os"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/conpty"
)

// TestMain lets native-runtime wiring tests re-exec this test binary through
// the same hidden pty-host entrypoint used by the production AO binary.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == "pty-host" {
		os.Exit(conpty.RunHost(os.Args[2:], os.Stdout))
	}
	os.Exit(m.Run())
}
