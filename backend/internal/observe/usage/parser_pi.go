package usage

import (
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	piUnattributedUsageModel = "Tools/summaries"
	// piPricingVersion labels a cost AO stored as Pi computed it.
	piPricingVersion = "pi:reported"
	// ompPricingVersion and kimchiPricingVersion label a cost the Pi fork
	// computed with its own model registry.
	ompPricingVersion    = "omp:reported"
	kimchiPricingVersion = "kimchi:reported"
)

// piDialect is what differs between Pi and the forks that write its session
// format: the namespace of their event keys, the label on the cost they
// report, and any extra record type that carries usage.
type piDialect struct {
	eventKeyPrefix string
	pricingVersion string
	// sideUsageType names a top-level record with Pi's "usage" shape (a
	// provider, model and usage block) that the fork writes for a model call
	// outside the conversation; empty when the fork writes none.
	sideUsageType string
	// gatewayProviders are the fork's own billing gateways. A zero cost the
	// fork reports for a call through one is the price it charged, not a
	// missing one, and no catalog lists the gateway to price it otherwise.
	gatewayProviders []string
}

var (
	piSessionDialect = piDialect{eventKeyPrefix: "pi", pricingVersion: piPricingVersion}
	// OMP records each auxiliary model call (its auto-thinking judge, for
	// one) as a model_usage entry beside the conversation.
	ompSessionDialect    = piDialect{eventKeyPrefix: "omp", pricingVersion: ompPricingVersion, sideUsageType: "model_usage"}
	kimchiSessionDialect = piDialect{
		eventKeyPrefix: "kimchi", pricingVersion: kimchiPricingVersion,
		gatewayProviders: []string{"kimchi", "kimchi-dev"},
	}
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
	parsePiFamily(source, records, piSessionDialect, result)
}

// parsePiFamily parses a Pi-format session transcript, written by Pi or by a
// fork of it (OMP, Kimchi), which keep Pi's records and usage normalization.
func parsePiFamily(source domain.UsageSourceContext, records []jsonlRecord, dialect piDialect, result *parseResult) {
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
		case native.Type == "usage" || dialect.sideUsageType != "" && native.Type == dialect.sideUsageType:
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
				dialect.eventKeyPrefix, source.NativeRootID, native.Type, identity,
			),
			Costs: piReportedCosts(usage.Cost, dialect.pricingVersion,
				slices.Contains(dialect.gatewayProviders, billingProvider)),
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

// piReportedCosts converts the cost Pi computed into AO's cost columns, under
// the dialect's pricingVersion. Pi prices providers AO's catalog does not list
// (a z.ai coding plan, OpenRouter), so its figure is kept as stored. A missing
// cost, or a zero one through another provider, means Pi had no price either,
// and the catalog is left to try. Through the agent's own gateway a zero cost
// is what the gateway charged, so it is kept.
func piReportedCosts(cost *piNativeCost, pricingVersion string, ownGateway bool) domain.UsageEventCosts {
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
	if !ok || total == 0 && !ownGateway {
		return domain.UsageEventCosts{}
	}
	return domain.UsageEventCosts{
		InputCostNanos:       &input,
		CachedInputCostNanos: &cached,
		OutputCostNanos:      &output,
		EstimatedCostNanos:   &total,
		PricingVersion:       pricingVersion,
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
