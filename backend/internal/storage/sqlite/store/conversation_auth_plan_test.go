package store_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/gen"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// planRecorder explains every single-row query before running it. A test can
// then assert on the plan of the statement sqlc compiled in, instead of on a
// hand-copied duplicate of its text that keeps passing after the real query
// drifts.
type planRecorder struct {
	gen.DBTX
	t     *testing.T
	plans []string
}

func (r *planRecorder) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	r.t.Helper()
	r.plans = append(r.plans, r.explain(ctx, query, args)...)
	return r.DBTX.QueryRowContext(ctx, query, args...)
}

func (r *planRecorder) explain(ctx context.Context, query string, args []any) []string {
	r.t.Helper()
	rows, err := r.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		r.t.Fatalf("explain query plan: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			r.t.Fatalf("scan query plan row: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		r.t.Fatalf("read query plan: %v", err)
	}
	return details
}

// Break caught: SelectVerifiedAuthenticationTurn runs inside the projection
// transaction of every completed turn, and its EXISTS found the turn.completed
// event by walking every archive row of the branch through
// idx_conversation_provider_events_branch and reading each row to test method.
// On a long conversation that scan held the single writer for seconds.
func TestVerifiedAuthenticationTurnLookupSearchesOnlyTurnCompletedArchiveRows(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	s := sqlitetest.MustOpenAt(t, dataDir)
	seedProject(t, s, "plan")
	record := sampleRecord("plan")
	record.Mode = domain.SessionModeChat
	session, err := s.CreateSession(ctx, record)
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := s.CreateConversation(ctx, "plan-conv", domain.ConversationScopeSession, "plan", session.ID, histClock)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimChatControllerGeneration(ctx, session.ID, "gen-1"); err != nil {
		t.Fatal(err)
	}
	startAt := histClock.Add(time.Second)
	if err := s.AdoptProviderTurn(ctx, conversation.ID, session.ID, "gen-1", "turn", "provider-turn", startAt); err != nil {
		t.Fatal(err)
	}
	// A streamed delta sits in the archive next to the completion, as it does in
	// every real turn; the lookup has to find the completion among them.
	if _, err := s.ProjectProviderEvent(ctx, conversation.ID, session.ID, "gen-1", "delta", "reasoning.delta", `{"providerTurnId":"provider-turn"}`, startAt, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	completedAt := startAt.Add(time.Second)
	if _, err := s.ProjectProviderEvent(ctx, conversation.ID, session.ID, "gen-1", "completion", "turn.completed", `{"providerTurnId":"provider-turn","turnState":"completed"}`, completedAt, func(txCtx context.Context) error {
		return s.SettleTurn(txCtx, conversation.ID, "provider-turn", domain.TurnStateCompleted, "", completedAt)
	}); err != nil {
		t.Fatal(err)
	}

	raw, err := sql.Open("sqlite", "file:"+filepath.Join(dataDir, "ao.db"))
	if err != nil {
		t.Fatalf("open raw sqlite: %v", err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	recorder := &planRecorder{DBTX: raw, t: t}
	turn, err := gen.New(recorder).SelectVerifiedAuthenticationTurn(ctx, gen.SelectVerifiedAuthenticationTurnParams{
		ConversationID: conversation.ID,
		Generation:     "gen-1",
		ProviderTurnID: "provider-turn",
		AfterAt:        sql.NullTime{Time: histClock, Valid: true},
	})
	if err != nil {
		t.Fatalf("select verified authentication turn: %v", err)
	}
	if turn.ProviderTurnID != "provider-turn" {
		t.Fatalf("verified turn = %+v, want provider-turn", turn)
	}

	plan := strings.Join(recorder.plans, "\n")
	if !strings.Contains(plan, "USING INDEX idx_conversation_provider_events_turn_completed (branch_id=?)") {
		t.Errorf("archive lookup does not search the turn.completed index:\n%s", plan)
	}
	if strings.Contains(plan, "idx_conversation_provider_events_branch") || strings.Contains(plan, "SCAN e") {
		t.Errorf("archive lookup walks the branch's whole provider-event archive:\n%s", plan)
	}
}
