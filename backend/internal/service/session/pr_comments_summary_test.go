package session

import (
	"context"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestSummarizeThreadsGroupsCommentsByThreadOldestFirst(t *testing.T) {
	t0 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	threads := []domain.PullRequestReviewThread{
		{ThreadID: "T1", Path: "a.go", Line: 10, Resolved: true},
		{ThreadID: "T2", Path: "b.go", Line: 3},
	}
	comments := []domain.PullRequestComment{
		{ThreadID: "T2", ID: "c3", Author: "bob", Body: "later in T2", CreatedAt: t0.Add(3 * time.Hour)},
		{ThreadID: "T1", ID: "c2", Author: "me", Body: "reply", CreatedAt: t0.Add(2 * time.Hour), Resolved: true},
		{ThreadID: "T1", ID: "c1", Author: "amy", Body: "first", URL: "https://x/c1", CreatedAt: t0, Resolved: true},
		{ThreadID: "T2", ID: "c0", Author: "ci", IsBot: true, Body: "bot note", CreatedAt: t0.Add(time.Hour)},
		// Repeated through a URL alias: listed once.
		{ThreadID: "T1", ID: "c1", Author: "amy", Body: "first", CreatedAt: t0, Resolved: true},
		// Legacy comment with no thread id: not part of any thread.
		{ID: "legacy", Author: "old", Body: "legacy"},
		// Thread with no row: anchor and resolution come from its comments.
		{ThreadID: "T3", ID: "c9", Author: "zed", File: "c.go", Line: 7, Resolved: false, CreatedAt: t0.Add(4 * time.Hour)},
	}
	got := summarizeThreads(threads, comments)
	if len(got) != 3 {
		t.Fatalf("threads = %+v, want 3", got)
	}
	if got[0].ID != "T1" || got[0].Path != "a.go" || got[0].Line != 10 || !got[0].Resolved {
		t.Fatalf("thread[0] = %+v", got[0])
	}
	if len(got[0].Comments) != 2 || got[0].Comments[0].ID != "c1" || got[0].Comments[1].ID != "c2" || got[0].Comments[0].URL != "https://x/c1" || !got[0].Comments[0].CreatedAt.Equal(t0) {
		t.Fatalf("thread[0] comments = %+v", got[0].Comments)
	}
	if got[1].ID != "T2" || got[1].Resolved || got[1].Path != "b.go" || len(got[1].Comments) != 2 || got[1].Comments[0].ID != "c0" || !got[1].Comments[0].IsBot {
		t.Fatalf("thread[1] = %+v", got[1])
	}
	if got[2].ID != "T3" || got[2].Path != "c.go" || got[2].Line != 7 || got[2].Resolved {
		t.Fatalf("thread[2] = %+v", got[2])
	}
	if summarizeThreads(threads, nil) != nil {
		t.Fatal("threads without comments must summarize to nil (absent on the wire)")
	}
}

func TestListPRSummariesCarriesThreadsAndConversationFromStoredRows(t *testing.T) {
	prURL := "https://github.com/acme/repo/pull/7"
	t0 := time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)
	st := newPRDetailsStore(prURL)
	st.threads = map[string][]domain.PullRequestReviewThread{prURL: {{ThreadID: "T1", Path: "a.go", Line: 1}}}
	st.comments = map[string][]domain.PullRequestComment{prURL: {{ThreadID: "T1", ID: "c1", Author: "amy", Body: "nit", CreatedAt: t0}}}
	st.details[prURL] = domain.PullRequestDetails{
		ObservedAt: t0,
		Conversation: []domain.PullRequestConversationComment{
			{ID: "IC_2", Author: "bob", Body: "second", CreatedAt: t0.Add(time.Hour)},
			{ID: "IC_1", Author: "ci", IsBot: true, Body: "first", URL: "https://x/ic1", CreatedAt: t0},
		},
	}

	got, err := (&Service{store: st}).ListPRSummaries(context.Background(), "mer-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("ListPRSummaries = %+v, err %v", got, err)
	}
	pr := got[0]
	if len(pr.Threads) != 1 || pr.Threads[0].ID != "T1" || len(pr.Threads[0].Comments) != 1 || pr.Threads[0].Comments[0].Author != "amy" {
		t.Fatalf("threads = %+v", pr.Threads)
	}
	if len(pr.Conversation) != 2 || pr.Conversation[0].ID != "IC_1" || !pr.Conversation[0].IsBot || pr.Conversation[0].URL != "https://x/ic1" || pr.Conversation[1].ID != "IC_2" {
		t.Fatalf("conversation = %+v, want oldest first", pr.Conversation)
	}
}
