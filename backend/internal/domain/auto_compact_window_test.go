package domain

import "testing"

func TestAutoCompactWindowProblemNamesWhatTheAgentCannotTake(t *testing.T) {
	for _, tc := range []struct {
		harness AgentHarness
		window  string
		ok      bool
	}{
		{HarnessCodex, "", true},
		{HarnessClaudeCode, "200k", true},
		{HarnessClaudeCode, "500k", true},
		{HarnessClaudeCode, "1m", true},
		{HarnessOpenCode, "off", true},
		{HarnessOpenCodeV2, "off", true},
		{HarnessCodex, "1m", false},
		{HarnessOpenCode, "200k", false},
		{HarnessClaudeCode, "off", false},
		{HarnessCodex, "off", false},
		{HarnessClaudeCode, "2m", false},
	} {
		if problem := AutoCompactWindowProblem(tc.harness, tc.window); (problem == "") != tc.ok {
			t.Errorf("AutoCompactWindowProblem(%s, %q) = %q, want ok=%v", tc.harness, tc.window, problem, tc.ok)
		}
	}
}

func TestOnlyCodexTakesFastMode(t *testing.T) {
	if !CanUseFastMode(HarnessCodex) || CanUseFastMode(HarnessClaudeCode) || CanUseFastMode(HarnessOpenCode) {
		t.Fatal("Fast mode reaches Codex only")
	}
}
