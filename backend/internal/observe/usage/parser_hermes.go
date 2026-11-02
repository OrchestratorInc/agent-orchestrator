package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// maxHermesUsageRows bounds the per-row baselines a Hermes source keeps.
	// One row exists per session, model, billing route and task, so a
	// conversation needs a handful; the bound only stops unbounded growth.
	maxHermesUsageRows = 4096
	// hermesCostStatusActual marks a cost Hermes took from the provider
	// rather than estimating it from its own pricing table.
	hermesCostStatusActual = "actual"
	// hermesPricingVersion labels a cost AO stored as Hermes reported it.
	hermesPricingVersion = "hermes:provider_reported"
)

// hermesParserStateV1 is the last observed value of every session_model_usage
// row, keyed by the row's identity. Hermes only ever adds a call's delta to
// these rows, so the difference from the previous observation is exactly the
// usage of the calls in between, as Codex's cumulative baseline is.
type hermesParserStateV1 struct {
	Rows map[string]hermesUsageCounters `json:"rows"`
}

type hermesUsageCounters struct {
	APICalls           int64 `json:"api_calls"`
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	CacheReadTokens    int64 `json:"cache_read_tokens"`
	CacheWriteTokens   int64 `json:"cache_write_tokens"`
	ReasoningTokens    int64 `json:"reasoning_tokens"`
	EstimatedCostNanos int64 `json:"estimated_cost_nanos"`
	ActualCostNanos    int64 `json:"actual_cost_nanos"`
}

func validateHermesParserState(state *hermesParserStateV1) error {
	if len(state.Rows) > maxHermesUsageRows {
		return errors.New("hermes state exceeds row bound")
	}
	for _, counters := range state.Rows {
		if !counters.valid() {
			return errors.New("hermes state has negative counters")
		}
	}
	return nil
}

func (c hermesUsageCounters) valid() bool {
	return c.APICalls >= 0 && c.InputTokens >= 0 && c.OutputTokens >= 0 &&
		c.CacheReadTokens >= 0 && c.CacheWriteTokens >= 0 && c.ReasoningTokens >= 0 &&
		c.EstimatedCostNanos >= 0 && c.ActualCostNanos >= 0
}

// below reports whether any counter decreased since the baseline.
func (c hermesUsageCounters) below(baseline hermesUsageCounters) bool {
	return c.APICalls < baseline.APICalls || c.InputTokens < baseline.InputTokens ||
		c.OutputTokens < baseline.OutputTokens || c.CacheReadTokens < baseline.CacheReadTokens ||
		c.CacheWriteTokens < baseline.CacheWriteTokens || c.ReasoningTokens < baseline.ReasoningTokens ||
		c.EstimatedCostNanos < baseline.EstimatedCostNanos || c.ActualCostNanos < baseline.ActualCostNanos
}

func (c hermesUsageCounters) minus(baseline hermesUsageCounters) hermesUsageCounters {
	return hermesUsageCounters{
		APICalls:           c.APICalls - baseline.APICalls,
		InputTokens:        c.InputTokens - baseline.InputTokens,
		OutputTokens:       c.OutputTokens - baseline.OutputTokens,
		CacheReadTokens:    c.CacheReadTokens - baseline.CacheReadTokens,
		CacheWriteTokens:   c.CacheWriteTokens - baseline.CacheWriteTokens,
		ReasoningTokens:    c.ReasoningTokens - baseline.ReasoningTokens,
		EstimatedCostNanos: c.EstimatedCostNanos - baseline.EstimatedCostNanos,
		ActualCostNanos:    c.ActualCostNanos - baseline.ActualCostNanos,
	}
}

// hermesUsageRow is one session_model_usage row as read from state.db.
type hermesUsageRow struct {
	SessionID        string  `json:"session_id"`
	Model            string  `json:"model"`
	BillingProvider  string  `json:"billing_provider"`
	BillingBaseURL   string  `json:"billing_base_url"`
	BillingMode      string  `json:"billing_mode"`
	Task             string  `json:"task"`
	APICallCount     int64   `json:"api_call_count"`
	InputTokens      int64   `json:"input_tokens"`
	OutputTokens     int64   `json:"output_tokens"`
	CacheReadTokens  int64   `json:"cache_read_tokens"`
	CacheWriteTokens int64   `json:"cache_write_tokens"`
	ReasoningTokens  int64   `json:"reasoning_tokens"`
	EstimatedCostUSD float64 `json:"estimated_cost_usd"`
	ActualCostUSD    float64 `json:"actual_cost_usd"`
	CostStatus       string  `json:"cost_status"`
	CostSource       string  `json:"cost_source"`
	LastSeen         float64 `json:"last_seen"`
}

