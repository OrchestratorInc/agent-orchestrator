package qwenacp

import (
	"testing"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

func TestCompactionMessageRecognizesQwenCompression(t *testing.T) {
	for name, test := range map[string]struct {
		text string
		meta map[string]any
		want acpdriver.CompactionReport
	}{
		"0.28 progress": {
			text: "Compressing context...",
			meta: map[string]any{"source": "slash_command", "contextCompression": map[string]any{"phase": "progress"}},
			want: acpdriver.CompactionReport{Status: "started"},
		},
		"0.28 done": {
			text: "Context compressed (48231 -> 9874).",
			meta: map[string]any{"source": "slash_command", "contextCompression": map[string]any{
				"phase": "done", "originalTokenCount": 48231.0, "newTokenCount": 9874.0,
			}},
			want: acpdriver.CompactionReport{Status: "completed", TokensBefore: 48231, TokensAfter: 9874},
		},
		"0.28 nothing to strip": {
			meta: map[string]any{"contextCompression": map[string]any{"phase": "noop"}},
			want: acpdriver.CompactionReport{Status: "completed"},
		},
		"older progress": {text: "Compressing context...", want: acpdriver.CompactionReport{Status: "started"}},
		"older done": {
			text: "Context compressed (~48231 -> 9874).  \n⚠️ Some tool output was dropped",
			want: acpdriver.CompactionReport{Status: "completed", TokensBefore: 48231, TokensAfter: 9874},
		},
		"on its own": {
			text: "IMPORTANT: This conversation approached the input token limit for qwen3-coder-plus. " +
				"A compressed context will be sent for future messages (compressed from: 120412 to 18230 tokens).",
			want: acpdriver.CompactionReport{Status: "completed", TokensBefore: 120412, TokensAfter: 18230},
		},
		"on its own for screenshots, estimated": {
			text: "IMPORTANT: This conversation accumulated enough tool screenshots to trigger compaction for qwen3.5-plus. " +
				"A compressed context will be sent for future messages (compressed from: ~90000 to unknown tokens).",
			want: acpdriver.CompactionReport{Status: "completed", TokensBefore: 90000},
		},
	} {
		t.Run(name, func(t *testing.T) {
			got, ok := compactionMessage(test.text, test.meta)
			if !ok || got != test.want {
				t.Fatalf("compactionMessage = %+v, %v; want %+v", got, ok, test.want)
			}
		})
	}
}

// Reply text that only mentions compression, and Qwen's note about clipped
// /compress instructions, stay in the reply.
func TestCompactionMessageLeavesOtherTextAlone(t *testing.T) {
	for _, test := range []struct {
		text string
		meta map[string]any
	}{
		{text: "Compressing context... is what Qwen prints while it summarizes."},
		{text: "I will compress the context next."},
		{text: "Context compressed (a lot)."},
		{text: "Instructions were clipped to 2000 characters.", meta: map[string]any{
			"contextCompressionNotice": map[string]any{"phase": "notice", "instructionsLimit": 2000.0},
		}},
		{text: "x", meta: map[string]any{"contextCompression": map[string]any{"phase": "later"}}},
	} {
		if got, ok := compactionMessage(test.text, test.meta); ok {
			t.Errorf("compactionMessage(%q) = %+v, want the text left as reply", test.text, got)
		}
	}
}
