package usage

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	piUnattributedUsageModel = "Tools/summaries"
	// piPricingVersion labels a cost AO stored as Pi computed it.
	piPricingVersion = "pi:reported"
)

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

// piNativeUsage is Pi's provider-neutral usage: Pi normalizes every provider
// to the same shape, with input excluding both cache counters and output
// already including reasoning.
type piNativeUsage struct {
	Input      int64         `json:"input"`
	Output     int64         `json:"output"`
	CacheRead  int64         `json:"cacheRead"`
	CacheWrite int64         `json:"cacheWrite"`
	Cost       *piNativeCost `json:"cost"`
}

// piNativeCost is the USD cost Pi computed from its own model registry.
type piNativeCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
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
		if input == 0 && usage.Output == 0 {
			// A failed or aborted request: Pi records a zeroed usage block
			// for it, and there is nothing to bill.
			continue
		}
		providerID := domain.UsageProviderOpenAI
		var tokens domain.UsageTokenMetrics
		if strings.EqualFold(provider, "anthropic") {
			providerID = domain.UsageProviderAnthropic
			tokens, ok = normalizeAnthropicUsage(
				usage.Input, usage.CacheWrite, usage.CacheRead, usage.Output, nil, nil,
			)
		} else {
			// Pi has already normalized every other provider (z.ai,
			// OpenRouter, Google, ...) to the same counters, so the OpenAI
			// vocabulary holds them; BillingProviderID keeps who billed it.
			tokens, ok = normalizeOpenAIUsage(input, usage.CacheRead, usage.CacheWrite, usage.Output)
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
			Costs: piReportedCosts(usage.Cost),
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

// piReportedCosts converts the cost Pi computed into AO's cost columns. Pi
// prices providers AO's catalog does not list (a z.ai coding plan, OpenRouter),
// so its figure is kept as stored. A missing or zero cost means Pi had no
// price either, and the catalog is left to try.
func piReportedCosts(cost *piNativeCost) domain.UsageEventCosts {
	if cost == nil {
		return domain.UsageEventCosts{}
	}
	input, okInput := usdToNanos(cost.Input + cost.CacheWrite)
	cached, okCached := usdToNanos(cost.CacheRead)
	output, okOutput := usdToNanos(cost.Output)
	if !okInput || !okCached || !okOutput {
		return domain.UsageEventCosts{}
	}
	total, ok := sumNonNegative(input, cached, output)
	if !ok || total == 0 {
		return domain.UsageEventCosts{}
	}
	return domain.UsageEventCosts{
		InputCostNanos:       &input,
		CachedInputCostNanos: &cached,
		OutputCostNanos:      &output,
		EstimatedCostNanos:   &total,
		PricingVersion:       piPricingVersion,
	}
}

// usdToNanos rounds a nonnegative USD amount to whole nano-dollars.
func usdToNanos(usd float64) (int64, bool) {
	nanos := math.Round(usd * 1e9)
	if math.IsNaN(nanos) || nanos < 0 || nanos >= math.MaxInt64 {
		return 0, false
	}
	return int64(nanos), true
}
