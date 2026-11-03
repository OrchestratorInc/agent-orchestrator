package usage

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// grokUSDTicksPerNano converts Grok's costUsdTicks, 10^10 ticks per USD,
	// to AO's nanodollars.
	grokUSDTicksPerNano = 10
	// grokPricingVersion labels a cost AO stored as Grok reported it.
	grokPricingVersion = "grok:provider_reported"
	grokSummaryFile    = "summary.json"
	grokUsageFile      = "usage.json"
)

// grokUsageTotals is one set of cumulative counters in Grok's usage.json.
// inputTokens includes cachedReadTokens, and outputTokens already includes
// reasoningTokens: Grok's costUsdTicks prices exactly those counts.
type grokUsageTotals struct {
	InputTokens         int64 `json:"inputTokens"`
	OutputTokens        int64 `json:"outputTokens"`
	CachedReadTokens    int64 `json:"cachedReadTokens"`
	CacheCreationTokens int64 `json:"cacheCreationTokens"`
	ReasoningTokens     int64 `json:"reasoningTokens"`
	CostUSDTicks        int64 `json:"costUsdTicks"`
	ModelCalls          int64 `json:"modelCalls"`
}

func (t grokUsageTotals) counters() (hermesUsageCounters, bool) {
	counters := hermesUsageCounters{
		APICalls:           t.ModelCalls,
		InputTokens:        t.InputTokens,
		OutputTokens:       t.OutputTokens,
		CacheReadTokens:    t.CachedReadTokens,
		CacheWriteTokens:   t.CacheCreationTokens,
		ReasoningTokens:    t.ReasoningTokens,
		EstimatedCostNanos: t.CostUSDTicks / grokUSDTicksPerNano,
	}
	return counters, t.CostUSDTicks >= 0 && counters.valid()
}

// grokUsageDocument is Grok's per-session usage.json, which Grok rewrites in
// place with the conversation's running totals.
type grokUsageDocument struct {
	SessionID string `json:"sessionId"`
	UpdatedAt string `json:"updatedAt"`
	Session   struct {
		grokUsageTotals
		PrimaryModelID string                     `json:"primaryModelId"`
		ModelUsage     map[string]grokUsageTotals `json:"modelUsage"`
	} `json:"session"`
}

// rows returns the per-model totals, or the session total under its primary
// model when Grok recorded no per-model split.
func (d grokUsageDocument) rows() map[string]grokUsageTotals {
	if len(d.Session.ModelUsage) > 0 {
		return d.Session.ModelUsage
	}
	if d.Session.grokUsageTotals == (grokUsageTotals{}) {
		return nil
	}
	return map[string]grokUsageTotals{d.Session.PrimaryModelID: d.Session.grokUsageTotals}
}

// grokUsageRow is one model's cumulative totals as read from usage.json.
// Inherited rows come from the parent of a forked or restored session.
type grokUsageRow struct {
	Model     string              `json:"model"`
	Counters  hermesUsageCounters `json:"counters"`
	UpdatedAt string              `json:"updated_at,omitempty"`
	Inherited bool                `json:"inherited,omitempty"`
}

func readGrokUsageDocument(path string) (grokUsageDocument, error) {
	data, err := readSnapshotFile(path)
	if err != nil {
		return grokUsageDocument{}, err
	}
	var document grokUsageDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return grokUsageDocument{}, err
	}
	return document, nil
}

// grokParentUsagePath returns the usage.json of the session this one was
// forked or restored from, when Grok keeps it beside this one. Grok's totals
// include that inherited history, which the parent already reported.
func grokParentUsagePath(sessionDir string) string {
	data, err := readSnapshotFile(filepath.Join(sessionDir, grokSummaryFile))
	if err != nil {
		return ""
	}
	var summary struct {
		ParentSessionID string `json:"parent_session_id"`
	}
	if json.Unmarshal(data, &summary) != nil {
		return ""
	}
	parent := strings.TrimSpace(summary.ParentSessionID)
	if parent == "" || parent == "." || parent == ".." || filepath.Base(parent) != parent {
		return ""
	}
	return filepath.Join(filepath.Dir(sessionDir), parent, grokUsageFile)
}

// readGrokUsageChunk reads the bound session's usage.json, plus its parent's
// totals so the first read does not bill the inherited history again.
func readGrokUsageChunk(ctx context.Context, path string, source domain.UsageSourceRecord) (sqliteUsageChunk, error) {
	if err := ctx.Err(); err != nil {
		return sqliteUsageChunk{}, err
	}
	document, err := readGrokUsageDocument(path)
	if err != nil {
		return sqliteUsageChunk{}, err
	}
	if document.SessionID != source.NativeSessionID {
		return sqliteUsageChunk{}, errors.New("grok usage belongs to another session")
	}
	chunk := sqliteUsageChunk{atEnd: true}
	appendRows := func(rows map[string]grokUsageTotals, updatedAt string, inherited bool) error {
		models := make([]string, 0, len(rows))
		for model := range rows {
			models = append(models, model)
		}
		sort.Strings(models)
		for _, model := range models {
			counters, ok := rows[model].counters()
			if !ok {
				return errors.New("grok usage has negative counters")
			}
			if !inherited {
				chunk.nextOffset = counterVolume(chunk.nextOffset, counters)
			}
			data, err := json.Marshal(grokUsageRow{
				Model: model, Counters: counters, UpdatedAt: updatedAt, Inherited: inherited,
			})
			if err != nil {
				return err
			}
			chunk.records = append(chunk.records, jsonlRecord{Data: data, Offset: int64(len(chunk.records))})
		}
		return nil
	}
	// The parser uses the parent's rows only on its first read. A parent
	// that is gone or unreadable leaves the baseline at zero.
	if parentPath := grokParentUsagePath(filepath.Dir(path)); parentPath != "" {
		if parent, err := readGrokUsageDocument(parentPath); err == nil {
			if err := appendRows(parent.rows(), "", true); err != nil {
				return sqliteUsageChunk{}, err
			}
		}
	}
	if err := appendRows(document.rows(), document.UpdatedAt, false); err != nil {
		return sqliteUsageChunk{}, err
	}
	return chunk, nil
}

