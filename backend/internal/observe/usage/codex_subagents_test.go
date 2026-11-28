package usage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	usagesvc "github.com/OrchestratorInc/agent-orchestrator/backend/internal/service/usage"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/storage/sqlite"
)

const (
	testCodexParentID     = "11111111-1111-4111-8111-111111111111"
	testCodexChildID      = "22222222-2222-4222-8222-222222222222"
	testCodexGrandchildID = "33333333-3333-4333-8333-333333333333"
)

func TestParseCodexCorrelatesSpawnAgentAcrossParserRestart(t *testing.T) {
	now := time.Unix(1700000000, 0).UTC()
	source := usageSource(domain.UsageSourceCodexRollout)
	first := parseRecords(source, []jsonlRecord{{Data: codexResponseItem(t, map[string]any{
		"type":      "function_call",
		"name":      "spawn_agent",
		"call_id":   "call_across_restart",
		"arguments": `{"message":"never persist this"}`,
	})}}, 200, now)
	firstState := parserStateFromResult(t, first, domain.UsageSourceCodexRollout)
	if len(firstState.Codex.PendingSpawnCallIDs) != 1 ||
		firstState.Codex.PendingSpawnCallIDs[0] != "call_across_restart" {
		t.Fatalf("first parser state = %+v", firstState.Codex)
	}

	source.Source.ParserStateJSON = first.Cursor.ParserStateJSON
	second := parseRecords(source, []jsonlRecord{
		{Offset: 200, Data: codexResponseItem(t, map[string]any{
			"type":    "function_call_output",
			"call_id": "call_across_restart",
			"output":  `{"agent_id":"` + testCodexChildID + `","reply":"never persist this either"}`,
		})},
		{Offset: 400, Data: codexResponseItem(t, map[string]any{
			"type":    "function_call_output",
			"call_id": "call_across_restart",
			"output":  `{"agent_id":"` + testCodexChildID + `"}`,
		})},
	}, 600, now.Add(time.Second))
	secondState := parserStateFromResult(t, second, domain.UsageSourceCodexRollout)
	if len(secondState.Codex.PendingSpawnCallIDs) != 0 ||
		len(secondState.Codex.DiscoveredChildIDs) != 1 ||
		secondState.Codex.DiscoveredChildIDs[0] != testCodexChildID {
		t.Fatalf("restarted parser state = %+v", secondState.Codex)
	}
	if strings.Contains(first.Cursor.ParserStateJSON, "never persist this") ||
		strings.Contains(second.Cursor.ParserStateJSON, "never persist this either") {
		t.Fatalf("parser state persisted transcript content: %s", second.Cursor.ParserStateJSON)
	}
}

func TestParseCodexKeepsPendingSpawnAfterMalformedOrUnmatchedOutput(t *testing.T) {
	source := usageSource(domain.UsageSourceCodexRollout)
	records := []jsonlRecord{
		{Data: codexResponseItem(t, map[string]any{
			"type":    "function_call",
			"name":    "spawn_agent",
			"call_id": "call_pending",
		})},
		{Data: codexResponseItem(t, map[string]any{
			"type":    "function_call_output",
			"call_id": "call_unmatched",
			"output":  `{"agent_id":"` + testCodexChildID + `"}`,
		})},
		{Data: codexResponseItem(t, map[string]any{
			"type":    "function_call_output",
			"call_id": "call_pending",
			"output":  `{"agent_id":`,
		})},
	}

	result := parseRecords(source, records, 600, time.Unix(1700000000, 0).UTC())
	state := parserStateFromResult(t, result, domain.UsageSourceCodexRollout)
	if len(state.Codex.PendingSpawnCallIDs) != 1 || state.Codex.PendingSpawnCallIDs[0] != "call_pending" ||
		len(state.Codex.DiscoveredChildIDs) != 0 {
		t.Fatalf("Codex malformed output state = %+v", state.Codex)
	}
	if result.Cursor.AnomalyCount != 1 || result.Cursor.LastErrorCode != domain.UsageErrorMalformedJSONL {
		t.Fatalf("malformed output cursor = %+v", result.Cursor)
	}
}

