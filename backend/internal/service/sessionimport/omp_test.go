package sessionimport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const ompSession = "01a17cf3-99d0-76cd-b084-4cdce17a0fec"

// ompFile names a transcript the way OMP does: <ISO-time>_<uuid>.jsonl under a
// dashed-cwd folder. Shapes below are synthetic but mirror a real session v3.
func ompFile(root string) string {
	return filepath.Join(root, "sessions", "-work-repo", "2026-10-27T10-41-41-840Z_"+ompSession+".jsonl")
}

func TestOMPTranscriptKeepsOnlyUserAndAssistantText(t *testing.T) {
	// A linear chain (b1 -> m1 -> m2 -> m3 -> m4 -> c2); non-message entries
	// (model_change, custom) keep the parent chain connected but show nothing.
	body := `{"type":"session","version":3,"id":"` + ompSession + `","timestamp":"2026-10-27T10:41:41.840Z","cwd":"/work/repo"}
{"type":"title","v":1,"title":"","updatedAt":"2026-10-27T10:41:41.840Z"}
{"type":"model_change","id":"b1","parentId":null,"timestamp":"2026-10-27T10:41:41.913Z","model":"zai/glm-5"}
{"type":"message","id":"m1","parentId":"b1","timestamp":"2026-10-27T10:41:42.000Z","message":{"role":"user","content":[{"type":"text","text":"start the refactor"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-27T10:41:43.000Z","message":{"role":"assistant","content":[{"type":"thinking","text":"hidden reasoning"},{"type":"toolCall","text":"run tests"},{"type":"text","text":"Working on it."}]}}
{"type":"custom","customType":"tool_execution_start","id":"c1","parentId":"m2","timestamp":"2026-10-27T10:41:44.000Z","data":{}}
{"type":"message","id":"m3","parentId":"m2","timestamp":"2026-10-27T10:41:45.000Z","message":{"role":"toolResult","content":[{"type":"text","text":"tool output noise"}]}}
{"type":"title_change","title":"Refactor the parser"}
{"type":"message","id":"m4","parentId":"m3","timestamp":"2026-10-27T10:41:46.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Done."}]}}
{"type":"custom","customType":"session_exit","id":"c2","parentId":"m4","timestamp":"2026-10-27T10:41:47.000Z","data":{}}
`
	root := t.TempDir()
	path := writeFile(t, ompFile(root), body)
	got, err := readTranscript(context.Background(), domain.HarnessOMP, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: start the refactor", "assistant: Working on it.", "assistant: Done.")
	if got.nativeID != ompSession {
		t.Fatalf("nativeID = %q, want %q", got.nativeID, ompSession)
	}
	if got.cwd != "/work/repo" {
		t.Fatalf("cwd = %q", got.cwd)
	}
	if got.title != "Refactor the parser" {
		t.Fatalf("title = %q", got.title)
	}
	if got.shortened {
		t.Fatal("shortened should be false")
	}
}

// A rewind appends a replacement under an earlier parent, so one parent has two
// children. Only the branch reachable from the last entry (the leaf) is shown.
func TestOMPShowsOnlyActiveBranch(t *testing.T) {
	body := `{"type":"session","version":3,"id":"` + ompSession + `","timestamp":"2026-10-27T10:41:41.840Z","cwd":"/work/repo"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-27T10:41:42.000Z","message":{"role":"user","content":[{"type":"text","text":"keep this"}]}}
{"type":"message","id":"a1","parentId":"m1","timestamp":"2026-10-27T10:41:43.000Z","message":{"role":"assistant","content":[{"type":"text","text":"abandoned answer"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-27T10:41:44.000Z","message":{"role":"user","content":[{"type":"text","text":"redo it"}]}}
{"type":"message","id":"a2","parentId":"m2","timestamp":"2026-10-27T10:41:45.000Z","message":{"role":"assistant","content":[{"type":"text","text":"active answer"}]}}
`
	root := t.TempDir()
	got, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), body))
	if err != nil {
		t.Fatal(err)
	}
	// a1 ("abandoned answer") is a sibling of m2 off the active path and must not show.
	wantTexts(t, got.messages, "user: keep this", "user: redo it", "assistant: active answer")
}

