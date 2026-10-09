package modelcatalog

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLabelClaudeAliasVersionsUsesNewestProviderModelPerFamily(t *testing.T) {
	aliases := normalize(claudeCodeModels())
	reference := []ports.AgentModelInfo{
		{ID: "claude-opus-5-1-20260301", Label: "Claude Opus 5.1"},
		{ID: "claude-opus-5-5-20260901", Label: "Claude Opus 5.5"},
		{ID: "claude-sonnet-5-5-20260901"}, // no display name: the ID carries the version
		{ID: "claude-haiku-5-5-20261001", Label: "Claude Haiku 5.5"},
		{ID: "opus", Label: "Opus 9.9"}, // aliases are never a reference
	}
	got := map[string]string{}
	for _, item := range LabelClaudeAliasVersions(aliases, normalize(reference)) {
		got[item.ID] = item.Label
	}
	want := map[string]string{
		"opus": "Opus 5.5", "sonnet": "Sonnet 5.5", "haiku": "Haiku 5.5",
		"opus[1m]": "Opus 5.5 (1M context)", "fable": "Fable 5.1",
	}
	for id, label := range want {
		if got[id] != label {
			t.Fatalf("label for %s = %q, want %q (all: %v)", id, got[id], label, got)
		}
	}
}

func TestLabelClaudeAliasVersionsKeepsAliasWithoutEvidence(t *testing.T) {
	aliases := normalize(claudeCodeModels())
	for _, reference := range [][]ports.AgentModelInfo{
		nil,
		{{ID: "glm-5", Label: "GLM 5"}},
		{{ID: "sonnet", Label: "Sonnet"}},
	} {
		for _, item := range LabelClaudeAliasVersions(aliases, reference) {
			if item.ID == "opus" && item.Label != "Opus" {
				t.Fatalf("opus label = %q with reference %v, want the bare alias", item.Label, reference)
			}
		}
	}
}

func TestDiscovererLabelsAliasesOnlyForClaudeCode(t *testing.T) {
	models := []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}}
	reference := []ports.AgentModelInfo{{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"}}
	if got := (Discoverer{}).LabelAliases("opencode", models, reference); got[0].Label != "Opus" {
		t.Fatalf("opencode label = %q, want it untouched", got[0].Label)
	}
	if got := (Discoverer{}).LabelAliases("claude-code", models, reference); got[0].Label != "Opus 5.5" {
		t.Fatalf("claude-code label = %q, want Opus 5.5", got[0].Label)
	}
	if models[0].Label != "Opus" {
		t.Fatal("labeling must not mutate the caller's slice")
	}
}
