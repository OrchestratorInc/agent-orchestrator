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

func TestCommandCodeChatAdoptedImportCannotReseedPrivateHistory(t *testing.T) {
	for _, corrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "corrupt"}[corrupt], func(t *testing.T) {
			m, st, launcher, rec, ws := commandCodeChatImport(t)
			_, err := m.resumeChatController(context.Background(), "resume", rec, st.projects[string(rec.ProjectID)], ws, true, false, "", domain.SessionInterfaceTransitionHistoryStrict)
			if !errors.Is(err, launcher.startErr) || len(launcher.started) != 1 {
				t.Fatalf("first open did not reach the driver after preparation: %v", err)
			}
			matches, err := filepath.Glob(filepath.Join(commandcode.SessionHomeDir(m.dataDir, rec.ID), ".commandcode", "projects", "*", rec.Metadata.AgentSessionID+".jsonl"))
			if err != nil || len(matches) != 1 {
				t.Fatalf("first open did not prepare the exact private history: %v %v", matches, err)
			}
			private := matches[0]
			if corrupt {
				err = os.WriteFile(private, []byte("corrupt native history"), 0o600)
			} else {
				err = os.Remove(private)
			}
			if err != nil {
				t.Fatal(err)
			}
			rec = st.sessions[rec.ID]
			rec.Metadata.ImportSource.Adopted = true
			st.sessions[rec.ID] = rec
			_, err = m.resumeChatController(context.Background(), "resume", rec, st.projects[string(rec.ProjectID)], ws, true, false, "", domain.SessionInterfaceTransitionHistoryStrict)
			if err == nil || errors.Is(err, launcher.startErr) || len(launcher.started) != 1 {
				t.Fatalf("adopted invalid history reached Chat launch: started=%d err=%v", len(launcher.started), err)
			}
			contents, readErr := os.ReadFile(private)
			if corrupt && (readErr != nil || string(contents) != "corrupt native history") || !corrupt && !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("adopted history was reseeded: %q %v", contents, readErr)
			}
		})
	}
}