func TestOMPTitleFallsBackToFirstPrompt(t *testing.T) {
	body := `{"type":"session","version":3,"id":"` + ompSession + `","timestamp":"2026-10-27T10:41:41.840Z","cwd":"/work/repo"}
{"type":"message","id":"m1","parentId":null,"timestamp":"2026-10-27T10:41:42.000Z","message":{"role":"user","content":[{"type":"text","text":"explain the build"}]}}
{"type":"message","id":"m2","parentId":"m1","timestamp":"2026-10-27T10:41:43.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Sure."}]}}
`
	root := t.TempDir()
	got, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), body))
	if err != nil {
		t.Fatal(err)
	}
	if got.title != "explain the build" {
		t.Fatalf("title = %q, want first prompt", got.title)
	}
}

func TestOMPRejectsIdentityMismatchAndPromptlessSessions(t *testing.T) {
	root := t.TempDir()
	// The session header's id must match the id in the file name.
	mismatch := `{"type":"session","version":3,"id":"00000000-0000-7000-8000-000000000000","cwd":"/work/repo"}
{"type":"message","id":"m1","parentId":null,"message":{"role":"user","content":[{"type":"text","text":"hi"}]}}
`
	if _, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), mismatch)); err == nil {
		t.Fatal("expected native identity mismatch")
	}
	// A session with no user prompt is not a conversation to continue.
	noPrompt := `{"type":"session","version":3,"id":"` + ompSession + `","cwd":"/work/repo"}
{"type":"message","id":"a1","parentId":null,"message":{"role":"assistant","content":[{"type":"text","text":"hello"}]}}
`
	if _, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), noPrompt)); err == nil {
		t.Fatal("expected promptless session to be rejected")
	}
}

func TestOMPNativeIDFromPath(t *testing.T) {
	if got := ompNativeIDFromPath("2026-10-27T10-41-41-840Z_" + ompSession + ".jsonl"); got != ompSession {
		t.Fatalf("name only: got %q, want %q", got, ompSession)
	}
	if got := ompNativeIDFromPath("/root/sessions/-a-b/2026-10-27T10-41-41-840Z_" + ompSession + ".jsonl"); got != ompSession {
		t.Fatalf("full path: got %q, want %q", got, ompSession)
	}
	if got := ompNativeIDFromPath("noseparator.jsonl"); got != "noseparator" {
		t.Fatalf("no separator: got %q, want %q", got, "noseparator")
	}
	// sourceFiles derives the same grouping id from the file name.
	f := sourceFiles{harness: domain.HarnessOMP, paths: []string{ompFile("/root")}}
	if got := f.nativeID(); got != ompSession {
		t.Fatalf("sourceFiles.nativeID() = %q, want %q", got, ompSession)
	}
}

func TestOMPDiscoverImportAndRefresh(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	cwd := t.TempDir()
	encodedCWD, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"type":"title","title":"Synthetic OMP chat"}` + "\n" +
		`{"type":"session","id":"` + ompSession + `","cwd":` + string(encodedCWD) + `}` + "\n" +
		`{"type":"message","id":"m1","parentId":null,"message":{"role":"user","content":"original prompt"}}` + "\n"
	path := writeFile(t, ompFile(root), body)
	writeFile(t, path+".lock.os", "not a transcript")
	st := &importStore{projects: []domain.ProjectRecord{{ID: "omp-project", Path: cwd}}}
	svc := New(st, "")
	svc.roots = map[domain.AgentHarness]string{domain.HarnessOMP: root}
	preview, err := svc.Scan(ctx)
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("scan = %#v, %v", preview, err)
	}
	if got := preview.Candidates[0]; got.Harness != domain.HarnessOMP || got.Title != "Synthetic OMP chat" || got.ProjectID != "omp-project" || got.MessageCount != 1 {
		t.Fatalf("candidate = %#v", got)
	}
	results, err := svc.Import(ctx, []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %#v, %v", results, err)
	}
	rec := st.sessions[0]
	if rec.Metadata.ImportSource.NativeID != ompSession || rec.Metadata.NativeTranscriptPath != path {
		t.Fatalf("import lost its native identity: %#v", rec.Metadata)
	}
	body += `{"type":"message","id":"m2","parentId":"m1","message":{"role":"assistant","content":"later reply"}}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := svc.RefreshArchive(ctx, rec); err != nil {
		t.Fatal(err)
	}
	wantTexts(t, st.messages, "user: original prompt", "assistant: later reply")
}

