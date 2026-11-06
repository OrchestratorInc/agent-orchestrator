package usage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
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

// sqliteUsageReader returns up to limit usage rows for the bound native
// session whose row cursor is greater than after, ordered by that cursor. Each
// row is encoded as one JSON record whose Offset is its cursor (a row ID, or
// another strictly increasing per-row key), so the source's durable byte_offset
// column doubles as the row cursor and the per-kind parser reuses the JSONL
// parsing contract.
type sqliteUsageReader func(ctx context.Context, db *sql.DB, nativeSessionID string, after int64, limit int) ([]jsonlRecord, error)

var sqliteUsageReaders = map[domain.UsageSourceKind]sqliteUsageReader{
	domain.UsageSourceGooseLedger:     readGooseLedgerRows,
	domain.UsageSourceOpenCodeDB:      readOpenCodeSteps,
	domain.UsageSourceKiloDB:          readOpenCodeSteps,
	domain.UsageSourceAgyConversation: readAgyGenerations,
}

// snapshotUsageReader returns up to limit usage records after the cursor from
// a file the agent rewrites whole rather than appends to. It shares the row
// cursor contract of sqliteUsageReader.
type snapshotUsageReader func(ctx context.Context, path, nativeSessionID string, after int64, limit int) ([]jsonlRecord, error)

var snapshotUsageReaders = map[domain.UsageSourceKind]snapshotUsageReader{
	domain.UsageSourceGrokUsage:       readGrokTurns,
	domain.UsageSourceDeepSeekSession: readDeepSeekReplies,
}

// sqliteUsageSource reports whether the source is read by row cursor (a
// SQLite database, or a rewritten snapshot file) rather than byte offset.
func sqliteUsageSource(kind domain.UsageSourceKind) bool {
	_, ok := sqliteUsageReaders[kind]
	if !ok {
		_, ok = snapshotUsageReaders[kind]
	}
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
	path := source.Source.ArtifactPath
	limit := i.sqliteChunkRows
	var records []jsonlRecord
	if readSnapshot, snapshot := snapshotUsageReaders[source.Source.Kind]; snapshot {
		// The agent replaces the whole file after each turn, so its identity
		// changes by design; the turn cursor alone says what is new. A file
		// with the size and mtime of a read that left nothing unread holds
		// nothing new, and decoding it again can cost hundreds of MiB.
		stamp, statErr := snapshotStampOf(path)
		if seen, ok := i.snapshots.Load(source.Source.ID); statErr != nil || !ok || seen != stamp {
			var err error
			records, err = readSnapshot(ctx, path, source.Source.NativeSessionID, source.Source.ByteOffset, limit)
			if err != nil {
				i.snapshots.Delete(source.Source.ID)
				if ctx.Err() != nil {
					return result, ctx.Err()
				}
				if errors.Is(err, os.ErrNotExist) {
					return i.retrySource(ctx, source.Source, domain.UsageErrorArtifactMissing, now, nil)
				}
				return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, fmt.Errorf("read usage snapshot: %w", err))
			}
			if statErr == nil && len(records) < limit {
				i.snapshots.Store(source.Source.ID, stamp)
			} else {
				i.snapshots.Delete(source.Source.ID)
			}
		}
	} else {
		read := sqliteUsageReaders[source.Source.Kind]
		identity, err := usagesvc.SourceIdentity(ctx, path)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			return i.retrySource(ctx, source.Source, domain.UsageErrorArtifactMissing, now, nil)
		}
		if identity != source.Source.FileIdentity {
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
		records, err = read(ctx, db, source.Source.NativeSessionID, source.Source.ByteOffset, limit)
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
	}
	nextOffset := source.Source.ByteOffset
	for _, record := range records {
		// Calls billed in one row share its cursor, so a record may repeat the
		// one before it; none may go back or repeat the stored cursor.
		if record.Offset < nextOffset || record.Offset == source.Source.ByteOffset {
			return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, errors.New("usage rows are not ordered by row id"))
		}
		nextOffset = record.Offset
	}
	progressed := nextOffset > source.Source.ByteOffset
	atEnd := len(records) < limit
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
	// A poll that found nothing new writes nothing: every active source is
	// polled, and a write to a shared database's WAL queues every source on it.
	unchanged := !progressed && len(parsed.Events) == 0 &&
		parsed.Cursor.State == source.Source.State &&
		parsed.Cursor.ParserStateJSON == source.Source.ParserStateJSON &&
		parsed.Cursor.AnomalyCount == source.Source.AnomalyCount &&
		source.Source.FailureCount == 0 && source.Source.LastErrorCode == "" &&
		sameRetryTime(parsed.Cursor.NextRetryAt, source.Source.NextRetryAt)
	if !unchanged {
		if done, err := i.applyParsedChunk(ctx, source, &parsed, nil, &result, now); err != nil || done {
			return result, err
		}
		i.notifyLateRouteEvidence(ctx, source.Source.ID)
	}
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

// snapshotStamp is the size and mtime of a snapshot file, which the agent
// changes whenever it rewrites the file.
type snapshotStamp struct {
	size    int64
	modTime int64
}

func snapshotStampOf(path string) (snapshotStamp, error) {
	info, err := os.Stat(path)
	if err != nil {
		return snapshotStamp{}, err
	}
	return snapshotStamp{size: info.Size(), modTime: info.ModTime().UnixNano()}, nil
}

func sameRetryTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
