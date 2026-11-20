package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/commandcode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func commandCodeChatImport(t *testing.T) (*Manager, *fakeStore, *recordingLauncher, domain.SessionRecord, ports.WorkspaceInfo) {
	t.Helper()
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	id := "11111111-2222-4333-8444-555555555555"
	root := filepath.Join(home, ".commandcode")
	path := filepath.Join(root, "projects", "original", id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	header, err := json.Marshal(map[string]any{"type": "session", "version": 3, "id": id, "cwd": t.TempDir(), "timestamp": "2026-11-03T00:00:00Z"})
	if err != nil {
		t.Fatal(err)
	}
	body := string(header) + "\n" + `{"type":"message","id":"u","parentId":null,"timestamp":"2026-11-03T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"synthetic request"}]}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{startErr: errors.New("synthetic driver boundary")}
	m, st, _ := newChatManager(launcher)
	m.dataDir = t.TempDir()
	m.agents = testAgentRegistry{domain.HarnessCommandCode: commandcode.New()}
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessCommandCode, Mode: domain.SessionModeChat}
	rec.Metadata = domain.SessionMetadata{
		WorkspacePath: cwd, NativeTranscriptPath: path, AgentSessionID: id, ProviderConversationID: id,
		ImportSource: &domain.SessionImportSource{NativeID: id, ConfigDir: root, Transferred: true, Prepared: true},
	}
	st.sessions[rec.ID] = rec
	return m, st, launcher, rec, ports.WorkspaceInfo{Path: cwd}
}

func TestCommandCodeChatImportRefusesUnwritablePrivateHomeBeforeLaunch(t *testing.T) {
	m, st, launcher, rec, ws := commandCodeChatImport(t)
	blocked := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocked, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.dataDir = blocked
	_, err := m.resumeChatController(context.Background(), "resume", rec, st.projects[string(rec.ProjectID)], ws, true, false, "", domain.SessionInterfaceTransitionHistoryStrict)
	if err == nil || errors.Is(err, launcher.startErr) || len(launcher.started) != 0 {
		t.Fatalf("private preparation failure reached Chat launch: started=%d err=%v", len(launcher.started), err)
	}
	if !st.sessions[rec.ID].NeedsImportResume() {
		t.Fatal("failed private preparation adopted the import")
	}
}

func TestCommandCodeChatImportCheckpointsPrivateHistoryBeforeLaunch(t *testing.T) {
	m, st, launcher, rec, ws := commandCodeChatImport(t)
	_, err := m.resumeChatController(context.Background(), "resume", rec, st.projects[string(rec.ProjectID)], ws, true, false, "", domain.SessionInterfaceTransitionHistoryStrict)
	if !errors.Is(err, launcher.startErr) || len(launcher.started) != 1 {
		t.Fatalf("first open did not reach the driver after preparation: %v", err)
	}
	prepared := st.sessions[rec.ID]
	if prepared.Metadata.ImportSource.ResumeConfigDir != filepath.Join(commandcode.SessionHomeDir(m.dataDir, rec.ID), ".commandcode") {
		t.Fatalf("private identity was not checkpointed: %+v", prepared.Metadata.ImportSource)
	}
	if err := commandcode.PrepareNativeHistory(context.Background(), m.dataDir, rec.ID, rec.Metadata.AgentSessionID, ws.Path); err != nil {
		t.Fatalf("prepared native history unavailable at driver boundary: %v", err)
	}
}
