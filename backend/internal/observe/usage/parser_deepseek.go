package usage

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	maxDeepSeekSessionBytes = 64 << 20
	maxDeepSeekDecodedBytes = 512 << 20
	maxDeepSeekRecordBytes  = 32 << 20
)

type deepseekLogRecord struct {
	Type string `json:"type"`
	ID   string `json:"id"`
	Seq  int64  `json:"seq"`
	Time int64  `json:"time"`
	Data struct {
		Turn    int64 `json:"turn"`
		Step    int64 `json:"step"`
		Message struct {
			ID     string `json:"id"`
			Source struct {
				Provider string `json:"provider"`
				Model    string `json:"model"`
			} `json:"source"`
		} `json:"message"`
		Usage json.RawMessage `json:"usage"`
	} `json:"data"`
}

type deepseekReplyRecord struct {
	Seq       int64           `json:"seq"`
	Time      int64           `json:"time"`
	Turn      int64           `json:"turn"`
	Step      int64           `json:"step"`
	MessageID string          `json:"message_id"`
	Provider  string          `json:"provider"`
	Model     string          `json:"model"`
	Usage     json.RawMessage `json:"usage"`
}

type deepseekUsage struct {
	InputTokens      *int64 `json:"inputTokens"`
	OutputTokens     *int64 `json:"outputTokens"`
	CacheReadTokens  int64  `json:"cacheReadTokens"`
	CacheWriteTokens int64  `json:"cacheWriteTokens"`
}

func readDeepSeekReplies(ctx context.Context, path, nativeSessionID string, after int64, limit int) ([]jsonlRecord, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxDeepSeekSessionBytes {
		return nil, errors.New("deepseek session log exceeds the read limit")
	}
	file, err := os.Open(path) //nolint:gosec // validated provider-owned path.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	decoder, err := zstd.NewReader(file, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(maxDeepSeekDecodedBytes))
	if err != nil {
		return nil, err
	}
	defer decoder.Close()
	reader := bufio.NewReaderSize(io.LimitReader(decoder, maxDeepSeekDecodedBytes), 64<<10)

	var records []jsonlRecord
	for lineIndex := 0; ; lineIndex++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		line, readErr := readDeepSeekLine(reader)
		if readErr != nil {
			if lineIndex == 0 {
				return nil, errors.New("deepseek session log has no header")
			}
			break
		}
		var record deepseekLogRecord
		if err := json.Unmarshal(line, &record); err != nil {
			return nil, err
		}
		if lineIndex == 0 {
			if record.Type != "session" || record.ID != nativeSessionID {
				return nil, errors.New("deepseek session log names another session")
			}
			continue
		}
		if record.Type != "assistant/message" || record.Seq <= after || len(record.Data.Usage) == 0 {
			continue
		}
		data, err := json.Marshal(deepseekReplyRecord{
			Seq: record.Seq, Time: record.Time, Turn: record.Data.Turn, Step: record.Data.Step,
			MessageID: record.Data.Message.ID, Provider: record.Data.Message.Source.Provider,
			Model: record.Data.Message.Source.Model, Usage: record.Data.Usage,
		})
		if err != nil {
			return nil, err
		}
		records = append(records, jsonlRecord{Data: data, Offset: record.Seq})
		if len(records) >= limit {
			break
		}
	}
	return records, nil
}

func readDeepSeekLine(reader *bufio.Reader) ([]byte, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(line)+len(chunk) > maxDeepSeekRecordBytes {
			return nil, errors.New("deepseek session record exceeds the read limit")
		}
		line = append(line, chunk...)
		switch {
		case err == nil:
			return bytes.TrimSpace(line), nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		default:
			return nil, err
		}
	}
}

func parseDeepSeek(source domain.UsageSourceContext, records []jsonlRecord, result *parseResult) {
	for _, record := range records {
		var reply deepseekReplyRecord
		var usage deepseekUsage
		if err := json.Unmarshal(record.Data, &reply); err != nil || strings.TrimSpace(reply.Model) == "" ||
			json.Unmarshal(reply.Usage, &usage) != nil || usage.InputTokens == nil || usage.OutputTokens == nil {
			recordMalformed(result)
			continue
		}
		if *usage.InputTokens == 0 && *usage.OutputTokens == 0 && usage.CacheReadTokens == 0 {
			continue
		}
		input, ok := sumNonNegative(*usage.InputTokens, usage.CacheReadTokens, usage.CacheWriteTokens)
		if !ok {
			recordMalformed(result)
			continue
		}
		tokens, ok := normalizeOpenAIUsage(input, usage.CacheReadTokens, usage.CacheWriteTokens, *usage.OutputTokens)
		if !ok {
			recordMalformed(result)
			continue
		}
		providerUsage, _ := json.Marshal(map[string]int64{
			"inputTokens": *usage.InputTokens, "outputTokens": *usage.OutputTokens,
			"cacheReadTokens": usage.CacheReadTokens, derivedCacheWriteKey: usage.CacheWriteTokens,
		})
		billingProvider := canonicalBillingProvider(reply.Provider)
		messageKey := reply.MessageID
		if strings.TrimSpace(messageKey) == "" {
			messageKey = "seq-" + strconv.FormatInt(reply.Seq, 10)
		}
		event := domain.ModelUsageEvent{
			ProviderID:            domain.UsageProviderOpenAI,
			BillingProviderID:     billingProvider,
			BillingProviderSource: domain.ObservedBillingProviderSource(billingProvider),
			ModelID:               reply.Model,
			MeasurementKind:       domain.UsageMeasurementNativeReported,
			Tokens:                tokens,
			ProviderUsageJSON:     boundedProviderUsage(providerUsage),
			CreatedAt:             time.UnixMilli(reply.Time).UTC(),
			ProviderMessageID:     providerMessageID(reply.MessageID),
			SourceEventKey:        stableSourceEventKey("deepseek", source.NativeRootID, messageKey),
		}
		result.Events = append(result.Events, event)
	}
}
