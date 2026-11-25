package claudeacp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatPromptCacheReader = (*checkpointDriver)(nil)

// promptCacheTranscriptBytes bounds how much of the transcript's tail is read.
// The latest request sits at the end; a tail this size holds many requests
// even when single tool results are large.
const promptCacheTranscriptBytes = 4 << 20

// PromptCacheState reads where the conversation's prompt cache stands from
// Claude's own transcript. Claude records every request's usage there,
// including which cache lifetime its writes used, and the record survives a
// hibernated or restarted provider, unlike anything held in memory.
func (d *checkpointDriver) PromptCacheState(ctx context.Context, request ports.NativeCheckpointRequest) (ports.PromptCacheState, bool, error) {
	if request.ProviderConversationID == "" {
		return ports.PromptCacheState{}, false, nil
	}
	config, ok := d.plugin.(ports.AgentNativeSessionConfigProvider)
	if !ok {
		return ports.PromptCacheState{}, false, nil
	}
	locator, ok := d.plugin.(ports.AgentTranscriptLocator)
	if !ok {
		return ports.PromptCacheState{}, false, nil
	}
	dir, err := config.NativeSessionConfigDir(ctx, request.Env)
	if err != nil {
		return ports.PromptCacheState{}, false, err
	}
	path, found, err := locator.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: request.ProviderConversationID, ConfigDir: dir})
	if err != nil || !found {
		return ports.PromptCacheState{}, false, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ports.PromptCacheState{}, false, nil
		}
		return ports.PromptCacheState{}, false, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return ports.PromptCacheState{}, false, err
	}
	offset := max(info.Size()-promptCacheTranscriptBytes, 0)
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return ports.PromptCacheState{}, false, err
	}
	state, found := readPromptCacheState(ctx, file, offset > 0)
	return state, found, ctx.Err()
}

type promptCacheRecord struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	Timestamp string `json:"timestamp"`
	Message   struct {
		Model string `json:"model"`
		Usage *struct {
			InputTokens              int64 `json:"input_tokens"`
			CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
			CacheCreation            *struct {
				Ephemeral1h int64 `json:"ephemeral_1h_input_tokens"`
				Ephemeral5m int64 `json:"ephemeral_5m_input_tokens"`
			} `json:"cache_creation"`
		} `json:"usage"`
	} `json:"message"`
	CompactMetadata *struct {
		PostTokens int64 `json:"postTokens"`
	} `json:"compactMetadata"`
}

// readPromptCacheState scans transcript records in order. A request's time is
// the record that prompted it (the user message or tool result), not the
// response: the cache entry dates from when the provider read the prompt, and
// a long response would otherwise push the estimated expiry past the real one.
// skipFirst drops a partial line left by starting mid-file.
func readPromptCacheState(ctx context.Context, r io.Reader, skipFirst bool) (ports.PromptCacheState, bool) {
	reader := bufio.NewReaderSize(r, 64<<10)
	var (
		state    ports.PromptCacheState
		found    bool
		promptAt time.Time
		knownTTL time.Duration
	)
	for first := true; ; first = false {
		if ctx.Err() != nil {
			return ports.PromptCacheState{}, false
		}
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 && !(first && skipFirst) {
			applyPromptCacheRecord(bytes.TrimSpace(line), &state, &found, &promptAt, &knownTTL)
		}
		if err != nil {
			break
		}
	}
	if found && state.TTL <= 0 {
		// A request served fully from cache writes no entry and reports no
		// lifetime, but the conversation's lifetime does not change between
		// requests, so the latest one that did report it stands in.
		state.TTL = knownTTL
	}
	return state, found
}

func applyPromptCacheRecord(line []byte, state *ports.PromptCacheState, found *bool, promptAt *time.Time, knownTTL *time.Duration) {
	if len(line) == 0 {
		return
	}
	var record promptCacheRecord
	if json.Unmarshal(line, &record) != nil {
		return
	}
	at, err := time.Parse(time.RFC3339Nano, record.Timestamp)
	if err != nil {
		return
	}
	switch {
	case record.Type == "user":
		*promptAt = at
	case record.Type == "system" && record.Subtype == "compact_boundary":
		// The history was summarized. What the next request carries is the
		// summary, so the size before compaction no longer describes it.
		if *found {
			state.ContextTokens = 0
			if record.CompactMetadata != nil {
				state.ContextTokens = record.CompactMetadata.PostTokens
			}
			state.LastRequestAt = at
		}
	case record.Type == "assistant" && record.Message.Usage != nil && record.Message.Model != "<synthetic>":
		usage := record.Message.Usage
		requestAt := at
		if !promptAt.IsZero() && !promptAt.After(at) {
			requestAt = *promptAt
		}
		state.LastRequestAt = requestAt
		state.ContextTokens = usage.InputTokens + usage.CacheReadInputTokens + usage.CacheCreationInputTokens
		state.TTL = 0
		if creation := usage.CacheCreation; creation != nil {
			switch {
			case creation.Ephemeral1h > 0:
				state.TTL = time.Hour
			case creation.Ephemeral5m > 0:
				state.TTL = 5 * time.Minute
			}
		}
		if state.TTL > 0 {
			*knownTTL = state.TTL
		}
		*found = true
	}
}
