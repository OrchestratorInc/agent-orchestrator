package sessionmanager

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/runtimeselect"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestAccountsManagerSwitchProductionRuntimeAdmission(t *testing.T) {
	m, st, _, _, rec, cfg := accountSwitchFixture(t)
	m.runtime = runtimeselect.New(nil, filepath.Join(m.dataDir, "unused-running.json"))
	// Admission must work without allowing this unit test to start a process.
	stopped, cancel := context.WithCancel(t.Context())
	cancel()
	m.backgroundContext = stopped
	if _, err := m.StartAccountsManagerSwitch(t.Context(), rec.ID, cfg); err != nil {
		t.Fatalf("production-selected runtime rejected account-switch admission: %v", err)
	}
	if _, ok := m.runtime.(ports.RuntimeLaunchHandleResolver); !ok {
		t.Fatal("production-selected runtime dropped launch identity capability")
	}
	_ = waitAccountSwitch(t, m, st, cfg.OperationID)
}
