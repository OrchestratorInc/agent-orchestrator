package sessionimport

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestGeminiJSONLImportUsesNativeHeaderAndCurrentBranch(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	dir := filepath.Join(root, "tmp", "synthetic-project", "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), ".project_root"), []byte(cwd), 0o600); err != nil {
		t.Fatal(err)
	}
	id := "11111111-1111-4111-8111-111111111111"
	path := filepath.Join(dir, "session-2026-11-02T08-00-11111111.jsonl")
	body := `{"sessionId":"11111111-1111-4111-8111-111111111111","projectHash":"synthetic","kind":"main"}
{"id":"context","type":"user","content":"<environment_context>injected</environment_context>"}
{"id":"u1","type":"user","timestamp":"2026-11-02T08:00:00Z","content":[{"text":"remember the synthetic otter"}]}
{"id":"a1","type":"gemini","content":[{"thought":true,"text":"private thought"},{"functionCall":{"name":"read"}},{"text":"draft"}]}
{"$patch":{"id":"a1","content":[{"text":"remembered"}]}}
{"id":"discard","type":"user","content":"rewound prompt"}
{"$rewindTo":"discard"}
{"$set":{"lastUpdated":"2026-11-02T08:01:00Z"}}
{"torn":`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	st := &importStore{projects: []domain.ProjectRecord{{ID: "synthetic", Path: cwd}}}
	svc := New(st, "", func(domain.AgentHarness) bool { return false })
	svc.roots = map[domain.AgentHarness]string{domain.HarnessGemini: root}
	svc.now = func() time.Time { return time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC) }
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	c := preview.Candidates[0]
	if c.Title != "remember the synthetic otter" || c.MessageCount != 2 || c.ProjectID != "synthetic" {
		t.Fatal(c)
	}
	results, err := svc.Import(context.Background(), []string{c.ID})
	if err != nil || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	if rec := st.sessions[0]; rec.Metadata.AgentSessionID != id || rec.Mode != domain.SessionModeTUI || st.messages[1].Text != "remembered" {
		t.Fatalf("record = %#v, messages = %#v", rec, st.messages)
	}
}

func TestGeminiLegacyJSONAndRegistryCWD(t *testing.T) {
	root := canonical(t.TempDir())
	cwd := t.TempDir()
	dir := filepath.Join(root, "tmp", "synthetic", "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	registry, err := json.Marshal(map[string]any{"projects": map[string]string{cwd: "synthetic"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "projects.json"), registry, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session-2026-11-02T08-00-22222222.json")
	if err := os.WriteFile(path, []byte(`{
  "sessionId":"22222222-2222-4222-8222-222222222222",
  "projectHash":"synthetic",
  "messages":[{"id":"u","type":"user","content":"legacy prompt"},{"id":"a","type":"gemini","content":"legacy answer"}]
}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readGeminiTranscriptWithProjects(context.Background(), root, nil, path)
	if err != nil || got.cwd != cwd || len(got.messages) != 2 {
		t.Fatalf("legacy = %#v, %v", got, err)
	}
}

func TestGeminiLegacyHashResolvesOnlyRegisteredProjectsWithoutMetadata(t *testing.T) {
	root, cwd := t.TempDir(), canonical(t.TempDir())
	sum := sha256.Sum256([]byte(cwd))
	hash := hex.EncodeToString(sum[:])
	dir := filepath.Join(root, "tmp", hash, "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session-2026-11-02T08-00-33333333.json")
	body, err := json.Marshal(map[string]any{"sessionId": "33333333-3333-4333-8333-333333333333", "projectHash": hash, "messages": []map[string]string{{"id": "u", "type": "user", "content": "synthetic legacy prompt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readGeminiTranscriptWithProjects(context.Background(), root, nil, path); err == nil {
		t.Fatal("unregistered hash was guessed")
	}
	st := &importStore{projects: []domain.ProjectRecord{{ID: "legacy", Path: cwd}}}
	svc := New(st, "", nil)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessGemini: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].ProjectID != "legacy" {
		t.Fatalf("legacy preview = %#v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("legacy import = %#v, %v", results, err)
	}
	if err := svc.RefreshArchive(context.Background(), st.sessions[0]); err != nil {
		t.Fatal(err)
	}
	for _, metadata := range []string{filepath.Join(root, "projects.json"), filepath.Join(filepath.Dir(dir), ".project_root")} {
		if _, err := os.Stat(metadata); !os.IsNotExist(err) {
			t.Fatalf("discovery wrote provider metadata %s: %v", metadata, err)
		}
	}
}

func TestGeminiSkimsOversizeToolRecordAndKeepsVisibleText(t *testing.T) {
	before := maxRecordBytes
	maxRecordBytes = 256
	t.Cleanup(func() { maxRecordBytes = before })
	root, cwd := canonical(t.TempDir()), t.TempDir()
	dir := filepath.Join(root, "tmp", "synthetic", "chats")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(dir), ".project_root"), []byte(cwd), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "session-2026-11-02T08-00-11111111.jsonl")
	body := `{"sessionId":"11111111-1111-4111-8111-111111111111","projectHash":"synthetic"}
{"id":"u","type":"user","content":"synthetic prompt"}
{"id":"a","type":"gemini","content":"visible answer","toolCalls":[{"result":"` + strings.Repeat("synthetic tool payload ", 1000) + `"}]}
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readGeminiTranscriptWithProjects(context.Background(), root, nil, path)
	if err != nil || len(got.messages) != 2 || !got.shortened || got.messages[1].Text != "visible answer" {
		t.Fatalf("oversize = %#v, %v", got, err)
	}
}
