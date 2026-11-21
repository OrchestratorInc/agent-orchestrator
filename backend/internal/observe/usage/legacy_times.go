package usage

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const legacyTimeBatch = 512

func (r *LegacyRepairer) repairEventTimes(ctx context.Context) error {
	sources, err := r.store.ListUsageSourcesWithUntimedEvents(ctx)
	if err != nil {
		return err
	}
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		times, err := r.replayEventTimes(ctx, source)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.config.OnError(fmt.Errorf("replay times for usage source %d: %w", source.Source.ID, err))
			continue
		}
		if _, err := r.store.SetUsageEventTimes(ctx, source.Source.BindingID, times); err != nil {
			return err
		}
	}
	return nil
}

func (r *LegacyRepairer) replayEventTimes(ctx context.Context, source domain.UsageSourceContext) (map[string]time.Time, error) {
	times := map[string]time.Time{}
	err := r.replaySource(ctx, source, func(event domain.ModelUsageEvent) {
		if !event.CreatedAt.IsZero() {
			times[event.SourceEventKey] = event.CreatedAt
		}
	})
	return times, err
}

func (r *LegacyRepairer) repairServiceTiers(ctx context.Context) error {
	sources, err := r.store.ListUsageTierRepairSources(ctx)
	if err != nil {
		return err
	}
	filled := 0
	for _, source := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		tiers := map[string]string{}
		err := r.replaySource(ctx, source, func(event domain.ModelUsageEvent) {
			if event.ServiceTier != "" {
				tiers[event.SourceEventKey] = event.ServiceTier
			}
		})
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			r.config.OnError(fmt.Errorf("replay tiers for usage source %d: %w", source.Source.ID, err))
			continue
		}
		n, err := r.store.CompleteUsageTierRepair(ctx, source.Source.ID, source.Source.BindingID, tiers)
		if err != nil {
			return err
		}
		filled += n
	}
	if filled > 0 && r.config.OnTiersFilled != nil {
		r.config.OnTiersFilled(ctx)
	}
	return nil
}

func (r *LegacyRepairer) replaySource(ctx context.Context, source domain.UsageSourceContext, visit func(domain.ModelUsageEvent)) error {
	if sqliteUsageSource(source.Source.Kind) {
		return nil
	}
	reader, closeReader, err := openUsageArtifact(source.Source.ArtifactPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer closeReader()

	state, err := newParserState(source.Source.Kind)
	if err != nil {
		return nil
	}
	if persisted, err := decodeParserState(source.Source); err == nil && state.Codex != nil && persisted.Codex != nil {
		state.Codex.NativeSessionID = persisted.Codex.NativeSessionID
		state.Codex.DirectParentID = persisted.Codex.DirectParentID
	}

	batch := make([]jsonlRecord, 0, legacyTimeBatch)
	var offset int64
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		parsed := parseRecordsWithState(source, batch, offset, r.config.Clock().UTC(), state)
		if parsed.err != nil {
			return parsed.err
		}
		for _, event := range parsed.Events {
			visit(event)
		}
		batch = batch[:0]
		return ctx.Err()
	}

	lines := bufio.NewReaderSize(reader, r.config.RecordBytes)
	oversized := false
	for {
		line, readErr := lines.ReadSlice('\n')
		start := offset
		offset += int64(len(line))
		if errors.Is(readErr, bufio.ErrBufferFull) {
			oversized = true
			continue
		}
		if !oversized {
			if data := bytes.TrimSpace(line); len(data) > 0 {
				batch = append(batch, jsonlRecord{Data: append([]byte(nil), data...), Offset: start})
			}
		}
		oversized = false
		if len(batch) >= legacyTimeBatch {
			if err := flush(); err != nil {
				return err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	return flush()
}

func openUsageArtifact(path string) (io.Reader, func(), error) {
	file, err := os.Open(path) //nolint:gosec // registered transcript path.
	if err == nil {
		return file, func() { _ = file.Close() }, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, nil, err
	}
	compressed, err := os.Open(path + ".zst") //nolint:gosec // registered transcript path.
	if err != nil {
		return nil, nil, err
	}
	decoder, err := zstd.NewReader(compressed, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
	if err != nil {
		_ = compressed.Close()
		return nil, nil, err
	}
	return decoder, func() { decoder.Close(); _ = compressed.Close() }, nil
}
