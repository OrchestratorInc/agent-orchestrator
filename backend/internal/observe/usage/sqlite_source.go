package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"time"

	// Registers the pure-Go "sqlite" driver used to read agent databases.
	_ "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

const (
	// defaultSQLiteChunkRows bounds one ingestion pass over a SQLite source,
	// as ChunkBytes bounds one pass over a JSONL transcript.
	defaultSQLiteChunkRows = 1000
	// defaultSQLitePollInterval re-reads an active SQLite source. Agents write
	// usage rows into the database's WAL, which an exact-file watch on the
	// main database does not always see until a checkpoint.
	defaultSQLitePollInterval = 15 * time.Second
)

// sqliteUsageChunk is one bounded read of a SQLite source. Each row is encoded
// as one JSON record so the per-kind parser reuses the JSONL parsing contract.
// nextOffset is the cursor stored in the source's byte_offset column: a row ID
// for append-only tables, or a monotonic usage volume for tables of cumulative
// counters, which the parser diffs against its own baseline.
type sqliteUsageChunk struct {
	records    []jsonlRecord
	nextOffset int64
	atEnd      bool
}

// sqliteUsageReader reads the bound native session's usage rows from cursor.
type sqliteUsageReader func(ctx context.Context, db *sql.DB, source domain.UsageSourceRecord, limit int) (sqliteUsageChunk, error)

var sqliteUsageReaders = map[domain.UsageSourceKind]sqliteUsageReader{
	domain.UsageSourceGooseLedger: readGooseLedgerChunk,
	domain.UsageSourceHermesState: readHermesUsageChunk,
}

// sqliteCumulativeSource reports whether the kind diffs cumulative counters.
// Its parser baseline must survive a source replacement: replaying counters
// from zero would bill every earlier call again.
func sqliteCumulativeSource(kind domain.UsageSourceKind) bool {
	return kind == domain.UsageSourceHermesState
}

// readRowIDChunk adapts an append-only reader whose records carry their row
// ID as Offset, ordered by row ID, to the chunk contract.
func readRowIDChunk(records []jsonlRecord, after int64, limit int) (sqliteUsageChunk, error) {
	chunk := sqliteUsageChunk{records: records, nextOffset: after, atEnd: len(records) < limit}
	for _, record := range records {
		if record.Offset <= chunk.nextOffset {
			return sqliteUsageChunk{}, errors.New("usage rows are not ordered by row id")
		}
		chunk.nextOffset = record.Offset
	}
	return chunk, nil
}

func sqliteUsageSource(kind domain.UsageSourceKind) bool {
	_, ok := sqliteUsageReaders[kind]
	return ok
}

