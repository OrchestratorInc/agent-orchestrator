package usage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	piUnattributedUsageModel = "Tools/summaries"
	piPricingVersion         = "pi:reported"
	ompPricingVersion        = "omp:reported"
	kimchiPricingVersion     = "kimchi:reported"
	primeAgentPricingVersion = "prime-agent:reported"
)

type piDialect struct {
	eventKeyPrefix   string
	pricingVersion   string
	sideUsageType    string
	gatewayProviders []string
}

var (
	piSessionDialect     = piDialect{eventKeyPrefix: "pi", pricingVersion: piPricingVersion}
	ompSessionDialect    = piDialect{eventKeyPrefix: "omp", pricingVersion: ompPricingVersion, sideUsageType: "model_usage"}
	kimchiSessionDialect = piDialect{
		eventKeyPrefix: "kimchi", pricingVersion: kimchiPricingVersion,
		gatewayProviders: []string{"kimchi", "kimchi-dev"},
	}
	primeAgentSessionDialect = piDialect{eventKeyPrefix: "prime-agent", pricingVersion: primeAgentPricingVersion}
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
		ResponseID    string          `json:"responseId"`
		Usage         json.RawMessage `json:"usage"`
	} `json:"message"`
}

func piProviderMessageID(native piSessionRecord, dialect piDialect) string {
	if native.ID != "" && native.Timestamp != "" {
		sum := sha256.Sum256([]byte(native.Type + "\x00" + native.ID + "\x00" + native.Timestamp))
		return dialect.eventKeyPrefix + "-entry:" + hex.EncodeToString(sum[:16])
	}
	if native.Message != nil {
		return providerMessageID(native.Message.ResponseID)
	}
	return ""
}

type piNativeUsage struct {
	Input        int64         `json:"input"`
	Output       int64         `json:"output"`
	CacheRead    int64         `json:"cacheRead"`
	CacheWrite   int64         `json:"cacheWrite"`
	CacheWrite1H *int64        `json:"cacheWrite1h"`
	Cost         *piNativeCost `json:"cost"`
}

type piNativeCost struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
}

func parsePi(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	parsePiFamily(source, records, piSessionDialect, result)
}

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
			continue
		}
		providerID := routedUsageProvider(provider)
		tokens, ok := normalizeRoutedUsage(providerID, usage.Input, usage.CacheRead, usage.CacheWrite, usage.Output)
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
			ProviderUsageJSON:     providerUsageWithCacheWrite(usageRaw, &usage.CacheWrite, usage.CacheWrite1H),
			ProviderMessageID:     piProviderMessageID(native, dialect),
			CreatedAt:             parseUsageTimestamp(native.Timestamp),
			SourceEventKey: stableSourceEventKey(
				dialect.eventKeyPrefix, source.NativeRootID, native.Type, identity,
			),
			Costs: piReportedCosts(usage.Cost, dialect.pricingVersion,
				slices.Contains(dialect.gatewayProviders, billingProvider)),
		}
		result.addEvent(eventsByKey, event)
	}
}

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

func usdToNanos(usd float64) (int64, bool) {
	nanos := math.Round(usd * 1e9)
	if math.IsNaN(nanos) || nanos < 0 || nanos >= math.MaxInt64 {
		return 0, false
	}
	return int64(nanos), true
}
