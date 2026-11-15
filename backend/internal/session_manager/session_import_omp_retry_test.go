//go:build !windows

package sessionmanager

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/omp"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/sessionimport"
)

type ompRetryAgent struct{ fakeAgent }

func (ompRetryAgent) RelocateImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) error {
	return omp.New().RelocateImportedSession(ctx, r)
}

type ompRetryArchiveStore struct {
	*fakeStore
	messages []sessionimport.Message
}

func (s *ompRetryArchiveStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	var projects []domain.ProjectRecord
	for _, project := range s.projects {
		projects = append(projects, project)
	}
	return projects, nil
}

func (s *ompRetryArchiveStore) CreateImportedSession(_ context.Context, rec domain.SessionRecord, messages []sessionimport.Message) (domain.SessionRecord, bool, error) {
	s.sessions[rec.ID] = rec
	s.messages = append([]sessionimport.Message(nil), messages...)
	return rec, true, nil
}

func (s *ompRetryArchiveStore) ReplaceImportedMessages(_ context.Context, _ domain.SessionID, messages []sessionimport.Message) error {
	s.messages = append([]sessionimport.Message(nil), messages...)
	return nil
}

func TestOMPFailedFirstLoadKeepsArchivePairedWithRelocatedCopy(t *testing.T) {
	ctx := context.Background()
	root, launch, original := t.TempDir(), t.TempDir(), t.TempDir()
	const nativeID = "01a17cf3-99d0-76cd-b084-4cdce17a0fec"
	header, err := json.Marshal(map[string]string{"type": "session", "id": nativeID, "cwd": original})
	if err != nil {
		t.Fatal(err)
	}
	body := string(header) + "\n" + `{"type":"message","id":"m1","parentId":null,"message":{"role":"user","content":"copied native turn"}}` + "\n"
	src := filepath.Join(root, "sessions", "original", "2026-10-27_"+nativeID+".jsonl")
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher := &recordingLauncher{startErr: ports.ErrChatResumeFailed, providerConversationID: nativeID}
	m, st, _ := newChatManager(launcher)
	m.agents = relocatingAgents{agent: ompRetryAgent{}}
	archive := &ompRetryArchiveStore{fakeStore: st}
	m.SetImportArchiveRefresher(sessionimport.New(archive, "", func(domain.AgentHarness) bool { return true }))
	rec := domain.SessionRecord{ID: "mer-1", ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessOMP, Mode: domain.SessionModeChat, Activity: domain.Activity{State: domain.ActivityIdle}}
	rec.Metadata = domain.SessionMetadata{
		WorkspacePath:          launch,
		Branch:                 "ao/import",
		ProviderConversationID: nativeID,
		AgentSessionID:         nativeID,
		NativeTranscriptPath:   src,
		ImportSource:           &domain.SessionImportSource{NativeID: nativeID, ConfigDir: root, CWD: original, Transferred: true, Prepared: true},
	}
	st.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(ctx, rec.ID); !errors.Is(err, ports.ErrChatResumeFailed) {
		t.Fatalf("first load = %v", err)
	}
	pending := st.sessions[rec.ID]
	if !pending.NeedsImportResume() || pending.Metadata.ImportSource.ResumeCwd != launch {
		t.Fatalf("failed load lost the relocation checkpoint: %+v", pending.Metadata.ImportSource)
	}
	if len(archive.messages) != 1 || archive.messages[0].Text != "copied native turn" {
		t.Fatalf("initial archive = %+v", archive.messages)
	}
	files, err := filepath.Glob(filepath.Join(root, "sessions", "*", "*.jsonl"))
	if err != nil || len(files) != 2 {
		t.Fatalf("native original and copy = %v, %v", files, err)
	}
	var privatePath string
	for _, path := range files {
		if path != src {
			privatePath = path
		}
	}
	copied, err := os.ReadFile(privatePath)
	if err != nil {
		t.Fatal(err)
	}
	body += `{"type":"message","id":"m2","parentId":"m1","message":{"role":"user","content":"original-only turn after failed load"}}` + "\n"
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	launcher.startErr = nil
	result, err := m.ResumeAgentWithMode(ctx, rec.ID)
	if err != nil || result.Session.NeedsImportResume() || result.Mode != RestoreModeNative || result.Session.Metadata.ProviderConversationID != nativeID {
		t.Fatalf("retry = %+v, %v", result, err)
	}
	if len(archive.messages) != 1 || archive.messages[0].Text != "copied native turn" {
		t.Fatalf("retry archived turns absent from the native copy: %+v", archive.messages)
	}
	if after, err := os.ReadFile(privatePath); err != nil || string(after) != string(copied) {
		t.Fatalf("retry replaced the pinned native copy: %v", err)
	}
	cfg := launcher.started[len(launcher.started)-1]
	if cfg.ProviderConversationID != nativeID || cfg.HistoryMode != ports.ChatHistoryRequired || cfg.FreshIfProviderConversationMissing {
		t.Fatalf("retry lost strict native identity: %+v", cfg)
	}
}
