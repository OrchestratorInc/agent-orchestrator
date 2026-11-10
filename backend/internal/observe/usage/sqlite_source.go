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

	_ "modernc.org/sqlite" // register sqlite driver

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
)

const defaultSQLiteChunkRows = 1000

type sqliteUsageReader func(ctx context.Context, db *sql.DB, nativeSessionID string, after int64, limit int) ([]jsonlRecord, error)

var sqliteUsageReaders = map[domain.UsageSourceKind]sqliteUsageReader{
	domain.UsageSourceGooseLedger:     readGooseLedgerRows,
	domain.UsageSourceOpenCodeDB:      readOpenCodeSteps,
	domain.UsageSourceKiloDB:          readOpenCodeSteps,
	domain.UsageSourceAgyConversation: readAgyGenerations,
}

type snapshotUsageReader func(ctx context.Context, path, nativeSessionID string, after int64, limit int) ([]jsonlRecord, error)

var snapshotUsageReaders = map[domain.UsageSourceKind]snapshotUsageReader{
	domain.UsageSourceGrokUsage:       readGrokTurns,
	domain.UsageSourceDeepSeekSession: readDeepSeekReplies,
}

func sqliteUsageSource(kind domain.UsageSourceKind) bool {
	_, ok := sqliteUsageReaders[kind]
	if !ok {
		_, ok = snapshotUsageReaders[kind]
	}
	return ok
}

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
		var err error
		records, err = readSnapshot(ctx, path, source.Source.NativeSessionID, source.Source.ByteOffset, limit)
		if err != nil {
			if ctx.Err() != nil {
				return result, ctx.Err()
			}
			if errors.Is(err, os.ErrNotExist) {
				return i.retrySource(ctx, source.Source, domain.UsageErrorArtifactMissing, now, nil)
			}
			return i.retrySource(ctx, source.Source, domain.UsageErrorSourceReadFailed, now, fmt.Errorf("read usage snapshot: %w", err))
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
		finalizationSettled := source.Source.NextRetryAt != nil && !source.Source.NextRetryAt.After(now)
		if finalizationSettled && !progressed {
			parsed.Cursor.State = domain.UsageSourceComplete
		} else {
			settleAt := nextFinalizationRetry(now, i.finalizationWait, source.Source.NextRetryAt, progressed)
			parsed.Cursor.NextRetryAt = &settleAt
			result.RetryAt = &settleAt
		}
	}
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
	return result, nil
}

func sameRetryTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
