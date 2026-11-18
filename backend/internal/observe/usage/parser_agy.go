package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	agyFieldGeneration     = 1
	agyFieldUsage          = 4
	agyFieldRetryInfos     = 17
	agyFieldRetryUsage     = 2
	agyStepCreatedAt       = 1
	agyStepCompletedAt     = 8
	agyStepModelUsage      = 9
	agyFieldRequestConfig  = 3
	agyFieldConfigModel    = 1
	agyFieldConfigName     = 28
	agyUsageModel          = 1
	agyUsageInput          = 2
	agyUsageOutput         = 3
	agyUsageCacheWrite     = 4
	agyUsageCacheRead      = 5
	agyUsageMessageID      = 7
	agyUsageThinkingOutput = 9
	agyUsageResponseID     = 11
	agyModelLearnRows      = 8
)

var agyKnownModels = map[uint64]string{
	1020: "gemini-3.5-flash-low",
	1071: "gemini-3.6-flash-high",
	1072: "gemini-3.6-flash-medium",
	1073: "gemini-3.6-flash-low",
	1298: "gemini-3.7-flash-high",
	1299: "gemini-3.7-flash-medium",
	1300: "gemini-3.7-flash-low",
	1318: "gemini-3.8-flash-high",
	1319: "gemini-3.8-flash-medium",
	1320: "gemini-3.8-flash-low",
	1016: "gemini-3.1-pro-preview",
	1036: "gemini-3.1-pro-preview",
	1037: "gemini-3.1-pro-preview",
	1018: "gemini-3-flash-preview",
	1047: "gemini-3-flash-preview",
	1084: "gemini-3-flash-preview",
	1050: "gemini-3.1-flash-lite",
	1026: "claude-opus-4-6",
	1035: "claude-sonnet-4-6",
	1402: "claude-opus-5-5",
}

var agyCatalogAliases = map[string]string{
	"gemini-3.1-pro": "gemini-3.1-pro-preview",
}

var agyThinkingSuffixes = []string{"-extra-low", "-minimal", "-low", "-medium", "-high"}

type agyCallRecord struct {
	Model          string `json:"model"`
	ModelEnum      uint64 `json:"model_enum,omitempty"`
	Input          int64  `json:"input"`
	Output         int64  `json:"output"`
	CacheRead      int64  `json:"cache_read"`
	CacheWrite     int64  `json:"cache_write"`
	ThinkingOutput int64  `json:"thinking_output"`
	MessageID      string `json:"message_id,omitempty"`
	ResponseID     string `json:"response_id,omitempty"`
	CreatedAt      int64  `json:"created_at,omitempty"`
}

