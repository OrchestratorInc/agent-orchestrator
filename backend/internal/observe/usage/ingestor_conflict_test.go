package usage

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/cdc"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/usage"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

// Break caught: a source event conflict leaves the cursor in front of the
// rejected chunk, so every later pass saw an unread tail, reopened the idle
// binding to finalizing, hit the same conflict, settled the binding back to
// partial and asked for another pass one finalization wait later. Each idle
// session published a session_updated pair every two seconds for as long as
// the daemon ran.
func TestIngestorParksConflictingSourceInsteadOfReopeningItsBinding(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	store, session, source := seedConflictingClaudeSource(
		ctx, t, domain.ActivityExited, domain.UsageBindingFinalizing, now,
	)

	reads := &countingApplyStore{Store: store}
	base := latestChangeSeq(t, store)
	runIdleCoordinator(ctx, t, store, NewIngestor(reads, IngestorConfig{Clock: func() time.Time { return now }}), &now, 10*time.Minute)

	assertSourceState(t, store, source.ID, domain.UsageSourceComplete, domain.UsageErrorSourceEventConflict)
	assertBindingState(t, store, session.ID, domain.UsageBindingPartial)
	if reads.applies != 1 {
		t.Fatalf("conflicting chunk was applied %d times in ten idle minutes, want 1", reads.applies)
	}
	// Recording the conflict code and settling the binding are the only two
	// facts that changed.
	if got := sessionUpdatesAfter(t, store, base, session.ID); got != 2 {
		t.Fatalf("session_updated rows = %d in ten idle minutes, want 2", got)
	}

	// A restarted daemon may carry newer replay rules, so it gets one attempt
	// at the parked source and then leaves it alone again.
	reads.applies = 0
	base = latestChangeSeq(t, store)
	runIdleCoordinator(ctx, t, store, NewIngestor(reads, IngestorConfig{Clock: func() time.Time { return now }}), &now, 10*time.Minute)
	if reads.applies != 1 {
		t.Fatalf("restarted daemon applied the parked chunk %d times, want 1", reads.applies)
	}
	if got := sessionUpdatesAfter(t, store, base, session.ID); got > 2 {
		t.Fatalf("session_updated rows = %d after restart, want at most 2", got)
	}
	assertBindingState(t, store, session.ID, domain.UsageBindingPartial)
}

// Resuming a session reactivates its sources. That is new evidence, so the
// parked source gets a fresh attempt instead of staying parked.
func TestIngestorRetriesParkedConflictAfterReactivation(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	store, _, source := seedConflictingClaudeSource(
		ctx, t, domain.ActivityIdle, domain.UsageBindingActive, now,
	)

	reads := &countingApplyStore{Store: store}
	ingestor := NewIngestor(reads, IngestorConfig{Clock: func() time.Time { return now }})
	for range 3 {
		_, err := ingestor.Ingest(ctx, source.ID)
		mustNoError(t, err)
	}
	if reads.applies != 1 {
		t.Fatalf("parked chunk applied %d times, want 1", reads.applies)
	}
	if _, err := store.ReactivateUsageSource(ctx, source.ID, now); err != nil {
		t.Fatal(err)
	}
	_, err := ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err)
	if reads.applies != 2 {
		t.Fatalf("reactivated source applied %d times, want a fresh attempt", reads.applies)
	}
	assertSourceState(t, store, source.ID, domain.UsageSourceComplete, domain.UsageErrorSourceEventConflict)
}

// Break caught: a reboot that renumbers the volume changes every transcript's
// file identity, so every source is replaced and replayed from byte zero. The
// legacy repairer had already given the stored rows an inferred billing
// provider, the replay names none, and ApplyUsageChunk rejected the replay as a
// source event conflict. The replacement never advanced past byte zero and its
// binding looped between partial and finalizing.
func TestIngestorReplayAfterIdentityChangeKeepsRepairedAttribution(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	dataDir := t.TempDir()
	store, session, binding, path := seedRepairedClaudeTranscript(ctx, t, dataDir, &now)
	retired := latestSourceForPath(ctx, t, store, path)

	db := openRawUsageDB(t, dataDir)
	_, err := db.ExecContext(ctx, `UPDATE usage_sources SET file_identity = 'unix:0:0' WHERE id = ?`, retired.ID)
	mustNoError(t, err)

	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	runIdleCoordinator(ctx, t, store, ingestor, &now, time.Minute)
	replacement := latestSourceForPath(ctx, t, store, path)
	if replacement.ID == retired.ID || replacement.Generation != retired.Generation+1 {
		t.Fatalf("identity change did not replace the source: %+v", replacement)
	}
	assertReplayedCleanly(ctx, t, store, db, session.ID, binding.ID, replacement, path)

	base := latestChangeSeq(t, store)
	runIdleCoordinator(ctx, t, store, ingestor, &now, 10*time.Minute)
	if got := sessionUpdatesAfter(t, store, base, session.ID); got != 0 {
		t.Fatalf("settled replacement published %d session_updated rows while idle", got)
	}

	// Real usage still invalidates the session.
	appendJSONLRecord(t, path, []byte(claudeUsageRecord("msg-3", 30, 3)))
	base = latestChangeSeq(t, store)
	runIdleCoordinator(ctx, t, store, ingestor, &now, time.Minute)
	if got := sessionUpdatesAfter(t, store, base, session.ID); got == 0 {
		t.Fatal("new usage published no session_updated row")
	}
	assertTokenAggregate(t, store, session.ID, 20+33)
}

