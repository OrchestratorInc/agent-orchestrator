package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Four statements keep a turn only when the active lineage holds one of its
// messages or activities at or before the fork point. That EXISTS is
// correlated on turn_id, which no index covers. Without a conversation_id
// bound it scans the whole message and activity tables, and once
// sqlite_stat1 exists the planner skip-scans (branch_id, sequence) across
// every branch in the database instead. Bound by conversation_id, it
// range-seeks the (conversation_id, sequence) index.
const (
	lineageMessageBound  = "lineage_message.conversation_id = conversation_turns.conversation_id"
	lineageActivityBound = "lineage_activity.conversation_id = conversation_turns.conversation_id"
)

// lineageProbe is a gen.DBTX that runs on its own read-only connection to the
// store's file and remembers the last statement the generated code issued, so
// the test can explain and re-run exactly that text.
type lineageProbe struct {
	db    *sql.DB
	query string
	args  []any
}

func (p *lineageProbe) ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error) {
	return p.db.ExecContext(ctx, q, args...)
}

func (p *lineageProbe) PrepareContext(ctx context.Context, q string) (*sql.Stmt, error) {
	return p.db.PrepareContext(ctx, q)
}

func (p *lineageProbe) QueryContext(ctx context.Context, q string, args ...any) (*sql.Rows, error) {
	p.query, p.args = q, args
	return p.db.QueryContext(ctx, q, args...)
}

func (p *lineageProbe) QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row {
	p.query, p.args = q, args
	return p.db.QueryRowContext(ctx, q, args...)
}

func (p *lineageProbe) plan(t *testing.T) string {
	t.Helper()
	rows, err := p.db.Query("EXPLAIN QUERY PLAN "+p.query, p.args...)
	if err != nil {
		t.Fatalf("explain: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var lines []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan plan: %v", err)
		}
		lines = append(lines, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("plan rows: %v", err)
	}
	return strings.Join(lines, "\n")
}

