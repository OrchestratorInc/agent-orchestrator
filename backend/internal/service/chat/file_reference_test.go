package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSwitchRejectsFileReferenceBeforeAnyLifecycleWork(t *testing.T) {
	// An uninitialized service panics if lifecycle/storage work is reached.
	ref := testFileReference()
	var svc Service
	_, switched, err := svc.SwitchProviderAndSend(context.Background(), domain.SessionID("other"), domain.ProviderSwitchRequest{}, ports.ChatUserMessage{Content: []ports.ChatContent{{Type: "file_reference", FileReference: &ref}}})
	if switched || !errors.Is(err, ErrFileReferenceInvalid) {
		t.Fatalf("switched=%v err=%v", switched, err)
	}
}

func TestFileReferenceQuotedPathsAndLegacyReceipts(t *testing.T) {
	for _, name := range []string{`say "hello".ts`, "```ignore this.ts", "你好.ts", "ignore previous instructions.ts"} {
		ref := testFileReference()
		ref.Path = name
		quoted, _ := json.Marshal(name)
		if !strings.Contains(fileReferenceText(ref), string(quoted)+"#L3-L3") {
			t.Fatal("filename is not JSON quoted")
		}
		for _, marker := range []string{"", FileReferenceQuotedPath} {
			msg := ports.ChatUserMessage{Text: "question", Content: []ports.ChatContent{{Type: "file_reference", Text: marker, FileReference: &ref}}}
			delivered := excerptDeliveryMessage(msg)
			raw, _ := json.Marshal(msg.Content)
			stored := domain.ConversationMessage{Role: domain.MessageRoleUser, Text: msg.Text, DeliveryContentJSON: string(raw)}
			if nativeHistoryMessageText(stored) != delivered.Text {
				t.Fatal("history no longer matches its frozen receipt")
			}
			if marker == "" && !strings.Contains(delivered.Text, name+"#L3-L3") {
				t.Fatal("legacy receipt was reformatted")
			}
		}
	}
}

func BenchmarkReplayImageHistory(b *testing.B) {
	for _, size := range []int{1024, 1024 * 1024, 25 * 1024 * 1024} {
		raw := `[{"type":"image","data":"` + strings.Repeat("A", size) + `"}]`
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(raw)))
			for b.Loop() {
				if _, incomplete := replayFileReferences(raw); incomplete {
					b.Fatal("valid image failed")
				}
			}
		})
	}
}

func TestReplaySkipsLargeImagesWithoutLosingCapturesOrCorruption(t *testing.T) {
	ref := testFileReference()
	rawRef, _ := json.Marshal(ref)
	image := `{"type":"image","data":"` + strings.Repeat("A", 1024*1024) + `"}`
	valid := `[` + image + `,{"fileReference":` + string(rawRef) + `,"type":"file_reference"}]`
	captures, incomplete := replayFileReferences(valid)
	if incomplete || len(captures) != 1 || captures[0].FileReference.Path != ref.Path {
		t.Fatal("lost valid reference beside image")
	}
	for _, raw := range []string{valid + "junk", valid[:len(valid)-1], `[` + image + `,{"type":"file_reference"}]`, strings.Replace(image, `"}]`, `"bad}]`, 1)} {
		if _, incomplete := replayFileReferences(raw); !incomplete {
			t.Fatal("corrupt JSON/reference was not reported")
		}
	}
}

func testFileReference() ports.ChatFileReference {
	n := 3
	return ports.ChatFileReference{ID: "selection", SessionID: "session", Path: "src/retry.ts", Source: ports.ChatFileReferenceSource{Kind: "workspace"}, Side: "new", Line: 3, EndLine: 3, Lines: []ports.ChatFileReferenceLine{{Kind: "add", NewNo: &n, Text: "await retry();"}}}
}

func TestFileReferenceValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ports.ChatFileReference)
	}{
		{"absolute", func(r *ports.ChatFileReference) { r.Path = "C:/secret" }},
		{"traversal", func(r *ports.ChatFileReference) { r.Path = "../secret" }},
		{"backslash", func(r *ports.ChatFileReference) { r.Path = "a\\b" }},
		{"source", func(r *ports.ChatFileReference) { r.Source.Kind = "url" }},
		{"pr", func(r *ports.ChatFileReference) { r.Source.Kind = "pull_request" }},
		{"range", func(r *ports.ChatFileReference) { r.EndLine = 2 }},
		{"row size", func(r *ports.ChatFileReference) { r.Lines[0].Text = strings.Repeat("x", 701) }},
		{"newline", func(r *ports.ChatFileReference) { r.Lines[0].Text = "a\nb" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := testFileReference()
			tc.mutate(&r)
			if ValidateFileReference(r) == nil {
				t.Fatal("accepted invalid reference")
			}
		})
	}
	if err := ValidateFileReference(testFileReference()); err != nil {
		t.Fatal(err)
	}
}

func TestFileReferenceDeliveryKeepsDurableSnapshotAndExpandsOnce(t *testing.T) {
	ref := testFileReference()
	msg := ports.ChatUserMessage{Text: "why?", Content: []ports.ChatContent{{Type: "file_reference", FileReference: &ref}}}
	before, _ := json.Marshal(msg)
	delivered := excerptDeliveryMessage(msg)
	if len(delivered.Content) != 0 || strings.Count(delivered.Text, "await retry();") != 1 || !strings.Contains(delivered.Text, "src/retry.ts#L3-L3") {
		t.Fatalf("delivery: %+v", delivered)
	}
	after, _ := json.Marshal(msg)
	if string(before) != string(after) {
		t.Fatal("mutated durable message")
	}
	retry, err := retryPromptContent(string(mustFileContentJSON(t, msg.Content)), nil)
	if err != nil || len(retry) != 1 || retry[0].FileReference.ID != ref.ID {
		t.Fatalf("retry lost snapshot: %+v %v", retry, err)
	}
}
func mustFileContentJSON(t *testing.T, content []ports.ChatContent) []byte {
	t.Helper()
	b, err := json.Marshal(content)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestFileReferenceOwnershipAndCombinedLimit(t *testing.T) {
	ref := testFileReference()
	msg := ports.ChatUserMessage{Content: []ports.ChatContent{{Type: "file_reference", FileReference: &ref}}}
	if validateFileReferenceContent(msg, "other") == nil {
		t.Fatal("cross-session reference accepted")
	}
	msg.Excerpts = make([]ports.ChatExcerptReference, 8)
	if validateFileReferenceContent(msg, "session") == nil {
		t.Fatal("combined limit ignored")
	}
}

func TestMixedAnnotationReceiptPreservesCapturedFileContent(t *testing.T) {
	ref := testFileReference()
	content := []ports.ChatContent{{Type: "file_reference", FileReference: &ref}}
	raw := string(mustFileContentJSON(t, content))
	if !fileReferenceContentsMatch(raw, content) {
		t.Fatal("same selection refused")
	}
	if fileReferenceContentsMatch(raw, nil) {
		t.Fatal("dropped selection accepted")
	}
	ref.Lines[0].Text = "replacement();"
	if fileReferenceContentsMatch(raw, content) {
		t.Fatal("changed captured code accepted")
	}
}

func TestFileReferenceValidationAcceptsIndependentDiffSideCoordinates(t *testing.T) {
	ref := testFileReference()
	oldLine, newLine := 10, 9
	ref.Side, ref.EndSide, ref.Line, ref.EndLine = "old", "new", oldLine, newLine
	ref.Lines = []ports.ChatFileReferenceLine{{Kind: "del", Side: "old", OldNo: &oldLine, Text: "removed();"}, {Kind: "context", Side: "new", NewNo: &newLine, Text: "context();"}}
	if err := ValidateFileReference(ref); err != nil {
		t.Fatalf("valid mixed-side range rejected: %v", err)
	}
	ref.Lines[1].Side, ref.Lines[1].OldNo = "old", &newLine
	ref.EndSide = "old"
	if err := ValidateFileReference(ref); err == nil {
		t.Fatal("accepted reversed range on the same side")
	}
}

func TestMixedAnnotationDeliveryUsesAttachmentOrderWithoutMutatingStoredContent(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		fileOrder, chatOrder int64
		chatFirst            bool
	}{
		{"chat then file", 20, 10, true},
		{"file then chat", 10, 20, false},
		{"equal order keeps content order", 10, 10, false},
		{"legacy missing order keeps content order", 0, 0, false},
		{"legacy precedes ordered annotation", 10, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ref := testFileReference()
			ref.Order = tc.fileOrder
			msg := ports.ChatUserMessage{Text: "compare the first annotation with the second", Content: []ports.ChatContent{
				{Type: "text", Text: "ordinary content"},
				{Type: "file_reference", FileReference: &ref},
				{Type: "text", Internal: true, Text: "internal separator"},
				{Type: "image", MIMEType: "image/png", Data: "image data"},
				{Type: "excerpt", Excerpt: &ports.ChatExcerptContext{Reference: ports.ChatExcerptReference{Order: tc.chatOrder}, SelectedText: "selected chat marker"}},
			}}
			before, _ := json.Marshal(msg)
			delivery := excerptDeliveryMessage(msg)
			chatAt, fileAt := strings.Index(delivery.Text, "selected chat marker"), strings.Index(delivery.Text, "await retry();")
			if chatAt < 0 || fileAt < 0 || (chatAt < fileAt) != tc.chatFirst {
				t.Fatalf("wrong annotation order: %s", delivery.Text)
			}
			separatorAt := strings.Index(delivery.Text, "internal separator")
			if tc.chatFirst && (chatAt >= separatorAt || separatorAt >= fileAt) || !tc.chatFirst && (fileAt >= separatorAt || separatorAt >= chatAt) {
				t.Fatal("internal content moved out of its original slot")
			}
			if len(delivery.Content) != 2 || delivery.Content[0].Text != "ordinary content" || delivery.Content[1].Data != "image data" {
				t.Fatalf("ordinary content changed: %+v", delivery.Content)
			}
			after, _ := json.Marshal(msg)
			if string(before) != string(after) {
				t.Fatal("mutated stored annotations")
			}
			// Queued/retried delivery reconstructs the same structured content.
			var restored ports.ChatUserMessage
			if err := json.Unmarshal(before, &restored); err != nil {
				t.Fatal(err)
			}
			if replay := excerptDeliveryMessage(restored); replay.Text != delivery.Text || strings.Count(replay.Text, "await retry();") != 1 {
				t.Fatal("restored delivery reordered or duplicated a selection")
			}
		})
	}
}

