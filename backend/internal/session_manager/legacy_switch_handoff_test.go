package sessionmanager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type legacyHandoffFakeStore struct {
	*fakeStore
	path string
}

func (s *legacyHandoffFakeStore) LegacySwitchHandoffPath(context.Context, domain.SessionID, domain.AgentHarness, string) (string, error) {
	return s.path, nil
}

func TestNativeRestoreCarriesAHandoffFromMainsAgentSwitching(t *testing.T) {
	data := t.TempDir()
	path := filepath.Join(data, "handoffs", "mer-1", "sw-1", "final.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"schemaVersion":1,"switchId":"sw-1","sessionId":"mer-1","fromAgent":"codex","targetAgent":"claude-code","continuation":"EARLIER CONTEXT"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := domain.SessionRecord{ID: "mer-1", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode, Metadata: domain.SessionMetadata{AgentSessionID: "native-1"}}
	m := &Manager{dataDir: data, store: &legacyHandoffFakeStore{fakeStore: newFakeStore(), path: path}}
	if got := m.withLegacySwitchHandoff(context.Background(), rec, "AO RULES"); got != "AO RULES\n\nEARLIER CONTEXT" {
		t.Fatalf("system prompt = %q, want the old handoff added", got)
	}
	// A file outside AO's handoffs folder is not read.
	outside := filepath.Join(t.TempDir(), "final.json")
	if err := os.WriteFile(outside, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	m.store = &legacyHandoffFakeStore{fakeStore: newFakeStore(), path: outside}
	if got := m.withLegacySwitchHandoff(context.Background(), rec, "AO RULES"); got != "AO RULES" {
		t.Fatalf("system prompt = %q, want it unchanged", got)
	}
}
