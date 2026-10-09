package copilot

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDeriveActivityState(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload string
		want    domain.ActivityState
		ok      bool
	}{
		{"session start", "session-start", `{}`, domain.ActivityActive, true},
		{"user prompt", "user-prompt-submit", `{}`, domain.ActivityActive, true},
		{"pre tool use is active not waiting", "pre-tool-use", `{"toolName":"bash"}`, domain.ActivityActive, true},
		{"permission request is observe-only", "permission-request", `{"toolName":"bash"}`, "", false},
		{"permission prompt notification", "notification", `{"notification_type":"permission_prompt"}`, domain.ActivityWaitingInput, true},
		{"elicitation notification", "notification", `{"notificationType":"elicitation_dialog"}`, domain.ActivityWaitingInput, true},
		{"other notification ignored", "notification", `{"notification_type":"agent_idle"}`, "", false},
		{"post tool maps to permission-resolved", "permission-resolved", `{"toolName":"bash"}`, domain.ActivityActive, true},
		{"stop", "stop", `{}`, domain.ActivityIdle, true},
		{"unknown", "session-end", `{}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DeriveActivityState(tt.event, []byte(tt.payload))
			if got != tt.want || ok != tt.ok {
				t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, %v)", tt.event, got, ok, tt.want, tt.ok)
			}
		})
	}
}