func TestParseCodexRejectsUnboundedSpawnIdentifiers(t *testing.T) {
	source := usageSource(domain.UsageSourceCodexRollout)
	result := parseRecords(source, []jsonlRecord{{Data: codexResponseItem(t, map[string]any{
		"type":    "function_call",
		"name":    "spawn_agent",
		"call_id": strings.Repeat("a", 257),
	})}}, 300, time.Unix(1700000000, 0).UTC())
	state := parserStateFromResult(t, result, domain.UsageSourceCodexRollout)
	if len(state.Codex.PendingSpawnCallIDs) != 0 || result.Cursor.AnomalyCount != 1 {
		t.Fatalf("unbounded call state/cursor = %+v / %+v", state.Codex, result.Cursor)
	}
}

func TestParseCodexRetainsPendingSpawnWhenChildCapacityIsExceeded(t *testing.T) {
	discovered := make([]string, maxCodexAttributionIDs)
	for index := range discovered {
		discovered[index] = fmt.Sprintf("%08x-0000-4000-8000-%012x", index, index)
	}
	state := &codexParserStateV1{
		PendingSpawnCallIDs: []string{"call_at_capacity"},
		DiscoveredChildIDs:  discovered,
	}
	payload, err := json.Marshal(map[string]any{
		"type":    "function_call_output",
		"call_id": "call_at_capacity",
		"output":  `{"agent_id":"` + testCodexGrandchildID + `"}`,
	})
	mustNoError(t, err)
	result := parseResult{}
	parseCodexResponseItem(payload, state, &result)
	if len(state.PendingSpawnCallIDs) != 1 || state.PendingSpawnCallIDs[0] != "call_at_capacity" ||
		len(state.DiscoveredChildIDs) != maxCodexAttributionIDs || result.Cursor.AnomalyCount != 1 {
		t.Fatalf("capacity pending/discovered/anomalies = %d/%d/%d", len(state.PendingSpawnCallIDs), len(state.DiscoveredChildIDs), result.Cursor.AnomalyCount)
	}
}

func TestIngestorSignalsReconcileOnlyForNewlyCommittedCodexChild(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedCodexIngestionSource(t, dataDir)
	call := codexResponseItem(t, map[string]any{
		"type":      "function_call",
		"name":      "spawn_agent",
		"call_id":   "call_committed_child",
		"arguments": `{"message":"private"}`,
	})
	mustNoError(t, os.WriteFile(path, append(call, '\n'), 0o600))

	firstIngestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	first, err := firstIngestor.Ingest(ctx, source.ID)
	mustNoError(t, err, "ingest spawn call")
	if first.Reconcile {
		t.Fatalf("spawn call requested reconciliation before an output committed: %+v", first)
	}
	firstState, err := decodeParserState(domain.UsageSourceRecord{
		Kind:            domain.UsageSourceCodexRollout,
		ByteOffset:      int64(len(call) + 1),
		ParserStateJSON: readParserStateJSON(t, dataDir, source.ID),
	})
	if err != nil || len(firstState.Codex.PendingSpawnCallIDs) != 1 {
		t.Fatalf("persisted pending state = %+v, err=%v", firstState, err)
	}

	output := codexResponseItem(t, map[string]any{
		"type":    "function_call_output",
		"call_id": "call_committed_child",
		"output":  `{"agent_id":"` + testCodexChildID + `"}`,
	})
	mustNoError(t, osAppend(path, string(output)+"\n"))
	restarted := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now.Add(time.Second) }})
	second, err := restarted.Ingest(ctx, source.ID)
	mustNoError(t, err, "ingest matching output after restart")
	if !second.Reconcile {
		t.Fatalf("newly committed child did not request reconciliation: %+v", second)
	}

	mustNoError(t, osAppend(path, string(output)+"\n"))
	third, err := restarted.Ingest(ctx, source.ID)
	mustNoError(t, err, "ingest duplicate output")
	if third.Reconcile {
		t.Fatalf("duplicate output requested reconciliation: %+v", third)
	}
}