func (p *lineageProbe) rows(t *testing.T, query string) [][]string {
	t.Helper()
	rows, err := p.db.Query(query, p.args...)
	if err != nil {
		t.Fatalf("run statement: %v", err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]string
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		row := make([]string, len(cols))
		for i, v := range values {
			row[i] = fmt.Sprint(v)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// seedForkedConversation builds root -> child -> nested branches with enough
// prompts per branch that ANALYZE makes the branch index skip-scan eligible,
// and returns the ids of the turns the active (nested) lineage keeps.
func seedForkedConversation(t *testing.T, s *sqlite.Store) (conversationID string, visibleTurns []string) {
	t.Helper()
	ctx := context.Background()
	seedProject(t, s, "lineage")
	rec := sampleRecord("lineage")
	rec.Mode = domain.SessionModeChat
	rec.Metadata.ProviderConversationID = "thread-root"
	rec.Metadata.ControllerGeneration = "generation-root"
	session, err := s.CreateSession(ctx, rec)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	conversation, err := s.CreateConversation(ctx, "conversation-lineage", domain.ConversationScopeSession, "lineage", session.ID, testNow)
	if err != nil {
		t.Fatalf("CreateConversation: %v", err)
	}

	prompt := func(generation, suffix string) {
		appendBranchPrompt(t, s, session, conversation, generation, suffix, "prompt "+suffix)
		turnID, providerTurnID := "turn-"+suffix, "provider-"+suffix
		if err := s.BindTurnToProvider(ctx, turnID, providerTurnID, testNow); err != nil {
			t.Fatalf("BindTurnToProvider %s: %v", suffix, err)
		}
		if err := s.UpsertActivity(ctx, conversation.ID, providerTurnID, domain.ConversationActivity{
			ID: "activity-" + suffix, Kind: domain.ActivityKindCommand, Status: domain.ActivityStatusCompleted,
			Summary: "run " + suffix, ProviderItemID: "item-" + suffix,
		}, testNow); err != nil {
			t.Fatalf("UpsertActivity %s: %v", suffix, err)
		}
	}
	sequenceOf := func(suffix string) int64 {
		snapshot, err := s.LoadConversationSnapshot(ctx, conversation.ID)
		if err != nil {
			t.Fatalf("LoadConversationSnapshot: %v", err)
		}
		for _, m := range snapshot.Messages {
			if m.ID == "message-"+suffix {
				return m.Sequence
			}
		}
		t.Fatalf("message %s not found", suffix)
		return 0
	}
	fork := func(id, parent, afterSuffix, replacedSuffix, generation string) {
		branch := domain.ConversationBranch{
			ID: id, ConversationID: conversation.ID, SessionID: session.ID,
			ProviderConversationID: "thread-" + id, ParentBranchID: parent,
			ForkAfterTurnID: "turn-" + afterSuffix, ReplacedTurnID: "turn-" + replacedSuffix,
			ForkAfterSequence: sequenceOf(afterSuffix),
		}
		if err := s.CreateConversationBranch(ctx, branch, testNow.Add(time.Minute)); err != nil {
			t.Fatalf("CreateConversationBranch %s: %v", id, err)
		}
		activateTestBranch(t, s, session, conversation, id, branch.ProviderConversationID, generation)
	}

	rootID := conversation.ActiveBranchID
	for i := 0; i < 25; i++ {
		prompt("generation-root", fmt.Sprintf("r%02d", i))
	}
	fork("branch-child", rootID, "r09", "r10", "generation-child")
	for i := 0; i < 25; i++ {
		prompt("generation-child", fmt.Sprintf("c%02d", i))
	}
	fork("branch-nested", "branch-child", "c04", "c05", "generation-nested")
	for i := 0; i < 5; i++ {
		prompt("generation-nested", fmt.Sprintf("n%02d", i))
	}

	for i := 0; i <= 9; i++ {
		visibleTurns = append(visibleTurns, fmt.Sprintf("turn-r%02d", i))
	}
	for i := 0; i <= 4; i++ {
		visibleTurns = append(visibleTurns, fmt.Sprintf("turn-c%02d", i))
	}
	for i := 0; i <= 4; i++ {
		visibleTurns = append(visibleTurns, fmt.Sprintf("turn-n%02d", i))
	}
	return conversation.ID, visibleTurns
}

func TestLineageTurnQueriesAreBoundByConversation(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	conversationID, visible := seedForkedConversation(t, s)
	path := "file:" + filepath.ToSlash(filepath.Join(dir, "ao.db"))

	openProbe := func() *lineageProbe {
		db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(5000)")
		if err != nil {
			t.Fatalf("open probe connection: %v", err)
		}
		t.Cleanup(func() { _ = db.Close() })
		return &lineageProbe{db: db}
	}

	// The same four statements, before and after the database has statistics.
	for _, stage := range []string{"no statistics", "after ANALYZE"} {
		if stage == "after ANALYZE" {
			writer, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
			if err != nil {
				t.Fatalf("open analyze connection: %v", err)
			}
			if _, err := writer.Exec("ANALYZE"); err != nil {
				t.Fatalf("ANALYZE: %v", err)
			}
			_ = writer.Close()
		}
		probe := openProbe()
		q := gen.New(probe)
		statements := map[string]func() error{
			"SelectConversationTurns": func() error {
				_, err := q.SelectConversationTurns(ctx, conversationID)
				return err
			},
			"SelectConversationTurnsPage": func() error {
				_, err := q.SelectConversationTurnsPage(ctx, gen.SelectConversationTurnsPageParams{
					ConversationID: conversationID, OldestSequence: 0, BeforeSequence: 1 << 40,
				})
				return err
			},
			"SelectLatestConversationTurn": func() error {
				_, err := q.SelectLatestConversationTurn(ctx, conversationID)
				return err
			},
			"ListVisibleRunningTurnsForConversation": func() error {
				_, err := q.ListVisibleRunningTurnsForConversation(ctx, conversationID)
				return err
			},
		}
		for name, run := range statements {
			t.Run(stage+"/"+name, func(t *testing.T) {
				if err := run(); err != nil {
					t.Fatalf("%s: %v", name, err)
				}

				// Plan: the lineage lookups seek the conversation's own range.
				plan := probe.plan(t)
				for _, table := range []string{"lineage_message", "lineage_activity"} {
					found := false
					for _, line := range strings.Split(plan, "\n") {
						if !strings.Contains(line, table) {
							continue
						}
						found = true
						if !strings.Contains(line, "conversation_id=?") || strings.Contains(line, "ANY(") || strings.HasPrefix(strings.TrimSpace(line), "SCAN") {
							t.Errorf("%s does not seek by conversation_id: %q\nfull plan:\n%s", table, line, plan)
						}
					}
					if !found {
						t.Errorf("plan has no %s lookup:\n%s", table, plan)
					}
				}

				// Results: dropping the bound gives the statement as it was, and
				// both return the same rows on a forked conversation.
				if strings.Count(probe.query, lineageMessageBound) != 1 || strings.Count(probe.query, lineageActivityBound) != 1 {
					t.Fatalf("%s does not carry the conversation_id bound on both lineage lookups", name)
				}
				unbound := strings.ReplaceAll(strings.ReplaceAll(probe.query, "AND "+lineageMessageBound, ""), "AND "+lineageActivityBound, "")
				if unbound == probe.query {
					t.Fatalf("could not rebuild the unbound statement for %s", name)
				}
				got, want := probe.rows(t, probe.query), probe.rows(t, unbound)
				if len(got) == 0 && name != "ListVisibleRunningTurnsForConversation" {
					t.Fatalf("%s returned no rows on a conversation with turns", name)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s rows differ with the conversation_id bound:\n got  %v\n want %v", name, got, want)
				}
			})
		}
	}

	// And the visible lineage itself is the nested branch's: root up to the
	// first fork, child up to the second, then the nested turns.
	snapshot, err := s.LoadConversationSnapshot(ctx, conversationID)
	if err != nil {
		t.Fatalf("LoadConversationSnapshot: %v", err)
	}
	got := make([]string, 0, len(snapshot.Turns))
	for _, turn := range snapshot.Turns {
		got = append(got, turn.ID)
	}
	if !reflect.DeepEqual(got, visible) {
		t.Fatalf("visible turns = %v, want %v", got, visible)
	}
}

// The repair pass lists the sources that still own an open (unattributed or
// merely inferred) event. The partial index (usage_source_id, id) holds exactly
// those events. Written as a join against usage_sources, the planner left that
// index for a scan of the sources plus the full usage_source index once
// sqlite_stat1 existed, reading every event of every source (+23 ms on a
// 32k-event database). This data set is too small to make the planner flip, so
// the test passes on the join form too: it pins the result (the 20 of 60 seeded
// sources that own an open event) and the plan shape, not the flip.
func TestLegacyUsageSourceListingReadsOnlyTheOpenAttributionIndex(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dir)
	now := time.Date(2026, 8, 23, 12, 0, 0, 0, time.UTC)
	session := seedUsageSession(t, s, domain.HarnessClaudeCode)
	binding := mustUpsertUsageBinding(t, s, session, now, domain.UsageBindingRecord{
		NativeRootID: "root", InitialModelID: "claude-x", State: domain.UsageBindingActive,
	})

	const sources, eventsPerSource, openSources, openPerSource = 60, 60, 20, 20
	var open []int64
	for i := 0; i < sources; i++ {
		source := mustInsertUsageSource(t, s, now, domain.UsageSourceRecord{
			BindingID: binding.ID, Kind: domain.UsageSourceClaudeMain,
			NativeSessionID: fmt.Sprintf("child-%d", i), ArtifactPath: fmt.Sprintf("/tmp/claude/%d.jsonl", i),
			FileIdentity: fmt.Sprintf("dev:%d", i), State: domain.UsageSourcePending,
		})
		events := make([]domain.ModelUsageEvent, 0, eventsPerSource)
		for j := 0; j < eventsPerSource; j++ {
			event := anthropicUsageEvent(fmt.Sprintf("s%d-e%d", i, j), 5, 10, 5, 4)
			if i >= openSources || j >= openPerSource {
				event.BillingProviderID = "anthropic"
				event.BillingProviderSource = domain.UsageBillingProviderObserved
			}
			events = append(events, event)
		}
		if i < openSources {
			open = append(open, source.ID)
		}
		mustNoError(t, s.ApplyUsageChunk(ctx, source.ID, 0, source.UpdatedAt, domain.SourceCursorState{
			ByteOffset: 10, State: domain.UsageSourceActive, UpdatedAt: now,
		}, events), "seed events")
	}
	path := "file:" + filepath.ToSlash(filepath.Join(dir, "ao.db"))

	for _, stage := range []string{"no statistics", "after ANALYZE"} {
		if stage == "after ANALYZE" {
			writer, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)")
			mustNoError(t, err)
			_, err = writer.Exec("ANALYZE")
			mustNoError(t, err, "ANALYZE")
			_ = writer.Close()
		}
		db, err := sql.Open("sqlite", path+"?mode=ro&_pragma=busy_timeout(5000)")
		mustNoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		probe := &lineageProbe{db: db}
		ids, err := gen.New(probe).ListLegacyUsageSourceIDs(ctx)
		mustNoError(t, err, "ListLegacyUsageSourceIDs")
		if !reflect.DeepEqual(ids, open) {
			t.Fatalf("%s: legacy sources = %v, want %v", stage, ids, open)
		}
		plan := probe.plan(t)
		if !strings.Contains(plan, "idx_model_usage_events_open_attribution") || strings.Contains(plan, "idx_model_usage_events_usage_source") {
			t.Errorf("%s: the listing does not use the open-attribution index alone:\n%s", stage, plan)
		}
	}
}
