package sessionimport

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type importStore struct {
	projects []domain.ProjectRecord
	sessions []domain.SessionRecord
	messages []Message
}

func (s *importStore) ListProjects(context.Context) ([]domain.ProjectRecord, error) {
	return s.projects, nil
}
func (s *importStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return s.sessions, nil
}
func (s *importStore) CreateImportedSession(_ context.Context, r domain.SessionRecord, msg []Message) (domain.SessionRecord, bool, error) {
	r.ID = domain.SessionID(fmt.Sprintf("import-%d", len(s.sessions)+1))
	s.sessions = append(s.sessions, r)
	s.messages = msg
	return r, true, nil
}
func writeClaude(t *testing.T, root, id, cwd, text string, at time.Time) string {
	t.Helper()
	dir := filepath.Join(root, "projects", "project")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, id+".jsonl")
	b, err := json.Marshal(map[string]any{"type": "user", "sessionId": id, "cwd": cwd, "timestamp": at, "message": map[string]any{"role": "user", "content": text}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(b, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestPreviewAndSelectedImport(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	project := t.TempDir()
	st := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	svc := New(st)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessClaudeCode: root}
	current := "11111111-1111-4111-8111-111111111111"
	old := "22222222-2222-4222-8222-222222222222"
	path := writeClaude(t, root, current, project, "fix parser", time.Now())
	writeClaude(t, root, old, "", "old standalone", time.Now().AddDate(0, 0, -60))
	writeClaude(t, root, "33333333-3333-4333-8333-333333333333", t.TempDir(), "unregistered", time.Now())
	corrupt := writeClaude(t, root, "44444444-4444-4444-8444-444444444444", project, "invalid", time.Now())
	if err := os.WriteFile(corrupt, []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(filepath.Dir(path), "55555555-5555-4555-8555-555555555555.jsonl")); err != nil {
		t.Fatal(err)
	}
	preview, err := svc.Scan(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Candidates) != 2 {
		t.Fatalf("eligible = %#v", preview.Candidates)
	}
	var recentID, oldID string
	for _, c := range preview.Candidates {
		if c.Title == "fix parser" {
			recentID = c.ID
			if !c.Suggested || c.ProjectID != "repo" {
				t.Fatal(c)
			}
		} else {
			oldID = c.ID
			if c.Suggested || c.ProjectID != "" {
				t.Fatal(c)
			}
		}
	}
	if len(st.sessions) != 0 {
		t.Fatal("scan created sessions")
	}
	results, err := svc.Import(ctx, []string{oldID})
	if err != nil || results[0].Status != "created" {
		t.Fatalf("import: %v %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Activity.State != domain.ActivityIdle || rec.Metadata.WorkspacePath != "" || !rec.NeedsImportResume() || rec.Mode != domain.SessionModeChat {
		t.Fatal(rec)
	}
	repeated, err := svc.Import(ctx, []string{oldID})
	if err != nil || repeated[0].SessionID != rec.ID || len(st.sessions) != 1 {
		t.Fatalf("repeat: %v %v", repeated, err)
	}
	writeClaude(t, root, current, project, "changed", time.Now())
	stale, err := svc.Import(ctx, []string{recentID})
	if err != nil || stale[0].Status != "failed" || len(st.sessions) != 1 {
		t.Fatalf("stale: %v %v", stale, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	preview, err = svc.Scan(ctx)
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("missing: %v %v", preview, err)
	}
}
func TestCodexCanonicalMessagesAndValidation(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sessions", "2026")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "rollout.jsonl")
	body := `{"type":"session_meta","payload":{"id":"11111111-1111-4111-8111-111111111111","cwd":""}}
{"type":"event_msg","payload":{"type":"user_message","message":"hello"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}}
{"type":"response_item","payload":{"type":"function_call","content":"private tool output"}}
{"type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"world"}]}}
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	source, err := readTranscript(context.Background(), domain.HarnessCodex, root, path)
	if err != nil || len(source.messages) != 2 || source.messages[0].Text != "hello" || source.messages[1].Text != "world" {
		t.Fatalf("transcript: %#v %v", source, err)
	}
	if err := os.WriteFile(path, []byte(body+"{broken\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readTranscript(context.Background(), domain.HarnessCodex, root, path); err == nil {
		t.Fatal("malformed accepted")
	}
	relative := writeClaude(t, root, "22222222-2222-4222-8222-222222222222", "relative-folder", "invalid source folder", time.Now())
	if _, err := readTranscript(context.Background(), domain.HarnessClaudeCode, root, relative); err == nil {
		t.Fatal("relative source working directory accepted")
	}
}

func (s *importStore) ListWorkspaceRepos(context.Context, string) ([]domain.WorkspaceRepoRecord, error) {
	return nil, nil
}

func TestClaudeImportFollowsActiveAncestry(t *testing.T) {
	root := t.TempDir()
	id := "11111111-1111-4111-8111-111111111111"
	path := writeClaude(t, root, id, "", "placeholder", time.Now())
	body := `{"type":"user","uuid":"u1","parentUuid":null,"message":{"content":"original"}}
{"type":"assistant","uuid":"a1","parentUuid":"u1","message":{"content":"first answer"}}
{"type":"user","uuid":"old-u2","parentUuid":"a1","message":{"content":"discarded prompt"}}
{"type":"assistant","uuid":"old-a2","parentUuid":"old-u2","message":{"content":"discarded answer"}}
{"type":"user","uuid":"u2","parentUuid":"a1","message":{"content":"edited prompt"}}
{"type":"user","uuid":"context","parentUuid":"u2","isMeta":true,"turnCompanion":true,"message":{"content":"hidden context"}}
{"type":"assistant","uuid":"a2","parentUuid":"context","message":{"content":"current answer"}}
{"type":"assistant","uuid":"side","parentUuid":"a2","isSidechain":true,"message":{"content":"subagent"}}
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	history, err := readTranscript(context.Background(), domain.HarnessClaudeCode, root, path)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"original", "first answer", "edited prompt", "current answer"}
	if len(history.messages) != len(want) {
		t.Fatalf("history = %#v", history.messages)
	}
	for i, message := range history.messages {
		if message.Text != want[i] {
			t.Fatalf("message %d = %q, want %q", i, message.Text, want[i])
		}
	}
	for name, tail := range map[string]string{
		"broken parent": `{"type":"user","uuid":"broken","parentUuid":"missing","message":{"content":"broken"}}`,
		"cycle":         `{"type":"user","uuid":"cycle","parentUuid":"cycle","message":{"content":"cycle"}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(body+tail+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := readTranscript(context.Background(), domain.HarnessClaudeCode, root, path); err == nil {
				t.Fatal("invalid ancestry accepted")
			}
		})
	}
}
