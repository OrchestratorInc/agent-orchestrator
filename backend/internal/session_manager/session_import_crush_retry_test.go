package sessionmanager

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/crush"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type crushRetryImportAgent struct {
	terminalImportAgent
	preparations int
}

func (a *crushRetryImportAgent) PrepareImportedSession(ctx context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	a.preparations++
	return crush.New().PrepareImportedSession(ctx, r)
}

func (a *crushRetryImportAgent) GetRestoreCommand(_ context.Context, cfg ports.RestoreConfig) ([]string, bool, error) {
	return []string{"crush", "--session", cfg.Session.Metadata[ports.MetadataKeyAgentSessionID]}, true, nil
}

func TestCrushImportLaunchFailureReusesPreparedDatabase(t *testing.T) {
	id := "12345678-1234-1234-1234-123456789012"
	source := filepath.Join(t.TempDir(), "crush.db")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, parent_session_id TEXT, title TEXT); INSERT INTO sessions VALUES ('12345678-1234-1234-1234-123456789012', NULL, 'source')`); err != nil {
		t.Fatal(err)
	}
	agent := &crushRetryImportAgent{}
	runtime := &fakeRuntime{createErrSequence: []error{errors.New("synthetic launch failure"), nil}}
	m, store, _ := newExitedResumeManager(t, runtime, agent)
	rec := store.sessions["mer-1"]
	workspace := t.TempDir()
	rec.Mode, rec.Harness, rec.Activity.State = domain.SessionModeTUI, domain.HarnessCrush, domain.ActivityIdle
	rec.Metadata = domain.SessionMetadata{
		WorkspacePath: workspace, Branch: "ao/import", AgentSessionID: id, ProviderConversationID: id,
		NativeTranscriptPath: filepath.Join(source, id),
		ImportSource:         &domain.SessionImportSource{NativeID: id, ConfigDir: t.TempDir(), Transferred: true, Prepared: true},
	}
	store.sessions[rec.ID] = rec
	if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil {
		t.Fatal("expected synthetic launch failure")
	}
	if store.sessions[rec.ID].Metadata.ImportSource.ResumeNativeID != id || agent.preparations != 1 {
		t.Fatal("preparation was not checkpointed before launch")
	}
	copyDB, err := sql.Open("sqlite", filepath.Join(workspace, ".crush", "crush.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyDB.Close() }()
	if _, err := copyDB.Exec(`UPDATE sessions SET title = 'later AO turn'`); err != nil {
		t.Fatal(err)
	}
	result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
	if err != nil || result.Session.NeedsImportResume() || agent.preparations != 1 {
		t.Fatalf("retry = %#v, preparations=%d, %v", result, agent.preparations, err)
	}
	var title string
	if err := copyDB.QueryRow(`SELECT title FROM sessions WHERE id = ?`, id).Scan(&title); err != nil || title != "later AO turn" {
		t.Fatalf("prepared turns overwritten: %q, %v", title, err)
	}
	if err := db.QueryRow(`SELECT title FROM sessions WHERE id = ?`, id).Scan(&title); err != nil || title != "source" {
		t.Fatalf("source changed: %q, %v", title, err)
	}
}
