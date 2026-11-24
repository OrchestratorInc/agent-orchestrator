package primeagent

import (
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestDeriveActivityState(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload string
		state   domain.ActivityState
		ok      bool
	}{
		{"promptless startup is idle", "session-start", `{"reason":"startup"}`, domain.ActivityIdle, true},
		{"prompt submit is active", "user-prompt-submit", `{"prompt":"fix it"}`, domain.ActivityActive, true},
		{"agent end is idle", "stop", `{}`, domain.ActivityIdle, true},
		{"quit is exited", "session-end", `{"reason":"quit"}`, domain.ActivityExited, true},
		{"reload is not terminal", "session-end", `{"reason":"reload"}`, "", false},
		{"new session is not terminal", "session-end", `{"reason":"new"}`, "", false},
		{"resume is not terminal", "session-end", `{"reason":"resume"}`, "", false},
		{"fork is not terminal", "session-end", `{"reason":"fork"}`, "", false},
		{"unknown shutdown reason is ignored", "session-end", `{"reason":"future-reason"}`, "", false},
		{"missing shutdown reason is ignored", "session-end", `{}`, "", false},
		{"malformed shutdown payload is ignored", "session-end", `{`, "", false},
		{"unknown event is ignored", "unknown", `{}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state, ok := DeriveActivityState(tt.event, []byte(tt.payload))
			if state != tt.state || ok != tt.ok {
				t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, %v)", tt.event, state, ok, tt.state, tt.ok)
			}
		})
	}
}
