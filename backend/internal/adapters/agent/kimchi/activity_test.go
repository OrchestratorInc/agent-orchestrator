package kimchi

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
		{"notification idle_prompt -> idle", "notification", `{"notification_type":"idle_prompt"}`, domain.ActivityIdle, true},
		{"notification permission_prompt -> blocked", "notification", `{"notification_type":"permission_prompt"}`, domain.ActivityBlocked, true},
		{"notification agent_needs_input -> waiting_input", "notification", `{"notification_type":"agent_needs_input"}`, domain.ActivityWaitingInput, true},
		{"notification agent_completed -> idle", "notification", `{"notification_type":"agent_completed"}`, domain.ActivityIdle, true},
		{"notification auth_success -> no signal", "notification", `{"notification_type":"auth_success"}`, "", false},
		{"notification empty type -> no signal", "notification", `{}`, "", false},
		{"notification malformed payload -> no signal", "notification", `not json`, "", false},
		{"session-end logout -> exited", "session-end", `{"reason":"logout"}`, domain.ActivityExited, true},
		{"session-end prompt_input_exit -> exited", "session-end", `{"reason":"prompt_input_exit"}`, domain.ActivityExited, true},
		{"session-end other -> exited", "session-end", `{"reason":"other"}`, domain.ActivityExited, true},
		{"session-end absent reason -> exited", "session-end", `{}`, domain.ActivityExited, true},
		{"session-end quit -> exited", "session-end", `{"reason":"quit"}`, domain.ActivityExited, true},
		{"session-end reload -> no signal", "session-end", `{"reason":"reload"}`, "", false},
		{"session-end new -> no signal", "session-end", `{"reason":"new"}`, "", false},
		{"session-end resume -> no signal", "session-end", `{"reason":"resume"}`, "", false},
		{"session-end fork -> no signal", "session-end", `{"reason":"fork"}`, "", false},
		{"pre-tool-use -> active", "pre-tool-use", `{}`, domain.ActivityActive, true},
		{"post-tool-use -> active", "post-tool-use", `{}`, domain.ActivityActive, true},
		{"post-tool-use-failure -> active", "post-tool-use-failure", `{}`, domain.ActivityActive, true},
		{"legacy post-tool-use-fail -> active", "post-tool-use-fail", `{}`, domain.ActivityActive, true},
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
