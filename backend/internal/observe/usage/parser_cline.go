package usage

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// clineMessagesDocument is Cline CLI's <session>.messages.json, which Cline
// rewrites whole after each turn.
type clineMessagesDocument struct {
	SessionID string            `json:"sessionId"`
	Messages  []json.RawMessage `json:"messages"`
}

type clineMessage struct {
	ID        string          `json:"id"`
	Role      string          `json:"role"`
	TS        int64           `json:"ts"`
	Metrics   json.RawMessage `json:"metrics"`
	ModelInfo *struct {
		ID       string `json:"id"`
		Provider string `json:"provider"`
	} `json:"modelInfo"`
}

// clineMetrics is the usage Cline records on the assistant message one model
// call produced. inputTokens is the call's whole prompt: cache reads and
// writes are parts of it, not additions to it.
type clineMetrics struct {
	InputTokens      int64 `json:"inputTokens"`
	OutputTokens     int64 `json:"outputTokens"`
	CacheReadTokens  int64 `json:"cacheReadTokens"`
	CacheWriteTokens int64 `json:"cacheWriteTokens"`
}

func parseClineSnapshot(source domain.UsageSourceContext, data []byte, result *parseResult) {
	var document clineMessagesDocument
	if err := json.Unmarshal(data, &document); err != nil {
		recordMalformed(result)
		return
	}
	// Registration checked the path and manifest; a document naming another
	// session is a different file, and none of it is this session's usage.
	if document.SessionID != source.NativeRootID {
		result.Cursor.AnomalyCount++
		result.Cursor.LastErrorCode = domain.UsageErrorArtifactPathRejected
		return
	}
	eventsByKey := make(map[string]domain.ModelUsageEvent)
	for _, raw := range document.Messages {
		var message clineMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			recordMalformed(result)
			continue
		}
		if message.Role != "assistant" || !jsonValueReported(message.Metrics) {
			continue
		}
		var metrics clineMetrics
		if err := json.Unmarshal(message.Metrics, &metrics); err != nil {
			recordMalformed(result)
			continue
		}
		identity := firstNonEmpty(message.ID)
		var model, provider string
		if message.ModelInfo != nil {
			model = firstNonEmpty(message.ModelInfo.ID)
			provider = strings.TrimSpace(message.ModelInfo.Provider)
		}
		if identity == "" || model == "" {
			recordMalformed(result)
			continue
		}
		providerID := domain.UsageProviderOpenAI
		var tokens domain.UsageTokenMetrics
		ok := metrics.CacheReadTokens >= 0 && metrics.CacheWriteTokens >= 0
		if ok {
			if strings.EqualFold(provider, "anthropic") {
				// Anthropic prices cache writes apart from plain input, which
				// only its vocabulary keeps separate.
				providerID = domain.UsageProviderAnthropic
				direct := metrics.InputTokens - metrics.CacheReadTokens - metrics.CacheWriteTokens
				tokens, ok = normalizeAnthropicUsage(
					direct, metrics.CacheWriteTokens, metrics.CacheReadTokens, metrics.OutputTokens, nil, nil,
				)
			} else {
				tokens, ok = normalizeOpenAIUsage(
					metrics.InputTokens, metrics.CacheReadTokens, metrics.CacheWriteTokens, metrics.OutputTokens,
				)
			}
		}
		if !ok {
			recordMalformed(result)
			continue
		}
		billingProvider := canonicalBillingProvider(provider)
		var createdAt time.Time
		if message.TS > 0 {
			createdAt = time.UnixMilli(message.TS).UTC()
		}
		event := domain.ModelUsageEvent{
			ProviderID:            providerID,
			BillingProviderID:     billingProvider,
			BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
			ModelID:               model,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			// The object keeps Cline's own cost beside the tokens: it is
			// what Cline reported, while AO's columns price the catalog.
			ProviderUsageJSON: boundedProviderUsage(message.Metrics),
			// Resuming or forking a Cline session copies earlier messages,
			// IDs included, into the new session's document. The ID makes
			// a copy count once across sessions.
			ProviderMessageID: firstNonEmpty("cline:" + identity),
			CreatedAt:         createdAt,
			SourceEventKey:    stableSourceEventKey("cline", source.NativeRootID, identity),
		}
		if existing, duplicate := eventsByKey[event.SourceEventKey]; duplicate {
			if !usageEventsEqual(existing, event) {
				result.Cursor.AnomalyCount++
				result.Cursor.LastErrorCode = domain.UsageErrorSourceEventConflict
			}
			continue
		}
		eventsByKey[event.SourceEventKey] = event
		result.Events = append(result.Events, event)
	}
}
