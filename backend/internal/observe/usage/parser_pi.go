package usage

import (
	"encoding/json"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const piUnattributedUsageModel = "Tools/summaries"

type piSessionRecord struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Timestamp string          `json:"timestamp"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Usage     json.RawMessage `json:"usage"`
	Message   *struct {
		Role          string          `json:"role"`
		Provider      string          `json:"provider"`
		Model         string          `json:"model"`
		ResponseModel string          `json:"responseModel"`
		Usage         json.RawMessage `json:"usage"`
	} `json:"message"`
}

type piNativeUsage struct {
	Input      int64 `json:"input"`
	Output     int64 `json:"output"`
	CacheRead  int64 `json:"cacheRead"`
	CacheWrite int64 `json:"cacheWrite"`
}

func parsePi(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	eventsByKey := make(map[string]domain.ModelUsageEvent)
	for _, record := range records {
		var native piSessionRecord
		if err := json.Unmarshal(record.Data, &native); err != nil {
			recordMalformed(result)
			continue
		}

		var provider, model string
		var usageRaw json.RawMessage
		switch {
		case native.Type == "message" && native.Message != nil && native.Message.Role == "assistant":
			provider = strings.TrimSpace(native.Message.Provider)
			model = firstNonEmpty(native.Message.ResponseModel, native.Message.Model)
			usageRaw = native.Message.Usage
			if !jsonValueReported(usageRaw) {
				continue
			}
			if model == "" {
				recordMalformed(result)
				continue
			}
		case native.Type == "usage":
			provider = strings.TrimSpace(native.Provider)
			model = strings.TrimSpace(native.Model)
			usageRaw = native.Usage
			if provider == "" || model == "" || !jsonValueReported(usageRaw) {
				recordMalformed(result)
				continue
			}
		case native.Type == "message" && native.Message != nil && native.Message.Role == "toolResult" &&
			jsonValueReported(native.Message.Usage):
			model = piUnattributedUsageModel
			usageRaw = native.Message.Usage
		case (native.Type == "compaction" || native.Type == "branch_summary") && jsonValueReported(native.Usage):
			model = piUnattributedUsageModel
			usageRaw = native.Usage
		default:
			continue
		}

		var usage piNativeUsage
		if err := json.Unmarshal(usageRaw, &usage); err != nil {
			recordMalformed(result)
			continue
		}
		input, ok := sumNonNegative(usage.Input, usage.CacheRead, usage.CacheWrite)
		if !ok {
			recordMalformed(result)
			continue
		}
		providerID := domain.UsageProviderOpenAI
		var tokens domain.UsageTokenMetrics
		switch {
		case strings.EqualFold(provider, "anthropic"):
			providerID = domain.UsageProviderAnthropic
			tokens, ok = normalizeAnthropicUsage(
				usage.Input, usage.CacheWrite, usage.CacheRead, usage.Output, nil, nil,
			)
		case provider == "" || strings.EqualFold(provider, "openai"):
			tokens, ok = normalizeOpenAIUsage(input, usage.CacheRead, usage.CacheWrite, usage.Output)
		default:
			// The normalized usage schema currently supports only these two
			// provider families. Do not label other providers as OpenAI.
			continue
		}
		if !ok {
			recordMalformed(result)
			continue
		}

		billingProvider := ""
		if model != piUnattributedUsageModel {
			billingProvider = canonicalBillingProvider(provider)
		}
		identity := firstNonEmpty(native.ID, strconv.FormatInt(record.Offset, 10))
		event := domain.ModelUsageEvent{
			ProviderID:            providerID,
			BillingProviderID:     billingProvider,
			BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
			ModelID:               model,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			ProviderUsageJSON:     boundedProviderUsage(usageRaw),
			CreatedAt:             parseUsageTimestamp(native.Timestamp),
			SourceEventKey: stableSourceEventKey(
				"pi", source.NativeRootID, native.Type, identity,
			),
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
