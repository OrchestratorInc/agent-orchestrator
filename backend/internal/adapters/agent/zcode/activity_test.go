package zcode

import (
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"testing"
)

func TestNativePermissionBoundaryAndToolCompletion(t *testing.T) {
	for _, tc := range []struct {
		event string
		want  domain.ActivityState
	}{{"permission-request", domain.ActivityBlocked}, {"post-tool-use", domain.ActivityActive}, {"post-tool-use-failure", domain.ActivityActive}, {"stop", domain.ActivityIdle}} {
		got, ok := DeriveActivityState(tc.event, nil)
		if !ok || got != tc.want {
			t.Fatalf("%s = %s,%v; want %s", tc.event, got, ok, tc.want)
		}
	}
	if _, ok := DeriveActivityState("session-end", nil); ok {
		t.Fatal("unobserved process exit was invented")
	}
}
