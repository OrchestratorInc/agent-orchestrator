package usage

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const jcodeTestNativeID = "session_elephant_1793375224099_dccf0b8667353cd1"

// writeJCodeSession writes a jcode v0.99.1-shaped session file: one JSON
// document on a single line, rewritten in place on every save.
func writeJCodeSession(t *testing.T, home string, messages ...string) {
	t.Helper()
	body := `{"id":"` + jcodeTestNativeID + `","title":"t","model":"claude-opus-5-5","provider_key":"claude","working_dir":"/repo","messages":[` + strings.Join(messages, ",") + `]}`
	writeUsageFixture(t, filepath.Join(home, "sessions", jcodeTestNativeID+".json"), body)
}

const (
	jcodeUser       = `{"id":"message_1","role":"user","content":[],"timestamp":"2026-10-30T15:41:19.111Z"}`
	jcodeAssistant1 = `{"id":"message_2","role":"assistant","content":[],"timestamp":"2026-10-30T15:41:21.634Z","token_usage":{"prompt_tokens":26544,"input_tokens":423,"output_tokens":225,"cache_read_input_tokens":18804,"cache_creation_input_tokens":7317}}`
	jcodeToolResult = `{"id":"message_3","role":"user","content":[],"timestamp":"2026-10-30T15:41:21.664Z","tool_duration_ms":12}`
	jcodeAssistant2 = `{"id":"message_4","role":"assistant","content":[],"timestamp":"2026-10-30T15:41:23.277Z","token_usage":{"prompt_tokens":26830,"input_tokens":61,"output_tokens":108,"cache_read_input_tokens":26121,"cache_creation_input_tokens":648}}`
	jcodeAssistant3 = `{"id":"message_6","role":"assistant","content":[],"timestamp":"2026-10-30T15:42:00.000Z","token_usage":{"prompt_tokens":27000,"input_tokens":20,"output_tokens":9,"cache_read_input_tokens":26900,"cache_creation_input_tokens":80}}`
)

func readLedgerIDs(t *testing.T, path string) []string {
	t.Helper()
	file, err := os.Open(path) //nolint:gosec // test-owned path
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	defer func() { _ = file.Close() }()
	var ids []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		var line struct {
			Type, ID, Model, Provider string
		}
		if err := json.Unmarshal(scanner.Bytes(), &line); err != nil || line.Type != "jcode.usage" || line.Model != "claude-opus-5-5" || line.Provider != "claude" {
			t.Fatalf("ledger line %q: %v", scanner.Text(), err)
		}
		ids = append(ids, line.ID)
	}
	return ids
}

func TestCollectorExportsJCodeMessageUsageOnceAcrossRewrites(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessJCode, jcodeTestNativeID, false)
	jcodeHome := t.TempDir()
	ledgerRoot := filepath.Join(t.TempDir(), "jcode", "usage")
	writeJCodeSession(t, jcodeHome, jcodeUser, jcodeAssistant1, jcodeToolResult, jcodeAssistant2)
	collector := NewCollector(store, SourceRoots{JCodeHome: jcodeHome, JCodeLedger: ledgerRoot}, nil)
	ctx := context.Background()

	mustNoError(t, collector.RecordHook(ctx, session.ID, HookSignal{Harness: domain.HarnessJCode, Event: "stop", NativeSessionID: jcodeTestNativeID}))
	ledger := filepath.Join(ledgerRoot, jcodeTestNativeID+".jsonl")
	if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2,message_4" {
		t.Fatalf("ledger ids = %s", got)
	}
	bindings, err := store.ListUsageBindingsForSession(ctx, session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
	sources, err := store.ListUsageSourcesForBinding(ctx, bindings[0].ID)
	if err != nil || len(sources) != 1 || sources[0].Kind != domain.UsageSourceJCodeLedger ||
		sources[0].ArtifactPath != canonicalUsagePath(t, ledger) || sources[0].NativeSessionID != jcodeTestNativeID {
		t.Fatalf("sources=%+v err=%v", sources, err)
	}

	// jcode rewrites the whole file each save; a resumed session lists every
	// earlier message again. Only the new message may be appended.
	writeJCodeSession(t, jcodeHome, jcodeUser, jcodeAssistant1, jcodeToolResult, jcodeAssistant2, jcodeAssistant3)
	mustNoError(t, collector.RecordHook(ctx, session.ID, HookSignal{Harness: domain.HarnessJCode, Event: "stop", NativeSessionID: jcodeTestNativeID}))
	mustNoError(t, collector.RecordHook(ctx, session.ID, HookSignal{Harness: domain.HarnessJCode, Event: "user-prompt-submit", NativeSessionID: jcodeTestNativeID}))
	if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2,message_4,message_6" {
		t.Fatalf("ledger ids after rewrite = %s", got)
	}
	if info, err := os.Stat(ledger); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("ledger mode = %v, %v", info, err)
	}
}