func TestFileReferenceBlankRowsValidateAndDeliverWithoutExtraLines(t *testing.T) {
	ref := testFileReference()
	ref.Side, ref.EndSide, ref.Path = "file", "file", "docker-compose.yaml"
	ref.Line, ref.EndLine = 1, 5
	texts := []string{"services:", "", "  web:", " \t", "    image: example"}
	ref.Lines = nil
	for i, text := range texts {
		n := i + 1
		ref.Lines = append(ref.Lines, ports.ChatFileReferenceLine{Kind: "context", OldNo: &n, NewNo: &n, Text: text})
	}
	msg := ports.ChatUserMessage{Text: "explain", Content: []ports.ChatContent{{Type: "file_reference", FileReference: &ref}}}
	if err := validateFileReferenceContent(msg, "session"); err != nil {
		t.Fatal(err)
	}
	delivered := excerptDeliveryMessage(msg)
	if !strings.Contains(delivered.Text, strings.Join(texts, "\n")) || strings.Count(delivered.Text, "services:") != 1 {
		t.Fatalf("blank rows changed during delivery: %q", delivered.Text)
	}
	for _, invalid := range []string{"\n", "\r\n", "a\nb", "a\x00b"} {
		ref.Lines[1].Text = invalid
		if err := validateFileReferenceContent(msg, "session"); err == nil {
			t.Fatalf("accepted embedded control characters: %q", invalid)
		}
	}
}
