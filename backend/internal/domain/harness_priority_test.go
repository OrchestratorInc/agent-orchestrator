package domain

import "testing"

func TestChooseDefaultHarness(t *testing.T) {
	ready, unknown, notReady := AgentReadinessReady, AgentReadinessUnknown, AgentReadinessNotReady
	tests := []struct {
		name  string
		state map[AgentHarness]AgentEffectiveReadiness
		want  AgentHarness
	}{
		{"claude ready wins over codex ready", map[AgentHarness]AgentEffectiveReadiness{HarnessClaudeCode: ready, HarnessCodex: ready}, HarnessClaudeCode},
		{"codex when claude is not installed", map[AgentHarness]AgentEffectiveReadiness{HarnessClaudeCode: notReady, HarnessCodex: ready}, HarnessCodex},
		{"a ready agent beats an unverifiable higher one", map[AgentHarness]AgentEffectiveReadiness{HarnessClaudeCode: unknown, HarnessCodex: ready}, HarnessCodex},
		{"cursor when only it is ready", map[AgentHarness]AgentEffectiveReadiness{HarnessClaudeCode: notReady, HarnessCodex: notReady, HarnessCursor: ready}, HarnessCursor},
		{"unknown everywhere keeps the top of the list", map[AgentHarness]AgentEffectiveReadiness{}, HarnessClaudeCode},
		{"unknown claude beats not-ready codex", map[AgentHarness]AgentEffectiveReadiness{HarnessClaudeCode: unknown, HarnessCodex: notReady}, HarnessClaudeCode},
		{"nothing usable falls back to claude so launch reports its own error", map[AgentHarness]AgentEffectiveReadiness{
			HarnessClaudeCode: notReady, HarnessCodex: notReady, HarnessCursor: notReady, HarnessOpenCode: notReady, HarnessOpenCodeV2: notReady, HarnessAider: notReady,
		}, HarnessClaudeCode},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChooseDefaultHarness(func(h AgentHarness) AgentEffectiveReadiness {
				if s, ok := tt.state[h]; ok {
					return s
				}
				return unknown
			})
			if got != tt.want {
				t.Fatalf("ChooseDefaultHarness = %q, want %q", got, tt.want)
			}
		})
	}
}
