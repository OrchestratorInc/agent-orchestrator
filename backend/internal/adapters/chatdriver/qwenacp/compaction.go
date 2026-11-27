package qwenacp

import (
	"regexp"
	"strconv"
	"strings"

	acpdriver "github.com/OrchestratorInc/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// Qwen Code reports compression in its reply rather than as a status. Since
// 0.28 its /compress output carries `_meta.contextCompression` (phase progress,
// then done with both token counts). Earlier builds send the same two lines
// with no `_meta`, and a compression Qwen runs on its own before a model
// request is a single diagnostic line in every build. These are the sentences
// Qwen's source writes (compressCommand.ts, Session.ts formatCompressionDiagnostic);
// if Qwen rewords them, the line shows as reply text again, as it did before.
var (
	qwenCompressing = "Compressing context..."
	// "Context compressed (48231 -> 9874)." An estimated count carries a "~",
	// and a warning can follow on its own line.
	qwenCompressed = regexp.MustCompile(`(?s)^Context compressed \(~?(\d+) -> ~?(\d+)\)\.(?:\s*\n.*)?$`)
	// "IMPORTANT: This conversation approached the input token limit for
	// qwen3-coder-plus. A compressed context will be sent for future messages
	// (compressed from: 120412 to 18230 tokens)."
	qwenAutoCompressed = regexp.MustCompile(`(?s)^IMPORTANT: This conversation (?:approached the input token limit|accumulated enough tool screenshots to trigger compaction) for .+?\. ` +
		`A compressed context will be sent for future messages \(compressed from: ~?(\d+|unknown) to ~?(\d+|unknown) tokens\)\.(?:\s*\n.*)?$`)
)

// compactionMessage recognizes Qwen Code's compression reports, so the chat
// shows one compaction row instead of Qwen's sentences.
func compactionMessage(text string, meta map[string]any) (acpdriver.CompactionReport, bool) {
	if compression, ok := meta["contextCompression"].(map[string]any); ok {
		switch phase, _ := compression["phase"].(string); phase {
		case "progress":
			return acpdriver.CompactionReport{Status: "started"}, true
		case "done":
			return acpdriver.CompactionReport{
				Status:       "completed",
				TokensBefore: tokenCount(compression["originalTokenCount"]),
				TokensAfter:  tokenCount(compression["newTokenCount"]),
			}, true
		case "noop":
			// Nothing to strip: the history is as it was, so nothing is
			// recorded as compacted. A row Qwen's progress opened says so.
			return acpdriver.CompactionReport{Status: "skipped"}, true
		}
		return acpdriver.CompactionReport{}, false
	}
	trimmed := strings.TrimSpace(text)
	if trimmed == qwenCompressing {
		return acpdriver.CompactionReport{Status: "started"}, true
	}
	for _, pattern := range []*regexp.Regexp{qwenCompressed, qwenAutoCompressed} {
		if match := pattern.FindStringSubmatch(trimmed); match != nil {
			return acpdriver.CompactionReport{
				Status:       "completed",
				TokensBefore: parseCount(match[1]),
				TokensAfter:  parseCount(match[2]),
			}, true
		}
	}
	return acpdriver.CompactionReport{}, false
}

func tokenCount(value any) int64 {
	if number, ok := value.(float64); ok && number > 0 {
		return int64(number)
	}
	return 0
}

func parseCount(text string) int64 {
	count, err := strconv.ParseInt(text, 10, 64)
	if err != nil || count < 0 {
		return 0
	}
	return count
}
