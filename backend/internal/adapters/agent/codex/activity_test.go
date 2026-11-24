package codex

import (
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestDeriveActivityState(t *testing.T) {
	tests := []struct {
		name   string
		event  string
		want   domain.ActivityState
		wantOK bool
	}{
		{"user prompt -> active", "user-prompt-submit", domain.ActivityActive, true},
		{"permission request -> waiting_input", "permission-request", domain.ActivityWaitingInput, true},
		{"stop -> idle", "stop", domain.ActivityIdle, true},
		{"session start -> no signal", "session-start", "", false},
		{"unknown event -> no signal", "frobnicate", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DeriveActivityState(tt.event, []byte(`{}`))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, %v)",
					tt.event, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
