package sessionimport

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const copilotTestID = "41fc8602-ec68-477c-9e91-f49a531ed5c0"

func copilotEvent(t *testing.T, kind, id string, data map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"type": kind, "id": id, "timestamp": time.Now().UTC().Truncate(time.Second), "data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body) + "\n"
}

// subagentEvent is a child-agent event marked only by the top-level agentId.
func subagentEvent(t *testing.T, kind, id, agentID string, data map[string]any) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"type": kind, "id": id, "agentId": agentID, "timestamp": time.Now().UTC().Truncate(time.Second), "data": data,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body) + "\n"
}

func copilotFixture(t *testing.T, root, id, cwd string) string {
	t.Helper()
	body := copilotEvent(t, "session.start", "start", map[string]any{"sessionId": id, "context": map[string]string{"cwd": cwd}}) +
		copilotEvent(t, "user.message", "u1", map[string]any{"content": "Fix <native> parsing", "transformedContent": "INJECTED WORKSPACE AND TOOL CONTEXT"}) +
		copilotEvent(t, "assistant.message", "a1", map[string]any{"content": "I will keep your existing history.", "reasoningText": "PRIVATE REASONING"})
	return writeFile(t, filepath.Join(root, "session-state", id, "events.jsonl"), body)
}

func TestCopilotTranscriptShowsTypedMessagesAndMetadata(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	path := copilotFixture(t, root, copilotTestID, cwd)
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte(copilotEvent(t, "system.message", "s1", map[string]any{"content": "INJECTED SYSTEM"})+
		copilotEvent(t, "model.messages_snapshot", "m1", map[string]any{"content": "INTERNAL SNAPSHOT"})+
		copilotEvent(t, "tool.execution_complete", "tool", map[string]any{"content": "TOOL OUTPUT"})+
		copilotEvent(t, "assistant.message_delta", "delta", map[string]any{"content": "DUPLICATE STREAM"})+
		copilotEvent(t, "assistant.message", "a1", map[string]any{"content": "I will keep your existing history."})+"{torn\n")...)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	name, _ := json.Marshal("  Copilot   parser work  ")
	writeFile(t, filepath.Join(filepath.Dir(path), "workspace.yaml"), "id: "+copilotTestID+"\nname: "+string(name)+"\n")
	got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Fix <native> parsing", "assistant: I will keep your existing history.")
	if got.nativeID != copilotTestID || got.cwd != cwd || got.title != "Copilot parser work" || got.path != path || got.last.IsZero() || got.shortened {
		t.Fatalf("unexpected native metadata: %+v", got)
	}
	if got.messages[0].At.IsZero() {
		t.Fatal("original message timestamps were not retained")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || string(unchanged) != string(body) {
		t.Fatal("reading modified the native transcript")
	}
}

func TestCopilotResumeContextAndWorkspaceTitle(t *testing.T) {
	root := t.TempDir()
	path := copilotFixture(t, root, copilotTestID, t.TempDir())
	latest := t.TempDir()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(copilotEvent(t, "session.resume", "resume", map[string]any{"context": map[string]string{"cwd": latest}}))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append resume: %v, %v", writeErr, closeErr)
	}
	got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil || got.cwd != latest || got.title != "Fix <native> parsing" {
		t.Fatalf("latest context/fallback title = %+v, %v", got, err)
	}
	// A malformed sidecar must not hide the conversation in events.jsonl.
	writeFile(t, filepath.Join(filepath.Dir(path), "workspace.yaml"), "name: [broken\n")
	if _, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path); err != nil {
		t.Fatal(err)
	}
}

