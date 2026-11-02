package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/autohand"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestAutohandFirstOpenRefusesTornNativeHistoryBeforeLaunch(t *testing.T) {
	id := "11111111-2222-4333-8444-555555555555-1790000000000"
	root, source, workspace := t.TempDir(), t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "sessions", id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	metadata, err := json.Marshal(map[string]string{"sessionId": id, "projectPath": source})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "metadata.json"), metadata, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "conversation.jsonl")
	if err := os.WriteFile(path, []byte("{\"role\":\"user\",\"content\":\"synthetic prompt\"}\n{\"role\":"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := &fakeRuntime{}
	m, st, _ := newExitedResumeManager(t, rt, autohand.New())
	rec := st.sessions["mer-1"]
	rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessAutohand, domain.ActivityIdle
	rec.Metadata = domain.SessionMetadata{WorkspacePath: workspace, Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id, NativeTranscriptPath: path,
		ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: root, CWD: source, Transferred: true, Prepared: true}}
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil {
		t.Fatal("torn native history reached launch/adoption")
	}
	if rt.created != 0 || !st.sessions[rec.ID].NeedsImportResume() {
		t.Fatalf("malformed history changed session: created=%d metadata=%+v", rt.created, st.sessions[rec.ID].Metadata)
	}
}
