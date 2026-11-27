package sessionmanager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/openhands"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type openHandsRetryImportAgent struct {
	terminalImportAgent
	preparations int
	restoreRoot  string
}

func (a *openHandsRetryImportAgent) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	a.preparations++
	return openhands.New().PrepareImportedSession(ctx, r)
}

func (a *openHandsRetryImportAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	a.restoreRoot = cfg.Session.Metadata[ports.MetadataKeyImportConfigDir]
	return []string{"openhands", "--resume", cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]}, true, nil
}

func TestOpenHandsImportCheckpointFailureRecoversPrivateHistory(t *testing.T) {
	id := "12345678-1234-1234-1234-123456789012"
	source := t.TempDir()
	dir := filepath.Join(source, strings.ReplaceAll(id, "-", ""))
	if err := os.MkdirAll(filepath.Join(dir, "events"), 0o700); err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(map[string]any{"id": id, "workspace": map[string]string{"working_dir": source}, "leaf_event_id": "87654321-1234-1234-1234-123456789012"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "base_state.json")
	if err := os.WriteFile(path, state, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events", "event-00000-87654321-1234-1234-1234-123456789012.json"), []byte(`{"id":"87654321-1234-1234-1234-123456789012","source":"user","llm_message":{"role":"user","content":[{"type":"text","text":"Retained native history"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	fingerprint, err := openhands.ConversationFingerprint(context.Background(), source, id)
	if err != nil {
		t.Fatal(err)
	}
	agent := &openHandsRetryImportAgent{}
	runtime := &fakeRuntime{}
	m, store, _ := newExitedResumeManager(t, runtime, agent)
	m.dataDir, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	m.store = &failingImportWorkspaceStore{fakeStore: store, failAt: 1}
	rec := store.sessions["mer-1"]
	rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessOpenHands, domain.ActivityIdle
	rec.Metadata = domain.SessionMetadata{
		WorkspacePath: t.TempDir(), Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id,
		NativeTranscriptPath: path,
		ImportSource:         &domain.SessionImportSource{NativeID: id, ConfigDir: source, CWD: source, Fingerprint: fingerprint, Transferred: true, Prepared: true},
	}
	store.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil {
		t.Fatal("expected injected checkpoint failure")
	}
	if runtime.created != 0 || store.sessions[rec.ID].Metadata.ImportSource.ResumeNativeID != "" || agent.preparations != 1 {
		t.Fatal("failed checkpoint launched provider or published identity")
	}
	copies, err := filepath.Glob(filepath.Join(m.dataDir, "native-imports", "openhands", ".native-*"))
	if err != nil || len(copies) != 1 {
		t.Fatalf("published copies = %v, %v", copies, err)
	}
	if err := os.RemoveAll(source); err != nil {
		t.Fatal(err)
	}
	result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
	if err != nil || result.Session.NeedsImportResume() || agent.preparations != 2 {
		t.Fatalf("checkpoint retry = %#v, preparations=%d, %v", result, agent.preparations, err)
	}
	if agent.restoreRoot != copies[0] || store.sessions[rec.ID].Metadata.ImportSource.ResumeConfigDir != copies[0] {
		t.Fatal("native restore did not use the proven private store")
	}
}
