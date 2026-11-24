package codexappserver

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// Fixtures here are verbatim captures from `codex app-server` (codex-cli 0.146.0),
// not hand-written approximations. That habit has already earned its keep once in
// this package: a real frame revealed a numeric JSON-RPC id being decoded as a
// string, which every invented fixture had spelled as a string and so never caught.

// realTurnDiffFrame is the exact params of a turn/diff/updated notification, from a
// turn that created one file and appended one line to another. Note what it is: one
// aggregated unified diff STRING, not a structured file list. Everything the turn
// view shows has to be read out of this text.
const realTurnDiffFrame = `{"threadId":"019fc22f-d197-7470-b88d-6f24d10e4c4e","turnId":"019fc22f-d320-7682-b553-ede7a2353a9a","diff":"diff --git a/newfile.txt b/newfile.txt\nnew file mode 100644\nindex 0000000000000000000000000000000000000000..ce013625030ba8dba906f756967f9e9ca394464a\n--- /dev/null\n+++ b/newfile.txt\n@@ -0,0 +1 @@\n+hello\ndiff --git a/seed.txt b/seed.txt\nindex 8b05eaec0a58845cf3ade46832033cb5bf618b99..bfdfbcdb37668fc8dd05070f5becf2e4fe311070\n--- a/seed.txt\n+++ b/seed.txt\n@@ -1,3 +1,4 @@\n alpha\n bravo\n charlie\n+delta\n"}`

// realOutputDeltaFrame is the exact params of an item/commandExecution/outputDelta.
// The itemId prefix (`exec-`) and the uuid thread/turn ids are the provider's, kept
// so a shape change in any of the three correlation keys fails here.
const realOutputDeltaFrame = `{"threadId":"019fc233-13cc-7393-a188-b670c47914b3","turnId":"019fc233-1553-76d0-865a-0abd212a1d99","itemId":"exec-7631c23c-79e6-4d69-9a6a-4b70ef5c10eb","delta":"tick-2\n"}`

func TestNormalizeCommandOutputDeltaFromRealFrame(t *testing.T) {
	event := normalizeOne(t, "item/commandExecution/outputDelta", realOutputDeltaFrame)

	if event.Kind != ports.ChatEventCommandOutputDelta {
		t.Fatalf("kind = %q, want %q", event.Kind, ports.ChatEventCommandOutputDelta)
	}
	if event.Delta != "tick-2\n" {
		t.Errorf("delta = %q", event.Delta)
	}
	// The item id is what the projection appends against. Losing it would silently
	// send output to no activity at all.
	if event.ProviderItemID != "exec-7631c23c-79e6-4d69-9a6a-4b70ef5c10eb" {
		t.Errorf("item id = %q", event.ProviderItemID)
	}
	if event.ProviderTurnID != "019fc233-1553-76d0-865a-0abd212a1d99" {
		t.Errorf("turn id = %q", event.ProviderTurnID)
	}
}

func TestNormalizeEmptyOutputDeltaIsDropped(t *testing.T) {
	normalizeNone(t, "item/commandExecution/outputDelta",
		`{"threadId":"t","turnId":"u","itemId":"exec-1","delta":""}`)
}

func TestNormalizeTurnDiffFromRealFrame(t *testing.T) {
	event := normalizeOne(t, "turn/diff/updated", realTurnDiffFrame)

	if event.Kind != ports.ChatEventTurnDiff {
		t.Fatalf("kind = %q, want %q", event.Kind, ports.ChatEventTurnDiff)
	}
	if event.ProviderTurnID != "019fc22f-d320-7682-b553-ede7a2353a9a" {
		t.Errorf("turn id = %q", event.ProviderTurnID)
	}
	if event.Diff == nil {
		t.Fatal("diff is nil")
	}
	if len(event.Diff.Files) != 2 {
		t.Fatalf("files = %d, want 2: %+v", len(event.Diff.Files), event.Diff.Files)
	}

	added := event.Diff.Files[0]
	if added.Path != "newfile.txt" || added.Status != diffStatusAdded {
		t.Errorf("new file = %+v", added)
	}
	// `+++ b/newfile.txt` and `--- /dev/null` must not be counted as a change line.
	if added.Additions != 1 || added.Deletions != 0 {
		t.Errorf("new file counts = +%d -%d, want +1 -0", added.Additions, added.Deletions)
	}

	modified := event.Diff.Files[1]
	if modified.Path != "seed.txt" || modified.Status != diffStatusModified {
		t.Errorf("modified file = %+v", modified)
	}
	if modified.Additions != 1 || modified.Deletions != 0 {
		t.Errorf("modified counts = +%d -%d, want +1 -0", modified.Additions, modified.Deletions)
	}
}

// The provider re-sends an identical diff several times per turn (three times in
// the captured session). Normalization has to be a pure function of the payload,
// or a projector cannot safely treat repeats as idempotent.
func TestNormalizeRepeatedTurnDiffIsIdentical(t *testing.T) {
	first := normalizeOne(t, "turn/diff/updated", realTurnDiffFrame)
	second := normalizeOne(t, "turn/diff/updated", realTurnDiffFrame)

	want, _ := json.Marshal(first.Diff)
	got, _ := json.Marshal(second.Diff)
	if string(want) != string(got) {
		t.Errorf("repeat differed:\n%s\n%s", want, got)
	}
}

// An empty diff is a state, not an absence: a turn that reverted its own edits has
// nothing changed, and a stale file list must not survive that.
func TestNormalizeEmptyTurnDiffStillReports(t *testing.T) {
	event := normalizeOne(t, "turn/diff/updated", `{"threadId":"t","turnId":"u","diff":""}`)
	if event.Diff == nil {
		t.Fatal("empty diff produced no Diff payload")
	}
	if len(event.Diff.Files) != 0 {
		t.Errorf("files = %+v, want none", event.Diff.Files)
	}
}

