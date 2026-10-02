package sessionmanager

import (
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAccountSwitchReadinessRejectsMissingChatCapability(t *testing.T) {
	defer func() {
		if recovered := recover(); recovered != nil {
			t.Errorf("missing Chat capability panicked instead of rejecting readiness")
		}
	}()
	m := &Manager{switchTargetStartWait: time.Second}
	err := m.acknowledgeAccountSwitch(t.Context(), nil,
		domain.AccountsManagerSwitch{ID: "switch", SessionID: "session", TargetGeneration: "target"},
		domain.SessionRecord{ID: "session", Mode: domain.SessionModeChat})
	if !errors.Is(err, ErrInterfaceHandoffUnsupported) {
		t.Fatalf("missing capability readiness error = %v", err)
	}
}
