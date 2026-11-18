package sessionimport

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codewhale"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func writeCodewhaleImport(t *testing.T, root, cwd string) string {
	t.Helper()
	const id = "55555555-5555-4555-8555-555555555555"
	body, err := json.Marshal(map[string]any{
		"schema_version": 1,
		"metadata":       map[string]any{"id": id, "workspace": cwd, "title": "Synthetic Codewhale chat", "model": "synthetic-model", "created_at": "2026-11-03T00:00:00Z", "updated_at": "2026-11-03T00:00:00Z", "message_count": 2, "total_tokens": 0},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "explain the synthetic whale"}}},
			map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "synthetic answer"}, map[string]any{"type": "thinking", "text": "private thought"}}},
		},
		"system_prompt": nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	return writeFile(t, filepath.Join(root, "sessions", id+".json"), string(body))
}

func TestCodewhaleImportAndPrivateNativeIdentity(t *testing.T) {
	root, cwd, launch := t.TempDir(), t.TempDir(), t.TempDir()
	const id = "55555555-5555-4555-8555-555555555555"
	path := writeCodewhaleImport(t, root, cwd)
	st := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic-cw", Path: cwd}}}
	svc := New(st, "", func(domain.AgentHarness) bool { return true })
	svc.roots = map[domain.AgentHarness]string{domain.HarnessCodewhale: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Mode != domain.SessionModeTUI || rec.Metadata.AgentSessionID != id || rec.Metadata.NativeTranscriptPath != path {
		t.Fatalf("record = %#v", rec)
	}
	wantTexts(t, st.messages, "user: explain the synthetic whale", "assistant: synthetic answer")
	if err := svc.RefreshArchive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	adapter := codewhale.New()
	prepared, err := adapter.PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{NativeSessionID: id, ConfigDir: root, TranscriptPath: path, SourceCWD: cwd, LaunchCWD: launch})
	if err != nil || prepared.NativeSessionID == "" || prepared.NativeSessionID == id || prepared.TranscriptPath == path {
		t.Fatalf("prepared = %#v, %v", prepared, err)
	}
	availability, err := adapter.ProbeNativeSession(context.Background(), prepared)
	if err != nil || availability != ports.NativeSessionAvailabilityAvailable {
		t.Fatalf("probe = %s, %v", availability, err)
	}
	if _, err := adapter.PrepareImportedSession(context.Background(), ports.ImportedSessionRelocation{NativeSessionID: "wrong", ConfigDir: root, TranscriptPath: path, LaunchCWD: launch}); err == nil {
		t.Fatal("prepared a mismatched source identity")
	}
}

func TestCodewhaleScanRefusesNativeHistoryAndApprovalSidecars(t *testing.T) {
	for _, kind := range []string{"journal", "approval"} {
		t.Run(kind, func(t *testing.T) {
			root, cwd := t.TempDir(), t.TempDir()
			writeCodewhaleImport(t, root, cwd)
			const id = "55555555-5555-4555-8555-555555555555"
			path := filepath.Join(root, "sessions", ".journal-archive", id+".jsonl")
			if kind == "approval" {
				path = filepath.Join(root, "sessions", id, "approval_receipts.jsonl")
			}
			writeFile(t, path, "{\"synthetic\":true}\n")
			st := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic-sidecar", Path: cwd}}}
			svc := New(st, "", nil)
			svc.roots = map[domain.AgentHarness]string{domain.HarnessCodewhale: root}
			preview, err := svc.Scan(context.Background())
			if err != nil || len(preview.Candidates) != 0 {
				t.Fatalf("dependent snapshot offered: %#v, %v", preview, err)
			}
		})
	}
}
