package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// One provider's unreadable store, here a corrupt opencode.db, hides only its
// own chats; the other providers' chats are still offered.
func TestScanSkipsAProviderWhoseStoreCannotBeRead(t *testing.T) {
	claudeRoot, openCodeRoot := t.TempDir(), t.TempDir()
	writeClaude(t, claudeRoot, "11111111-1111-4111-8111-111111111111", "", "still importable", time.Now())
	if err := os.WriteFile(filepath.Join(openCodeRoot, openCodeDBFile), []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	svc := New(&importStore{}, "")
	svc.roots = map[domain.AgentHarness]string{domain.HarnessClaudeCode: claudeRoot, domain.HarnessOpenCode: openCodeRoot}
	preview, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatalf("one unreadable provider failed the whole scan: %v", err)
	}
	if len(preview.Candidates) != 1 || preview.Candidates[0].Title != "still importable" {
		t.Fatalf("candidates = %+v", preview.Candidates)
	}
}

// A terminated import no longer holds its conversation: the chat is offered
// again instead of pointing at the dead session.
func TestScanOffersAChatWhoseImportWasTerminated(t *testing.T) {
	root := t.TempDir()
	id := "22222222-2222-4222-8222-222222222222"
	writeClaude(t, root, id, "", "import me again", time.Now())
	dead := domain.SessionRecord{ID: "imported-1", Harness: domain.HarnessClaudeCode, IsTerminated: true}
	dead.Metadata.ProviderConversationID = id
	dead.Metadata.ImportSource = &domain.SessionImportSource{NativeID: id, ConfigDir: canonical(root)}
	svc := New(&importStore{sessions: []domain.SessionRecord{dead}}, "")
	svc.roots = map[domain.AgentHarness]string{domain.HarnessClaudeCode: root}
	preview, err := svc.Scan(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Candidates) != 1 || preview.Candidates[0].SessionID != "" {
		t.Fatalf("candidates = %+v, want the chat offered with no session", preview.Candidates)
	}
}
