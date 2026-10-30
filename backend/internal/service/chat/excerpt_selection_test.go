package chat

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestSourceContainsExcerptSelection(t *testing.T) {
	tests := []struct {
		name, source, selection string
		want                    bool
	}{
		{"emphasis", "Use **bold** text here.", "Use bold text", true},
		{"link", "Open [the docs](https://example.com) now.", "the docs now", true},
		{"escaped punctuation", `Type \* literally.`, "Type * literally", true},
		{"encoded quotes", "Say &quot;hello&quot; now.", `Say "hello" now`, true},
		{"literal code entity", "Use `&quot;hello&quot;` now.", "&quot;hello&quot;", true},
		{"escaped raw HTML", "Use <b>bold</b> text.", "<b>bold</b> text", true},
		{"block raw HTML", "<div>\nhello\n</div>", "<div>\nhello\n</div>", true},
		{"ordinary quotes", `He said "hello" and it's fine.`, `"hello" and it's`, true},
		{"inline boundary", "First **important** and *useful* point.", "important and useful", true},
		{"line boundary", "First **important** line.\nNext line.", "line.\nNext", true},
		{"unrelated text", "Use **bold** text here.", "totally unrelated", false},
		{"link target", "Open [the docs](https://example.com) now.", "example.com", false},
		{"native image caption", "![alt text](https://example.com/a.png)", "alt text", true},
		{"native heading and bullets", "# Title\n\n- One\n- Two", "Title\n\n• One\n• Two", true},
		{"native partial paragraph and bullet range", "Hi! I'm an AO implementation worker for the **meetyou** project, ready to help.\n\nI don't see a specific task assigned yet. What would you like me to work on? For example:\n- Implement a feature or fix a bug\n- Address CI failures or review comments on a PR\n- Investigate something in the codebase", "an AO implementation worker for the meetyou project, ready to help.\n\nI don't see a specific task assigned yet. What would you like me to work on? For example:\n\n• Implement a feature or fix a bug\n• Address CI failur", true},
		{"native numbered list", "1. One\n2. Two", "1. One\n2. Two", true},
		{"native nested numbered list", "1. Outer\n   1. Inner\n   2. Next\n2. End", "1. Outer\n2. Inner\n3. Next\n4. End", true},
		{"native tasks", "- [ ] Pending\n- [x] Done", "☐ Pending\n☑ Done", true},
		{"native code label", "```ts\ncode();\n```", "ts\ncode();", true},
		{"native table", "| A | B |\n| --- | --- |\n| X | Y |", "A  |  B\nX  |  Y", true},
		{"fabricated bullet content", "- One\n- Two", "• One\n• Forged", false},
		{"fabricated code label", "```ts\ncode();\n```", "python\ncode();", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			source := domain.ConversationMessage{Role: domain.MessageRoleAssistant, Text: tt.source}
			if got := sourceContainsExcerptSelection(source, tt.selection); got != tt.want {
				t.Fatalf("sourceContainsExcerptSelection(%q, %q) = %v, want %v", tt.source, tt.selection, got, tt.want)
			}
		})
	}
}

func TestTruncateExcerptContextKeepsRuneBoundary(t *testing.T) {
	text := strings.Repeat("a", maxExcerptPairedTextBytes-1) + "é tail"
	got := truncateExcerptContext(text)
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "\n[truncated]") {
		t.Fatalf("truncated = %q", got[len(got)-20:])
	}
	if short := "short"; truncateExcerptContext(short) != short {
		t.Fatal("short context changed")
	}
}

func TestExcerptDeliveryFallsBackToReferenceText(t *testing.T) {
	// Turns queued before a controller existed store only the reference.
	msg := excerptDeliveryMessage(ports.ChatUserMessage{
		Text: "explain",
		Content: []ports.ChatContent{{Type: "excerpt", Excerpt: &ports.ChatExcerptContext{
			Reference: ports.ChatExcerptReference{Text: "  selected words  "},
		}}},
	})
	if len(msg.Content) != 0 || !strings.Contains(msg.Text, "---\nselected words\n---") ||
		strings.Contains(msg.Text, "Full paired turn") {
		t.Fatalf("delivery = %q", msg.Text)
	}
}