// Between checkpoints jcode appends each save's new messages to
// sessions/<id>.journal.jsonl and leaves the snapshot alone, so a finished
// turn can exist only in the journal. A journal entry may repeat snapshot
// messages, and its last line can be half written.
func TestCollectorExportsJCodeUsageFromTheSessionJournal(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessJCode, jcodeTestNativeID, false)
	jcodeHome := t.TempDir()
	ledgerRoot := filepath.Join(t.TempDir(), "jcode", "usage")
	writeJCodeSession(t, jcodeHome, jcodeUser, jcodeAssistant1)
	meta := `"meta":{"model":"claude-opus-5-5","provider_key":"claude","status":"active"}`
	journal := `{` + meta + `,"append_messages":[` + jcodeAssistant1 + `]}` + "\n" +
		`{` + meta + `,"append_messages":[` + jcodeToolResult + `,` + jcodeAssistant2 + `]}` + "\n" +
		`{` + meta + `,"append_messages":[` + jcodeAssistant3[:40]
	writeUsageFixture(t, filepath.Join(jcodeHome, "sessions", jcodeTestNativeID+".journal.jsonl"), journal)
	collector := NewCollector(store, SourceRoots{JCodeHome: jcodeHome, JCodeLedger: ledgerRoot}, nil)
	ctx := context.Background()

	mustNoError(t, collector.RecordHook(ctx, session.ID, HookSignal{Harness: domain.HarnessJCode, Event: "stop", NativeSessionID: jcodeTestNativeID}))
	ledger := filepath.Join(ledgerRoot, jcodeTestNativeID+".jsonl")
	if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2,message_4" {
		t.Fatalf("ledger ids = %s, want snapshot and journal messages once each", got)
	}

	// The half-written entry completes on the next save.
	writeUsageFixture(t, filepath.Join(jcodeHome, "sessions", jcodeTestNativeID+".journal.jsonl"), journal[:len(journal)-len(jcodeAssistant3[:40])]+jcodeAssistant3+"]}\n")
	mustNoError(t, collector.RecordHook(ctx, session.ID, HookSignal{Harness: domain.HarnessJCode, Event: "stop", NativeSessionID: jcodeTestNativeID}))
	if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2,message_4,message_6" {
		t.Fatalf("ledger ids after the entry completed = %s", got)
	}
}

func TestDiscoverJCodePathRejectsUnsafeIDsAndMissingSessions(t *testing.T) {
	jcodeHome := t.TempDir()
	ledgerRoot := filepath.Join(t.TempDir(), "usage")
	writeJCodeSession(t, jcodeHome, jcodeAssistant1)
	collector := NewCollector(collectorTestStore(t), SourceRoots{JCodeHome: jcodeHome, JCodeLedger: ledgerRoot}, nil)
	for _, id := range []string{"*", "../escape", "session_missing_1793375224099_0000000000000000", "fox"} {
		path, err := collector.discoverPath(context.Background(), domain.HarnessJCode, id)
		if err != nil || path != "" {
			t.Fatalf("discoverPath(%q) = %q, %v; want no source", id, path, err)
		}
	}
	if entries, _ := os.ReadDir(ledgerRoot); len(entries) != 0 {
		t.Fatalf("ledger created for unsafe ids: %v", entries)
	}
}

func TestDefaultSourceRootsIncludesJCodeHomeAndLedger(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, ".ao", "data")
	t.Setenv("HOME", home)
	t.Setenv("JCODE_HOME", "")
	got, err := DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.JCodeHome != filepath.Join(home, ".jcode") || got.JCodeLedger != filepath.Join(dataDir, "jcode", "usage") {
		t.Fatalf("roots = %+v", got)
	}
	t.Setenv("JCODE_HOME", filepath.Join(home, "custom-jcode"))
	got, err = DefaultSourceRoots(context.Background(), dataDir)
	mustNoError(t, err)
	if got.JCodeHome != filepath.Join(home, "custom-jcode") {
		t.Fatalf("JCODE_HOME ignored: %+v", got)
	}
}

// Kill finalizes without a further jcode hook; the turn saved after the last
// hook must still reach the ledger.
func TestFinalizeSessionExportsJCodeUsageSavedAfterLastHook(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessJCode, jcodeTestNativeID, false)
	session.Metadata.RuntimeLaunchID = "launch-1"
	mustNoError(t, store.UpdateSession(context.Background(), session))
	jcodeHome := t.TempDir()
	ledgerRoot := filepath.Join(t.TempDir(), "usage")
	writeJCodeSession(t, jcodeHome, jcodeUser, jcodeAssistant1)
	collector := NewCollector(store, SourceRoots{JCodeHome: jcodeHome, JCodeLedger: ledgerRoot}, nil)
	ctx := context.Background()
	mustNoError(t, collector.RecordHook(ctx, session.ID, HookSignal{Harness: domain.HarnessJCode, Event: "stop", LaunchID: "launch-1", NativeSessionID: jcodeTestNativeID}))

	writeJCodeSession(t, jcodeHome, jcodeUser, jcodeAssistant1, jcodeToolResult, jcodeAssistant2)
	current, _, err := store.GetSession(ctx, session.ID)
	mustNoError(t, err)
	mustNoError(t, collector.FinalizeSession(ctx, session.ID, "launch-1", current.Revision))
	if got := strings.Join(readLedgerIDs(t, filepath.Join(ledgerRoot, jcodeTestNativeID+".jsonl")), ","); got != "message_2,message_4" {
		t.Fatalf("ledger ids after finalize = %s", got)
	}
}
