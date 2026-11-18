package acp

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// claudeModelCatalogConversation holds the model choices Claude Code 2.1.300
// advertised in a Chat session: each family's newest model only under its
// alias, older models under their full IDs, and the launch model.
func claudeModelCatalogConversation(current string) *conversation {
	c := &conversation{}
	c.configOptions = []ports.ChatConfigOption{{
		ID: "model", Type: ports.ChatConfigOptionSelect,
		Current: ports.ChatConfigOptionValue{Select: current},
		Choices: []ports.ChatConfigOptionChoice{
			{Value: "default", Name: "Default (recommended)", Description: "Sonnet"},
			{Value: "sonnet", Name: "Sonnet", Description: "Sonnet 5.5 · Efficient for routine tasks"},
			{Value: "fable", Name: "Fable", Description: "Fable 5.1 · Most capable for your hardest and longest-running tasks"},
			{Value: "opus", Name: "Opus", Description: "Opus 5.5 · Best for everyday, complex tasks"},
			{Value: "haiku", Name: "Haiku", Description: "Haiku 5.5 · Fastest for quick answers"},
			{Value: "claude-haiku-5-5", Name: "claude-haiku-5-5"},
			{Value: "claude-opus-4-8", Name: "Opus 4.8"},
		},
	}}
	// The Claude binding marks full IDs optional and names them by version,
	// as claudeacp's claudeSessionOptions does.
	names := map[string]string{
		"claude-opus-5-5": "Opus 5.5", "claude-sonnet-5-5": "Sonnet 5.5",
		"claude-fable-5-1": "Fable 5.1", "claude-fable-5-2": "Fable 5.2",
		"claude-haiku-5-5": "Haiku 5.5", "claude-opus-4-8": "Opus 4.8",
	}
	c.optionsFor = func(settings ports.ChatTurnSettings) []SessionOption {
		if name, ok := names[settings.Model]; ok {
			return []SessionOption{{ID: "model", Value: settings.Model, Optional: true, Name: name}}
		}
		return []SessionOption{{ID: "model", Value: settings.Model}}
	}
	return c
}

func TestConfigOptionValueNamedMatchesTheDescriptionLabel(t *testing.T) {
	c := claudeModelCatalogConversation("claude-haiku-5-5")
	for name, want := range map[string]string{
		"Opus 5.5":   "opus",
		"sonnet 5.5": "sonnet",
		"Fable 5.1":  "fable",
		"Opus 4.8":   "claude-opus-4-8",
		"Fable 5.2":  "",
		"Opus 5":     "",
		"":           "",
	} {
		if got := c.configOptionValueNamed("model", name); got != want {
			t.Errorf("configOptionValueNamed(%q) = %q, want %q", name, got, want)
		}
	}
}

// A switch the session cannot honor used to be skipped, so the old model kept
// answering while AO showed the new one (#6399).
func TestCheckModelSwitchRefusesAnOptionalModelTheSessionCannotUse(t *testing.T) {
	c := claudeModelCatalogConversation("claude-haiku-5-5")
	for _, model := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "claude-fable-5-1", "claude-opus-4-8", "opus", "claude-haiku-5-5"} {
		if err := c.checkModelSwitch(context.Background(), model); err != nil {
			t.Errorf("checkModelSwitch(%q) = %v, want the switch allowed", model, err)
		}
	}
	err := c.checkModelSwitch(context.Background(), "claude-fable-5-2")
	if !errors.Is(err, ports.ErrChatConfigOptionInvalid) {
		t.Fatalf("checkModelSwitch(claude-fable-5-2) = %v, want ErrChatConfigOptionInvalid", err)
	}
}

func TestCheckModelSwitchLeavesTheOpenModelAndUnknownCatalogsAlone(t *testing.T) {
	// A session opened on a model it does not list must still start.
	c := claudeModelCatalogConversation("claude-fable-5-2")
	if err := c.checkModelSwitch(context.Background(), "claude-fable-5-2"); err != nil {
		t.Fatalf("checkModelSwitch(current model) = %v, want nil", err)
	}
	// Without an advertised model option nothing is known to refuse.
	c.configOptions = nil
	if err := c.checkModelSwitch(context.Background(), "claude-fable-5-2"); err != nil {
		t.Fatalf("checkModelSwitch(no catalog) = %v, want nil", err)
	}
}
