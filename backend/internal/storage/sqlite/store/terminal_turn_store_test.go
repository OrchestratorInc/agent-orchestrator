package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func newTerminalTurnSession(t *testing.T, project string) (*sqlite.Store, domain.SessionRecord) {
	t.Helper()
	s := newTestStore(t)
	seedProject(t, s, project)
	session, err := s.CreateSession(context.Background(), sampleRecord(project))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	return s, session
}

func TestTerminalTurnsPairRequestsWithResponses(t *testing.T) {
	s, session := newTerminalTurnSession(t, "turns")
	ctx := context.Background()
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	request := func(harness domain.AgentHarness, thread, text string) {
		t.Helper()
		at = at.Add(time.Minute)
		if err := s.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
			SessionID: session.ID, Harness: harness, NativeSessionID: thread, UserRequest: text, RequestedAt: at,
		}); err != nil {
			t.Fatalf("record request %q: %v", text, err)
		}
	}
	respond := func(text string) {
		t.Helper()
		at = at.Add(time.Minute)
		if err := s.RecordTerminalTurnResponse(ctx, session.ID, text, at); err != nil {
			t.Fatalf("record response %q: %v", text, err)
		}
	}

	respond("a stop with no open turn is ignored")
	request(domain.HarnessClaudeCode, "claude-1", "first ask")
	request(domain.HarnessClaudeCode, "claude-1", "queued ask")
	respond("first answer")
	respond("a second stop does not overwrite a closed turn")
	request(domain.HarnessCodex, "codex-1", "codex ask")

	turns, err := s.ListTerminalTurns(ctx, session.ID)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns) != 2 {
		t.Fatalf("turns = %d, want 2: %+v", len(turns), turns)
	}
	if turns[0].UserRequest != "first ask\n\nqueued ask" || turns[0].AgentResponse != "first answer" {
		t.Fatalf("first turn = %+v, want both requests and the first answer", turns[0])
	}
	if turns[1].UserRequest != "codex ask" || turns[1].AgentResponse != "" || turns[1].Harness != domain.HarnessCodex {
		t.Fatalf("second turn = %+v, want an open Codex turn", turns[1])
	}

	// Importing Claude's thread into Chat removes only that thread's turns.
	if err := s.DeleteTerminalTurnsForThread(ctx, session.ID, domain.HarnessClaudeCode, "claude-1"); err != nil {
		t.Fatalf("delete imported turns: %v", err)
	}
	turns, err = s.ListTerminalTurns(ctx, session.ID)
	if err != nil || len(turns) != 1 || turns[0].UserRequest != "codex ask" {
		t.Fatalf("after import turns = %+v, %v; want only the Codex turn", turns, err)
	}
}

func TestTerminalTurnsKeepOnlyTheNewest(t *testing.T) {
	s, session := newTerminalTurnSession(t, "trim")
	ctx := context.Background()
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	for index := 1; index <= domain.TerminalTurnRetention+5; index++ {
		at = at.Add(time.Minute)
		if err := s.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
			SessionID: session.ID, Harness: domain.HarnessClaudeCode, UserRequest: fmt.Sprintf("ask-%d", index), RequestedAt: at,
		}); err != nil {
			t.Fatalf("record request %d: %v", index, err)
		}
		if err := s.RecordTerminalTurnResponse(ctx, session.ID, "done", at); err != nil {
			t.Fatalf("record response %d: %v", index, err)
		}
	}
	turns, err := s.ListTerminalTurns(ctx, session.ID)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	if len(turns) != domain.TerminalTurnRetention || turns[0].UserRequest != "ask-6" {
		t.Fatalf("kept %d turns starting at %q, want %d starting at ask-6", len(turns), turns[0].UserRequest, domain.TerminalTurnRetention)
	}
}

func TestTerminalTurnsJoinOnlyOnTheSameThread(t *testing.T) {
	s, session := newTerminalTurnSession(t, "threads")
	ctx := context.Background()
	other := sampleRecord("threads")
	other.ID = "threads-2"
	if _, err := s.CreateSession(ctx, other); err != nil {
		t.Fatalf("create second session: %v", err)
	}
	at := time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
	request := func(id domain.SessionID, thread, text string) {
		t.Helper()
		at = at.Add(time.Minute)
		if err := s.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
			SessionID: id, Harness: domain.HarnessClaudeCode, NativeSessionID: thread, UserRequest: text, RequestedAt: at,
		}); err != nil {
			t.Fatalf("record request %q: %v", text, err)
		}
	}
	request(session.ID, "thread-1", "first ask")
	request(session.ID, "thread-1", "queued on the same thread")
	request(session.ID, "thread-2", "another thread starts its own turn")
	request(session.ID, "", "a request with no id joins the open turn")
	request(other.ID, "thread-2", "another session never joins")
	request(session.ID, "thread-2", "   ")
	if err := s.RecordTerminalTurnResponse(ctx, other.ID, "  other answer  ", at); err != nil {
		t.Fatalf("record response: %v", err)
	}

	turns, err := s.ListTerminalTurns(ctx, session.ID)
	if err != nil {
		t.Fatalf("list turns: %v", err)
	}
	want := []string{
		"first ask\n\nqueued on the same thread",
		"another thread starts its own turn\n\na request with no id joins the open turn",
	}
	if len(turns) != len(want) || turns[0].UserRequest != want[0] || turns[1].UserRequest != want[1] ||
		turns[0].AgentResponse != "" || turns[1].AgentResponse != "" {
		t.Fatalf("turns = %+v, want %q with no responses", turns, want)
	}
	if turns, err := s.ListTerminalTurns(ctx, other.ID); err != nil || len(turns) != 1 || turns[0].AgentResponse != "other answer" {
		t.Fatalf("other session turns = %+v, %v; want its one answered turn", turns, err)
	}
}

func TestTerminalTurnStopsGrowingPastItsCap(t *testing.T) {
	s, session := newTerminalTurnSession(t, "capped")
	ctx := context.Background()
	// An agent that never reports its stop leaves the turn open.
	for i := range 3 {
		if err := s.RecordTerminalTurnRequest(ctx, domain.TerminalTurn{
			SessionID: session.ID, Harness: domain.HarnessClaudeCode,
			UserRequest: fmt.Sprintf("%d %s", i, strings.Repeat("x", 20_000)), RequestedAt: time.Now(),
		}); err != nil {
			t.Fatalf("record request %d: %v", i, err)
		}
	}
	turns, err := s.ListTerminalTurns(ctx, session.ID)
	if err != nil || len(turns) != 2 {
		t.Fatalf("turns = %d, %v; want the first two joined and the third on its own", len(turns), err)
	}
}