func TestIngestorMarksUnresolvedCodexSpawnPartialAtStableFinalEOF(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	store, source, path, now := seedCodexIngestionSource(t, dataDir)
	call := codexResponseItem(t, map[string]any{
		"type":      "function_call",
		"name":      "spawn_agent",
		"call_id":   "call_never_resolved",
		"arguments": `{"message":"private"}`,
	})
	mustNoError(t, os.WriteFile(path, append(call, '\n'), 0o600))
	if _, err := store.UpdateUsageBindingState(ctx, source.BindingID, domain.UsageBindingFinalizing, "", now); err != nil {
		t.Fatal(err)
	}

	ingestor := NewIngestor(store, IngestorConfig{Clock: func() time.Time { return now }})
	first, err := ingestor.Ingest(ctx, source.ID)
	mustNoError(t, err, "initial final ingest")
	if first.RetryAt == nil {
		t.Fatalf("initial final ingest did not schedule stability wait: %+v", first)
	}
	now = now.Add(defaultFinalizationWait + time.Second)
	if _, err := ingestor.Ingest(ctx, source.ID); err != nil {
		t.Fatalf("stable final ingest: %v", err)
	}

	got, ok, err := store.GetUsageSourceForIngestion(ctx, source.ID)
	if err != nil || !ok {
		t.Fatalf("get finalized source: ok=%v err=%v", ok, err)
	}
	if got.Source.State != domain.UsageSourceComplete || got.Source.AnomalyCount != 1 ||
		got.Source.LastErrorCode != "unresolved_spawn_call" {
		t.Fatalf("final source = %+v", got.Source)
	}
	bindings, err := store.ListUsageBindingsForSession(ctx, got.SessionID)
	if err != nil || len(bindings) != 1 || bindings[0].State != domain.UsageBindingPartial {
		t.Fatalf("final binding = %+v, err=%v", bindings, err)
	}
}

func TestCodexSubagentsAggregateRecursivelyExactlyOnce(t *testing.T) {
	fixture := newCodexSubagentFixture(t, domain.ActivityIdle)
	fixture.write(testCodexParentID, "", 100, 20, testCodexChildID)
	fixture.write(testCodexChildID, testCodexParentID, 40, 10, testCodexGrandchildID)
	grandchildPath := filepath.Join(fixture.roots.CodexArchived, "rollout-"+testCodexGrandchildID+".jsonl")
	writeCodexRollout(t, grandchildPath, codexRolloutFixture(t, testCodexGrandchildID, testCodexChildID, 5, 2, ""))

	fixture.backfill()
	fixture.ingest(testCodexParentID, true)
	fixture.reconcile()
	fixture.ingest(testCodexChildID, true)
	fixture.reconcile()
	fixture.ingest(testCodexGrandchildID, false)

	fixture.assertTokens(177)
	bindings, err := fixture.store.ListUsageBindingsForSession(fixture.ctx, fixture.session.ID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("child attribution created extra bindings: %+v, err=%v", bindings, err)
	}
	sources, err := fixture.store.ListUsageSourcesForBinding(fixture.ctx, fixture.binding.ID)
	if err != nil || len(sources) != 3 {
		t.Fatalf("recursive sources = %+v, err=%v", sources, err)
	}
	for _, source := range sources {
		if source.NativeSessionID != testCodexParentID && source.SubagentID != source.NativeSessionID {
			t.Fatalf("child source attribution = %+v", source)
		}
	}
}

func TestCoordinatorReconcilesLateCodexChildBeforeBindingCompletes(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store, session, roots := seedCodexRolloutSession(t, domain.ActivityExited)
	parentPath := activeCodexRolloutPath(roots.CodexSessions, testCodexParentID)
	childPath := activeCodexRolloutPath(roots.CodexSessions, testCodexChildID)
	writeCodexRollout(t, parentPath, codexRolloutFixture(t, testCodexParentID, "", 100, 20, testCodexChildID))

	watcher, err := NewTranscriptWatcher(ctx, []string{roots.CodexSessions, roots.CodexArchived})
	mustNoError(t, err)
	collector := usagesvc.NewCollector(store, roots, nil)
	ingestor := NewIngestor(store, IngestorConfig{FinalizationWait: 50 * time.Millisecond})
	coordinator := NewCoordinator(store, ingestor, watcher, CoordinatorConfig{
		Workers:    1,
		RetryDelay: 20 * time.Millisecond,
		Initialize: collector.BackfillActive,
		Reconcile: func(reconcileCtx context.Context) error {
			return collector.ReconcileSources(reconcileCtx, -1)
		},
	})
	done := coordinator.Start(ctx)
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("usage coordinator did not stop")
		}
	})

	waitForTokenAggregate(t, store, session.ID, 120)
	binding := onlyUsageBinding(t, store, session.ID)
	if binding.State != domain.UsageBindingFinalizing {
		t.Fatalf("binding completed while discovered child was missing: %+v", binding)
	}

	writeCodexRollout(t, childPath, codexRolloutFixture(t, testCodexChildID, testCodexParentID, 40, 10, ""))
	waitForWatchableSource(ctx, t, store, childPath)
	waitForTokenAggregate(t, store, session.ID, 170)
	waitForUsageBindingState(t, store, session.ID, domain.UsageBindingComplete)
}