func TestOMPOversizedAndTornRecordsKeepTheConversation(t *testing.T) {
	oldRecord, oldString := maxRecordBytes, maxSkimString
	maxRecordBytes, maxSkimString = 4096, 1024
	t.Cleanup(func() { maxRecordBytes, maxSkimString = oldRecord, oldString })
	root := t.TempDir()
	body := `{"type":"session","id":"` + ompSession + `","cwd":"/work/repo"}` + "\n" +
		`{"type":"message","id":"m1","parentId":null,"message":{"role":"user","content":"keep the prompt"}}` + "\n" +
		`{"type":"message","id":"tool","parentId":"m1","message":{"role":"toolResult","content":"` + strings.Repeat("x", maxRecordBytes+1) + `"}}` + "\n" +
		`{"type":"message","id":"a1","parentId":"tool","message":{"role":"assistant","content":"keep the answer"}}` + "\n" +
		`{"type":"message","id":"torn"`
	got, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), body))
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: keep the prompt", "assistant: keep the answer")
}

func TestOMPRootOverrideAndDefault(t *testing.T) {
	t.Setenv(ompConfigDirEnv, "")
	if got := ompRoot("/synthetic/home"); got != filepath.Join("/synthetic/home", ".omp", "agent") {
		t.Fatalf("default root = %q", got)
	}
	t.Setenv(ompConfigDirEnv, "/custom/omp")
	if got := New(&importStore{}, "").roots[domain.HarnessOMP]; got != "/custom/omp" {
		t.Fatalf("override root = %q", got)
	}
}

func TestOMPModelAndEffortFollowTheActiveBranch(t *testing.T) {
	body := `{"type":"session","id":"` + ompSession + `","cwd":"/work/repo"}
{"type":"model_change","id":"model","parentId":null,"model":"synthetic/original"}
{"type":"thinking_level_change","id":"effort","parentId":"model","thinkingLevel":"medium"}
{"type":"message","id":"m1","parentId":"effort","message":{"role":"user","content":"keep this"}}
{"type":"model_change","id":"abandoned","parentId":"m1","model":"synthetic/abandoned"}
{"type":"message","id":"a1","parentId":"m1","message":{"role":"assistant","content":"active answer"}}
`
	root := t.TempDir()
	got, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), body))
	if err != nil {
		t.Fatal(err)
	}
	if got.model != "synthetic/original" || got.effort != "medium" {
		t.Fatalf("active settings = %q, %q", got.model, got.effort)
	}
}

func TestOMPBoundsHeldTextAcrossAbandonedBranches(t *testing.T) {
	oldHeld := maxClaudeHeld
	maxClaudeHeld = 128
	t.Cleanup(func() { maxClaudeHeld = oldHeld })
	body := `{"type":"session","id":"` + ompSession + `","cwd":"/work/repo"}` + "\n" +
		`{"type":"message","id":"m1","parentId":null,"message":{"role":"user","content":"opening prompt"}}` + "\n" +
		`{"type":"message","id":"a1","parentId":"m1","message":{"role":"assistant","content":"` + strings.Repeat("x", 100) + `"}}` + "\n" +
		`{"type":"message","id":"abandoned","parentId":"a1","message":{"role":"assistant","content":"` + strings.Repeat("y", 100) + `"}}` + "\n" +
		`{"type":"message","id":"a2","parentId":"a1","message":{"role":"assistant","content":"latest answer"}}` + "\n"
	root := t.TempDir()
	got, err := readTranscript(context.Background(), domain.HarnessOMP, root, writeFile(t, ompFile(root), body))
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: opening prompt", "assistant: latest answer")
	if !got.shortened {
		t.Fatal("text discarded to bound memory was not reported as shortened")
	}
}
