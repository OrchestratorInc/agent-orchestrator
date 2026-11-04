package usage

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Antigravity stores one gen_metadata row per model call in each
// conversation's database: a CortexStepGeneratorMetadata protobuf, whose
// schema is extracted from the language server rather than published.
//
//	1 chat_model        -> 4 usage, 17 retry_infos (each 2 usage)
//	3 planner_config    -> 1 model enum, 28 model name (first calls only)
//	ModelUsageStats     -> 1 model enum, 2 input (excluding cache reads),
//	                       3 output (thinking + response), 4 cache write,
//	                       5 cache read, 7 message id, 9 thinking output,
//	                       11 response id
//
// The calls carry no time of their own; the steps row that records the same
// usage (CortexStepMetadata: 1 created_at, 8 completed_at, 9 model_usage) does.
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
	// agyModelLearnRows bounds the leading calls read to learn the
	// conversation's model names, which only its first calls carry.
	agyModelLearnRows = 8
)

// agyKnownModels names the model enums the agy CLI has written, for a
// conversation whose leading calls no longer carry the name.
// The leading entries were seen in agy conversations; the rest follow the
// tables ccusage and T3 Code keep from Antigravity's model catalog.
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

// agyCatalogAliases maps names agy writes that the catalog spells otherwise.
var agyCatalogAliases = map[string]string{
	"gemini-3.1-pro": "gemini-3.1-pro-preview",
}

// agyThinkingSuffixes are the thinking levels agy appends to a model name;
// the catalog prices the model without them.
var agyThinkingSuffixes = []string{"-extra-low", "-minimal", "-low", "-medium", "-high"}

// agyCallRecord is one model call's usage, as the reader hands it to parseAgy.
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
	// CreatedAt is the call's time in Unix milliseconds, from its step.
	CreatedAt int64 `json:"created_at,omitempty"`
}

// readAgyGenerations reads the model calls after the cursor from one
// Antigravity conversation database. The database holds that conversation
// alone, so the native session ID selects the file, not rows. Its rows count
// from idx 0, which a new source's cursor already names, so a row's cursor is
// its idx plus one.
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
			// A row without usage is passed over only once a later row
			// exists; the newest one may still be getting its usage.
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

// stampAgyCalls gives each call the time of the step that recorded its
// response, matched by response ID.
func stampAgyCalls(ctx context.Context, db *sql.DB, records []jsonlRecord) ([]jsonlRecord, error) {
	rows, err := db.QueryContext(ctx, `SELECT metadata FROM steps WHERE metadata IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	times := make(map[string]int64)
	for rows.Next() {
		var metadata []byte
		if err := rows.Scan(&metadata); err != nil {
			return nil, err
		}
		usage, ok := protoFirstBytes(metadata, agyStepModelUsage)
		if !ok {
			continue
		}
		responseID, ok := protoFirstBytes(usage, agyUsageResponseID)
		if !ok || len(responseID) == 0 {
			continue
		}
		at, ok := agyTimestamp(metadata, agyStepCompletedAt)
		if !ok {
			at, ok = agyTimestamp(metadata, agyStepCreatedAt)
		}
		if ok {
			times[string(responseID)] = at
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index, record := range records {
		var call agyCallRecord
		if string(record.Data) == `{}` || json.Unmarshal(record.Data, &call) != nil {
			continue
		}
		if at, ok := times[call.ResponseID]; ok {
			call.CreatedAt = at
			encoded, err := json.Marshal(call)
			if err != nil {
				return nil, err
			}
			records[index].Data = encoded
		}
	}
	return records, nil
}

// agyTimestamp reads a google.protobuf.Timestamp field as Unix milliseconds.
func agyTimestamp(message []byte, number uint64) (int64, bool) {
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

// learnAgyModelNames maps the conversation's model enums to the names its
// leading calls record.
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

// decodeAgyCalls returns the row's call and each retried attempt that billed
// separately. A retry entry repeating the call's own response is the same call.
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
	for _, record := range records {
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
			"input": call.Input, "output": call.Output, "cacheRead": call.CacheRead, "cacheWrite": call.CacheWrite,
			"thinkingOutput": call.ThinkingOutput, "model": call.Model, "modelEnum": call.ModelEnum,
		})
		key := call.ResponseID
		if key == "" {
			key = call.MessageID
		}
		if key == "" {
			key = "idx-" + strconv.FormatInt(record.Offset, 10)
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
			ProviderMessageID:     strings.TrimSpace(call.ResponseID),
			SourceEventKey:        stableSourceEventKey("agy", source.NativeRootID, key),
		})
	}
}

// agyCatalogModel is the catalog's name for an agy model: agy appends the
// thinking level, which does not change the per-token price.
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

// agyBillingProvider names the catalog provider for a model Antigravity
// serves; an unrecognised family stays unpriced.
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

// protoField is one decoded protobuf field: a varint, or a length-delimited
// value. Fixed-width fields are skipped.
type protoField struct {
	number uint64
	bytes  bool
	varint uint64
	value  []byte
}

var errProtoMalformed = errors.New("malformed protobuf message")

// protoFields decodes one protobuf message's top-level fields.
func protoFields(data []byte) ([]protoField, error) {
	var fields []protoField
	for len(data) > 0 {
		key, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, errProtoMalformed
		}
		data = data[n:]
		field := protoField{number: key >> 3}
		if field.number == 0 {
			return nil, errProtoMalformed
		}
		switch key & 7 {
		case 0:
			value, n := binary.Uvarint(data)
			if n <= 0 {
				return nil, errProtoMalformed
			}
			field.varint, data = value, data[n:]
		case 1:
			if len(data) < 8 {
				return nil, errProtoMalformed
			}
			data = data[8:]
			continue
		case 2:
			length, n := binary.Uvarint(data)
			if n <= 0 || length > uint64(len(data)-n) {
				return nil, errProtoMalformed
			}
			field.bytes, field.value, data = true, data[n:n+int(length)], data[n+int(length):]
		case 5:
			if len(data) < 4 {
				return nil, errProtoMalformed
			}
			data = data[4:]
			continue
		default:
			return nil, errProtoMalformed
		}
		fields = append(fields, field)
	}
	return fields, nil
}

// protoFirstBytes returns the first length-delimited field with the number.
func protoFirstBytes(data []byte, number uint64) ([]byte, bool) {
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
