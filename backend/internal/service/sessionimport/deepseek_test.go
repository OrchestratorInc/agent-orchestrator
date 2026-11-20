package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestDeepSeekImportsEligibleNativeHistoryAsChat(t *testing.T) {
	root, cwd := canonical(t.TempDir()), canonical(t.TempDir())
	id := "00000000-0000-4000-8000-000000000522"
	path := filepath.Join(root, "sessions", "project", id, "session.v4.jsonl")
	header, err := json.Marshal(map[string]any{"type": "session", "version": 4, "id": id, "cwd": cwd, "createdAt": 1790000000000, "isSeeded": false, "delegationDepth": 0})
	if err != nil {
		t.Fatal(err)
	}
	body := `{"type":"user/message","seq":0,"time":1790000000000,"data":{"role":"user","source":{"kind":"user"},"content":[{"type":"text","text":"native marker"}]},"surfaceOp":"append"}` + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, string(header)+"\n"+body)
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: cwd}}}
	svc := New(st, "", chatSupported)
	svc.home = canonical(t.TempDir())
	svc.roots = map[domain.AgentHarness]string{domain.HarnessDeepSeek: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan=%+v,%v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import=%+v,%v", results, err)
	}
	if st.sessions[0].Mode != domain.SessionModeChat || st.sessions[0].Metadata.ProviderConversationID != id || !st.sessions[0].NeedsImportResume() {
		t.Fatalf("imported record=%+v", st.sessions[0])
	}
	wantTexts(t, st.messages, "user: native marker")
	// A future generation must invalidate discovery before cached text is reused.
	writeFile(t, filepath.Join(filepath.Dir(path), "session.v5.jsonl"), "unknown")
	preview, err = svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 0 {
		t.Fatalf("offered unknown generation=%+v,%v", preview, err)
	}
}
