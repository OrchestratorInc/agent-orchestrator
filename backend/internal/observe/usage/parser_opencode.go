package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	openCodeStepFinish     = "step-finish"
	openCodeCursorTieBits  = 10
	openCodePricingVersion = "opencode:reported"
	kiloPricingVersion     = "kilocode:reported"
)

var openCodeGatewayProviders = []string{"opencode", "kilo", "kilocode"}

type openCodeStepRow struct {
	PartID      string          `json:"part_id"`
	SessionID   string          `json:"session_id"`
	MessageID   string          `json:"message_id"`
	TimeCreated int64           `json:"time_created"`
	ProviderID  string          `json:"provider_id"`
	ModelID     string          `json:"model_id"`
	Step        json.RawMessage `json:"step"`
}

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
    WHERE p.session_id IN (SELECT id FROM tree) AND p.time_created > 0 AND p.time_created >= ?
      AND CASE WHEN json_valid(p.data) THEN json_extract(p.data, '$.type') END = ?
)
SELECT step_cursor, part_id, session_id, message_id, time_created, data,
       COALESCE(provider_id, ''), COALESCE(model_id, '')
FROM steps
WHERE step_cursor > ?
ORDER BY step_cursor
LIMIT ?`,
		nativeSessionID, openCodeCursorTieBits, int64(1)<<openCodeCursorTieBits-1,
		after>>openCodeCursorTieBits, openCodeStepFinish, after, limit)
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
			continue
		}
		provider := strings.TrimSpace(row.ProviderID)
		providerID := routedUsageProvider(provider)
		tokens, ok := normalizeRoutedUsage(providerID, usage.Input, usage.Cache.Read, usage.Cache.Write, output)
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
			ProviderUsageJSON:     providerUsageWithCacheWrite(row.Step, &usage.Cache.Write, nil),
			SourceEventKey:        stableSourceEventKey(keyPrefix, source.NativeRootID, row.PartID),
			Costs: openCodeReportedCosts(step.Cost, pricingVersion,
				slices.Contains(openCodeGatewayProviders, billingProvider)),
		}
		if row.TimeCreated > 0 {
			event.CreatedAt = time.UnixMilli(row.TimeCreated).UTC()
		}
		result.addEvent(eventsByKey, event)
	}
}

func openCodeReportedCosts(cost *float64, pricingVersion string, ownGateway bool) domain.UsageEventCosts {
	if cost == nil {
		return domain.UsageEventCosts{}
	}
	nanos, ok := usdToNanos(*cost)
	if !ok || nanos == 0 && !ownGateway {
		return domain.UsageEventCosts{}
	}
	return domain.UsageEventCosts{EstimatedCostNanos: &nanos, PricingVersion: pricingVersion}
}
