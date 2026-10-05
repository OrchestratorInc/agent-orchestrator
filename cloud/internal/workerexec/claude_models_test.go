package workerexec

import (
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func TestClaudeModelsFromACPOptions(t *testing.T) {
	modelChoices := acp.SessionConfigSelectOptionsUngrouped{
		{Value: "sonnet", Name: "Claude Sonnet"},
		{Value: "opus", Name: "Claude Opus"},
	}
	effortChoices := acp.SessionConfigSelectOptionsUngrouped{
		{Value: "medium", Name: "Medium"},
		{Value: "high", Name: "High"},
	}
	models, current, effort := claudeModelsFromOptions([]acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{Id: "model", CurrentValue: "sonnet", Options: acp.SessionConfigSelectOptions{Ungrouped: &modelChoices}}},
		{Select: &acp.SessionConfigOptionSelect{Id: "effort", CurrentValue: "high", Options: acp.SessionConfigSelectOptions{Ungrouped: &effortChoices}}},
	})
	if current != "sonnet" || effort != "high" || len(models) != 2 || !models[0].Default || len(models[0].Efforts) != 2 || len(models[1].Efforts) != 0 {
		t.Fatalf("catalog = %+v, current = %q/%q", models, current, effort)
	}
}

func TestClaudeDefaultModelUsesProviderResolvedName(t *testing.T) {
	resolved := "Claude Opus"
	choices := acp.SessionConfigSelectOptionsUngrouped{
		{Value: "default", Name: "Default (recommended)", Description: &resolved},
		{Value: "opus", Name: "Claude Opus"},
	}
	models, current, _ := claudeModelsFromOptions([]acp.SessionConfigOption{
		{Select: &acp.SessionConfigOptionSelect{Id: "model", CurrentValue: "default", Options: acp.SessionConfigSelectOptions{Ungrouped: &choices}}},
	})
	if current != "default" || models[0].DisplayName != "Claude Opus" {
		t.Fatalf("catalog = %+v, current = %q", models, current)
	}
}
