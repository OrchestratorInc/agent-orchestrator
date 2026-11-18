package codex

import (
	"path/filepath"
	"testing"
	"time"
)

func TestParseUnavailableModelReadsCodexChatGPTRefusal(t *testing.T) {
	for text, want := range map[string]string{
		"The 'gpt-5.2-codex' model is not supported when using Codex with a ChatGPT account.":                                   "gpt-5.2-codex",
		`{"detail":"The 'gpt-5' model is not supported when using Codex with a ChatGPT account."}`:                              "gpt-5",
		"unexpected status 400 Bad Request: The 'gpt-5.3-codex' model is not supported when using Codex with a ChatGPT account": "gpt-5.3-codex",
		"Usage limit exceeded": "",
		"The model is not supported when using Codex with a ChatGPT account": "",
	} {
		got, ok := ParseUnavailableModel(text)
		if got != want || ok != (want != "") {
			t.Errorf("ParseUnavailableModel(%q) = %q, %v; want %q", text, got, ok, want)
		}
	}
}

func TestUnavailableModelsPersistAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "harnesses", "codex", "unavailable-models.json")
	store := NewUnavailableModels(path)
	if err := store.Record("gpt-5.2-codex", time.Now()); err != nil {
		t.Fatal(err)
	}
	reloaded := NewUnavailableModels(path)
	if !reloaded.Unavailable("gpt-5.2-codex") || reloaded.Unavailable("gpt-6.1-sol") {
		t.Fatal("a restarted daemon must remember exactly the refused model")
	}
	if err := reloaded.Forget("gpt-5.2-codex"); err != nil {
		t.Fatal(err)
	}
	if NewUnavailableModels(path).Unavailable("gpt-5.2-codex") {
		t.Fatal("a forgotten model must stay forgotten after a restart")
	}
}

func TestRecordUnavailableModelFromIgnoresOtherFailures(t *testing.T) {
	ConfigureUnavailableModels("")
	t.Cleanup(func() { ConfigureUnavailableModels("") })
	if got := RecordUnavailableModelFrom("Usage limit exceeded", time.Now()); got != "" {
		t.Fatalf("recorded %q from an unrelated failure", got)
	}
	if got := RecordUnavailableModelFrom("The 'gpt-5' model is not supported when using Codex with a ChatGPT account.", time.Now()); got != "gpt-5" {
		t.Fatalf("recorded %q, want gpt-5", got)
	}
	if !DefaultUnavailableModels().Unavailable("gpt-5") {
		t.Fatal("the refusal was not recorded in the process-wide store")
	}
}
