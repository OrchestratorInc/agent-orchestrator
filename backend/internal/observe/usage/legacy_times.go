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

// repairEventTimes fills created_at on events stored before the parser read
// record times. The report windows by created_at, so until then those events
// count toward no period at all. Each source is reread from the start and an
// event gains the time the replay gives the same source event key; the key
// covers the provider's own identity for the event, so a match is that event.
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

// repairServiceTiers fills the service tier on events stored before the parser
// recorded one, so a Codex priority or Claude fast turn from then stops pricing
// at standard rates. Each queued source is reread once and leaves the queue:
// a NULL tier also means the provider reported none, so the rows alone could
// never say the reread is done.
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
			// Left queued: a read error may pass, and the next start retries.
			r.config.OnError(fmt.Errorf("replay tiers for usage source %d: %w", source.Source.ID, err))
			continue
		}
		n, err := r.store.CompleteUsageTierRepair(ctx, source.Source.ID, source.Source.BindingID, tiers)
		if err != nil {
			return err
		}
		filled += n
	}
	// Filled rows lost their cost; the backfiller prices them at their tier.
	if filled > 0 && r.config.OnTiersFilled != nil {
		r.config.OnTiersFilled(ctx)
	}
	return nil
}

// replaySource rereads a source from its start and hands visit every event the
// parser yields. A transcript that is gone, or a kind with no parser, yields
// nothing.
func (r *LegacyRepairer) replaySource(ctx context.Context, source domain.UsageSourceContext, visit func(domain.ModelUsageEvent)) error {
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

	// The buffer bounds a record: a longer line is skipped whole, as ingestion
	// skips it, without ever being held in memory.
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

// openUsageArtifact opens a transcript, or the zstd copy Codex leaves in its
// place when it compresses an old rollout.
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