// key is the row's primary key in Hermes's schema.
func (r hermesUsageRow) key() string {
	encoded, _ := json.Marshal([]string{
		r.SessionID, r.Model, r.BillingProvider, r.BillingBaseURL, r.BillingMode, r.Task,
	})
	return string(encoded)
}

func (r hermesUsageRow) counters() (hermesUsageCounters, bool) {
	estimated, ok := usdToNanos(r.EstimatedCostUSD)
	if !ok {
		return hermesUsageCounters{}, false
	}
	actual, ok := usdToNanos(r.ActualCostUSD)
	if !ok {
		return hermesUsageCounters{}, false
	}
	counters := hermesUsageCounters{
		APICalls:           r.APICallCount,
		InputTokens:        r.InputTokens,
		OutputTokens:       r.OutputTokens,
		CacheReadTokens:    r.CacheReadTokens,
		CacheWriteTokens:   r.CacheWriteTokens,
		ReasoningTokens:    r.ReasoningTokens,
		EstimatedCostNanos: estimated,
		ActualCostNanos:    actual,
	}
	return counters, counters.valid()
}

// readHermesUsageChunk reads every usage row of the bound root session and
// the sessions descending from it. Hermes mints a child session at each
// context compression, and delegated subagents run as child sessions; both
// link to their parent by parent_session_id and bill only their own calls.
// The cursor is the tree's total usage volume, which grows with every call.
func readHermesUsageChunk(ctx context.Context, db *sql.DB, source domain.UsageSourceRecord, _ int) (sqliteUsageChunk, error) {
	rows, err := db.QueryContext(ctx, `
WITH RECURSIVE tree(id) AS (
    SELECT id FROM sessions WHERE id = ?
    UNION
    SELECT s.id FROM sessions s JOIN tree ON s.parent_session_id = tree.id
)
SELECT u.session_id, u.model, u.billing_provider, u.billing_base_url,
       u.billing_mode, u.task, u.api_call_count, u.input_tokens,
       u.output_tokens, u.cache_read_tokens, u.cache_write_tokens,
       u.reasoning_tokens, u.estimated_cost_usd, u.actual_cost_usd,
       COALESCE(u.cost_status, ''), COALESCE(u.cost_source, ''),
       COALESCE(u.last_seen, 0)
FROM session_model_usage u
WHERE u.session_id IN (SELECT id FROM tree)
ORDER BY u.rowid
LIMIT ?`, source.NativeSessionID, maxHermesUsageRows)
	if err != nil {
		return sqliteUsageChunk{}, err
	}
	defer func() { _ = rows.Close() }()
	chunk := sqliteUsageChunk{atEnd: true}
	for rows.Next() {
		var row hermesUsageRow
		if err := rows.Scan(
			&row.SessionID, &row.Model, &row.BillingProvider, &row.BillingBaseURL,
			&row.BillingMode, &row.Task, &row.APICallCount, &row.InputTokens,
			&row.OutputTokens, &row.CacheReadTokens, &row.CacheWriteTokens,
			&row.ReasoningTokens, &row.EstimatedCostUSD, &row.ActualCostUSD,
			&row.CostStatus, &row.CostSource, &row.LastSeen,
		); err != nil {
			return sqliteUsageChunk{}, err
		}
		if volume, ok := sumNonNegative(
			chunk.nextOffset, row.APICallCount, row.InputTokens, row.OutputTokens,
			row.CacheReadTokens, row.CacheWriteTokens, row.ReasoningTokens,
		); ok {
			chunk.nextOffset = volume
		}
		data, err := json.Marshal(row)
		if err != nil {
			return sqliteUsageChunk{}, err
		}
		chunk.records = append(chunk.records, jsonlRecord{Data: data, Offset: int64(len(chunk.records))})
	}
	return chunk, rows.Err()
}