func TestCodexChildCheckpointMismatchRevalidatesIdentityAndParent(t *testing.T) {
	tests := []struct {
		name              string
		replacementID     string
		replacementParent string
	}{
		{name: "wrong child id", replacementID: testCodexGrandchildID, replacementParent: testCodexParentID},
		{name: "wrong parent", replacementID: testCodexChildID, replacementParent: testCodexGrandchildID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newCodexSubagentFixture(t, domain.ActivityIdle)
			fixture.write(testCodexParentID, "", 100, 20, testCodexChildID)
			childPath := fixture.write(testCodexChildID, testCodexParentID, 40, 10, "")
			fixture.backfill()
			fixture.ingest(testCodexParentID, true)
			fixture.reconcile()
			child := fixture.ingest(testCodexChildID, false)
			fixture.assertTokens(170)

			fixture.replace(childPath, codexRolloutFixture(
				t,
				tt.replacementID,
				tt.replacementParent,
				900,
				100,
				"",
			), false)
			result, err := fixture.ingestor.Ingest(fixture.ctx, child.ID)
			mustNoError(t, err, "detect child replacement")
			if result.ReplacementSourceID != 0 || !result.Reconcile {
				t.Fatalf("unvalidated replacement result = %+v", result)
			}
			mustNoError(t, fixture.collector.ReconcilePath(fixture.ctx, childPath), "reconcile invalid replacement")
			sources, err := fixture.store.ListUsageSourcesForBinding(fixture.ctx, fixture.binding.ID)
			mustNoError(t, err)
			childGenerations := 0
			for _, source := range sources {
				if source.NativeSessionID == testCodexChildID {
					childGenerations++
				}
			}
			if childGenerations != 1 {
				t.Fatalf("invalid child replacement registered %d generations: %+v", childGenerations, sources)
			}
			fixture.assertTokens(170)
		})
	}
}

func TestCodexChildReplacementDoesNotChangeDurableDirectParent(t *testing.T) {
	fixture := newCodexSubagentFixture(t, domain.ActivityIdle)
	rootPath := activeCodexRolloutPath(fixture.roots.CodexSessions, testCodexParentID)
	rootContent := strings.TrimSuffix(
		codexRolloutFixture(t, testCodexParentID, "", 100, 20, testCodexChildID),
		"\n",
	)
	alternateCallID := "call_spawn_alternate_parent"
	rootContent += "\n" + string(codexResponseItem(t, map[string]any{
		"type":    "function_call",
		"name":    "spawn_agent",
		"call_id": alternateCallID,
	})) + "\n" + string(codexResponseItem(t, map[string]any{
		"type":    "function_call_output",
		"call_id": alternateCallID,
		"output":  `{"agent_id":"` + testCodexGrandchildID + `"}`,
	})) + "\n"
	writeCodexRollout(t, rootPath, rootContent)
	childPath := fixture.write(testCodexChildID, testCodexParentID, 40, 10, "")
	fixture.write(testCodexGrandchildID, testCodexParentID, 5, 2, testCodexChildID)
	fixture.backfill()
	fixture.ingestOK(testCodexParentID)
	fixture.reconcile()
	child, _ := fixture.ingestOK(testCodexChildID)
	fixture.ingestOK(testCodexGrandchildID)
	fixture.assertTokens(177)

	fixture.replace(childPath, codexRolloutFixture(t, testCodexChildID, testCodexGrandchildID, 90, 20, ""), false)
	result, err := fixture.ingestor.Ingest(fixture.ctx, child.ID)
	mustNoError(t, err, "detect child rewrite")
	if !result.Reconcile || result.ReconcilePath != canonicalTranscriptPath(childPath) {
		t.Fatalf("rewrite result = %+v", result)
	}
	mustNoError(t, fixture.collector.ReconcilePath(fixture.ctx, childPath), "reconcile changed parent")
	sources, err := fixture.store.ListUsageSourcesForBinding(fixture.ctx, fixture.binding.ID)
	mustNoError(t, err)
	childGenerations := 0
	for _, source := range sources {
		if source.NativeSessionID == testCodexChildID {
			childGenerations++
			if source.ID != child.ID || source.State != domain.UsageSourceComplete ||
				source.LastErrorCode != domain.UsageErrorArtifactReplaced {
				t.Fatalf("changed-parent child source = %+v", source)
			}
		}
	}
	if childGenerations != 1 {
		t.Fatalf("child generations = %d, want one rejected generation", childGenerations)
	}
	fixture.assertTokens(177)
}

