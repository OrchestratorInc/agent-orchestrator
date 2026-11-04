package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// openCodeStepFinish is the part OpenCode writes once per provider call,
	// carrying that call's tokens and cost.
	openCodeStepFinish = "step-finish"
	// openCodeCursorTieBits orders steps committed in the same millisecond by
	// the low bits of their row ID.
	openCodeCursorTieBits = 10
	// openCodePricingVersion and kiloPricingVersion label a cost AO stored as
	// the agent computed it.
	openCodePricingVersion = "opencode:reported"
	kiloPricingVersion     = "kilocode:reported"
)

// openCodeStepRow is one step-finish part joined to its assistant message's
// provider and model. Kilo Code is an OpenCode fork and keeps the same schema.
type openCodeStepRow struct {
	PartID      string          `json:"part_id"`
	SessionID   string          `json:"session_id"`
	MessageID   string          `json:"message_id"`
	TimeCreated int64           `json:"time_created"`
	ProviderID  string          `json:"provider_id"`
	ModelID     string          `json:"model_id"`
	Step        json.RawMessage `json:"step"`
}

// openCodeStepFinishPart is the step-finish part body. input excludes both
// cache counters, and reasoning is billed as output but counted apart from it.
type openCodeStepFinishPart struct {
	Type   string `json:"type"`
	Tokens *struct {
		Input     int64 `json:"input"`
		Output    int64 `json:"output"`
		Reasoning int64 `json:"reasoning"`
		Cache     struct {
			Read  int64 `json:"read"`
			Write int64 `json:"write"`
		} `json:"cache"`
	} `json:"tokens"`
	Cost *float64 `json:"cost"`
}

// readOpenCodeSteps reads step-finish parts for the bound OpenCode or Kilo
// session and every sub-agent session it spawned. The assistant message row is
// rewritten as a call streams, but its step-finish part is written once the
// call completes, so the parts form a per-call ledger.
//
// The cursor is the part's creation time in milliseconds with the low row ID
// bits as a tie break. A bare row ID would not do: OpenCode undo deletes the
// newest messages, and SQLite then reuses their row IDs for the next ones.
//
// A bound session that is itself a sub-agent reads nothing. Its parent's
// binding already reads it, and the AO plugin reports the child's session ID
// too whenever OpenCode switches to it.
func readOpenCodeSteps(ctx context.Context, db *sql.DB, nativeSessionID string, after int64, limit int) ([]jsonlRecord, error) {
	rows, err := db.QueryContext(ctx, `
WITH RECURSIVE tree(id) AS (
    SELECT id FROM session WHERE id = ? AND parent_id IS NULL
    UNION
    SELECT s.id FROM session s JOIN tree ON s.parent_id = tree.id
),
steps AS (
    SELECT p.id AS part_id, p.session_id, p.message_id, p.time_created, p.data,
           CASE WHEN json_valid(m.data) THEN json_extract(m.data, '$.providerID') END AS provider_id,
           CASE WHEN json_valid(m.data) THEN json_extract(m.data, '$.modelID') END AS model_id,
           (p.time_created << ?) | (p.rowid & ?) AS step_cursor
    FROM part p
    JOIN message m ON m.id = p.message_id
    WHERE p.session_id IN (SELECT id FROM tree) AND p.time_created > 0
      AND CASE WHEN json_valid(p.data) THEN json_extract(p.data, '$.type') END = ?
)
SELECT step_cursor, part_id, session_id, message_id, time_created, data,
       COALESCE(provider_id, ''), COALESCE(model_id, '')
FROM steps
WHERE step_cursor > ?
ORDER BY step_cursor
LIMIT ?`,
		nativeSessionID, openCodeCursorTieBits, int64(1)<<openCodeCursorTieBits-1,
		openCodeStepFinish, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records []jsonlRecord
	for rows.Next() {
		var cursor int64
		var row openCodeStepRow
		var step string
		if err := rows.Scan(
			&cursor, &row.PartID, &row.SessionID, &row.MessageID, &row.TimeCreated,
			&step, &row.ProviderID, &row.ModelID,
		); err != nil {
			return nil, err
		}
		row.Step = json.RawMessage(step)
		data, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		records = append(records, jsonlRecord{Data: data, Offset: cursor})
	}
	return records, rows.Err()
}

func parseOpenCode(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	keyPrefix, pricingVersion := "opencode", openCodePricingVersion
	if source.Source.Kind == domain.UsageSourceKiloDB {
		keyPrefix, pricingVersion = "kilocode", kiloPricingVersion
	}
	eventsByKey := make(map[string]domain.ModelUsageEvent)
	for _, record := range records {
		var row openCodeStepRow
		var step openCodeStepFinishPart
		if err := json.Unmarshal(record.Data, &row); err != nil ||
			json.Unmarshal(row.Step, &step) != nil {
			recordMalformed(result)
			continue
		}
		if step.Type != openCodeStepFinish {
			continue
		}
		model := firstNonEmpty(row.ModelID)
		if model == "" || strings.TrimSpace(row.PartID) == "" || step.Tokens == nil {
			recordMalformed(result)
			continue
		}
		usage := step.Tokens
		input, okInput := sumNonNegative(usage.Input, usage.Cache.Read, usage.Cache.Write)
		output, okOutput := sumNonNegative(usage.Output, usage.Reasoning)
		if !okInput || !okOutput {
			recordMalformed(result)
			continue
		}
		if input == 0 && output == 0 {
			// A call that failed before the provider reported usage.
			continue
		}
		provider := strings.TrimSpace(row.ProviderID)
		providerID := domain.UsageProviderOpenAI
		var tokens domain.UsageTokenMetrics
		var ok bool
		if strings.EqualFold(provider, "anthropic") {
			providerID = domain.UsageProviderAnthropic
			tokens, ok = normalizeAnthropicUsage(usage.Input, usage.Cache.Write, usage.Cache.Read, output, nil, nil)
		} else {
			// OpenCode has already normalized every other provider to the same
			// counters, so the OpenAI vocabulary holds them; BillingProviderID
			// keeps who billed it.
			tokens, ok = normalizeOpenAIUsage(input, usage.Cache.Read, usage.Cache.Write, output)
		}
		if !ok {
			recordMalformed(result)
			continue
		}
		billingProvider := canonicalBillingProvider(provider)
		event := domain.ModelUsageEvent{
			ProviderID:            providerID,
			BillingProviderID:     billingProvider,
			BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
			ModelID:               model,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			ProviderUsageJSON:     boundedProviderUsage(row.Step),
			SourceEventKey:        stableSourceEventKey(keyPrefix, source.NativeRootID, row.PartID),
			Costs:                 openCodeReportedCosts(step.Cost, pricingVersion),
		}
		if row.TimeCreated > 0 {
			event.CreatedAt = time.UnixMilli(row.TimeCreated).UTC()
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

// openCodeReportedCosts keeps the USD cost OpenCode computed for the call from
// its own model catalog, which prices providers AO's catalog does not list. A
// zero cost means a free or plan-billed model, or one OpenCode has no price
// for, so the catalog is left to try.
func openCodeReportedCosts(cost *float64, pricingVersion string) domain.UsageEventCosts {
	if cost == nil {
		return domain.UsageEventCosts{}
	}
	nanos, ok := usdToNanos(*cost)
	if !ok || nanos == 0 {
		return domain.UsageEventCosts{}
	}
	return domain.UsageEventCosts{EstimatedCostNanos: &nanos, PricingVersion: pricingVersion}
}
