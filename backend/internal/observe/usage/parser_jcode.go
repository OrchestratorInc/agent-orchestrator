package usage

import (
	"encoding/json"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// jcodeLedgerRecord is one line of AO's jcode usage ledger: the token_usage of
// one assistant message, copied verbatim from jcode's native session file.
type jcodeLedgerRecord struct {
	Type       string          `json:"type"`
	ID         string          `json:"id"`
	Timestamp  string          `json:"timestamp"`
	Model      string          `json:"model"`
	Provider   string          `json:"provider"`
	TokenUsage json.RawMessage `json:"token_usage"`
}

// jcodeTokenUsage is jcode's per-message counter object. prompt_tokens is the
// whole prompt; for Claude it equals input_tokens (uncached) plus both cache
// buckets, while providers that report cached tokens inside their prompt count
// give prompt_tokens == input_tokens.
type jcodeTokenUsage struct {
	PromptTokens        *int64 `json:"prompt_tokens"`
	InputTokens         int64  `json:"input_tokens"`
	OutputTokens        int64  `json:"output_tokens"`
	CacheReadTokens     int64  `json:"cache_read_input_tokens"`
	CacheCreationTokens int64  `json:"cache_creation_input_tokens"`
}

func parseJCode(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	seen := make(map[string]domain.ModelUsageEvent)
	for _, record := range records {
		var native jcodeLedgerRecord
		if err := json.Unmarshal(record.Data, &native); err != nil {
			recordMalformed(result)
			continue
		}
		if native.Type != "jcode.usage" {
			continue
		}
		id := strings.TrimSpace(native.ID)
		model := firstNonEmpty(native.Model)
		if id == "" || model == "" || !jsonValueReported(native.TokenUsage) {
			recordMalformed(result)
			continue
		}
		var usage jcodeTokenUsage
		if err := json.Unmarshal(native.TokenUsage, &usage); err != nil {
			recordMalformed(result)
			continue
		}
		tokens, provider, ok := normalizeJCodeUsage(native.Provider, usage)
		if !ok {
			recordMalformed(result)
			continue
		}
		event := domain.ModelUsageEvent{
			ProviderID:        provider,
			ModelID:           model,
			MeasurementKind:   domain.UsageMeasurementNativeReported,
			Tokens:            tokens,
			ProviderUsageJSON: boundedProviderUsage(native.TokenUsage),
			CreatedAt:         parseUsageTimestamp(native.Timestamp),
			// The native message id is stable across rewrites of jcode's session
			// file and across resumes, so re-reading never re-adds a message.
			SourceEventKey: stableSourceEventKey("jcode", source.NativeRootID, source.Source.SubagentID, id, model),
		}
		if existing, duplicate := seen[event.SourceEventKey]; duplicate {
			if !usageEventsEqual(existing, event) {
				result.Cursor.AnomalyCount++
				result.Cursor.LastErrorCode = domain.UsageErrorSourceEventConflict
			}
			continue
		}
		seen[event.SourceEventKey] = event
		result.Events = append(result.Events, event)
	}
}

// normalizeJCodeUsage derives the canonical counters from jcode's object. The
// whole prompt is prompt_tokens when reported, so cached tokens are counted
// once whichever convention the provider used for input_tokens.
func normalizeJCodeUsage(provider string, usage jcodeTokenUsage) (domain.UsageTokenMetrics, domain.UsageProviderID, bool) {
	cached, cacheWrite := usage.CacheReadTokens, usage.CacheCreationTokens
	if usage.InputTokens < 0 || cached < 0 || cacheWrite < 0 || usage.OutputTokens < 0 {
		return domain.UsageTokenMetrics{}, "", false
	}
	prompt := usage.InputTokens + cached + cacheWrite
	if usage.PromptTokens != nil {
		prompt = *usage.PromptTokens
	}
	direct := prompt - cached - cacheWrite
	if direct < 0 {
		return domain.UsageTokenMetrics{}, "", false
	}
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "claude", "anthropic", "anthropic-api", "bedrock":
		tokens, ok := normalizeAnthropicUsage(direct, cacheWrite, cached, usage.OutputTokens, nil, nil)
		return tokens, domain.UsageProviderAnthropic, ok
	default:
		tokens, ok := normalizeOpenAIUsage(prompt, cached, cacheWrite, usage.OutputTokens)
		return tokens, domain.UsageProviderOpenAI, ok
	}
}