// openSQLiteUsageDB opens an agent-owned database strictly read-only. The
// agent keeps writing while AO reads, so the connection never takes a write
// lock, waits briefly on a busy writer, and refuses any statement that would
// modify the file.
func openSQLiteUsageDB(ctx context.Context, path string) (*sql.DB, error) {
	dsn := "file:" + (&url.URL{Path: filepath.ToSlash(path)}).EscapedPath() +
		"?mode=ro&_pragma=busy_timeout(1000)&_pragma=query_only(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// ingestSQLite processes one SQLite-backed source from its durable row cursor.
// It mirrors Ingest's lifecycle (replacement, finalization settling, pricing)
// without the byte-level integrity checks, which do not apply to a database
// the agent rewrites in place.
func (i *Ingestor) ingestSQLite(
	ctx context.Context,
	source domain.UsageSourceContext,
	parserState *parserStateEnvelope,
	now time.Time,
) (IngestResult, error) {
	result := IngestResult{
		SyncWatch: source.Source.State == domain.UsageSourceError &&
			source.Source.LastErrorCode == domain.UsageErrorArtifactMissing,
	}
	read := sqliteUsageReaders[source.Source.Kind]
	path := source.Source.ArtifactPath
	identity, err := usagesvc.SourceIdentity(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return i.retrySource(ctx, source.Source, domain.UsageErrorArtifactMissing, now, nil)
	}
	if identity != source.Source.FileIdentity {
		if sqliteCumulativeSource(source.Source.Kind) {
			return i.replaceCumulativeSource(ctx, source, identity, now)
		}
		return i.replaceSource(ctx, source, identity, now)
	}
	db, err := openSQLiteUsageDB(ctx, path)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, fmt.Errorf("open usage database: %w", err))
	}
	defer func() { _ = db.Close() }()
	chunk, err := read(ctx, db, source.Source, i.sqliteChunkRows)
	if err != nil {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, fmt.Errorf("read usage database: %w", err))
	}
	if after, err := usagesvc.SourceIdentity(ctx, path); err != nil || after != identity {
		if ctx.Err() != nil {
			return result, ctx.Err()
		}
		return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, errors.New("usage database replaced during ingestion"))
	}
	records, nextOffset, atEnd := chunk.records, chunk.nextOffset, chunk.atEnd
	progressed := nextOffset != source.Source.ByteOffset
	if source.Source.State == domain.UsageSourceComplete &&
		source.BindingState != domain.UsageBindingFinalizing && !progressed {
		return result, nil
	}
	if (source.BindingState == domain.UsageBindingComplete || source.BindingState == domain.UsageBindingPartial) && progressed {
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
	}
	parserState.Integrity.Checkpoint = nil
	parserState.Integrity.StableTail = nil
	parserState.Integrity.DiscardingOversizedRecord = false
	parsed := parseRecordsWithState(source, records, nextOffset, now, parserState)
	if parsed.err != nil {
		return i.retrySource(ctx, source.Source, domain.UsageErrorInvalidParserState, now, parsed.err)
	}
	if source.BindingState == domain.UsageBindingFinalizing && atEnd {
		// The agent may still commit its last rows after the stop hook, so a
		// finalizing source completes only after a quiet pass that read none.
		finalizationSettled := source.Source.NextRetryAt != nil && !source.Source.NextRetryAt.After(now)
		if finalizationSettled && !progressed {
			parsed.Cursor.State = domain.UsageSourceComplete
		} else {
			settleAt := nextFinalizationRetry(now, i.finalizationWait, source.Source.NextRetryAt, progressed)
			parsed.Cursor.NextRetryAt = &settleAt
			result.RetryAt = &settleAt
		}
	}
	if done, err := i.applyParsedChunk(ctx, source, &parsed, now); err != nil || done {
		return result, err
	}
	i.notifyLateRouteEvidence(ctx, source.Source.ID)
	if parsed.Cursor.State == domain.UsageSourceComplete {
		return result, i.completeBinding(ctx, source.Source.BindingID, now)
	}
	result.More = progressed && !atEnd
	if !result.More && result.RetryAt == nil &&
		(source.BindingState == domain.UsageBindingActive || source.BindingState == domain.UsageBindingDiscovering) {
		next := now.Add(i.sqlitePoll)
		result.RetryAt = &next
	}
	return result, nil
}

// replaceCumulativeSource starts a new generation for a replaced database but
// carries the counter baseline forward, so a restored or rewritten database
// bills only the usage it adds.
func (i *Ingestor) replaceCumulativeSource(
	ctx context.Context,
	source domain.UsageSourceContext,
	identity string,
	now time.Time,
) (IngestResult, error) {
	result := IngestResult{Refresh: true}
	replacement, err := i.store.ReplaceUsageSource(
		ctx,
		source.Source.ID,
		domain.UsageErrorArtifactReplaced,
		domain.UsageSourceRecord{
			BindingID:       source.Source.BindingID,
			Kind:            source.Source.Kind,
			NativeSessionID: source.Source.NativeSessionID,
			SubagentID:      source.Source.SubagentID,
			ArtifactPath:    source.Source.ArtifactPath,
			FileIdentity:    identity,
			Generation:      source.Source.Generation + 1,
			ByteOffset:      source.Source.ByteOffset,
			ParserStateJSON: source.Source.ParserStateJSON,
			State:           domain.UsageSourcePending,
			UpdatedAt:       now,
		},
		now,
	)
	if err != nil {
		return result, err
	}
	result.ReplacementSourceID = replacement.ID
	if source.BindingState == domain.UsageBindingComplete || source.BindingState == domain.UsageBindingPartial {
		if _, err := i.store.UpdateUsageBindingState(
			ctx,
			source.Source.BindingID,
			domain.UsageBindingFinalizing,
			"",
			now,
		); err != nil {
			return result, err
		}
	}
	return result, nil
}