func TestCopilotRejectsUnusableIdentityOrHistory(t *testing.T) {
	for _, kind := range []string{"mismatched header", "missing header", "assistant only", "relative cwd", "metadata mismatch"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := copilotFixture(t, root, copilotTestID, t.TempDir())
			header := copilotEvent(t, "session.start", "start", map[string]any{"sessionId": copilotTestID, "context": map[string]string{"cwd": "/work"}})
			user := copilotEvent(t, "user.message", "u", map[string]any{"content": "hello"})
			switch kind {
			case "mismatched header":
				header = strings.ReplaceAll(header, copilotTestID, "00000000-0000-4000-8000-000000000000")
			case "missing header":
				header = ""
			case "assistant only":
				user = copilotEvent(t, "assistant.message", "a", map[string]any{"content": "reply without a prompt"})
			case "relative cwd":
				header = strings.ReplaceAll(header, "/work", "relative/work")
			case "metadata mismatch":
				writeFile(t, filepath.Join(filepath.Dir(path), "workspace.yaml"), "id: 00000000-0000-4000-8000-000000000000\nname: wrong chat\n")
			}
			writeFile(t, path, header+user)
			if _, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path); err == nil {
				t.Fatal("unusable native conversation accepted")
			}
		})
	}
}

func TestCopilotDiscoveryUsesOnlyTopLevelNativeSessions(t *testing.T) {
	root := t.TempDir()
	path := copilotFixture(t, root, copilotTestID, t.TempDir())
	writeFile(t, filepath.Join(filepath.Dir(path), "session.db"), "not a transcript")
	writeFile(t, filepath.Join(filepath.Dir(path), "agents", copilotTestID, "events.jsonl"), "{}\n")
	writeFile(t, filepath.Join(root, "session-state", "not-a-uuid", "events.jsonl"), "{}\n")
	writeFile(t, filepath.Join(root, "session-state", "22222222-2222-4222-8222-222222222222", "workspace.yaml"), "name: no transcript\n")
	files, err := discoverFor(context.Background(), domain.HarnessCopilot, root, nil)
	if err != nil || len(files) != 1 || files[0].nativeID() != copilotTestID || len(files[0].paths) != 1 || files[0].paths[0] != path {
		t.Fatalf("native conversation discovery = %+v, %v", files, err)
	}
	before := files[0].cacheKey()
	writeFile(t, filepath.Join(filepath.Dir(path), "workspace.yaml"), "id: "+copilotTestID+"\nname: changed title\n")
	files, err = discoverFor(context.Background(), domain.HarnessCopilot, root, nil)
	if err != nil || len(files) != 1 || files[0].cacheKey() == before {
		t.Fatalf("metadata change did not invalidate preview cache: %+v, %v", files, err)
	}
}