func readAgyGenerations(ctx context.Context, db *sql.DB, _ string, after int64, limit int) ([]jsonlRecord, error) {
	names, err := learnAgyModelNames(ctx, db)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT idx, data FROM gen_metadata WHERE idx >= ? ORDER BY idx LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var records, pending []jsonlRecord
	for rows.Next() {
		var idx int64
		var data []byte
		if err := rows.Scan(&idx, &data); err != nil {
			return nil, err
		}
		cursor := idx + 1
		calls := decodeAgyCalls(data)
		if len(calls) == 0 {
			pending = append(pending, jsonlRecord{Data: []byte(`{}`), Offset: cursor})
			continue
		}
		for _, call := range calls {
			if call.Model == "" {
				call.Model = names[call.ModelEnum]
			}
			if call.Model == "" {
				call.Model = agyKnownModels[call.ModelEnum]
			}
			encoded, err := json.Marshal(call)
			if err != nil {
				return nil, err
			}
			pending = append(pending, jsonlRecord{Data: encoded, Offset: cursor})
		}
		records, pending = append(records, pending...), nil
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(records) == 0 {
		return records, nil
	}
	return stampAgyCalls(ctx, db, records)
}

func stampAgyCalls(ctx context.Context, db *sql.DB, records []jsonlRecord) ([]jsonlRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT metadata FROM steps WHERE metadata IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	times := make(map[string]int64)
	var latest int64
	for rows.Next() {
		var metadata []byte
		if err := rows.Scan(&metadata); err != nil {
			return nil, err
		}
		at, ok := agyTimestamp(metadata, agyStepCompletedAt)
		if !ok {
			at, ok = agyTimestamp(metadata, agyStepCreatedAt)
		}
		if !ok {
			continue
		}
		latest = max(latest, at)
		usage, ok := protoFirstBytes(metadata, agyStepModelUsage)
		if !ok {
			continue
		}
		if responseID, ok := protoFirstBytes(usage, agyUsageResponseID); ok && len(responseID) > 0 {
			times[string(responseID)] = at
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	calls := make([]*agyCallRecord, len(records))
	var last int64
	for index, record := range records {
		var call agyCallRecord
		if string(record.Data) == `{}` || json.Unmarshal(record.Data, &call) != nil {
			continue
		}
		if at, ok := times[call.ResponseID]; ok {
			call.CreatedAt = at
		}
		if call.CreatedAt > 0 {
			last = call.CreatedAt
		} else {
			call.CreatedAt = last
		}
		calls[index] = &call
	}
	next := latest
	for index := len(calls) - 1; index >= 0; index-- {
		call := calls[index]
		if call == nil {
			continue
		}
		if call.CreatedAt > 0 {
			next = call.CreatedAt
			continue
		}
		call.CreatedAt = next
	}
	for index, call := range calls {
		if call == nil {
			continue
		}
		encoded, err := json.Marshal(call)
		if err != nil {
			return nil, err
		}
		records[index].Data = encoded
	}
	return records, nil
}

func agyTimestamp(message []byte, number protowire.Number) (int64, bool) {
	raw, ok := protoFirstBytes(message, number)
	if !ok {
		return 0, false
	}
	fields, err := protoFields(raw)
	if err != nil {
		return 0, false
	}
	var seconds, nanos uint64
	for _, field := range fields {
		switch {
		case field.number == 1 && !field.bytes:
			seconds = field.varint
		case field.number == 2 && !field.bytes:
			nanos = field.varint
		}
	}
	if seconds == 0 || seconds > 1<<40 || nanos >= 1e9 {
		return 0, false
	}
	return int64(seconds)*1000 + int64(nanos)/1e6, true //nolint:gosec // bounded above.
}

func learnAgyModelNames(ctx context.Context, db *sql.DB) (map[uint64]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT data FROM gen_metadata ORDER BY idx LIMIT ?`, agyModelLearnRows)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	names := make(map[uint64]string)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		if enum, name, ok := agyConfigModel(data); ok {
			names[enum] = name
		}
	}
	return names, rows.Err()
}

func agyConfigModel(data []byte) (uint64, string, bool) {
	config, ok := protoFirstBytes(data, agyFieldRequestConfig)
	if !ok {
		return 0, "", false
	}
	fields, err := protoFields(config)
	if err != nil {
		return 0, "", false
	}
	var enum uint64
	var name string
	for _, field := range fields {
		switch {
		case field.number == agyFieldConfigModel && !field.bytes:
			enum = field.varint
		case field.number == agyFieldConfigName && field.bytes:
			name = strings.TrimSpace(string(field.value))
		}
	}
	return enum, name, name != ""
}

func decodeAgyCalls(data []byte) []agyCallRecord {
	generation, ok := protoFirstBytes(data, agyFieldGeneration)
	if !ok {
		return nil
	}
	fields, err := protoFields(generation)
	if err != nil {
		return nil
	}
	_, configName, _ := agyConfigModel(data)
	var calls []agyCallRecord
	seen := make(map[string]bool)
	add := func(usage []byte) {
		call, ok := decodeAgyUsage(usage)
		if !ok {
			return
		}
		if call.ResponseID != "" {
			if seen[call.ResponseID] {
				return
			}
			seen[call.ResponseID] = true
		}
		call.Model = configName
		calls = append(calls, call)
	}
	for _, field := range fields {
		if field.bytes && field.number == agyFieldUsage {
			add(field.value)
		}
	}
	for _, field := range fields {
		if field.bytes && field.number == agyFieldRetryInfos {
			if usage, ok := protoFirstBytes(field.value, agyFieldRetryUsage); ok {
				add(usage)
			}
		}
	}
	return calls
}

func decodeAgyUsage(usage []byte) (agyCallRecord, bool) {
	fields, err := protoFields(usage)
	if err != nil {
		return agyCallRecord{}, false
	}
	var call agyCallRecord
	seen := false
	for _, field := range fields {
		if field.bytes {
			switch field.number {
			case agyUsageMessageID:
				call.MessageID = string(field.value)
			case agyUsageResponseID:
				call.ResponseID = string(field.value)
			}
			continue
		}
		if field.varint > 1<<53 {
			return agyCallRecord{}, false
		}
		value := int64(field.varint) //nolint:gosec // bounded above.
		switch field.number {
		case agyUsageModel:
			call.ModelEnum = field.varint
		case agyUsageInput:
			call.Input, seen = value, true
		case agyUsageOutput:
			call.Output, seen = value, true
		case agyUsageCacheRead:
			call.CacheRead = value
		case agyUsageCacheWrite:
			call.CacheWrite = value
		case agyUsageThinkingOutput:
			call.ThinkingOutput = value
		}
	}
	if !seen {
		return agyCallRecord{}, false
	}
	return call, true
}

func parseAgy(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	rowOffset, rowCall := int64(-1), 0
	for _, record := range records {
		if record.Offset == rowOffset {
			rowCall++
		} else {
			rowOffset, rowCall = record.Offset, 0
		}
		if string(record.Data) == `{}` {
			continue
		}
		var call agyCallRecord
		if err := json.Unmarshal(record.Data, &call); err != nil {
			recordMalformed(result)
			continue
		}
		if call.Input == 0 && call.Output == 0 && call.CacheRead == 0 && call.CacheWrite == 0 {
			continue
		}
		model := agyCatalogModel(call.Model)
		if model == "" {
			model = "agy-model-" + strconv.FormatUint(call.ModelEnum, 10)
		}
		input, ok := sumNonNegative(call.Input, call.CacheRead, call.CacheWrite)
		if !ok || call.ThinkingOutput < 0 || call.ThinkingOutput > call.Output {
			recordMalformed(result)
			continue
		}
		tokens, ok := normalizeOpenAIUsage(input, call.CacheRead, call.CacheWrite, call.Output)
		if !ok {
			recordMalformed(result)
			continue
		}
		billingProvider := agyBillingProvider(model)
		providerUsage, _ := json.Marshal(map[string]any{
			"input": call.Input, "output": call.Output, "cacheRead": call.CacheRead, derivedCacheWriteKey: call.CacheWrite,
			"thinkingOutput": call.ThinkingOutput, "model": call.Model, "modelEnum": call.ModelEnum,
		})
		key := call.ResponseID
		if key == "" {
			key = call.MessageID
		}
		if key == "" {
			key = "idx-" + strconv.FormatInt(record.Offset, 10)
			if rowCall > 0 {
				key += "-" + strconv.Itoa(rowCall)
			}
		}
		var createdAt time.Time
		if call.CreatedAt > 0 {
			createdAt = time.UnixMilli(call.CreatedAt).UTC()
		}
		result.Events = append(result.Events, domain.ModelUsageEvent{
			CreatedAt:             createdAt,
			ProviderID:            domain.UsageProviderOpenAI,
			BillingProviderID:     billingProvider,
			BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
			ModelID:               model,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			ProviderUsageJSON:     boundedProviderUsage(providerUsage),
			ProviderMessageID:     providerMessageID(call.ResponseID),
			SourceEventKey:        stableSourceEventKey("agy", source.NativeRootID, key),
		})
	}
}

func agyCatalogModel(name string) string {
	model := strings.ToLower(strings.TrimSpace(name))
	for _, suffix := range agyThinkingSuffixes {
		if trimmed, ok := strings.CutSuffix(model, suffix); ok {
			model = trimmed
			break
		}
	}
	if alias, ok := agyCatalogAliases[model]; ok {
		return alias
	}
	return model
}

func agyBillingProvider(model string) string {
	switch {
	case strings.HasPrefix(model, "gemini-"):
		return "gemini"
	case strings.HasPrefix(model, "claude-"):
		return "anthropic"
	case strings.HasPrefix(model, "gpt-"):
		return "openai"
	default:
		return ""
	}
}

type protoField struct {
	number protowire.Number
	bytes  bool
	varint uint64
	value  []byte
}

func protoFields(data []byte) ([]protoField, error) {
	var fields []protoField
	for len(data) > 0 {
		number, kind, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		data = data[n:]
		field := protoField{number: number, bytes: kind == protowire.BytesType}
		switch kind {
		case protowire.VarintType:
			field.varint, n = protowire.ConsumeVarint(data)
		case protowire.BytesType:
			field.value, n = protowire.ConsumeBytes(data)
		default:
			n = protowire.ConsumeFieldValue(number, kind, data)
		}
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		data = data[n:]
		if kind == protowire.VarintType || kind == protowire.BytesType {
			fields = append(fields, field)
		}
	}
	return fields, nil
}

func protoFirstBytes(data []byte, number protowire.Number) ([]byte, bool) {
	fields, err := protoFields(data)
	if err != nil {
		return nil, false
	}
	for _, field := range fields {
		if field.number == number && field.bytes {
			return field.value, true
		}
	}
	return nil, false
}
