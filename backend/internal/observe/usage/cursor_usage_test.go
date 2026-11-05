package usage

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakeCursorStore struct {
	sessions []domain.SessionRecord
	account  string
	until    time.Time
	haveRead bool
	reads    []domain.CursorUsageRead
}

func (s *fakeCursorStore) CursorUsageEnabled(context.Context) bool               { return true }
func (s *fakeCursorStore) SetCursorUsage(context.Context, bool, time.Time) error { return nil }
func (s *fakeCursorStore) ListAllSessions(context.Context) ([]domain.SessionRecord, error) {
	return s.sessions, nil
}

func (s *fakeCursorStore) GetUsageCursorRead(context.Context) (string, time.Time, bool, error) {
	return s.account, s.until, s.haveRead, nil
}

func (s *fakeCursorStore) ReplaceCursorUsage(_ context.Context, read domain.CursorUsageRead) error {
	s.reads = append(s.reads, read)
	s.account, s.until, s.haveRead = read.AccountKey, read.Until, true
	return nil
}

func TestCursorUsageKeepsAOSessionsAndResumesBeforeTheLastRead(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 500_000_000, time.UTC)
	created := now.Add(-48 * time.Hour)
	store := &fakeCursorStore{sessions: []domain.SessionRecord{
		{ID: "tui", Harness: domain.HarnessCursor, CreatedAt: created, Metadata: domain.SessionMetadata{AgentSessionID: "conv-a"}},
		{ID: "chat", Harness: domain.HarnessCursor, Mode: domain.SessionModeChat, CreatedAt: created.Add(time.Hour),
			Metadata: domain.SessionMetadata{ProviderConversationID: "conv-b"}},
		{ID: "claude", Harness: domain.HarnessClaudeCode, CreatedAt: created.Add(-time.Hour), Metadata: domain.SessionMetadata{AgentSessionID: "conv-x"}},
	}}

	var mu sync.Mutex
	var starts []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			StartDate string `json:"startDate"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		starts = append(starts, body.StartDate)
		mu.Unlock()
		event := func(conversation string) map[string]any {
			return map[string]any{
				"timestamp": fmt.Sprint(now.Add(-time.Minute).UnixMilli()), "model": "gpt-5", "conversationId": conversation,
				"tokenUsage": map[string]any{"inputTokens": 1, "outputTokens": 1},
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"totalUsageEventsCount": 3,
			"usageEventsDisplay":    []any{event("conv-a"), event("conv-b"), event("not-ao")},
		})
	}))
	defer server.Close()

	tokenReads := 0
	usage := NewCursorUsage(store, nil)
	usage.client, usage.endpoint, usage.now = server.Client(), server.URL, func() time.Time { return now }
	usage.token = func(context.Context) (string, error) {
		tokenReads++
		return cursorTestToken("user_1"), nil
	}

	if err := usage.read(ctx); err != nil {
		t.Fatalf("first read: %v", err)
	}
	first := store.reads[0]
	if !first.Since.Equal(created.Truncate(time.Second)) || !first.Until.Equal(now.Truncate(time.Second)) {
		t.Fatalf("first read covers %v to %v, want the oldest AO Cursor session to now", first.Since, first.Until)
	}
	if len(first.Events) != 2 || first.Events[0].SessionID != "tui" || first.Events[1].SessionID != "chat" {
		t.Fatalf("first read kept %+v, want conv-a for tui and conv-b for chat only", first.Events)
	}

	if err := usage.read(ctx); err != nil {
		t.Fatalf("second read: %v", err)
	}
	if want := now.Truncate(time.Second).Add(-cursorRefetch); !store.reads[1].Since.Equal(want) {
		t.Fatalf("second read starts %v, want %v", store.reads[1].Since, want)
	}
	mu.Lock()
	if len(starts) != 2 || starts[1] != strconv.FormatInt(now.Truncate(time.Second).Add(-cursorRefetch).UnixMilli(), 10) {
		t.Fatalf("dashboard windows started %v", starts)
	}
	mu.Unlock()

	// Every AO Cursor session ended before the margin: there is nothing new.
	for index := range store.sessions {
		store.sessions[index].IsTerminated = true
		store.sessions[index].UpdatedAt = now.Add(-2 * time.Hour)
	}
	if err := usage.read(ctx); err != nil {
		t.Fatalf("idle read: %v", err)
	}
	if len(store.reads) != 2 || tokenReads != 2 {
		t.Fatalf("idle read stored %d reads with %d token reads, want no new read and no Keychain read", len(store.reads), tokenReads)
	}
}
