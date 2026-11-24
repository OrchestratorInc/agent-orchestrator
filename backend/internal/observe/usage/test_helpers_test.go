package usage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func parseRecords(source domain.UsageSourceContext, records []jsonlRecord, nextOffset int64, now time.Time) parseResult {
	state, err := decodeParserState(source.Source)
	if err != nil {
		return parseResult{err: fmt.Errorf("decode parser state: %w", err)}
	}
	return parseRecordsWithState(source, records, nextOffset, now, state)
}

func readJSONLChunkFromFile(file *os.File, offset, maxBytes int64, maxRecord int, discardingOversizedRecord bool) (jsonlChunk, error) {
	info, err := file.Stat()
	if err != nil {
		return jsonlChunk{}, err
	}
	return readJSONLChunkFromSnapshot(context.Background(), file, info.Size(), offset, maxBytes, maxRecord, discardingOversizedRecord)
}

func seedUsageTestSession(
	t *testing.T,
	dataDir string,
	projectID domain.ProjectID,
	harness domain.AgentHarness,
	activity domain.ActivityState,
	nativeID string,
	now time.Time,
) (*sqlite.Store, domain.SessionRecord) {
	t.Helper()
	store, err := sqlitetest.Open(dataDir)
	mustNoError(t, err)
	t.Cleanup(func() { _ = store.Close() })
	ctx := context.Background()
	mustNoError(t, store.UpsertProject(ctx, domain.ProjectRecord{ID: string(projectID), Path: t.TempDir(), RegisteredAt: now}))
	session, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: projectID,
		Kind:      domain.KindWorker,
		Harness:   harness,
		Activity:  domain.Activity{State: activity, LastActivityAt: now},
		Metadata:  domain.SessionMetadata{AgentSessionID: nativeID},
		CreatedAt: now,
		UpdatedAt: now,
	})
	mustNoError(t, err)
	return store, session
}

func seedUsageTestBinding(
	t *testing.T,
	store *sqlite.Store,
	session domain.SessionRecord,
	nativeID string,
	state domain.UsageBindingState,
	now time.Time,
) domain.UsageBindingRecord {
	t.Helper()
	binding, err := store.UpsertUsageBinding(context.Background(), domain.UsageBindingRecord{
		SessionID:    session.ID,
		Harness:      session.Harness,
		NativeRootID: nativeID,
		State:        state,
		UpdatedAt:    now,
	})
	mustNoError(t, err)
	return binding
}

func mustNoError(t testing.TB, err error, context ...string) {
	t.Helper()
	if err != nil {
		if len(context) > 0 {
			t.Fatalf("%s: %v", context[0], err)
		}
		t.Fatal(err)
	}
}
