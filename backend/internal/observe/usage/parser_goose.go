package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// gooseCostSourceProviderReported marks a cost Goose took from the
	// provider's response rather than estimating it from its own catalog.
	gooseCostSourceProviderReported = "provider_reported"
	// gooseCostSourceCarriedForward marks the synthetic row Goose inserts to
	// reconcile session totals recorded before the ledger existed. It names no
	// model and is not a provider call.
	gooseCostSourceCarriedForward = "carried_forward"
	// goosePricingVersion labels a cost AO stored as Goose reported it.
	goosePricingVersion = "goose:provider_reported"
)

// gooseLedgerRow is one usage_ledger row joined to its session's provider.
// Goose inserts exactly one row per provider call and never updates it, so
// the ledger row ID is a stable, monotonic cursor. The accumulated_* session
// totals are deliberately not read: they would double count the ledger.
type gooseLedgerRow struct {
	ID               int64    `json:"id"`
	SessionID        string   `json:"session_id"`
	CreatedTimestamp int64    `json:"created_timestamp"`
	Model            string   `json:"model"`
	ProviderName     string   `json:"provider_name"`
	InputTokens      *int64   `json:"input_tokens"`
	OutputTokens     *int64   `json:"output_tokens"`
	TotalTokens      *int64   `json:"total_tokens"`
	CacheReadTokens  *int64   `json:"cache_read_tokens"`
	CacheWriteTokens *int64   `json:"cache_write_tokens"`
	Cost             *float64 `json:"cost"`
	CostSource       string   `json:"cost_source"`
	IsCompaction     bool     `json:"is_compaction"`
}

// readGooseLedgerRows reads ledger rows for the bound Goose session and every
// session it spawned. Goose files subagent calls under the child session and
// links it by parent_session_id, the same tree its own usage totals walk.
func readGooseLedgerRows(ctx context.Context, db *sql.DB, nativeSessionID string, after int64, limit int) ([]jsonlRecord, error) {
	rows, err := db.QueryContext(ctx, `
WITH RECURSIVE tree(id) AS (
    SELECT id FROM sessions WHERE id = ?
    UNION
    SELECT s.id FROM sessions s JOIN tree ON s.parent_session_id = tree.id
)
SELECT u.id, u.session_id, u.created_timestamp, COALESCE(u.model, ''),
       COALESCE(s.provider_name, ''), u.input_tokens, u.output_tokens,
       u.total_tokens, u.cache_read_tokens, u.cache_write_tokens, u.cost,
       COALESCE(u.cost_source, ''), COALESCE(u.is_compaction, 0)
FROM usage_ledger u
JOIN sessions s ON s.id = u.session_id
WHERE u.session_id IN (SELECT id FROM tree) AND u.id > ?
ORDER BY u.id
LIMIT ?`, nativeSessionID, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []jsonlRecord
	for rows.Next() {
		var row gooseLedgerRow
		var input, output, total, cacheRead, cacheWrite sql.NullInt64
		var cost sql.NullFloat64
		var compaction int64
		if err := rows.Scan(
			&row.ID, &row.SessionID, &row.CreatedTimestamp, &row.Model,
			&row.ProviderName, &input, &output, &total, &cacheRead, &cacheWrite,
			&cost, &row.CostSource, &compaction,
		); err != nil {
			return nil, err
		}
		row.InputTokens = nullInt64Ptr(input)
		row.OutputTokens = nullInt64Ptr(output)
		row.TotalTokens = nullInt64Ptr(total)
		row.CacheReadTokens = nullInt64Ptr(cacheRead)
		row.CacheWriteTokens = nullInt64Ptr(cacheWrite)
		if cost.Valid {
			row.Cost = &cost.Float64
		}
		row.IsCompaction = compaction != 0
		data, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		records = append(records, jsonlRecord{Data: data, Offset: row.ID})
	}
	return records, rows.Err()
}

func nullInt64Ptr(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	return &value.Int64
}

func parseGoose(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	eventsByKey := make(map[string]domain.ModelUsageEvent)
	for _, record := range records {
		var row gooseLedgerRow
		if err := json.Unmarshal(record.Data, &row); err != nil {
			recordMalformed(result)
			continue
		}
		if row.CostSource == gooseCostSourceCarriedForward {
			continue
		}
		model := firstNonEmpty(row.Model)
		if model == "" || row.ID <= 0 {
			recordMalformed(result)
			continue
		}
		event, ok := gooseUsageEvent(source, row, model)
		if !ok {
			recordMalformed(result)
			continue
		}
		event.ProviderUsageJSON = boundedProviderUsage(record.Data)
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

// gooseUsageEvent maps one ledger row. Goose normalizes every provider's usage
// so input_tokens is the whole prompt, cache reads and writes included.
func gooseUsageEvent(source domain.UsageSourceContext, row gooseLedgerRow, model string) (domain.ModelUsageEvent, bool) {
	cacheRead := valueOrZero(row.CacheReadTokens)
	cacheWrite := valueOrZero(row.CacheWriteTokens)
	if cacheRead < 0 || cacheWrite < 0 ||
		row.OutputTokens != nil && *row.OutputTokens < 0 ||
		row.TotalTokens != nil && *row.TotalTokens < 0 {
		return domain.ModelUsageEvent{}, false
	}
	provider := strings.TrimSpace(row.ProviderName)
	providerID := domain.UsageProviderOpenAI
	if strings.EqualFold(provider, "anthropic") {
		providerID = domain.UsageProviderAnthropic
	}
	tokens := domain.UsageTokenMetrics{OutputTokens: row.OutputTokens}
	if row.InputTokens != nil {
		input := *row.InputTokens
		output := valueOrZero(row.OutputTokens)
		var ok bool
		if providerID == domain.UsageProviderAnthropic {
			cached, sumOK := sumNonNegative(cacheRead, cacheWrite)
			if !sumOK || cached > input {
				return domain.ModelUsageEvent{}, false
			}
			tokens, ok = normalizeAnthropicUsage(input-cached, cacheWrite, cacheRead, output, nil, nil)
		} else {
			tokens, ok = normalizeOpenAIUsage(input, cacheRead, cacheWrite, output)
		}
		if !ok {
			return domain.ModelUsageEvent{}, false
		}
		tokens.OutputTokens = row.OutputTokens
	}
	billingProvider := gooseBillingProvider(provider)
	event := domain.ModelUsageEvent{
		ProviderID:            providerID,
		BillingProviderID:     billingProvider,
		BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
		ModelID:               model,
		MeasurementKind:       domain.UsageMeasurementNativeReported,
		Tokens:                tokens,
		SourceEventKey: stableSourceEventKey(
			"goose", source.NativeRootID, strconv.FormatInt(row.ID, 10),
		),
	}
	if row.CreatedTimestamp > 0 {
		event.CreatedAt = time.Unix(row.CreatedTimestamp, 0).UTC()
	}
	if row.CostSource == gooseCostSourceProviderReported && row.Cost != nil {
		nanos, ok := usdToNanos(*row.Cost)
		if !ok {
			return domain.ModelUsageEvent{}, false
		}
		event.Costs = domain.UsageEventCosts{
			EstimatedCostNanos: &nanos,
			PricingVersion:     goosePricingVersion,
		}
	}
	return event, true
}

// gooseBillingProvider records the provider Goose's session was configured
// with, renaming Goose's ids for the two cloud routes the catalog names
// differently.
func gooseBillingProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "aws_bedrock":
		return "bedrock"
	case "gcp_vertex_ai":
		return "vertex_ai"
	}
	return canonicalBillingProvider(provider)
}

func valueOrZero(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
