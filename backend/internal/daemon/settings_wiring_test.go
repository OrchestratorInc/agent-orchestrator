package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Turning account handoff on or off wakes the usage-limit loop, which rests
// while handoff is off and nothing is armed.
func TestSettingsStoreSignalsAccountHandoffChange(t *testing.T) {
	changed := make(chan struct{}, 1)
	store := settingsStore{store: sqlitetest.MustOpen(t), accountHandoffChanged: changed}
	for _, enabled := range []bool{true, false} {
		if err := store.SetAccountHandoff(context.Background(), enabled, time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		select {
		case <-changed:
		default:
			t.Fatalf("setting account handoff to %v did not signal", enabled)
		}
	}
}
