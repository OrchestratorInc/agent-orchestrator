package usage

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// maxGeminiCountedMessageIDs bounds the IDs a Gemini source remembers. Gemini
// CLI re-appends only its newest message, so a short window covers every
// restatement while keeping parser state small.
const maxGeminiCountedMessageIDs = 64

// geminiParserStateV1 remembers the messages already counted from a Gemini
// chat log. Gemini appends a whole message again whenever it changes (its
// tokens arrive, a tool call completes), and those restatements can land in a
// later chunk. Emitting one again would replay a stored event, which a
// repaired attribution makes conflict.
type geminiParserStateV1 struct {
	CountedMessageIDs []string `json:"counted_message_ids,omitempty"`
}

func validateGeminiParserState(state *geminiParserStateV1) error {
	if state == nil {
		return nil
	}
	if len(state.CountedMessageIDs) > maxGeminiCountedMessageIDs {
		return errors.New("gemini state remembers too many messages")
	}
	for _, id := range state.CountedMessageIDs {
		if firstNonEmpty(id) != id || id == "" {
			return errors.New("gemini state has an invalid message id")
		}
	}
	return nil
}

// geminiLogRecord is one line of a Gemini CLI chat log: the session metadata
// line, a whole message, or a $set/$patch/$rewindTo update.
type geminiLogRecord struct {
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Type      string          `json:"type"`
	Model     string          `json:"model"`
	Tokens    json.RawMessage `json:"tokens"`
	Patch     json.RawMessage `json:"$patch"`
}

type geminiTokens struct {
	Input    int64 `json:"input"`
	Output   int64 `json:"output"`
	Cached   int64 `json:"cached"`
	Thoughts int64 `json:"thoughts"`
	Tool     int64 `json:"tool"`
	Total    int64 `json:"total"`
}

func parseGemini(
	source domain.UsageSourceContext,
	records []jsonlRecord,
	state *geminiParserStateV1,
	result *parseResult,
) {
	for _, record := range records {
		var native geminiLogRecord
		if err := json.Unmarshal(record.Data, &native); err != nil {
			recordMalformed(result)
			continue
		}
		// The metadata line and $set/$rewindTo updates carry no id, and a
		// $patch edits content only. A rewind hides messages that were already
		// billed, so it never takes usage back.
		if strings.TrimSpace(native.ID) == "" || jsonValueReported(native.Patch) ||
			native.Type != "gemini" || !jsonValueReported(native.Tokens) {
			continue
		}
		var tokens geminiTokens
		if err := json.Unmarshal(native.Tokens, &tokens); err != nil {
			recordMalformed(result)
			continue
		}
		identity := firstNonEmpty(native.ID)
		model := firstNonEmpty(native.Model)
		if identity == "" || model == "" || tokens.Total < 0 || tokens.Cached < 0 {
			recordMalformed(result)
			continue
		}
		if slices.Contains(state.CountedMessageIDs, identity) {
			continue
		}
		// Gemini's prompt count already includes cached content; the tool-use
		// prompt and thoughts are billed beside it, as input and output.
		input, ok := sumNonNegative(tokens.Input, tokens.Tool)
		if !ok {
			recordMalformed(result)
			continue
		}
		output, ok := sumNonNegative(tokens.Output, tokens.Thoughts)
		if !ok {
			recordMalformed(result)
			continue
		}
		normalized, ok := normalizeOpenAIUsage(input, tokens.Cached, 0, output)
		if !ok {
			recordMalformed(result)
			continue
		}
		result.Events = append(result.Events, domain.ModelUsageEvent{
			ProviderID:        domain.UsageProviderOpenAI,
			ModelID:           model,
			MeasurementKind:   domain.UsageMeasurementNativeReported,
			Tokens:            normalized,
			ProviderUsageJSON: boundedProviderUsage(native.Tokens),
			CreatedAt:         parseUsageTimestamp(native.Timestamp),
			SourceEventKey: stableSourceEventKey(
				"gemini", source.NativeRootID, identity,
			),
		})
		state.CountedMessageIDs = append(state.CountedMessageIDs, identity)
		if excess := len(state.CountedMessageIDs) - maxGeminiCountedMessageIDs; excess > 0 {
			state.CountedMessageIDs = slices.Delete(state.CountedMessageIDs, 0, excess)
		}
	}
}
