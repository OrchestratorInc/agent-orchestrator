package usage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func writeLargeJCodeArtifact(t *testing.T, path, prefix, suffix string) {
	t.Helper()
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	file, err := os.Create(path)
	mustNoError(t, err)
	t.Cleanup(func() { _ = file.Close() })
	write := func(text string) {
		_, err := io.WriteString(file, text)
		mustNoError(t, err)
	}
	write(prefix)
	chunk := strings.Repeat("x", 1<<20)
	for range 65 {
		write(chunk)
	}
	write(suffix)
	mustNoError(t, file.Close())
}

func TestJCodeUsageReadsPastLargeToolContent(t *testing.T) {
	for _, artifact := range []string{"snapshot", "journal"} {
		t.Run(artifact, func(t *testing.T) {
			store := collectorTestStore(t)
			session := collectorTestSession(t, store, domain.HarnessJCode, jcodeTestNativeID, false)
			home, root := t.TempDir(), t.TempDir()
			path := filepath.Join(home, "sessions", jcodeTestNativeID+".json")
			if artifact == "snapshot" {
				writeLargeJCodeArtifact(t, path,
					`{"id":"`+jcodeTestNativeID+`","model":"claude-opus-5-5","provider_key":"claude","messages":[{"role":"tool","content":"`,
					`"},`+jcodeAssistant1+`]}`)
			} else {
				writeJCodeSession(t, home, jcodeUser)
				writeLargeJCodeArtifact(t, filepath.Join(home, "sessions", jcodeTestNativeID+".journal.jsonl"),
					`{"append_messages":[{"role":"tool","content":"`,
					`"},`+jcodeAssistant1+`],"meta":{"model":"claude-opus-5-5","provider_key":"claude"}}`+"\n")
			}
			collector := NewCollector(store, SourceRoots{JCodeHome: home, JCodeLedger: root}, nil)
			mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{Event: "stop", NativeSessionID: jcodeTestNativeID}))
			ledger := filepath.Join(root, jcodeTestNativeID+".jsonl")
			if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2" {
				t.Fatalf("usage after >64 MiB tool content = %s", got)
			}
			if info, err := os.Stat(ledger); err != nil || info.Size() > 2048 {
				t.Fatalf("ledger retained tool content: %v, %v", info, err)
			}
		})
	}
}

func TestJCodeIncrementalReadsSurvivePartialJournalAndCheckpoint(t *testing.T) {
	store := collectorTestStore(t)
	session := collectorTestSession(t, store, domain.HarnessJCode, jcodeTestNativeID, false)
	home, root := t.TempDir(), t.TempDir()
	writeJCodeSession(t, home, jcodeAssistant1)
	journal := filepath.Join(home, "sessions", jcodeTestNativeID+".journal.jsonl")
	entry := `{"append_messages":[` + jcodeAssistant2 + "]}\n"
	writeUsageFixture(t, journal, entry[:len(entry)/2])
	collector := NewCollector(store, SourceRoots{JCodeHome: home, JCodeLedger: root}, nil)
	hook := func() {
		t.Helper()
		mustNoError(t, collector.RecordHook(context.Background(), session.ID, HookSignal{Event: "stop", NativeSessionID: jcodeTestNativeID}))
	}
	ledger := filepath.Join(root, jcodeTestNativeID+".jsonl")
	hook()
	if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2" {
		t.Fatalf("partial journal exported %s", got)
	}
	file, err := os.OpenFile(journal, os.O_APPEND|os.O_WRONLY, 0o600)
	mustNoError(t, err)
	_, err = file.WriteString(entry[len(entry)/2:])
	mustNoError(t, err)
	mustNoError(t, file.Close())
	hook()
	hook() // unchanged snapshot/journal must not re-export a message
	if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2,message_4" {
		t.Fatalf("completed journal exported %s", got)
	}
	writeJCodeSession(t, home, jcodeAssistant1, jcodeAssistant2, jcodeAssistant3)
	writeUsageFixture(t, journal, "")
	hook()
	if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2,message_4,message_6" {
		t.Fatalf("checkpoint exported %s", got)
	}
	mustNoError(t, os.Remove(ledger))
	hook()
	if got := strings.Join(readLedgerIDs(t, ledger), ","); got != "message_2,message_4,message_6" {
		t.Fatalf("ledger recreation lost cached native usage: %s", got)
	}
}
