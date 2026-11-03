package usage

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// fxPricingVersion labels a cost AO stored as fx reported it.
const fxPricingVersion = "fx:provider_reported"

// fxModelUsage is one model's running totals in fx's usage-v2.json. Its model
// is "<connection>/<model>", such as "codex/gpt-5.5", and a gateway
// connection's model names its vendor too: "gateway/openai/gpt-5.2".
type fxModelUsage struct {
	Model            string  `json:"model"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	TotalCost        float64 `json:"total_cost"`
	RequestCount     int64   `json:"request_count"`
}

func (m fxModelUsage) counters() (hermesUsageCounters, bool) {
	cost, ok := usdToNanos(m.TotalCost)
	if !ok {
		return hermesUsageCounters{}, false
	}
	counters := hermesUsageCounters{
		APICalls:           m.RequestCount,
		InputTokens:        m.InputTokens,
		OutputTokens:       m.OutputTokens,
		CacheReadTokens:    m.CacheReadTokens,
		CacheWriteTokens:   m.CacheWriteTokens,
		ReasoningTokens:    m.ReasoningTokens,
		EstimatedCostNanos: cost,
	}
	return counters, counters.valid()
}

// fxUsageDocument is fx's per-session usage-v2.json, which fx rewrites with
// the session's running totals. Its global usage.jsonl names no session, so
// it cannot be attributed.
type fxUsageDocument struct {
	SessionID string `json:"session_id"`
	Snapshot  struct {
		Models []fxModelUsage `json:"models"`
	} `json:"snapshot"`
}

// fxUsageRow is one model's running totals as read from usage-v2.json.
type fxUsageRow struct {
	Model    string              `json:"model"`
	Counters hermesUsageCounters `json:"counters"`
}

// readFXUsageChunk reads the bound session's per-model totals. fx keeps no
// per-model split for usage it could not attribute, so only models count.
func readFXUsageChunk(ctx context.Context, path string, source domain.UsageSourceRecord) (sqliteUsageChunk, error) {
	if err := ctx.Err(); err != nil {
		return sqliteUsageChunk{}, err
	}
	data, err := readSnapshotFile(path)
	if err != nil {
		return sqliteUsageChunk{}, err
	}
	var document fxUsageDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return sqliteUsageChunk{}, err
	}
	if document.SessionID != source.NativeSessionID {
		return sqliteUsageChunk{}, errors.New("fx usage belongs to another session")
	}
	rows := make(map[string]hermesUsageCounters, len(document.Snapshot.Models))
	for _, usage := range document.Snapshot.Models {
		counters, ok := usage.counters()
		if !ok {
			return sqliteUsageChunk{}, errors.New("fx usage has negative counters")
		}
		model := strings.TrimSpace(usage.Model)
		// Each model appears once; adding keeps any repeat counted.
		rows[model] = addCounters(rows[model], counters)
	}
	models := make([]string, 0, len(rows))
	for model := range rows {
		models = append(models, model)
	}
	sort.Strings(models)
	chunk := sqliteUsageChunk{atEnd: true}
	for _, model := range models {
		chunk.nextOffset = counterVolume(chunk.nextOffset, rows[model])
		data, err := json.Marshal(fxUsageRow{Model: model, Counters: rows[model]})
		if err != nil {
			return sqliteUsageChunk{}, err
		}
		chunk.records = append(chunk.records, jsonlRecord{Data: data, Offset: int64(len(chunk.records))})
	}
	return chunk, nil
}

func addCounters(a, b hermesUsageCounters) hermesUsageCounters {
	return hermesUsageCounters{
		APICalls:           a.APICalls + b.APICalls,
		InputTokens:        a.InputTokens + b.InputTokens,
		OutputTokens:       a.OutputTokens + b.OutputTokens,
		CacheReadTokens:    a.CacheReadTokens + b.CacheReadTokens,
		CacheWriteTokens:   a.CacheWriteTokens + b.CacheWriteTokens,
		ReasoningTokens:    a.ReasoningTokens + b.ReasoningTokens,
		EstimatedCostNanos: a.EstimatedCostNanos + b.EstimatedCostNanos,
		ActualCostNanos:    a.ActualCostNanos + b.ActualCostNanos,
	}
}

func parseFX(source domain.UsageSourceContext, records []jsonlRecord, state *snapshotParserStateV1, result *parseResult) {
	if state.Rows == nil {
		state.Rows = map[string]hermesUsageCounters{}
	}
	for _, record := range records {
		var row fxUsageRow
		if err := json.Unmarshal(record.Data, &row); err != nil || !row.Counters.valid() {
			recordMalformed(result)
			continue
		}
		delta, ok := advanceCumulativeRow(state.Rows, row.Model, row.Counters, result)
		if !ok {
			continue
		}
		event, ok := fxUsageEvent(source, row, delta)
		if !ok {
			recordMalformed(result)
			continue
		}
		result.Events = append(result.Events, event)
	}
}

// fxModelIdentity splits fx's "<connection>/<model>" into the vendor that
// served the model and the vendor's model ID. fx's Codex connection signs in
// with ChatGPT, so OpenAI serves it.
func fxModelIdentity(raw string) (vendor, model string) {
	connection, rest, ok := strings.Cut(strings.TrimSpace(raw), "/")
	if !ok {
		return "", connection
	}
	if inner, name, nested := strings.Cut(rest, "/"); nested {
		return inner, name
	}
	if strings.EqualFold(connection, "codex") {
		return "openai", rest
	}
	return connection, rest
}

// fxUsageEvent maps one model's growth since the baseline. fx reports input
// with its cached reads and writes included, as OpenAI does, and output with
// reasoning included. Input smaller than its cache buckets can only exclude
// them, so it is counted that way.
func fxUsageEvent(source domain.UsageSourceContext, row fxUsageRow, delta hermesUsageCounters) (domain.ModelUsageEvent, bool) {
	cached, ok := sumNonNegative(delta.CacheReadTokens, delta.CacheWriteTokens)
	if !ok {
		return domain.ModelUsageEvent{}, false
	}
	input := delta.InputTokens
	if input < cached {
		if input, ok = sumNonNegative(input, cached); !ok {
			return domain.ModelUsageEvent{}, false
		}
	}
	vendor, model := fxModelIdentity(row.Model)
	providerID := domain.UsageProviderOpenAI
	var tokens domain.UsageTokenMetrics
	if strings.EqualFold(vendor, "anthropic") {
		providerID = domain.UsageProviderAnthropic
		tokens, ok = normalizeAnthropicUsage(input-cached, delta.CacheWriteTokens, delta.CacheReadTokens, delta.OutputTokens, nil, nil)
	} else {
		tokens, ok = normalizeOpenAIUsage(input, delta.CacheReadTokens, delta.CacheWriteTokens, delta.OutputTokens)
	}
	if !ok {
		return domain.ModelUsageEvent{}, false
	}
	if model == "" {
		model = "unknown"
	}
	usage, err := json.Marshal(map[string]any{
		"model":              row.Model,
		"request_count":      delta.APICalls,
		"input_tokens":       delta.InputTokens,
		"output_tokens":      delta.OutputTokens,
		"cache_read_tokens":  delta.CacheReadTokens,
		"cache_write_tokens": delta.CacheWriteTokens,
		"reasoning_tokens":   delta.ReasoningTokens,
	})
	if err != nil {
		return domain.ModelUsageEvent{}, false
	}
	billingProvider := canonicalBillingProvider(vendor)
	cumulative := row.Counters
	event := domain.ModelUsageEvent{
		ProviderID:            providerID,
		BillingProviderID:     billingProvider,
		BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
		ModelID:               model,
		MeasurementKind:       domain.UsageMeasurementNativeReported,
		Tokens:                tokens,
		ProviderUsageJSON:     boundedProviderUsage(usage),
		// The cumulative values make the key stable across replays of the
		// same observation and distinct for every later one.
		SourceEventKey: stableSourceEventKey(
			"fx", source.NativeRootID, row.Model,
			strconv.FormatInt(cumulative.APICalls, 10),
			strconv.FormatInt(cumulative.InputTokens, 10),
			strconv.FormatInt(cumulative.OutputTokens, 10),
			strconv.FormatInt(cumulative.CacheReadTokens, 10),
			strconv.FormatInt(cumulative.CacheWriteTokens, 10),
			strconv.FormatInt(cumulative.ReasoningTokens, 10),
		),
	}
	// A subscription connection such as Codex reports no cost; the catalog
	// prices it.
	if delta.EstimatedCostNanos > 0 {
		cost := delta.EstimatedCostNanos
		event.Costs = domain.UsageEventCosts{
			EstimatedCostNanos: &cost,
			PricingVersion:     fxPricingVersion,
		}
	}
	return event, true
}