func parseHermes(source domain.UsageSourceContext, records []jsonlRecord, state *hermesParserStateV1, result *parseResult) {
	if state.Rows == nil {
		state.Rows = map[string]hermesUsageCounters{}
	}
	eventsByKey := make(map[string]domain.ModelUsageEvent)
	for _, record := range records {
		var row hermesUsageRow
		if err := json.Unmarshal(record.Data, &row); err != nil {
			recordMalformed(result)
			continue
		}
		current, ok := row.counters()
		if !ok || strings.TrimSpace(row.SessionID) == "" {
			recordMalformed(result)
			continue
		}
		key := row.key()
		baseline, known := state.Rows[key]
		if !known && len(state.Rows) >= maxHermesUsageRows {
			recordMalformed(result)
			continue
		}
		if current.below(baseline) {
			// A row only shrinks when Hermes deleted and recreated it. Its new
			// value is the next baseline; billing the drop would be negative
			// and billing the full new value could repeat earlier calls.
			result.Cursor.AnomalyCount++
			result.Cursor.LastErrorCode = domain.UsageErrorNonMonotonicCumulativeUsage
			state.Rows[key] = current
			continue
		}
		state.Rows[key] = current
		if current == baseline {
			continue
		}
		event, ok := hermesUsageEvent(source, row, current, current.minus(baseline))
		if !ok {
			recordMalformed(result)
			continue
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

// hermesUsageEvent maps one row's growth since the baseline. Hermes stores
// input without either cache bucket, and output already includes reasoning.
func hermesUsageEvent(
	source domain.UsageSourceContext,
	row hermesUsageRow,
	cumulative hermesUsageCounters,
	delta hermesUsageCounters,
) (domain.ModelUsageEvent, bool) {
	provider := strings.TrimSpace(row.BillingProvider)
	providerID := domain.UsageProviderOpenAI
	var tokens domain.UsageTokenMetrics
	var ok bool
	if strings.EqualFold(provider, "anthropic") {
		providerID = domain.UsageProviderAnthropic
		tokens, ok = normalizeAnthropicUsage(
			delta.InputTokens, delta.CacheWriteTokens, delta.CacheReadTokens, delta.OutputTokens, nil, nil,
		)
	} else {
		var input int64
		input, ok = sumNonNegative(delta.InputTokens, delta.CacheReadTokens, delta.CacheWriteTokens)
		if ok {
			tokens, ok = normalizeOpenAIUsage(input, delta.CacheReadTokens, delta.CacheWriteTokens, delta.OutputTokens)
		}
	}
	if !ok {
		return domain.ModelUsageEvent{}, false
	}
	model := firstNonEmpty(row.Model)
	if model == "" {
		model = "unknown"
	}
	usage, err := json.Marshal(map[string]any{
		"session_id":         row.SessionID,
		"model":              row.Model,
		"billing_provider":   row.BillingProvider,
		"billing_mode":       row.BillingMode,
		"task":               row.Task,
		"api_call_count":     delta.APICalls,
		"input_tokens":       delta.InputTokens,
		"output_tokens":      delta.OutputTokens,
		"cache_read_tokens":  delta.CacheReadTokens,
		"cache_write_tokens": delta.CacheWriteTokens,
		"reasoning_tokens":   delta.ReasoningTokens,
		"cost_status":        row.CostStatus,
		"cost_source":        row.CostSource,
	})
	if err != nil {
		return domain.ModelUsageEvent{}, false
	}
	billingProvider := canonicalBillingProvider(provider)
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
			"hermes", source.NativeRootID, row.key(),
			strconv.FormatInt(cumulative.APICalls, 10),
			strconv.FormatInt(cumulative.InputTokens, 10),
			strconv.FormatInt(cumulative.OutputTokens, 10),
			strconv.FormatInt(cumulative.CacheReadTokens, 10),
			strconv.FormatInt(cumulative.CacheWriteTokens, 10),
			strconv.FormatInt(cumulative.ReasoningTokens, 10),
		),
	}
	if row.LastSeen > 0 && !math.IsInf(row.LastSeen, 0) && row.LastSeen < float64(math.MaxInt32)*4 {
		seconds, fraction := math.Modf(row.LastSeen)
		event.CreatedAt = time.Unix(int64(seconds), int64(fraction*1e9)).UTC()
	}
	// Hermes's per-call path records a provider-returned cost under the
	// estimated column with status "actual"; actual_cost_usd is filled only
	// by explicit reconciliation. Anything else is Hermes's own estimate,
	// which the catalog replaces.
	reported := int64(-1)
	switch {
	case delta.ActualCostNanos > 0:
		reported = delta.ActualCostNanos
	case row.CostStatus == hermesCostStatusActual && delta.EstimatedCostNanos > 0:
		reported = delta.EstimatedCostNanos
	}
	if reported >= 0 {
		event.Costs = domain.UsageEventCosts{
			EstimatedCostNanos: &reported,
			PricingVersion:     hermesPricingVersion,
		}
	}
	return event, true
}
