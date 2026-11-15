package domain

import "testing"

func TestSessionLink(t *testing.T) {
	cases := []struct {
		project ProjectID
		id      SessionID
		want    string
	}{
		{"agent-orchestrator", "agent-orchestrator-12", "ao://sessions/agent-orchestrator/agent-orchestrator-12"},
		{"", "standalone-40", "ao://sessions/__standalone__/standalone-40"},
		{"proj 1", "worker/1", "ao://sessions/proj%201/worker%2F1"},
		{"p", "", ""},
	}
	for _, tc := range cases {
		if got := SessionLink(tc.project, tc.id); got != tc.want {
			t.Errorf("SessionLink(%q, %q) = %q, want %q", tc.project, tc.id, got, tc.want)
		}
	}
}