// Sources an older build already parked must heal once the fix ships, not
// only sources that conflict afterwards. This seeds the exact shape found in a
// live database: a partial binding whose replacement generation sits complete
// with source_event_conflict at byte zero over rows the repairer attributed.
func TestIngestorHealsSourceParkedByEarlierBuild(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1700000000, 0).UTC()
	dataDir := t.TempDir()
	store, session, binding, path := seedRepairedClaudeTranscript(ctx, t, dataDir, &now)
	retired := latestSourceForPath(ctx, t, store, path)

	replacement, err := store.ReplaceUsageSource(ctx, retired.ID, domain.UsageErrorArtifactReplaced, domain.UsageSourceRecord{
		BindingID:       retired.BindingID,
		Kind:            retired.Kind,
		NativeSessionID: retired.NativeSessionID,
		ArtifactPath:    retired.ArtifactPath,
		FileIdentity:    retired.FileIdentity,
		Generation:      retired.Generation + 1,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	}, now)
	mustNoError(t, err)
	_, err = store.MarkUsageSourceState(ctx, replacement.ID, domain.UsageSourceComplete, domain.UsageErrorSourceEventConflict, nil, now)
	mustNoError(t, err)
	_, err = store.UpdateUsageBindingState(ctx, binding.ID, domain.UsageBindingPartial, "", now)
	mustNoError(t, err)

	db := openRawUsageDB(t, dataDir)
	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	base := latestChangeSeq(t, store)
	runIdleCoordinator(ctx, t, store, ingestor, &now, time.Minute)
	assertReplayedCleanly(ctx, t, store, db, session.ID, binding.ID, latestSourceForPath(ctx, t, store, path), path)
	// Reopening the binding, clearing the conflict code and settling it.
	if got := sessionUpdatesAfter(t, store, base, session.ID); got > 3 {
		t.Fatalf("healing published %d session_updated rows, want at most 3", got)
	}

	base = latestChangeSeq(t, store)
	runIdleCoordinator(ctx, t, store, ingestor, &now, 10*time.Minute)
	if got := sessionUpdatesAfter(t, store, base, session.ID); got != 0 {
		t.Fatalf("healed source published %d session_updated rows while idle", got)
	}
}

// seedConflictingClaudeSource registers a one-turn Claude transcript whose
// replay can never apply: a different fact is already stored under the key it
// replays, which is what a rewritten transcript leaves behind.
func seedConflictingClaudeSource(
	ctx context.Context,
	t *testing.T,
	activity domain.ActivityState,
	bindingState domain.UsageBindingState,
	now time.Time,
) (*sqlite.Store, domain.SessionRecord, domain.UsageSourceRecord) {
	t.Helper()
	store, session := seedUsageTestSession(
		t, t.TempDir(), "usage", domain.HarnessClaudeCode, activity, "", now,
	)
	binding := seedUsageTestBinding(t, store, session, "claude-root", bindingState, now)
	path := filepath.Join(t.TempDir(), "claude-root.jsonl")
	line := claudeUsageRecord("msg-1", 8, 2) + "\n"
	mustNoError(t, os.WriteFile(path, []byte(line), 0o600))
	source := insertClaudeMainSource(t, store, binding, path, now)
	contextSource, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	if err != nil || !ok {
		t.Fatalf("source ok=%v err=%v", ok, err)
	}
	parsed := parseRecords(contextSource, []jsonlRecord{{Data: []byte(line)}}, int64(len(line)), now)
	if parsed.err != nil || len(parsed.Events) != 1 {
		t.Fatalf("parsed=%+v", parsed)
	}
	conflict := parsed.Events[0]
	(*conflict.Tokens.OutputTokens)++
	mustNoError(t, store.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
		State: domain.UsageSourcePending, UpdatedAt: now,
	}, []domain.ModelUsageEvent{conflict}))
	return store, session, source
}