func TestCopilotConfinesTranscriptAndMetadataReads(t *testing.T) {
	root := t.TempDir()
	path := copilotFixture(t, root, copilotTestID, t.TempDir())
	outside := writeFile(t, filepath.Join(t.TempDir(), "workspace.yaml"), "name: OUTSIDE PROVIDER HOME\n")
	if err := os.Symlink(outside, filepath.Join(filepath.Dir(path), "workspace.yaml")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil || got.title != "Fix <native> parsing" {
		t.Fatalf("out-of-root metadata was followed: %+v, %v", got, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside = copilotFixture(t, t.TempDir(), copilotTestID, t.TempDir())
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	if _, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path); err == nil {
		t.Fatal("out-of-root transcript symlink accepted")
	}
}

func TestCopilotOversizedRecordsKeepVisibleText(t *testing.T) {
	oldRecord, oldString := maxRecordBytes, maxSkimString
	maxRecordBytes, maxSkimString = 1<<10, 256
	t.Cleanup(func() { maxRecordBytes, maxSkimString = oldRecord, oldString })
	root := t.TempDir()
	path := copilotFixture(t, root, copilotTestID, t.TempDir())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(copilotEvent(t, "assistant.message", "large", map[string]any{"content": "Visible reply", "reasoningText": strings.Repeat("hidden", 2000)}))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append: %v, %v", writeErr, closeErr)
	}
	got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Fix <native> parsing", "assistant: I will keep your existing history.", "assistant: Visible reply")
}

func TestCopilotScanImportRefreshAndDeduplication(t *testing.T) {
	root, project := t.TempDir(), t.TempDir()
	path := copilotFixture(t, root, copilotTestID, project)
	store := &importStore{projects: []domain.ProjectRecord{{ID: "repo", Path: project}}}
	t.Setenv("COPILOT_HOME", root)
	svc := New(store, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessCopilot: svc.roots[domain.HarnessCopilot]}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 {
		t.Fatalf("Copilot preview = %+v, %v", preview, err)
	}
	candidate := preview.Candidates[0]
	if candidate.Harness != domain.HarnessCopilot || candidate.ProjectID != "repo" || candidate.MessageCount != 2 {
		t.Fatalf("candidate = %+v", candidate)
	}
	results, err := svc.Import(context.Background(), []string{candidate.ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" {
		t.Fatalf("import = %+v, %v", results, err)
	}
	rec := store.sessions[0]
	if rec.Harness != domain.HarnessCopilot || rec.Metadata.ProviderConversationID != copilotTestID || rec.Metadata.AgentSessionID != copilotTestID || canonical(rec.Metadata.NativeTranscriptPath) != canonical(path) || rec.Metadata.ImportSource.ConfigDir != canonical(root) || !rec.NeedsImportResume() {
		t.Fatalf("native continuation binding = %+v", rec)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(copilotEvent(t, "user.message", "u2", map[string]any{"content": "A turn written before first open"}))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append: %v, %v", writeErr, closeErr)
	}
	if err := svc.RefreshArchive(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	wantTexts(t, store.messages, "user: Fix <native> parsing", "assistant: I will keep your existing history.", "user: A turn written before first open")
	preview, err = svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 1 || preview.Candidates[0].SessionID != rec.ID {
		t.Fatalf("repeat scan = %+v, %v", preview, err)
	}
	results, err = svc.Import(context.Background(), []string{candidate.ID})
	if err != nil || results[0].Status != "already_imported" || len(store.sessions) != 1 {
		t.Fatalf("duplicate import = %+v, %v", results, err)
	}
}

func TestCopilotDefaultRootAndManagedHistories(t *testing.T) {
	t.Setenv("COPILOT_HOME", "")
	svc := New(&importStore{}, "", chatSupported)
	if svc.roots[domain.HarnessCopilot] != filepath.Join(svc.home, ".copilot") {
		t.Fatalf("default Copilot root = %q", svc.roots[domain.HarnessCopilot])
	}
	root := t.TempDir()
	copilotFixture(t, root, copilotTestID, t.TempDir())
	svc = New(&importStore{sessions: []domain.SessionRecord{{Harness: domain.HarnessCopilot, Metadata: domain.SessionMetadata{ProviderConversationID: copilotTestID}}}}, "", chatSupported)
	svc.roots = map[domain.AgentHarness]string{domain.HarnessCopilot: root}
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 0 {
		t.Fatalf("AO-managed conversation offered again: %+v, %v", preview, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readTranscript(ctx, domain.HarnessCopilot, root, filepath.Join(root, "session-state", copilotTestID, "events.jsonl")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled parse = %v", err)
	}
}

func TestCopilotHidesInjectedAndSubagentMessages(t *testing.T) {
	root := t.TempDir()
	path := copilotFixture(t, root, copilotTestID, t.TempDir())
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := f.WriteString(copilotEvent(t, "user.message", "skill", map[string]any{"content": "HIDDEN SKILL", "source": "skill-pdf"}) +
		copilotEvent(t, "user.message", "agent", map[string]any{"content": "INTER-AGENT PROMPT", "source": "agent-research"}) +
		copilotEvent(t, "user.message", "auto", map[string]any{"content": "AUTOPILOT CONTINUATION", "isAutopilotContinuation": true}) +
		copilotEvent(t, "assistant.message", "child", map[string]any{"content": "CHILD RESPONSE", "parentToolCallId": "task-tool"}) +
		subagentEvent(t, "assistant.message", "child-agent", "agent-1", map[string]any{"content": "SUBAGENT RESPONSE", "messageId": "m-child"}) +
		subagentEvent(t, "user.message", "child-prompt", "agent-1", map[string]any{"content": "SUBAGENT PROMPT"}) +
		copilotEvent(t, "session.title_changed", "title", map[string]any{"title": "User-visible title"}))
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("append: %v, %v", writeErr, closeErr)
	}
	got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Fix <native> parsing", "assistant: I will keep your existing history.")
	if got.title != "User-visible title" {
		t.Fatalf("native title event ignored: %q", got.title)
	}
}

func TestCopilotRewindAndClearKeepOnlyCurrentHistory(t *testing.T) {
	for _, kind := range []string{"range", "explicit event ids", "clear"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := copilotFixture(t, root, copilotTestID, t.TempDir())
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			extra := copilotEvent(t, "user.message", "withdrawn", map[string]any{"content": "Withdraw this prompt"}) +
				copilotEvent(t, "assistant.message", "withdrawn-reply", map[string]any{"content": "Withdraw this reply"})
			if kind == "clear" {
				extra += copilotEvent(t, "session.context_cleared", "control", map[string]any{"messagesCleared": 4})
			} else {
				data := map[string]any{"upToEventId": "withdrawn", "eventsRemoved": 2}
				if kind == "explicit event ids" {
					data["eventIds"] = []string{"withdrawn", "withdrawn-reply"}
				}
				extra += copilotEvent(t, "session.snapshot_rewind", "control", data)
			}
			extra += copilotEvent(t, "user.message", "kept", map[string]any{"content": "Keep this prompt"})
			writeFile(t, path, string(body)+extra)
			got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "clear" {
				wantTexts(t, got.messages, "user: Keep this prompt")
			} else {
				wantTexts(t, got.messages, "user: Fix <native> parsing", "assistant: I will keep your existing history.", "user: Keep this prompt")
			}
		})
	}
}

func TestCopilotHeldHistoryIsBoundedAndKeepsOpeningAndNewest(t *testing.T) {
	oldHeld := maxHeldText
	maxHeldText = 512
	t.Cleanup(func() { maxHeldText = oldHeld })
	root := t.TempDir()
	path := copilotFixture(t, root, copilotTestID, t.TempDir())
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		body = append(body, []byte(copilotEvent(t, "assistant.message", string(rune('b'+i)), map[string]any{"content": strings.Repeat("middle ", 20)}))...)
	}
	body = append(body, []byte(copilotEvent(t, "assistant.message", "latest", map[string]any{"content": "Newest answer"}))...)
	writeFile(t, path, string(body))
	got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.shortened || len(got.messages) >= 13 || got.messages[0].Text != "Fix <native> parsing" || got.messages[len(got.messages)-1].Text != "Newest answer" {
		t.Fatalf("history budget lost its boundaries: %+v", got)
	}
}

func TestCopilotMetadataFallbackAndBudget(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	path := copilotFixture(t, root, copilotTestID, "")
	metadata := filepath.Join(filepath.Dir(path), "workspace.yaml")
	quotedCWD, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, metadata, "cwd: "+string(quotedCWD)+"\nname: metadata title\n")
	got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil || got.cwd != cwd || got.title != "metadata title" {
		t.Fatalf("metadata fallback = %+v, %v", got, err)
	}
	writeFile(t, metadata, "name: "+strings.Repeat("x", copilotMetadataBudget)+"\n")
	got, err = readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil || got.title != "Fix <native> parsing" {
		t.Fatalf("oversized optional metadata hid the conversation: %+v, %v", got, err)
	}
	if err := os.Remove(metadata); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(metadata, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path); err != nil {
		t.Fatalf("non-file optional metadata hid the conversation: %v", err)
	}
}

func TestCopilotExactRewindKeepsInterleavedEvents(t *testing.T) {
	root := t.TempDir()
	path := copilotFixture(t, root, copilotTestID, t.TempDir())
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	extra := copilotEvent(t, "user.message", "withdrawn", map[string]any{"content": "Withdraw this prompt"}) +
		copilotEvent(t, "assistant.message", "interleaved", map[string]any{"content": "Keep this interleaved answer"}) +
		copilotEvent(t, "session.snapshot_rewind", "rewind", map[string]any{"upToEventId": "withdrawn", "eventIds": []string{"withdrawn"}})
	writeFile(t, path, string(body)+extra)
	got, err := readTranscript(context.Background(), domain.HarnessCopilot, root, path)
	if err != nil {
		t.Fatal(err)
	}
	wantTexts(t, got.messages, "user: Fix <native> parsing", "assistant: I will keep your existing history.", "assistant: Keep this interleaved answer")
}
