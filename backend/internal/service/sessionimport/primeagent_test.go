package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestPrimeAgentImportsNativeTreeAsTerminal(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	id := "f395a38e-7815-4849-ae63-74e445dfc830"
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": cwd})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, id+".jsonl")
	data := string(header) + "\n" + piMsg("u1", "", "user", "synthetic prime question") + "\n" + piMsg("a1", "u1", "assistant", "synthetic answer") + "\n"
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic", Path: cwd}}}
	service := New(store, "", func(domain.AgentHarness) bool { return true })
	service.roots = map[domain.AgentHarness]string{domain.HarnessPrimeAgent: root}
	preview, err := service.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	results, err := service.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	record := store.sessions[0]
	if record.Mode != domain.SessionModeTUI || record.Harness != domain.HarnessPrimeAgent || record.Metadata.AgentSessionID != id || record.Metadata.ProviderConversationID != id || record.Metadata.ImportSource.ConfigDir != canonical(root) {
		t.Fatalf("native identity/root/Terminal mode lost: %#v", record)
	}
	wantTexts(t, store.messages, "user: synthetic prime question", "assistant: synthetic answer")
	after, err := os.ReadFile(path)
	if err != nil || string(after) != data {
		t.Fatalf("source history changed: %v", err)
	}
}

func TestPrimeAgentRootMatchesProviderOverridePrecedence(t *testing.T) {
	home := t.TempDir()
	t.Setenv("PRIME_AGENT_CODING_AGENT_DIR", filepath.Join(home, "profile"))
	t.Setenv("PRIME_AGENT_CODING_AGENT_SESSION_DIR", "~/legacy")
	t.Setenv("PRIME_AGENT_SESSION_DIR", "~/selected")
	if got := primeAgentRoot(home); got != filepath.Join(home, "selected") {
		t.Fatalf("primary override = %q", got)
	}
	// A present empty primary suppresses the legacy override, as in the CLI.
	t.Setenv("PRIME_AGENT_SESSION_DIR", "")
	if got := primeAgentRoot(home); got != filepath.Join(home, "profile", "sessions") {
		t.Fatalf("empty primary override = %q", got)
	}
}

func TestPrimeAgentRejectsUnsupportedNativeVersion(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "synthetic.jsonl")
	if err := os.WriteFile(path, []byte("{\"type\":\"session\",\"version\":2}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readPrimeAgentTranscript(context.Background(), root, path); err == nil {
		t.Fatal("unsupported native version was offered for import")
	}
}
