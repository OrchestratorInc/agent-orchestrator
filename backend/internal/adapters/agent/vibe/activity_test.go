package vibe

import (
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
)

func TestDeriveActivityState(t *testing.T) {
	tests := []struct {
		event string
		want  domain.ActivityState
		ok    bool
	}{
		{"pre-tool", domain.ActivityActive, true},
		{"post-tool", domain.ActivityActive, true},
		{"post-agent", domain.ActivityIdle, true},
		{"permission-request", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.event, func(t *testing.T) {
			got, ok := DeriveActivityState(tt.event, nil)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("DeriveActivityState(%q) = (%q, %v), want (%q, %v)", tt.event, got, ok, tt.want, tt.ok)
			}
		})
	}
}