func TestCodexChildCheckpointMismatchReconcilesValidatedGeneration(t *testing.T) {
	fixture := newCodexSubagentFixture(t, domain.ActivityIdle)
	fixture.write(testCodexParentID, "", 100, 20, testCodexChildID)
	childPath := fixture.write(testCodexChildID, testCodexParentID, 40, 10, "")
	fixture.backfill()
	fixture.ingest(testCodexParentID, true)
	fixture.reconcile()
	child := fixture.ingest(testCodexChildID, false)

	fixture.replace(childPath, codexRolloutFixture(t, testCodexChildID, testCodexParentID, 90, 20, ""), false)
	result, err := fixture.ingestor.Ingest(fixture.ctx, child.ID)
	mustNoError(t, err, "detect child rewrite")
	if result.ReplacementSourceID != 0 || !result.Reconcile || result.ReconcilePath != canonicalTranscriptPath(childPath) {
		t.Fatalf("rewrite result = %+v, want child reconciliation", result)
	}
	mustNoError(t, fixture.collector.ReconcilePath(fixture.ctx, childPath), "reconcile validated child")
	replacement := fixture.source(testCodexChildID)
	if replacement.ID == child.ID || replacement.Generation != child.Generation+1 || replacement.ByteOffset != 0 {
		t.Fatalf("validated replacement = %+v, previous=%+v", replacement, child)
	}
	if _, err := fixture.ingestor.Ingest(fixture.ctx, replacement.ID); err != nil {
		t.Fatalf("ingest validated replacement: %v", err)
	}
	fixture.assertTokens(280)
}

func TestCodexRootReplacementRejectsMismatchedSessionMeta(t *testing.T) {
	tests := []struct {
		name          string
		replacementID string
		parentID      string
		atomic        bool
	}{
		{
			name:          "mismatched root id",
			replacementID: testCodexChildID,
			atomic:        true,
		},
		{
			name:          "root rewritten as child",
			replacementID: testCodexParentID,
			parentID:      testCodexChildID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newCodexSubagentFixture(t, domain.ActivityIdle)
			rootPath := fixture.write(testCodexParentID, "", 100, 20, "")
			fixture.backfill()
			root := fixture.ingest(testCodexParentID, false)
			replacement := codexRolloutFixture(t, test.replacementID, test.parentID, 900, 100, "")
			fixture.replace(rootPath, replacement, test.atomic)

			result, err := fixture.ingestor.Ingest(fixture.ctx, root.ID)
			mustNoError(t, err, "detect root replacement")
			if result.ReplacementSourceID != 0 || !result.Reconcile || result.ReconcilePath != canonicalTranscriptPath(rootPath) {
				t.Fatalf("replacement result = %+v, want root reconciliation", result)
			}
			mustNoError(t, fixture.collector.ReconcilePath(fixture.ctx, rootPath), "reconcile invalid root")
			sources, err := fixture.store.ListUsageSourcesForBinding(fixture.ctx, fixture.binding.ID)
			mustNoError(t, err)
			if len(sources) != 1 || sources[0].State != domain.UsageSourceComplete ||
				sources[0].LastErrorCode != domain.UsageErrorArtifactReplaced {
				t.Fatalf("invalid replacement sources = %+v", sources)
			}
			fixture.assertTokens(120)
		})
	}
}