// seedRepairedClaudeTranscript ingests a finished two-turn Claude transcript
// that named no billing route, settles its binding, and then attributes the
// stored rows the way the legacy repairer does when no route was recorded.
func seedRepairedClaudeTranscript(
	ctx context.Context,
	t *testing.T,
	dataDir string,
	now *time.Time,
) (*sqlite.Store, domain.SessionRecord, domain.UsageBindingRecord, string) {
	t.Helper()
	store, session := seedUsageTestSession(
		t, dataDir, "usage", domain.HarnessClaudeCode, domain.ActivityExited, "", *now,
	)
	binding := seedUsageTestBinding(t, store, session, "claude-root", domain.UsageBindingFinalizing, *now)
	path := filepath.Join(t.TempDir(), "claude-root.jsonl")
	content := claudeUsageRecord("msg-1", 8, 2) + "\n" + claudeUsageRecord("msg-2", 9, 1) + "\n"
	mustNoError(t, os.WriteFile(path, []byte(content), 0o600))
	insertClaudeMainSource(t, store, binding, path, *now)

	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return *now }})
	runIdleCoordinator(ctx, t, store, ingestor, now, time.Minute)
	assertBindingState(t, store, session.ID, domain.UsageBindingComplete)
	assertTokenAggregate(t, store, session.ID, 20)

	db := openRawUsageDB(t, dataDir)
	_, err := db.ExecContext(ctx, `
UPDATE model_usage_events
SET billing_provider_id = 'anthropic', billing_provider_source = 'inferred'
WHERE binding_id = ?`, binding.ID)
	mustNoError(t, err)
	return store, session, binding, path
}

func assertReplayedCleanly(
	ctx context.Context,
	t *testing.T,
	store *sqlite.Store,
	db *sql.DB,
	sessionID domain.SessionID,
	bindingID int64,
	replacement domain.UsageSourceRecord,
	path string,
) {
	t.Helper()
	info, err := os.Stat(path)
	mustNoError(t, err)
	if replacement.State != domain.UsageSourceComplete || replacement.LastErrorCode != "" ||
		replacement.ByteOffset != info.Size() {
		t.Fatalf("replacement did not finish its replay: %+v (file size %d)", replacement, info.Size())
	}
	assertBindingState(t, store, sessionID, domain.UsageBindingComplete)
	assertTokenAggregate(t, store, sessionID, 20)
	var rows, kept, rehomed int
	mustNoError(t, db.QueryRowContext(ctx, `
SELECT count(*),
       sum(billing_provider_id = 'anthropic' AND billing_provider_source = 'inferred'),
       sum(usage_source_id = ?)
FROM model_usage_events
WHERE binding_id = ?`, replacement.ID, bindingID).Scan(&rows, &kept, &rehomed))
	if rows != 2 || kept != 2 || rehomed != 2 {
		t.Fatalf("rows=%d kept attribution=%d rehomed=%d, want 2 each", rows, kept, rehomed)
	}
}

// runIdleCoordinator drives ingestion the way Coordinator does while nothing
// writes a transcript: an inventory refresh at startup and every
// defaultCoordinatorRetry (discovery stays pending on a busy machine), plus
// each retry a pass asks for. Time only moves between passes.
func runIdleCoordinator(
	ctx context.Context,
	t *testing.T,
	store *sqlite.Store,
	ingestor *Ingestor,
	now *time.Time,
	window time.Duration,
) {
	t.Helper()
	end := now.Add(window)
	refreshAt := *now
	retries := make(map[int64]time.Time)
	for wakeups := 0; ; wakeups++ {
		if wakeups > 10_000 {
			t.Fatal("simulated coordinator never went idle")
		}
		due, dueSource := refreshAt, refreshRetryID
		for sourceID, at := range retries {
			if at.Before(due) {
				due, dueSource = at, sourceID
			}
		}
		if due.After(end) {
			*now = end
			return
		}
		*now = due
		var queue []int64
		if dueSource == refreshRetryID {
			refreshAt = due.Add(defaultCoordinatorRetry)
			sources, err := store.ListWatchableUsageSources(ctx)
			mustNoError(t, err)
			for _, source := range sources {
				if source.NextRetryAt != nil && source.NextRetryAt.After(due) {
					retries[source.ID] = source.NextRetryAt.UTC()
					continue
				}
				delete(retries, source.ID)
				queue = append(queue, source.ID)
			}
		} else {
			delete(retries, dueSource)
			queue = append(queue, dueSource)
		}
		for len(queue) > 0 {
			sourceID := queue[0]
			queue = queue[1:]
			result, err := ingestor.Ingest(ctx, sourceID)
			switch {
			case result.RetryAt != nil:
				retries[sourceID] = result.RetryAt.UTC()
			case err != nil:
				retries[sourceID] = due.Add(defaultCoordinatorRetry)
			}
			if result.More {
				queue = append(queue, sourceID)
			}
			if result.ReplacementSourceID != 0 {
				queue = append(queue, result.ReplacementSourceID)
			}
		}
	}
}

