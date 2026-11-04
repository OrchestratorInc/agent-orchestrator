package domain

import "testing"

func TestHandoffHeadroomSelectsRequestBucket(t *testing.T) {
	claude := AgentAccountLimits{Windows: []AgentAccountLimitWindow{
		{ID: "five_hour", UsedPercent: 20},
		{ID: "seven_day", UsedPercent: 40},
		{ID: "seven_day_opus", UsedPercent: 100, Scope: "opus"},
		{ID: "seven_day_sonnet", UsedPercent: 10, Scope: "sonnet"},
	}}
	codex := AgentAccountLimits{Windows: []AgentAccountLimitWindow{
		{ID: "codex:Session (5h)", UsedPercent: 30},
		{ID: "spark:Spark · Weekly", UsedPercent: 100, Scope: "GPT-5.3-Codex-Spark"},
	}}
	for _, tc := range []struct {
		name     string
		limits   AgentAccountLimits
		provider AgentAccountProvider
		model    string
		ok       bool
		headroom float64
	}{
		{"claude sonnet ignores the full opus meter", claude, AgentAccountProviderClaude, "claude-sonnet-5", true, 60},
		{"claude opus counts its own meter", claude, AgentAccountProviderClaude, "claude-opus-5", false, 0},
		{"claude alias matches a display scope", AgentAccountLimits{Windows: []AgentAccountLimitWindow{{UsedPercent: 5}, {UsedPercent: 95, Scope: "Claude Opus 5"}}}, AgentAccountProviderClaude, "opus", false, 0},
		{"claude default model counts every meter", claude, AgentAccountProviderClaude, "", false, 0},
		{"codex default model counts the overall bucket", codex, AgentAccountProviderCodex, "", true, 70},
		{"codex model bucket counts for its model", codex, AgentAccountProviderCodex, "gpt-5.3-codex-spark", false, 0},
		{"codex other model ignores the spark bucket", codex, AgentAccountProviderCodex, "gpt-5.5", true, 70},
		{"near the limit is not room", AgentAccountLimits{Windows: []AgentAccountLimitWindow{{UsedPercent: HandoffMaxUsedPercent}}}, AgentAccountProviderCodex, "", false, 0},
		{"no meter verifies nothing", AgentAccountLimits{}, AgentAccountProviderClaude, "claude-opus-5", false, 0},
		{"only another model's meter verifies nothing", AgentAccountLimits{Windows: []AgentAccountLimitWindow{{UsedPercent: 1, Scope: "spark"}}}, AgentAccountProviderCodex, "gpt-5.5", false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headroom, ok := tc.limits.HandoffHeadroom(tc.provider, tc.model)
			if ok != tc.ok || headroom != tc.headroom {
				t.Fatalf("HandoffHeadroom = %v, %v; want %v, %v", headroom, ok, tc.headroom, tc.ok)
			}
		})
	}
}