type codexSubagentFixture struct {
	t         *testing.T
	ctx       context.Context
	store     *sqlite.Store
	session   domain.SessionRecord
	roots     usagesvc.SourceRoots
	collector *usagesvc.Collector
	ingestor  *Ingestor
	binding   domain.UsageBindingRecord
}

func newCodexSubagentFixture(t *testing.T, activity domain.ActivityState) *codexSubagentFixture {
	t.Helper()
	store, session, roots := seedCodexRolloutSession(t, activity)
	return &codexSubagentFixture{
		t:         t,
		ctx:       context.Background(),
		store:     store,
		session:   session,
		roots:     roots,
		collector: usagesvc.NewCollector(store, roots, nil),
		ingestor:  NewIngestor(store, IngestorConfig{}),
	}
}

func (fixture *codexSubagentFixture) write(
	id, parentID string,
	input, output int64,
	spawnedChildID string,
) string {
	fixture.t.Helper()
	path := activeCodexRolloutPath(fixture.roots.CodexSessions, id)
	writeCodexRollout(fixture.t, path, codexRolloutFixture(
		fixture.t, id, parentID, input, output, spawnedChildID,
	))
	return path
}

func (fixture *codexSubagentFixture) backfill() {
	fixture.t.Helper()
	if err := fixture.collector.BackfillActive(fixture.ctx); err != nil {
		fixture.t.Fatalf("backfill Codex root: %v", err)
	}
	fixture.binding = onlyUsageBinding(fixture.t, fixture.store, fixture.session.ID)
}

func (fixture *codexSubagentFixture) source(nativeID string) domain.UsageSourceRecord {
	fixture.t.Helper()
	return latestUsageSource(fixture.t, fixture.store, fixture.binding.ID, nativeID)
}

func (fixture *codexSubagentFixture) ingest(nativeID string, wantReconcile bool) domain.UsageSourceRecord {
	fixture.t.Helper()
	source, result := fixture.ingestOK(nativeID)
	if result.Reconcile != wantReconcile {
		fixture.t.Fatalf("ingest %s = %+v; reconcile want %t", nativeID, result, wantReconcile)
	}
	return source
}

func (fixture *codexSubagentFixture) ingestOK(nativeID string) (domain.UsageSourceRecord, IngestResult) {
	fixture.t.Helper()
	source := fixture.source(nativeID)
	result, err := fixture.ingestor.Ingest(fixture.ctx, source.ID)
	if err != nil {
		fixture.t.Fatalf("ingest %s: %v", nativeID, err)
	}
	return source, result
}

func (fixture *codexSubagentFixture) reconcile() {
	fixture.t.Helper()
	if err := fixture.collector.ReconcileSources(fixture.ctx, -1); err != nil {
		fixture.t.Fatalf("reconcile Codex sources: %v", err)
	}
}

func (fixture *codexSubagentFixture) assertTokens(want int64) {
	fixture.t.Helper()
	assertTokenAggregate(fixture.t, fixture.store, fixture.session.ID, want)
}

func (fixture *codexSubagentFixture) replace(path, content string, atomic bool) {
	fixture.t.Helper()
	if atomic {
		temporary := path + ".replacement"
		writeCodexRollout(fixture.t, temporary, content)
		if err := os.Rename(temporary, path); err != nil {
			fixture.t.Fatal(err)
		}
		return
	}
	before, err := usagesvc.SourceIdentity(fixture.ctx, path)
	if err != nil {
		fixture.t.Fatal(err)
	}
	writeCodexRollout(fixture.t, path, content)
	after, err := usagesvc.SourceIdentity(fixture.ctx, path)
	if err != nil {
		fixture.t.Fatal(err)
	}
	if after != before {
		fixture.t.Fatalf("same-inode fixture changed identity: %q != %q", before, after)
	}
}

