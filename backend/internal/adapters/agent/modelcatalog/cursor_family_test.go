package modelcatalog

import "testing"

func TestCursorFamiliesPreserveSelectableVariants(t *testing.T) {
	models, err := parseCursorModels([]byte(`Available models

gpt-5.3-codex - Codex 5.3
gpt-5.3-codex-low-fast - Codex 5.3 Low Fast
gpt-5.3-codex-xhigh - Codex 5.3 Extra High
claude-fable-5-thinking-high - Claude Fable 5 1M Thinking (NO ZDR)
claude-fable-5-thinking-high-fast - Claude Fable 5 1M Thinking Fast (NO ZDR)
gemini-3.1-pro - Gemini 3.1 Pro
gemini-3.1-pro-high - Gemini 3.1 Pro High
unknown-high - Unknown High
unknown-low - Unknown Low
gpt-5.3-codex-mystery - Mystery
composer-2.5-fast - Composer 2.5 Fast
Tip: use --model
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 11 {
		t.Fatalf("models = %+v", models)
	}
	for i, family := range []string{"gpt-5.3-codex", "gpt-5.3-codex", "gpt-5.3-codex", "claude-fable-5", "claude-fable-5", "gemini-3.1-pro", "gemini-3.1-pro", "", "", "", ""} {
		if models[i].FamilyID != family {
			t.Fatalf("model %d family = %q, want %q", i, models[i].FamilyID, family)
		}
	}
	if models[1].ID != "gpt-5.3-codex-low-fast" || models[4].ID != "claude-fable-5-thinking-high-fast" || models[4].Label != "Claude Fable 5 1M Thinking Fast (NO ZDR)" {
		t.Fatalf("variants changed: %+v", models)
	}
	if models[0].FamilyLabel != "Codex 5.3" || models[3].FamilyLabel != "Claude Fable 5 1M (NO ZDR)" {
		t.Fatalf("family labels = %+v", models)
	}
}

func TestCursorCurrentDefaultAnnotation(t *testing.T) {
	models, err := parseCursorModels([]byte("Available models\nauto - Auto (current, default)\n"))
	if err != nil || len(models) != 1 || models[0].Label != "Auto" || !models[0].IsDefault {
		t.Fatalf("models=%+v err=%v", models, err)
	}
}
