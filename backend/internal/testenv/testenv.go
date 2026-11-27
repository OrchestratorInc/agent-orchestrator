// Package testenv keeps the environment of an enclosing AO session out of tests.
package testenv

import (
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/sessionenv"
)

// ClearSession unsets the session variables for the test process. Call it from
// TestMain before m.Run. It names each variable instead of clearing every AO_
// name because some tests re-run the test binary with an AO_ marker set.
func ClearSession() {
	for _, name := range sessionenv.SessionVars {
		_ = os.Unsetenv(name)
	}
}