func parseGrok(source domain.UsageSourceContext, records []jsonlRecord, state *snapshotParserStateV1, result *parseResult) {
	if state.Rows == nil {
		state.Rows = map[string]hermesUsageCounters{}
	}
	inherited := make(map[string]hermesUsageCounters)
	current := make([]grokUsageRow, 0, len(records))
	for _, record := range records {
		var row grokUsageRow
		if err := json.Unmarshal(record.Data, &row); err != nil || !row.Counters.valid() {
			recordMalformed(result)
			continue
		}
		if row.Inherited {
			inherited[row.Model] = row.Counters
			continue
		}
		current = append(current, row)
	}
	if !state.Seeded {
		// The parent may have kept working after the fork, so the history
		// both share is at most what each reports now.
		for _, row := range current {
			if parent, ok := inherited[row.Model]; ok {
				state.Rows[row.Model] = minCounters(parent, row.Counters)
			}
		}
		state.Seeded = true
	}
	for _, row := range current {
		delta, ok := advanceCumulativeRow(state.Rows, row.Model, row.Counters, result)
		if !ok {
			continue
		}
		event, ok := grokUsageEvent(source, row, delta)
		if !ok {
			recordMalformed(result)
			continue
		}
		result.Events = append(result.Events, event)
	}
}

// grokUsageEvent maps one model's growth since the baseline. Grok reports
// tokens the way xAI's API does: input includes cached reads, and output
// includes reasoning. A cache write is counted inside input unless the counts
// show Grok left it out.
func grokUsageEvent(source domain.UsageSourceContext, row grokUsageRow, delta hermesUsageCounters) (domain.ModelUsageEvent, bool) {
	input := delta.InputTokens
	if delta.CacheReadTokens+delta.CacheWriteTokens > input {
		var ok bool
		if input, ok = sumNonNegative(input, delta.CacheWriteTokens); !ok {
			return domain.ModelUsageEvent{}, false
		}
	}
	tokens, ok := normalizeOpenAIUsage(input, delta.CacheReadTokens, delta.CacheWriteTokens, delta.OutputTokens)
	if !ok {
		return domain.ModelUsageEvent{}, false
	}
	model := strings.TrimSpace(row.Model)
	if model == "" {
		model = "unknown"
	}
	usage, err := json.Marshal(map[string]any{
		"model":               row.Model,
		"modelCalls":          delta.APICalls,
		"inputTokens":         delta.InputTokens,
		"outputTokens":        delta.OutputTokens,
		"cachedReadTokens":    delta.CacheReadTokens,
		"cacheCreationTokens": delta.CacheWriteTokens,
		"reasoningTokens":     delta.ReasoningTokens,
	})
	if err != nil {
		return domain.ModelUsageEvent{}, false
	}
	cumulative := row.Counters
	event := domain.ModelUsageEvent{
		ProviderID:            domain.UsageProviderOpenAI,
		BillingProviderID:     "xai",
		BillingProviderSource: domain.ObservedBillingProviderSource("xai"),
		ModelID:               model,
		MeasurementKind:       domain.UsageMeasurementNativeReported,
		Tokens:                tokens,
		ProviderUsageJSON:     boundedProviderUsage(usage),
		// The cumulative values make the key stable across replays of the
		// same observation and distinct for every later one.
		SourceEventKey: stableSourceEventKey(
			"grok", source.NativeRootID, row.Model,
			strconv.FormatInt(cumulative.APICalls, 10),
			strconv.FormatInt(cumulative.InputTokens, 10),
			strconv.FormatInt(cumulative.OutputTokens, 10),
			strconv.FormatInt(cumulative.CacheReadTokens, 10),
			strconv.FormatInt(cumulative.CacheWriteTokens, 10),
			strconv.FormatInt(cumulative.ReasoningTokens, 10),
		),
	}
	if updatedAt, err := time.Parse(time.RFC3339Nano, row.UpdatedAt); err == nil {
		event.CreatedAt = updatedAt.UTC()
	}
	if delta.EstimatedCostNanos > 0 {
		cost := delta.EstimatedCostNanos
		event.Costs = domain.UsageEventCosts{
			EstimatedCostNanos: &cost,
			PricingVersion:     grokPricingVersion,
		}
	}
	return event, true
}