func TestParseTurnDiffStatuses(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/gone.txt b/gone.txt",
		"deleted file mode 100644",
		"index abc..000",
		"--- a/gone.txt",
		"+++ /dev/null",
		"@@ -1,2 +0,0 @@",
		"-one",
		"-two",
		"diff --git a/old.txt b/new.txt",
		"similarity index 92%",
		"rename from old.txt",
		"rename to new.txt",
		"index abc..def",
		"--- a/old.txt",
		"+++ b/new.txt",
		"@@ -1,2 +1,2 @@",
		" kept",
		"-before",
		"+after",
		"",
	}, "\n")

	files, truncated := parseTurnDiff(diff)
	if truncated {
		t.Error("small diff reported as truncated")
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v", files)
	}

	// A deletion's path lives on the `---` side; `+++ /dev/null` is not a path.
	deleted := files[0]
	if deleted.Path != "gone.txt" || deleted.Status != diffStatusDeleted {
		t.Errorf("deleted = %+v", deleted)
	}
	if deleted.Additions != 0 || deleted.Deletions != 2 {
		t.Errorf("deleted counts = +%d -%d, want +0 -2", deleted.Additions, deleted.Deletions)
	}

	renamed := files[1]
	if renamed.Status != diffStatusRenamed {
		t.Errorf("status = %q, want renamed", renamed.Status)
	}
	// Both ends are kept: a rename shown only as its new path reads as an addition.
	if renamed.Path != "new.txt" || renamed.OldPath != "old.txt" {
		t.Errorf("rename = %+v", renamed)
	}
	if renamed.Additions != 1 || renamed.Deletions != 1 {
		t.Errorf("rename counts = +%d -%d, want +1 -1", renamed.Additions, renamed.Deletions)
	}
}

// A context line beginning with a space, and the hunk header itself, are not
// changes. Getting this wrong inflates every count by the size of the context.
func TestParseTurnDiffIgnoresContextAndHeaders(t *testing.T) {
	diff := strings.Join([]string{
		"diff --git a/a.txt b/a.txt",
		"index abc..def",
		"--- a/a.txt",
		"+++ b/a.txt",
		"@@ -1,4 +1,4 @@",
		" context one",
		" context two",
		"-removed",
		"+added",
		"",
	}, "\n")

	files, _ := parseTurnDiff(diff)
	if len(files) != 1 {
		t.Fatalf("files = %+v", files)
	}
	if files[0].Additions != 1 || files[0].Deletions != 1 {
		t.Errorf("counts = +%d -%d, want +1 -1", files[0].Additions, files[0].Deletions)
	}
}

func TestParseTurnDiffTruncatesAtCap(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxDiffFiles+25; i++ {
		name := "fx" + strconv.Itoa(i) + ".txt"
		b.WriteString("diff --git a/" + name + " b/" + name + "\n")
		b.WriteString("index a..b\n--- a/" + name + "\n+++ b/" + name + "\n")
		b.WriteString("@@ -1 +1 @@\n-a\n+b\n")
	}

	files, truncated := parseTurnDiff(b.String())
	if !truncated {
		t.Fatal("a diff past the cap did not report truncation")
	}
	if len(files) != maxDiffFiles {
		t.Errorf("files = %d, want the cap %d", len(files), maxDiffFiles)
	}
}

// Truncation has to reach the projection, and Summary is the only channel
// ports.ChatTurnDiff leaves for it.
func TestNormalizeTruncatedTurnDiffCarriesTheMarker(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxDiffFiles+5; i++ {
		name := "f" + strconv.Itoa(i) + ".txt"
		b.WriteString("diff --git a/" + name + " b/" + name + "\n")
		b.WriteString("--- a/" + name + "\n+++ b/" + name + "\n@@ -1 +1 @@\n+b\n")
	}
	params, err := json.Marshal(map[string]string{"threadId": "t", "turnId": "u", "diff": b.String()})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	event := normalizeOne(t, "turn/diff/updated", string(params))
	if event.Summary != domain.ChatDiffTruncatedSummary {
		t.Errorf("summary = %q, want the truncation marker", event.Summary)
	}
}

// The item-scoped file-change notifications are read, and they do NOT compete with
// the turn diff: they land on the ITEM they belong to, while turn/diff/updated lands
// on the turn. Two accounts of the same edits would be a problem only if both wrote
// the same row, and neither does. See normalize_ingest_test.go for their payloads.
//
// The client-driven exec API stays unhandled: AO never asks the server to run
// anything, so an agent tool call never arrives on those methods.
func TestNormalizeItemFileChangeStreamsLandOnTheItem(t *testing.T) {
	patch := normalizeOne(t, "item/fileChange/patchUpdated",
		`{"threadId":"t","turnId":"u","itemId":"i","changes":[{"path":"a.txt","kind":{"type":"add"},"diff":"x\n"}]}`)
	if patch.ProviderItemID != "i" {
		t.Errorf("patchUpdated item id = %q, want the item it belongs to", patch.ProviderItemID)
	}
	if patch.Diff != nil {
		t.Error("patchUpdated wrote a turn diff: that is turn/diff/updated's row")
	}

	output := normalizeOne(t, "item/fileChange/outputDelta",
		`{"threadId":"t","turnId":"u","itemId":"i","delta":"whatever"}`)
	if output.ProviderItemID != "i" || output.Diff != nil {
		t.Errorf("outputDelta -> %+v", output)
	}

	normalizeNone(t, "command/exec/outputDelta", `{"processId":"p","delta":"out"}`)
	normalizeNone(t, "process/outputDelta", `{"processId":"p","delta":"out"}`)
}