func codexResponseItem(t *testing.T, payload map[string]any) []byte {
	t.Helper()
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-07-28T10:00:00Z",
		"type":      "response_item",
		"payload":   payload,
	})
	mustNoError(t, err)
	return line
}

func codexSessionMetaLine(t *testing.T, id, parentID string) []byte {
	t.Helper()
	payload := map[string]any{"id": id, "model_provider": "openai"}
	if parentID != "" {
		payload["source"] = map[string]any{
			"subagent": map[string]any{
				"thread_spawn": map[string]any{"parent_thread_id": parentID},
			},
		}
	}
	line, err := json.Marshal(map[string]any{
		"timestamp": "2026-07-28T10:00:00Z",
		"type":      "session_meta",
		"payload":   payload,
	})
	mustNoError(t, err)
	return line
}

func seedCodexRolloutSession(
	t *testing.T,
	activity domain.ActivityState,
) (*sqlite.Store, domain.SessionRecord, usagesvc.SourceRoots) {
	t.Helper()
	now := time.Now().UTC()
	store, session := seedUsageTestSession(
		t, t.TempDir(), "codex-subagents", domain.HarnessCodex, activity, testCodexParentID, now,
	)
	base := t.TempDir()
	return store, session, usagesvc.SourceRoots{
		CodexSessions: filepath.Join(base, "sessions"),
		CodexArchived: filepath.Join(base, "archived_sessions"),
	}
}

func codexRolloutFixture(t *testing.T, id, parentID string, input, output int64, spawnedChildID string) string {
	t.Helper()
	records := [][]byte{
		codexSessionMetaLine(t, id, parentID),
		[]byte(`{"type":"turn_context","payload":{"model":"gpt-5.6"}}`),
		codexTokenLine("2026-07-28T10:00:00Z", input, 0, 0, output, 0),
	}
	if spawnedChildID != "" {
		callID := "call_spawn_" + strings.ReplaceAll(spawnedChildID, "-", "_")
		records = append(records,
			codexResponseItem(t, map[string]any{
				"type":      "function_call",
				"name":      "spawn_agent",
				"call_id":   callID,
				"arguments": `{"message":"private child task"}`,
			}),
			codexResponseItem(t, map[string]any{
				"type":    "function_call_output",
				"call_id": callID,
				"output":  `{"agent_id":"` + spawnedChildID + `","nickname":"private"}`,
			}),
		)
	}
	return string(bytes.Join(records, []byte{'\n'})) + "\n"
}

func activeCodexRolloutPath(root, id string) string {
	return filepath.Join(root, "2026", "07", "28", "rollout-"+id+".jsonl")
}

func writeCodexRollout(t *testing.T, path, content string) {
	t.Helper()
	mustNoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	mustNoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func onlyUsageBinding(t *testing.T, store *sqlite.Store, sessionID domain.SessionID) domain.UsageBindingRecord {
	t.Helper()
	bindings, err := store.ListUsageBindingsForSession(context.Background(), sessionID)
	if err != nil || len(bindings) != 1 {
		t.Fatalf("usage bindings = %+v, err=%v", bindings, err)
	}
	return bindings[0]
}

func latestUsageSource(t *testing.T, store *sqlite.Store, bindingID int64, nativeID string) domain.UsageSourceRecord {
	t.Helper()
	sources, err := store.ListUsageSourcesForBinding(context.Background(), bindingID)
	mustNoError(t, err)
	var latest domain.UsageSourceRecord
	for _, source := range sources {
		if source.NativeSessionID != nativeID {
			continue
		}
		if latest.ID == 0 || source.Generation > latest.Generation ||
			(source.Generation == latest.Generation && source.ID > latest.ID) {
			latest = source
		}
	}
	if latest.ID == 0 {
		t.Fatalf("usage source for native session %q not found: %+v", nativeID, sources)
	}
	return latest
}

func waitForUsageBindingState(
	t *testing.T,
	store *sqlite.Store,
	sessionID domain.SessionID,
	want domain.UsageBindingState,
) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		binding := onlyUsageBinding(t, store, sessionID)
		if binding.State == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	binding := onlyUsageBinding(t, store, sessionID)
	t.Fatalf("binding state = %s, want %s: %+v", binding.State, want, binding)
}
