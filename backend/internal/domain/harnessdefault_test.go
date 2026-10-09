package domain

import "testing"

func TestHarnessDefaultPrecedence(t *testing.T) {
	defaults := HarnessDefault{Model: "global", Effort: "high"}
	for _, tc := range []struct {
		name         string
		config, want AgentConfig
	}{
		{"unset", AgentConfig{}, AgentConfig{Model: "global", Effort: "high"}},
		{"same model", AgentConfig{Model: "global"}, AgentConfig{Model: "global", Effort: "high"}},
		{"project model", AgentConfig{Model: "project"}, AgentConfig{Model: "project"}},
		{"project effort", AgentConfig{Effort: "low"}, AgentConfig{Model: "global", Effort: "low"}},
		{"project mode", AgentConfig{Mode: "medium"}, AgentConfig{Mode: "medium"}},
		{"permissions", AgentConfig{Permissions: PermissionModeDefault}, AgentConfig{Model: "global", Effort: "high", Permissions: PermissionModeDefault}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := defaults.Apply(tc.config); got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
