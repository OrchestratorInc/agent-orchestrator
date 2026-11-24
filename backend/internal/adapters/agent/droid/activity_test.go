package droid

import (
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestDeriveActivityState(t *testing.T) {
	tests := []struct {
		name    string
		event   string
		payload string
		want    domain.ActivityState
		wantOK  bool
	}{
		{"user prompt -> active", "user-prompt-submit", `{}`, domain.ActivityActive, true},
		{"stop -> idle", "stop", `{}`, domain.ActivityIdle, true},
		// Droid notifications fire only on permission-needed or 60s-idle, and the
		// payload carries no notification_type to discriminate — so every
		// notification maps to waiting_input (an automated Enter must
		// never answer a pending permission decision).
		{"notification -> waiting_input", "notification", `{"message":"Droid needs your permission"}`, domain.ActivityWaitingInput, true},
		{"notification empty payload -> waiting_input", "notification", `{}`, domain.ActivityWaitingInput, true},
		{"notification malformed payload -> waiting_input", "notification", `not json`, domain.ActivityWaitingInput, true},
		{"session-end logout -> exited", "session-end", `{"reason":"logout"}`, domain.ActivityExited, true},
		{"session-end prompt_input_exit -> exited", "session-end", `{"reason":"prompt_input_exit"}`, domain.ActivityExited, true},
		{"session-end other -> exited", "session-end", `{"reason":"other"}`, domain.ActivityExited, true},
		{"session-end absent reason -> exited", "session-end", `{}`, domain.ActivityExited, true},
		{"session-end clear -> no signal", "session-end", `{"reason":"clear"}`, "", false},
		{"session-start -> no signal", "session-start", `{}`, "", false},
		{"unknown event -> no signal", "frobnicate", `{}`, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := DeriveActivityState(tt.event, []byte(tt.payload))
			if got != tt.want || ok != tt.wantOK {
				t.Fatalf("DeriveActivityState(%q, %q) = (%q, %v), want (%q, %v)",
					tt.event, tt.payload, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
