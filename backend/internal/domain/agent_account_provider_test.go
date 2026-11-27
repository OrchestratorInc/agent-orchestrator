package domain

import "testing"

// TestAgentAccountProvidersMapToTheirHarness: every provider is run by one
// harness, and that harness's accounts are the provider's.
func TestAgentAccountProvidersMapToTheirHarness(t *testing.T) {
	want := map[AgentAccountProvider]AgentHarness{
		AgentAccountProviderClaude:      HarnessClaudeCode,
		AgentAccountProviderCodex:       HarnessCodex,
		AgentAccountProviderAntigravity: HarnessAgy,
	}
	if len(AgentAccountProviders) != len(want) {
		t.Fatalf("providers = %v, want %d", AgentAccountProviders, len(want))
	}
	for _, p := range AgentAccountProviders {
		harness, ok := AgentHarnessFor(p)
		if !ok || harness != want[p] {
			t.Fatalf("AgentHarnessFor(%s) = %q, %v, want %q", p, harness, ok, want[p])
		}
		if back, ok := AgentAccountProviderFor(harness); !ok || back != p {
			t.Fatalf("AgentAccountProviderFor(%s) = %q, %v, want %q", harness, back, ok, p)
		}
	}
	if _, ok := AgentAccountProviderFor(HarnessGemini); ok {
		t.Fatal("the Gemini CLI harness has accounts")
	}
}
