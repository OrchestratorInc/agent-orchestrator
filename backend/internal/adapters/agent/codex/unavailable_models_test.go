package codex

import (
	"os"
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

// A refusal belongs to the account that produced it, and survives a restart.
func TestUnavailableModelsAreKeptPerAccount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "harnesses", "codex", "unavailable-models.json")
	work, personal := filepath.Join(dir, "accounts", "work"), filepath.Join(dir, "accounts", "personal")
	store := NewUnavailableModels(path)
	if err := store.Record(work, "gpt-5.2-codex", time.Now()); err != nil {
		t.Fatal(err)
	}
	reloaded := NewUnavailableModels(path)
	if !reloaded.Unavailable(work, "gpt-5.2-codex") {
		t.Fatal("a restarted daemon must remember the refusal for its account")
	}
	if reloaded.Unavailable(personal, "gpt-5.2-codex") {
		t.Fatal("another account must not inherit the refusal")
	}
	if reloaded.Unavailable(work, "gpt-6.1-sol") {
		t.Fatal("only the refused model is unavailable")
	}
}

// Reading an old failed rollout again must not revive a refusal the account
// cleared by listing the model again; a newer refusal still counts.
func TestUnavailableModelsIgnoreARefusalOlderThanItsForgetting(t *testing.T) {
	home := filepath.Join(t.TempDir(), "codex")
	store := NewUnavailableModels("")
	refused := time.Date(2026, 11, 18, 5, 0, 0, 0, time.UTC)
	if err := store.Record(home, "gpt-5.4-mini", refused); err != nil {
		t.Fatal(err)
	}
	if err := store.Forget(home, "gpt-5.4-mini", refused.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(home, "gpt-5.4-mini", refused); err != nil {
		t.Fatal(err)
	}
	if store.Unavailable(home, "gpt-5.4-mini") {
		t.Fatal("an old refusal read again revived a forgotten model")
	}
	if err := store.Record(home, "gpt-5.4-mini", refused.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if !store.Unavailable(home, "gpt-5.4-mini") {
		t.Fatal("a refusal newer than the forgetting must count")
	}
}

// A write that failed is retried by the next one, even one that changes
// nothing, so the refusal is not lost at the next restart.
func TestUnavailableModelsRetryAFailedWrite(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "codex")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(blocker, "unavailable-models.json")
	home := filepath.Join(dir, "home")
	store := NewUnavailableModels(path)
	if err := store.Record(home, "gpt-5.2-codex", time.Now()); err == nil {
		t.Fatal("write into a file-as-directory succeeded; the test needs it to fail")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	if err := store.Record(home, "gpt-5.2-codex", time.Now()); err != nil {
		t.Fatalf("retried write: %v", err)
	}
	if !NewUnavailableModels(path).Unavailable(home, "gpt-5.2-codex") {
		t.Fatal("the refusal was not persisted after the storage recovered")
	}
}

func TestAccountHomeOfRolloutReadsCodexHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "accounts", "work")
	rollout := filepath.Join(home, "sessions", "2026", "11", "18", "rollout-2026-11-18T05-50-00-abc.jsonl")
	if got := AccountHomeOfRollout(rollout); AccountKey(got) != AccountKey(home) {
		t.Fatalf("AccountHomeOfRollout = %q, want %q", got, home)
	}
	if got := AccountHomeOfRollout(filepath.Join(home, "rollout.jsonl")); got != "" {
		t.Fatalf("path without a sessions dir = %q, want empty", got)
	}
}

func TestRecordUnavailableModelFromIgnoresOtherFailures(t *testing.T) {
	ConfigureUnavailableModels("")
	t.Cleanup(func() { ConfigureUnavailableModels("") })
	home := t.TempDir()
	if got := RecordUnavailableModelFrom(home, "Usage limit exceeded", time.Now()); got != "" {
		t.Fatalf("recorded %q from an unrelated failure", got)
	}
	if got := RecordUnavailableModelFrom(home, "The 'gpt-5' model is not supported when using Codex with a ChatGPT account.", time.Now()); got != "gpt-5" {
		t.Fatalf("recorded %q, want gpt-5", got)
	}
	if !DefaultUnavailableModels().Unavailable(home, "gpt-5") {
		t.Fatal("the refusal was not recorded in the process-wide store")
	}
}
