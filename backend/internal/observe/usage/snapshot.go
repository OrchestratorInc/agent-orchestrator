package usage

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// maxSnapshotBytes bounds one whole-file session document. A larger document
// is left unread rather than loaded into memory on every rewrite.
const maxSnapshotBytes = 64 << 20

// snapshotSourceKind reports whether a source is a whole JSON document that
// its agent rewrites in place, rather than an append-only JSONL log.
//
// Byte-offset tailing cannot follow such a file: every write may change bytes
// before the old end. A snapshot source instead re-reads the whole document
// when its digest changes and emits every billed message in it. Each message
// keeps a stable event key, so the store's per-binding uniqueness drops the
// ones already recorded; only new messages land.
//
// A snapshot source keeps byte_offset at zero. A rewrite may shrink the file,
// and registration treats a file smaller than its offset as a different one.
// With no offset and no integrity checkpoint, the legacy repairer also leaves
// these sources alone, so no inferred attribution can make a replay conflict.
func snapshotSourceKind(kind domain.UsageSourceKind) bool {
	return kind == domain.UsageSourceClineSession
}

// snapshotParserStateV1 identifies the document a snapshot source last
// applied, so an unchanged rewrite costs one read and no store write.
type snapshotParserStateV1 struct {
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

func validateSnapshotParserState(state *snapshotParserStateV1) error {
	if state == nil {
		return nil
	}
	if state.Size < 0 || state.Size > maxSnapshotBytes || !validSHA256Digest(state.SHA256) {
		return errors.New("invalid snapshot state")
	}
	return nil
}

func (i *Ingestor) ingestSnapshot(
	ctx context.Context,
	source domain.UsageSourceContext,
	state *parserStateEnvelope,
	file *os.File,
	info os.FileInfo,
	identity string,
	now time.Time,
	result IngestResult,
) (IngestResult, error) {
	if source.Source.FileIdentity != identity {
		return i.replaceSource(ctx, source, identity, now)
	}
	if info.Size() > maxSnapshotBytes {
		return i.retrySource(ctx, source.Source, domain.UsageErrorRecordTooLarge, now, nil)
	}
	data, err := io.ReadAll(contextReader{ctx: ctx, reader: io.NewSectionReader(file, 0, info.Size())})
	if err != nil || int64(len(data)) != info.Size() {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, nil)
	}
	observed := &snapshotParserStateV1{Size: info.Size(), SHA256: fmt.Sprintf("%x", sha256.Sum256(data))}
	unchanged := state.Snapshot != nil && *state.Snapshot == *observed
	finalizing := source.BindingState == domain.UsageBindingFinalizing
	// A source left in error by a torn read still applies its cursor once, so
	// the error clears even when the next document matches the last one.
	if unchanged && !finalizing && source.Source.State != domain.UsageSourceError {
		return result, nil
	}
	if !unchanged && (source.BindingState == domain.UsageBindingComplete ||
		source.BindingState == domain.UsageBindingPartial) {
		if _, err := i.store.UpdateUsageBindingState(
			ctx,
			source.Source.BindingID,
			domain.UsageBindingFinalizing,
			"",
			now,
		); err != nil {
			return result, err
		}
		source.BindingState = domain.UsageBindingFinalizing
		finalizing = true
	}

	parsed := parseResult{Cursor: cursorFromSource(source.Source, 0, now)}
	if !unchanged {
		// The agent rewrites the document in place, so a read can land
		// mid-write. That write's own filesystem event queues the source
		// again; the retry only covers a write that raises none.
		if !json.Valid(data) {
			return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, nil)
		}
		parsed = parseSnapshot(source, data, now)
		state.Snapshot = observed
	}
	encoded, err := json.Marshal(state)
	if err != nil {
		return i.retrySource(ctx, source.Source, domain.UsageErrorInvalidParserState, now, err)
	}
	parsed.Cursor.ParserStateJSON = string(encoded)

	if finalizing {
		settled := source.Source.NextRetryAt != nil && !source.Source.NextRetryAt.After(now)
		if unchanged && settled {
			parsed.Cursor.State = domain.UsageSourceComplete
		} else {
			settleAt := nextFinalizationRetry(now, i.finalizationWait, source.Source.NextRetryAt, !unchanged)
			parsed.Cursor.NextRetryAt = &settleAt
			result.RetryAt = &settleAt
		}
	}

	stable, err := transcriptSnapshotUnchanged(file, 0, transcriptSnapshot{
		identity: identity,
		size:     info.Size(),
		modTime:  info.ModTime(),
	})
	if err != nil {
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, err)
	}
	if !stable {
		return i.retrySource(
			ctx,
			source.Source,
			domain.UsageErrorSourceReadFailed,
			now,
			errors.New("snapshot changed during ingestion"),
		)
	}
	stored, stop, err := i.storeParsed(ctx, source, parsed, now, result)
	if stop {
		return stored, err
	}
	if parsed.Cursor.State == domain.UsageSourceComplete {
		return result, i.completeBinding(ctx, source.Source.BindingID, now)
	}
	return result, nil
}

// parseSnapshot maps one whole session document into usage events.
func parseSnapshot(source domain.UsageSourceContext, data []byte, now time.Time) parseResult {
	result := parseResult{Cursor: cursorFromSource(source.Source, 0, now)}
	switch source.Source.Kind {
	case domain.UsageSourceClineSession:
		parseClineSnapshot(source, data, &result)
	default:
		result.Cursor.AnomalyCount++
		result.Cursor.LastErrorCode = domain.UsageErrorUnsupportedSourceFormat
	}
	return result
}
