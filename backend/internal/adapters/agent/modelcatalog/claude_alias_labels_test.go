package modelcatalog

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestLabelClaudeAliasVersionsUsesNewestProviderModelPerFamily(t *testing.T) {
	aliases := []ports.AgentModelInfo{
		{ID: "sonnet", Label: "Sonnet"}, {ID: "opus", Label: "Opus"},
		{ID: "haiku", Label: "Haiku"}, {ID: "fable", Label: "Fable 5.1"},
		{ID: "opus[1m]", Label: "Opus (1M context)"},
	}
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

func TestLabelClaudeAliasVersionsKeepsConfiguredContextVariant(t *testing.T) {
	reference := []ports.AgentModelInfo{{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"}}
	// A configured default the catalog lacks is appended labeled with its ID.
	models := applyClaudeConfiguredDefault(append([]ports.AgentModelInfo(nil), reference...), "opus[1m]")
	got := map[string]string{}
	for _, item := range LabelClaudeAliasVersions(models, reference) {
		got[item.ID] = item.Label
	}
	if got["opus[1m]"] != "Opus 5.5 (1M context)" {
		t.Fatalf("opus[1m] label = %q, want Opus 5.5 (1M context) (all: %v)", got["opus[1m]"], got)
	}
	if got["claude-opus-5-5"] != "Claude Opus 5.5" {
		t.Fatalf("concrete label = %q, want it unchanged", got["claude-opus-5-5"])
	}
}

func TestLabelClaudeAliasVersionsKeepsAliasWithoutEvidence(t *testing.T) {
	aliases := []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}}
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

func TestLabelClaudeAliasVersionsDoesNotMutateInput(t *testing.T) {
	models := []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}}
	reference := []ports.AgentModelInfo{{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"}}
	if got := LabelClaudeAliasVersions(models, reference); got[0].Label != "Opus 5.5" {
		t.Fatalf("label = %q, want Opus 5.5", got[0].Label)
	}
	if models[0].Label != "Opus" {
		t.Fatal("labeling must not mutate the caller's slice")
	}
}