type countingApplyStore struct {
	*sqlite.Store
	applies int
}

func (s *countingApplyStore) ApplyUsageChunk(
	ctx context.Context,
	sourceID int64,
	expectedOffset int64,
	expectedRevision time.Time,
	nextState domain.SourceCursorState,
	events []domain.ModelUsageEvent,
) error {
	s.applies++
	return s.Store.ApplyUsageChunk(ctx, sourceID, expectedOffset, expectedRevision, nextState, events)
}

func claudeUsageRecord(messageID string, input, output int64) string {
	return fmt.Sprintf(
		`{"type":"assistant","uuid":"uuid-%s","message":{"id":%q,"model":"claude-x","stop_reason":"end_turn","usage":{"input_tokens":%d,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":%d}}}`,
		messageID, messageID, input, output,
	)
}

func insertClaudeMainSource(
	t *testing.T,
	store *sqlite.Store,
	binding domain.UsageBindingRecord,
	path string,
	now time.Time,
) domain.UsageSourceRecord {
	t.Helper()
	identity, err := usagesvc.SourceIdentity(context.Background(), path)
	mustNoError(t, err)
	source, err := store.InsertUsageSource(context.Background(), domain.UsageSourceRecord{
		BindingID:       binding.ID,
		Kind:            domain.UsageSourceClaudeMain,
		NativeSessionID: binding.NativeRootID,
		ArtifactPath:    path,
		FileIdentity:    identity,
		State:           domain.UsageSourcePending,
		UpdatedAt:       now,
	})
	mustNoError(t, err)
	return source
}

func latestSourceForPath(ctx context.Context, t *testing.T, store *sqlite.Store, path string) domain.UsageSourceRecord {
	t.Helper()
	sources, err := store.ListWatchableUsageSources(ctx)
	mustNoError(t, err)
	for _, source := range sources {
		if source.ArtifactPath == path {
			return source
		}
	}
	t.Fatalf("no watchable source for %s", path)
	return domain.UsageSourceRecord{}
}

func assertSourceState(t *testing.T, store *sqlite.Store, sourceID int64, state domain.UsageSourceState, code string) {
	t.Helper()
	got, ok, err := store.GetUsageSourceForIngestion(context.Background(), sourceID)
	if err != nil || !ok {
		t.Fatalf("source ok=%v err=%v", ok, err)
	}
	if got.Source.State != state || got.Source.LastErrorCode != code {
		t.Fatalf("source state=%s code=%q, want %s %q", got.Source.State, got.Source.LastErrorCode, state, code)
	}
}

func assertBindingState(t *testing.T, store *sqlite.Store, sessionID domain.SessionID, state domain.UsageBindingState) {
	t.Helper()
	bindings, err := store.ListUsageBindingsForSession(context.Background(), sessionID)
	if err != nil || len(bindings) != 1 || bindings[0].State != state {
		t.Fatalf("bindings=%+v err=%v, want one %s binding", bindings, err, state)
	}
}

func latestChangeSeq(t *testing.T, store *sqlite.Store) int64 {
	t.Helper()
	seq, err := store.LatestSeq(context.Background())
	mustNoError(t, err)
	return seq
}

func sessionUpdatesAfter(t *testing.T, store *sqlite.Store, after int64, sessionID domain.SessionID) int {
	t.Helper()
	events, err := store.EventsAfter(context.Background(), after, 100_000)
	mustNoError(t, err)
	count := 0
	for _, event := range events {
		if event.Type == cdc.EventSessionUpdated && event.SessionID == string(sessionID) {
			count++
		}
	}
	return count
}

func openRawUsageDB(t *testing.T, dataDir string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	mustNoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
