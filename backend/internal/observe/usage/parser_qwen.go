package usage

import (
	"encoding/json"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/pricing"
)

type qwenUsageRecord struct {
	SchemaVersion  int    `json:"schemaVersion"`
	ID             string `json:"id"`
	Timestamp      string `json:"timestamp"`
	SessionID      string `json:"sessionId"`
	Model          string `json:"model"`
	AuthType       string `json:"authType"`
	InputTokens    int64  `json:"inputTokens"`
	OutputTokens   int64  `json:"outputTokens"`
	CachedTokens   int64  `json:"cachedTokens"`
	ThoughtsTokens int64  `json:"thoughtsTokens"`
	TotalTokens    *int64 `json:"totalTokens"`
}

func parseQwen(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	eventsByKey := make(map[string]domain.ModelUsageEvent)
	for _, record := range records {
		var native qwenUsageRecord
		if err := json.Unmarshal(record.Data, &native); err != nil {
			recordMalformed(result)
			continue
		}
		if native.SessionID != source.NativeRootID {
			continue
		}
		model := firstNonEmpty(native.Model)
		identity := firstNonEmpty(native.ID)
		// Qwen's current validator accepts versions through 1. A future version
		// needs review before its fields can be mapped into AO's token buckets.
		if native.SchemaVersion != 1 || model == "" || identity == "" ||
			native.InputTokens < 0 || native.OutputTokens < 0 ||
			native.CachedTokens < 0 || native.ThoughtsTokens < 0 {
			recordMalformed(result)
			continue
		}
		output, ok := qwenOutputTokens(native)
		if !ok {
			recordMalformed(result)
			continue
		}
		_, ok = sumNonNegative(native.InputTokens, output)
		if !ok || native.TotalTokens != nil && *native.TotalTokens < 0 {
			recordMalformed(result)
			continue
		}
		// AO requires cached input to be a subset of input so uncached input
		// remains non-negative, even though Qwen's validator is more permissive.
		tokens, ok := normalizeOpenAIUsage(
			native.InputTokens, native.CachedTokens, 0, output,
		)
		if !ok {
			recordMalformed(result)
			continue
		}
		billingProvider := qwenBillingProvider(source.ProviderHint)
		event := domain.ModelUsageEvent{
			ProviderID:            domain.UsageProviderOpenAI,
			BillingProviderID:     billingProvider,
			BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
			ModelID:               model,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			ProviderUsageJSON:     providerUsageWithCacheWrite(record.Data, qwenCacheWriteTokens(native), nil),
			CreatedAt:             parseUsageTimestamp(native.Timestamp),
			SourceEventKey: stableSourceEventKey(
				"qwen", source.NativeRootID, identity, model,
			),
		}
		result.addEvent(eventsByKey, event)
	}
}

// qwenOutputTokens returns the record's billed output. Qwen copies Gemini's
// usage shape into its log, but what candidatesTokenCount holds depends on the
// backend that filled it. The OpenAI-compatible converters (openai,
// openai-responses, qwen-oauth) store completion_tokens, which already
// includes reasoning, and the Anthropic converter stores output_tokens, which
// includes thinking; thoughtsTokens there is a breakdown, not an addition.
// Only Gemini's native API (gemini, vertex-ai) reports thoughts outside
// candidatesTokenCount. An auth type Qwen did not record falls back to the
// reported total, which counts thoughts once whichever convention applied.
func qwenOutputTokens(native qwenUsageRecord) (int64, bool) {
	switch strings.TrimSpace(native.AuthType) {
	case "gemini", "vertex-ai":
		return sumNonNegative(native.OutputTokens, native.ThoughtsTokens)
	case "openai", "openai-responses", "qwen-oauth", "anthropic":
		return native.OutputTokens, true
	}
	if native.ThoughtsTokens > 0 && native.TotalTokens != nil {
		separate, ok := sumNonNegative(native.InputTokens, native.OutputTokens, native.ThoughtsTokens)
		if ok && *native.TotalTokens == separate {
			return sumNonNegative(native.OutputTokens, native.ThoughtsTokens)
		}
	}
	return native.OutputTokens, true
}

// qwenBillingProvider takes the route the collector resolved from Qwen's
// settings; Qwen's own log names only the auth type, which says nothing about
// which OpenAI-compatible endpoint answered.
func qwenBillingProvider(hint string) string {
	hint = strings.TrimSpace(hint)
	if hint == "" || hint == pricing.UnidentifiedBillingRoute {
		return ""
	}
	return pricing.CanonicalProviderID(hint)
}

// qwenCacheWriteTokens is the cache-write count AO can derive for a record, or
// nil when it cannot. The OpenAI-compatible and Gemini usage objects Qwen reads
// carry no separate write bucket, so their writes are zero, and a catalog row
// that publishes a write rate (z.ai's does, at 0) can still price the input.
// Qwen's Anthropic converter folds cache creation into input without saying
// how much, so its write count stays unknown rather than understated.
func qwenCacheWriteTokens(native qwenUsageRecord) *int64 {
	switch strings.TrimSpace(native.AuthType) {
	case "openai", "openai-responses", "qwen-oauth", "gemini", "vertex-ai":
		zero := int64(0)
		return &zero
	}
	return nil
}
