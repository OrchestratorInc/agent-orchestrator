package usage

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	grokPricingVersion  = "grok:reported"
	grokCursorModelBits = 8
	grokTicksPerNano    = 10
	maxGrokUsageBytes   = 16 << 20
)

type grokUsageCounters struct {
	InputTokens         int64  `json:"inputTokens"`
	OutputTokens        int64  `json:"outputTokens"`
	CachedReadTokens    int64  `json:"cachedReadTokens"`
	CacheCreationTokens int64  `json:"cacheCreationTokens"`
	ReasoningTokens     int64  `json:"reasoningTokens"`
	ModelCalls          int64  `json:"modelCalls"`
	CostUsdTicks        *int64 `json:"costUsdTicks"`
}

type grokUsageFile struct {
	SessionID string `json:"sessionId"`
	Turns     []struct {
		grokUsageCounters
		TurnNumber     int64                        `json:"turnNumber"`
		EndedAt        string                       `json:"endedAt"`
		PrimaryModelID string                       `json:"primaryModelId"`
		ModelUsage     map[string]grokUsageCounters `json:"modelUsage"`
	} `json:"turns"`
}

type grokTurnRecord struct {
	TurnNumber int64             `json:"turn_number"`
	EndedAt    string            `json:"ended_at"`
	ModelID    string            `json:"model_id"`
	Usage      grokUsageCounters `json:"usage"`
}

func readGrokTurns(ctx context.Context, path, nativeSessionID string, after int64, limit int) ([]jsonlRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxGrokUsageBytes {
		return nil, errors.New("grok usage file exceeds the read limit")
	}
	raw, err := os.ReadFile(path) //nolint:gosec // validated provider-owned path.
	if err != nil {
		return nil, err
	}
	var file grokUsageFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return nil, err
	}
	if file.SessionID != "" && file.SessionID != nativeSessionID {
		return nil, errors.New("grok usage file names another session")
	}
	var records []jsonlRecord
	for _, turn := range file.Turns {
		if turn.TurnNumber <= 0 || turn.TurnNumber >= 1<<(63-grokCursorModelBits) {
			continue
		}
		models := make([]string, 0, len(turn.ModelUsage))
		for model := range turn.ModelUsage {
			models = append(models, model)
		}
		sort.Strings(models)
		if len(models) == 0 && strings.TrimSpace(turn.PrimaryModelID) != "" {
			models = []string{turn.PrimaryModelID}
			turn.ModelUsage = map[string]grokUsageCounters{turn.PrimaryModelID: turn.grokUsageCounters}
		}
		for index, model := range models {
			if index >= 1<<grokCursorModelBits {
				break
			}
			cursor := turn.TurnNumber<<grokCursorModelBits | int64(index)
			if cursor <= after {
				continue
			}
			data, err := json.Marshal(grokTurnRecord{
				TurnNumber: turn.TurnNumber, EndedAt: turn.EndedAt, ModelID: model, Usage: turn.ModelUsage[model],
			})
			if err != nil {
				return nil, err
			}
			records = append(records, jsonlRecord{Data: data, Offset: cursor})
		}
	}
	sort.Slice(records, func(i, j int) bool { return records[i].Offset < records[j].Offset })
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

func parseGrok(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	for _, record := range records {
		var turn grokTurnRecord
		if err := json.Unmarshal(record.Data, &turn); err != nil || strings.TrimSpace(turn.ModelID) == "" {
			recordMalformed(result)
			continue
		}
		usage := turn.Usage
		if usage.InputTokens == 0 && usage.OutputTokens == 0 {
			continue
		}
		tokens, ok := normalizeOpenAIUsage(usage.InputTokens, usage.CachedReadTokens, usage.CacheCreationTokens, usage.OutputTokens)
		if !ok || usage.ReasoningTokens < 0 || usage.ReasoningTokens > usage.OutputTokens {
			recordMalformed(result)
			continue
		}
		providerUsage, _ := json.Marshal(map[string]int64{
			"inputTokens": usage.InputTokens, "outputTokens": usage.OutputTokens,
			"cachedReadTokens": usage.CachedReadTokens, derivedCacheWriteKey: usage.CacheCreationTokens,
			"reasoningTokens": usage.ReasoningTokens, "modelCalls": usage.ModelCalls,
		})
		event := domain.ModelUsageEvent{
			ProviderID:            domain.UsageProviderOpenAI,
			BillingProviderID:     "xai",
			BillingProviderSource: domain.ObservedBillingProviderSource("xai"),
			ModelID:               turn.ModelID,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			ProviderUsageJSON:     boundedProviderUsage(providerUsage),
			CreatedAt:             parseUsageTimestamp(turn.EndedAt),
			SourceEventKey: stableSourceEventKey(
				"grok", source.NativeRootID, strconv.FormatInt(turn.TurnNumber, 10), turn.ModelID,
			),
			Costs: grokReportedCosts(usage.CostUsdTicks),
		}
		result.Events = append(result.Events, event)
	}
}

func grokReportedCosts(ticks *int64) domain.UsageEventCosts {
	if ticks == nil || *ticks <= 0 {
		return domain.UsageEventCosts{}
	}
	nanos := (*ticks + grokTicksPerNano/2) / grokTicksPerNano
	return domain.UsageEventCosts{EstimatedCostNanos: &nanos, PricingVersion: grokPricingVersion}
}
